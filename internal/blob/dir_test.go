package blob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// validID is a canonical lowercase UUID, the only shape the store accepts.
const validID = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"

func newTestDir(t *testing.T) (*Dir, string) {
	t.Helper()
	root := t.TempDir()
	d, err := NewDir(root)
	if err != nil {
		t.Fatalf("NewDir(%q) = %v, want nil error", root, err)
	}
	return d, root
}

// filesUnder returns every regular file below root, as paths relative to root,
// so a test can assert what the store did and did not create.
func filesUnder(t *testing.T, root string) []string {
	t.Helper()
	var got []string
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		got = append(got, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %q: %v", root, err)
	}
	return got
}

// TestBadIDsAreRefusedByEveryMethod is the security property: the id arrives
// from a public URL path, so anything that is not a canonical UUID must be
// refused with ErrBadID rather than cleaned into a path.
func TestBadIDsAreRefusedByEveryMethod(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"parent traversal", "../../etc/passwd"},
		{"bare dotdot", ".."},
		{"single dot", "."},
		{"forward slash", "a/b"},
		{"backslash", `a\b`},
		{"empty", ""},
		{"nul byte appended", validID + "\x00"},
		{"nul byte embedded", "3f2504e0-4f89-41d3-9a0c-0305e82c33\x0001"},
		{"uppercase uuid", strings.ToUpper(validID)},
		{"uuid with extension", validID + ".jpg"},
		{"uuid without dashes", strings.ReplaceAll(validID, "-", "")},
		{"traversal padded to uuid length", "3f2504e0-4f89-41d3-9a0c-../../etc/pa"},
		{"absolute path", "/etc/passwd"},
		{"non-hex in hex position", "3g2504e0-4f89-41d3-9a0c-0305e82c3301"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, root := newTestDir(t)
			ctx := context.Background()

			if _, err := d.Path(tt.id); !errors.Is(err, ErrBadID) {
				t.Errorf("Path(%q) error = %v, want ErrBadID", tt.id, err)
			}
			if err := d.Put(ctx, tt.id, []byte("payload")); !errors.Is(err, ErrBadID) {
				t.Errorf("Put(%q) error = %v, want ErrBadID", tt.id, err)
			}
			if _, err := d.Open(ctx, tt.id); !errors.Is(err, ErrBadID) {
				t.Errorf("Open(%q) error = %v, want ErrBadID", tt.id, err)
			}
			if err := d.Delete(ctx, tt.id); !errors.Is(err, ErrBadID) {
				t.Errorf("Delete(%q) error = %v, want ErrBadID", tt.id, err)
			}
			// A refusal that still touched the filesystem would not be a refusal.
			if got := filesUnder(t, root); len(got) != 0 {
				t.Errorf("refused id %q created %v under root, want nothing", tt.id, got)
			}
		})
	}
}

// TestValidIDIsAccepted is the other half of the table: the allowlist must let
// through exactly what this application generates.
func TestValidIDIsAccepted(t *testing.T) {
	d, root := newTestDir(t)

	got, err := d.Path(validID)
	if err != nil {
		t.Fatalf("Path(%q) = %v, want nil error", validID, err)
	}
	if want := filepath.Join(root, "3f", validID); got != want {
		t.Errorf("Path(%q) = %q, want %q", validID, got, want)
	}
	if err := d.Put(context.Background(), validID, []byte("payload")); err != nil {
		t.Fatalf("Put(%q) = %v, want nil error", validID, err)
	}
}

