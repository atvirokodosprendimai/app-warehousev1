// Package blob stores the photo bytes for warehouse offers on the local
// filesystem. The bytes live here rather than in SQLite so that image data and
// row data can be backed up, replicated and served by different means.
//
// Every blob id is the UUID of a photo and, at the same time, the public URL
// path segment that a marketplace such as Shopify or eBay fetches the image
// from. An id therefore reaches this package from an untrusted URL path, and
// the package treats it as such: an id that is not a canonical lowercase UUID
// is refused with [ErrBadID] rather than cleaned. See [Dir.Path].
package blob

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// Dir implements [core.BlobStore] over a directory tree. It satisfies the
// interface at compile time so that a drift in the port is a build failure here
// rather than a wiring failure in cmd.
var _ core.BlobStore = (*Dir)(nil)

const (
	// dirPerm is the mode for the root and for shard directories. It is subject
	// to the process umask, as every mkdir(2) is.
	dirPerm fs.FileMode = 0o755
	// filePerm is the mode a published blob ends up with. It is applied with
	// chmod rather than at creation, because [os.CreateTemp] always creates
	// 0o600 and chmod is not filtered by the umask.
	filePerm fs.FileMode = 0o644
)

// ErrBadID reports an id that is not a canonical lowercase UUID and is
// therefore refused. Callers should map it to 400, never to 404: it means the
// request named something this application could not have generated.
var ErrBadID = errors.New("blob: bad id")

// Dir is a blob store rooted at a single directory. A blob for id is held at
// root/<first two characters of id>/<id>. The two-character shard keeps any one
// directory to roughly 1/256th of the collection, because a flat directory of
// tens of thousands of files degrades on some filesystems and is unusable for a
// human debugging with ls.
//
// A Dir is safe for concurrent use: every method derives its paths from its
// arguments and holds no mutable state.
type Dir struct {
	root string
}

// NewDir returns a store rooted at root, creating the directory if it does not
// exist. It fails when root exists as something other than a directory, or when
// the process cannot write into it — both are checked here, at construction,
// rather than at the first upload, so that a misconfigured deployment fails
// where an operator is looking.
func NewDir(root string) (*Dir, error) {
	if root == "" {
		return nil, errors.New("blob: root must not be empty")
	}
	switch fi, err := os.Stat(root); {
	case err == nil && !fi.IsDir():
		return nil, fmt.Errorf("blob: root %q exists and is not a directory", root)
	case err == nil:
		// Already a directory; the write probe below decides whether it is usable.
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(root, dirPerm); err != nil {
			return nil, fmt.Errorf("blob: create root %q: %w", root, err)
		}
	default:
		return nil, fmt.Errorf("blob: inspect root %q: %w", root, err)
	}

	// Probe by actually creating a file: the permission bits alone do not answer
	// the question on a read-only mount, and neither does running as root.
	probe, err := os.CreateTemp(root, ".writable-")
	if err != nil {
		return nil, fmt.Errorf("blob: root %q is not writable: %w", root, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	return &Dir{root: root}, nil
}

// Path returns the on-disk location of the blob for id, or [ErrBadID].
//
// This is the single place the id becomes a path, and it validates the id as a
// string before any path is built. [filepath.Join] and [filepath.Clean] must
// not be relied on for this: they resolve "../" away and would hand back a
// tidy-looking path outside the root. Refusing rather than sanitising also
// keeps the mapping injective — a cleaned id would silently fold two different
// requests onto one file.
func (d *Dir) Path(id string) (string, error) {
	if !canonicalUUID(id) {
		return "", fmt.Errorf("%w: %q", ErrBadID, id)
	}
	return filepath.Join(d.root, id[:2], id), nil
}

// canonicalUUID reports whether id is exactly 8-4-4-12 lowercase hex with
// dashes, which is precisely what this application generates. It is an
// allowlist on purpose: a denylist of dangerous characters has to be right
// about every encoding an id could arrive in, while an allowlist only has to be
// right about the one shape that is legitimate.
func canonicalUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
				return false
			}
		}
	}
	return true
}

// Put stores data as the blob for id, replacing any previous bytes. It returns
// [ErrBadID] for an id that is not a canonical UUID, and ctx.Err() if ctx is
// already cancelled.
func (d *Dir) Put(ctx context.Context, id string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := d.Path(id)
	if err != nil {
		return err
	}
	shard := filepath.Dir(path)
	if err := os.MkdirAll(shard, dirPerm); err != nil {
		return fmt.Errorf("blob: create shard for %s: %w", id, err)
	}

	// Why write-to-temp, fsync, rename rather than os.WriteFile: this blob's URL
	// may already be published to a marketplace, so a fetch can arrive while the
	// bytes are landing. rename(2) is atomic, so a reader sees either the
	// previous file or the whole new one and never a half-written image; the
	// fsync is what stops a crash from publishing the name without the bytes.
	// The temp file is created in the SAME directory because rename is atomic
	// only within one filesystem.
	tmp, err := os.CreateTemp(shard, "."+id+".tmp-")
	if err != nil {
		return fmt.Errorf("blob: create temp for %s: %w", id, err)
	}
	tmpName := tmp.Name()
	// Harmless after a successful rename, and it is what keeps a failed write
	// from leaving a stub behind.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("blob: write temp for %s: %w", id, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("blob: sync temp for %s: %w", id, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("blob: close temp for %s: %w", id, err)
	}
	// CreateTemp always makes 0o600; the blob is served to the world, so widen it
	// before it is published, not after.
	if err := os.Chmod(tmpName, filePerm); err != nil {
		return fmt.Errorf("blob: chmod temp for %s: %w", id, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("blob: publish %s: %w", id, err)
	}
	return nil
}

// Open returns the stored bytes for id. It returns an error wrapping
// [core.ErrNotFound] when no blob is stored, so that a handler can errors.Is it
// and answer 404 rather than 500; [ErrBadID] when the id is not a canonical
// UUID; and ctx.Err() if ctx is already cancelled.
func (d *Dir) Open(ctx context.Context, id string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := d.Path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("blob: open %s: %w", id, core.ErrNotFound)
		}
		return nil, fmt.Errorf("blob: open %s: %w", id, err)
	}
	return data, nil
}

// Delete removes the bytes for id. It returns nil when the blob is already
// gone, [ErrBadID] when the id is not a canonical UUID, and ctx.Err() if ctx is
// already cancelled.
//
// Why an absent blob is success: the offer service deletes the database row
// first and the bytes second, so a retry after a partial failure has to
// converge. Reporting "already deleted" as an error would make that retry fail
// forever on work that is in fact complete.
func (d *Dir) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := d.Path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("blob: delete %s: %w", id, err)
	}
	return nil
}
