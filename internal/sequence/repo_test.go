package sequence

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newRepo opens a database with the REAL migrations, so the counters table and
// its seeded row are exactly what production has.
func newRepo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "seq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepo(db.Write), db
}

// TestAllocationIsSequentialFromOne pins the first reference an installation
// hands out, because it is the one somebody writes on the first box.
func TestAllocationIsSequentialFromOne(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()

	for want := int64(1); want <= 5; want++ {
		got, err := r.NextSequence(ctx, core.SequenceOfferSKU)
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if got != want {
			t.Fatalf("allocation %d = %d", want, got)
		}
	}
	if got := core.FormatSKU(1); got != "WH0000001" {
		t.Errorf("FormatSKU(1) = %q, want WH0000001", got)
	}
}

// TestConcurrentAllocationNeverRepeats is the property the whole port exists for.
//
// ⚠ It also proves that UPDATE ... RETURNING is honoured by this CGO-free
// driver at all. modernc.org/sqlite is a translation of SQLite rather than a
// binding to it, so which statements it supports is a property of that build —
// and a RETURNING clause that were silently ignored would surface here as a scan
// error rather than as duplicate numbers, which is why the test asserts the
// error too rather than only the set.
//
// The failure this guards against is not theoretical: two operators taking stock
// in at the same moment would each write the same reference on a different box,
// and the second offer would be refused by the UNIQUE constraint long after the
// label was stuck on.
func TestConcurrentAllocationNeverRepeats(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()

	const (
		goroutines   = 8
		perGoroutine = 25
		total        = goroutines * perGoroutine
	)

	var (
		mu   sync.Mutex
		seen = make(map[int64]int, total)
		errs []error
		wg   sync.WaitGroup
		// A barrier, so the allocations genuinely overlap rather than being
		// spread out by goroutine start-up.
		start = make(chan struct{})
	)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < perGoroutine; i++ {
				n, err := r.NextSequence(ctx, core.SequenceOfferSKU)
				mu.Lock()
				if err != nil {
					errs = append(errs, err)
				} else {
					seen[n]++
				}
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("%d allocation(s) failed, first: %v", len(errs), errs[0])
	}
	if len(seen) != total {
		t.Fatalf("%d distinct numbers from %d allocations — the counter handed the "+
			"same reference to two callers", len(seen), total)
	}
	for n, count := range seen {
		if count != 1 {
			t.Errorf("number %d was handed out %d times", n, count)
		}
	}
	// Monotonic and gapless: 1..total inclusive.
	for want := int64(1); want <= total; want++ {
		if seen[want] != 1 {
			t.Fatalf("number %d was never allocated; the sequence has a gap", want)
		}
	}
}

// TestUnknownCounterIsRefused guards the typo case: a misspelled counter must
// not quietly start its own sequence at 1 and mint references that collide with
// the real one.
func TestUnknownCounterIsRefused(t *testing.T) {
	r, _ := newRepo(t)

	_, err := r.NextSequence(context.Background(), "offer_skus")
	if !errors.Is(err, ErrUnknownSequence) {
		t.Fatalf("err = %v, want ErrUnknownSequence", err)
	}
}

// TestNumbersAreNotReused pins the reason a counter exists rather than
// MAX(sku)+1: a reference written on a label is spent whether or not the row
// that used it survives.
func TestNumbersAreNotReused(t *testing.T) {
	r, db := newRepo(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := r.NextSequence(ctx, core.SequenceOfferSKU); err != nil {
			t.Fatalf("allocate: %v", err)
		}
	}
	// Nothing about deleting the rows that used 1..3 may rewind the counter.
	if _, err := db.Write.Exec(`DELETE FROM offers`); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err := r.NextSequence(ctx, core.SequenceOfferSKU)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if got != 4 {
		t.Errorf("after deleting the rows, the next number is %d — a reference already "+
			"written on a box would be handed out twice", got)
	}
}