func TestPutWritesTheBlobToItsShardedPath(t *testing.T) {
	d, root := newTestDir(t)
	data := []byte("jpeg bytes")

	if err := d.Put(context.Background(), validID, data); err != nil {
		t.Fatalf("Put = %v, want nil error", err)
	}

	want := filepath.Join(root, "3f", validID)
	fi, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat %q: %v; files under root: %v", want, err, filesUnder(t, root))
	}
	if perm := fi.Mode().Perm(); perm != filePerm {
		t.Errorf("blob mode = %v, want %v", perm, filePerm)
	}
	on, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read %q: %v", want, err)
	}
	if !bytes.Equal(on, data) {
		t.Errorf("on-disk bytes = %q, want %q", on, data)
	}

	// The shard directory is created on demand. Its exact mode is dirPerm masked
	// by the process umask, so assert only that the owner can traverse it.
	shard, err := os.Stat(filepath.Join(root, "3f"))
	if err != nil {
		t.Fatalf("stat shard: %v", err)
	}
	if !shard.IsDir() {
		t.Errorf("shard %q is not a directory", filepath.Join(root, "3f"))
	}
	if perm := shard.Mode().Perm(); perm&0o700 != 0o700 {
		t.Errorf("shard mode = %v, want at least rwx for owner", perm)
	}

	// Exactly one file, and it is the blob: no temp file survived the rename.
	if got := filesUnder(t, root); len(got) != 1 || got[0] != filepath.Join("3f", validID) {
		t.Errorf("files under root = %v, want exactly [%s]", got, filepath.Join("3f", validID))
	}
}

// TestRoundTripIsByteIdentical guards the thing an image most easily breaks: a
// blob is not text, so NUL bytes, high-bit bytes and a trailing newline must all
// come back unchanged.
func TestRoundTripIsByteIdentical(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"nul bytes", []byte{0x00, 0x01, 0x00, 0x00, 0xff, 0x00}},
		{"high bit bytes", []byte{0xff, 0xd8, 0xff, 0xe0, 0x80, 0x81, 0xfe}},
		{"jpeg-like header and trailer", append([]byte{0xff, 0xd8, 0xff}, 0x00, 0x0a, 0xff, 0xd9)},
		{"every byte value", allBytes()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, _ := newTestDir(t)
			ctx := context.Background()

			if err := d.Put(ctx, validID, tt.data); err != nil {
				t.Fatalf("Put = %v, want nil error", err)
			}
			got, err := d.Open(ctx, validID)
			if err != nil {
				t.Fatalf("Open = %v, want nil error", err)
			}
			if !bytes.Equal(got, tt.data) {
				t.Errorf("Open returned %d bytes %x, want %d bytes %x", len(got), got, len(tt.data), tt.data)
			}
		})
	}
}

func allBytes() []byte {
	b := make([]byte, 256)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// TestOpenOfAMissingBlobWrapsErrNotFound is what lets a handler answer 404
// instead of 500.
func TestOpenOfAMissingBlobWrapsErrNotFound(t *testing.T) {
	d, _ := newTestDir(t)

	_, err := d.Open(context.Background(), validID)
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Open of an absent blob = %v, want an error wrapping core.ErrNotFound", err)
	}
	// A missing blob is not a malformed request; conflating the two would make a
	// handler answer 400 for a photo that was simply deleted.
	if errors.Is(err, ErrBadID) {
		t.Errorf("Open of an absent blob = %v, want it NOT to wrap ErrBadID", err)
	}
}

// TestDeleteIsANoOpWhenTheBlobIsAlreadyGone: the offer service removes the row
// before the bytes, so a retry has to converge rather than fail forever.
func TestDeleteIsANoOpWhenTheBlobIsAlreadyGone(t *testing.T) {
	d, root := newTestDir(t)
	ctx := context.Background()

	if err := d.Put(ctx, validID, []byte("jpeg bytes")); err != nil {
		t.Fatalf("Put = %v, want nil error", err)
	}
	if err := d.Delete(ctx, validID); err != nil {
		t.Fatalf("first Delete = %v, want nil error", err)
	}
	if err := d.Delete(ctx, validID); err != nil {
		t.Fatalf("second Delete = %v, want nil error (delete must be idempotent)", err)
	}
	// And a delete of a blob that never existed is equally a no-op.
	if err := d.Delete(ctx, "00000000-0000-4000-8000-000000000000"); err != nil {
		t.Fatalf("Delete of a never-stored blob = %v, want nil error", err)
	}
	if got := filesUnder(t, root); len(got) != 0 {
		t.Errorf("files under root after delete = %v, want none", got)
	}
	if _, err := d.Open(ctx, validID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Open after Delete = %v, want core.ErrNotFound", err)
	}
}

// TestPutReplacesInPlaceAndLeavesNoTempFile: the rename must publish over the
// previous blob, and the staging file must not survive it. A leaked temp file is
// how a shard fills with rubbish nobody ever serves.
func TestPutReplacesInPlaceAndLeavesNoTempFile(t *testing.T) {
	d, root := newTestDir(t)
	ctx := context.Background()

	if err := d.Put(ctx, validID, []byte("first version, deliberately longer")); err != nil {
		t.Fatalf("first Put = %v, want nil error", err)
	}
	second := []byte("second")
	if err := d.Put(ctx, validID, second); err != nil {
		t.Fatalf("second Put = %v, want nil error", err)
	}

	got, err := d.Open(ctx, validID)
	if err != nil {
		t.Fatalf("Open = %v, want nil error", err)
	}
	if !bytes.Equal(got, second) {
		t.Errorf("Open = %q, want %q (the second Put must fully replace the first)", got, second)
	}
	if files := filesUnder(t, root); len(files) != 1 {
		t.Errorf("files under root = %v, want exactly one (a temp file was left behind)", files)
	}
}

func TestNewDirCreatesAMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "photos", "blobs")

	if _, err := NewDir(root); err != nil {
		t.Fatalf("NewDir(%q) = %v, want nil error", root, err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat %q: %v", root, err)
	}
	if !fi.IsDir() {
		t.Errorf("NewDir created %q as a non-directory", root)
	}
	// The probe file must not survive construction.
	if got := filesUnder(t, root); len(got) != 0 {
		t.Errorf("files under a fresh root = %v, want none", got)
	}
}

func TestNewDirRefusesARootThatIsNotADirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "photos")
	if err := os.WriteFile(root, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seeding %q: %v", root, err)
	}

	d, err := NewDir(root)
	if err == nil {
		t.Fatalf("NewDir(%q) = %v, nil; want an error", root, d)
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("NewDir error = %q, want it to say the root is not a directory", err)
	}
}

func TestNewDirRefusesARootItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permission, so this check cannot fail here")
	}
	root := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(root, 0o555); err != nil {
		t.Fatalf("seeding %q: %v", root, err)
	}

	if _, err := NewDir(root); err == nil {
		t.Fatalf("NewDir(%q) = nil error, want a failure on an unwritable root", root)
	} else if !strings.Contains(err.Error(), "not writable") {
		t.Errorf("NewDir error = %q, want it to say the root is not writable", err)
	}
}

func TestNewDirRefusesAnEmptyRoot(t *testing.T) {
	if _, err := NewDir(""); err == nil {
		t.Fatal(`NewDir("") = nil error, want a failure`)
	}
}

// TestCancelledContextIsHonouredBeforeIO checks the cheap guarantee the port
// asks for: a request the caller has already abandoned does not reach the disk.
func TestCancelledContextIsHonouredBeforeIO(t *testing.T) {
	d, root := newTestDir(t)
	if err := d.Put(context.Background(), validID, []byte("jpeg bytes")); err != nil {
		t.Fatalf("seeding Put = %v, want nil error", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	other := "11111111-2222-4333-8444-555555555555"
	if err := d.Put(ctx, other, []byte("should not land")); !errors.Is(err, context.Canceled) {
		t.Errorf("Put with a cancelled context = %v, want context.Canceled", err)
	}
	if _, err := d.Open(ctx, validID); !errors.Is(err, context.Canceled) {
		t.Errorf("Open with a cancelled context = %v, want context.Canceled", err)
	}
	if err := d.Delete(ctx, validID); !errors.Is(err, context.Canceled) {
		t.Errorf("Delete with a cancelled context = %v, want context.Canceled", err)
	}
	// The seeded blob must still be there, and the cancelled Put must not be.
	if got := filesUnder(t, root); len(got) != 1 || got[0] != filepath.Join("3f", validID) {
		t.Errorf("files under root = %v, want only the seeded blob", got)
	}
}

// TestPutPublishesByRenameSoAnOpenReaderIsUndisturbed states the atomicity
// property deterministically, which the concurrent test below can only sample:
// a marketplace fetch that opened the image before a re-upload must still read
// the whole previous version. rename(2) swaps the directory entry and leaves the
// old bytes intact for anyone already holding them open, whereas an in-place
// os.WriteFile truncates them under the reader — which is the half-served image
// this package exists to prevent.
func TestPutPublishesByRenameSoAnOpenReaderIsUndisturbed(t *testing.T) {
	d, _ := newTestDir(t)
	ctx := context.Background()

	first := bytes.Repeat([]byte{0xd8, 0x00, 0xff}, 4096)
	if err := d.Put(ctx, validID, first); err != nil {
		t.Fatalf("first Put = %v, want nil error", err)
	}
	path, err := d.Path(validID)
	if err != nil {
		t.Fatalf("Path = %v, want nil error", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat before overwrite: %v", err)
	}
	// Stand in for a marketplace that has the image open when the re-upload lands.
	reader, err := os.Open(path)
	if err != nil {
		t.Fatalf("open before overwrite: %v", err)
	}
	defer reader.Close()

	if err := d.Put(ctx, validID, []byte("second version, deliberately shorter")); err != nil {
		t.Fatalf("second Put = %v, want nil error", err)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("reading the already-open blob: %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Errorf("a reader holding the blob open saw %d bytes after the overwrite, want the original %d: Put wrote in place instead of renaming", len(got), len(first))
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after overwrite: %v", err)
	}
	if os.SameFile(before, after) {
		t.Error("the published blob is the same file object as before the overwrite: Put mutated the live file rather than renaming a finished one over it")
	}
}

// TestConcurrentPutsOfOneIDPublishOneWholeVersion is the atomicity claim under
// -race: whichever writer wins the rename, a reader must see one complete
// payload and never a splice of two.
func TestConcurrentPutsOfOneIDPublishOneWholeVersion(t *testing.T) {
	d, _ := newTestDir(t)
	ctx := context.Background()

	const writers = 8
	payloads := make([][]byte, writers)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, 4096)
	}

	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = d.Put(ctx, validID, payloads[i])
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Put %d = %v, want nil error", i, err)
		}
	}

	got, err := d.Open(ctx, validID)
	if err != nil {
		t.Fatalf("Open = %v, want nil error", err)
	}
	for _, want := range payloads {
		if bytes.Equal(got, want) {
			return
		}
	}
	t.Fatalf("Open returned %d bytes starting %x, which matches no single payload: a torn write was published", len(got), got[:min(16, len(got))])
}

// TestConcurrentPutsOfDistinctIDsAreIndependent exercises on-demand shard
// creation from several goroutines at once, where MkdirAll must not race.
func TestConcurrentPutsOfDistinctIDsAreIndependent(t *testing.T) {
	d, _ := newTestDir(t)
	ctx := context.Background()

	const n = 32
	ids := make([]string, n)
	for i := range ids {
		// Deliberately share the first two characters across pairs so that
		// several goroutines create the same shard directory.
		ids[i] = fmt.Sprintf("%02x%06x-0000-4000-8000-%012x", i/2, i, i)
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = d.Put(ctx, ids[i], []byte(ids[i]))
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Put(%q) = %v, want nil error", ids[i], err)
		}
	}

	for _, id := range ids {
		got, err := d.Open(ctx, id)
		if err != nil {
			t.Fatalf("Open(%q) = %v, want nil error", id, err)
		}
		if string(got) != id {
			t.Errorf("Open(%q) = %q, want %q", id, got, id)
		}
	}
}
