package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNothingOpensAStreamWithoutGoingThroughThisPackage turns this package's own
// doc comment into something that can fail.
//
// ⚠ THE CLAIM WAS ALREADY WRITTEN DOWN AND NOTHING CHECKED IT. The package
// comment above says nothing in this application opens a datastar stream
// directly, and ADR-009 carries the same sentence as a Risk with the note that a
// source grep in a test would close it. Until this ran, it was a sentence.
//
// (This comment deliberately does not spell the constructor's name: the test
// searches the source tree it lives in, and quoting the literal made the first
// version of this file report ITSELF as the violation — which is the check
// working, but not usefully.)
//
// ★ WHAT GOES WRONG WHEN SOMEBODY BYPASSES IT IS INVISIBLE FROM THE SERVER SIDE.
// Two settings have to be right before the first byte: the headers must be
// primed before any compression middleware sees the body, and the write deadline
// must be cleared. Get either wrong and the handler still runs, the database
// write still lands, the response is still 200 — and the browser silently
// applies no patches, or the stream dies partway through a session with nothing
// logged. No handler test can see it, which is why the guard is structural.
func TestNothingOpensAStreamWithoutGoingThroughThisPackage(t *testing.T) {
	// Built rather than written, so this file does not match its own search and
	// report itself as the violation.
	needle := "datastar." + "NewSSE"

	// The one file allowed to hold it: this package's constructor, which is what
	// every other caller is supposed to reach for instead.
	const allowed = "sse.go"

	root := "../.." // internal/
	var offenders []string
	found := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), needle) {
			return nil
		}
		found++
		if filepath.Base(path) == allowed && filepath.Base(filepath.Dir(path)) == "render" {
			return nil
		}
		offenders = append(offenders, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// ⚠ The positive control. A walk that reads nothing, a root that moved, or a
	// needle that stopped matching would all produce an empty offender list —
	// byte-identical to the rule holding. If the legitimate call is not found,
	// this test is not looking at the source it thinks it is.
	if found == 0 {
		t.Fatalf("no file under %s mentions %s at all, not even this package's own "+
			"constructor — so this test searched something other than the source "+
			"tree and its clean result means nothing", root, needle)
	}

	for _, path := range offenders {
		t.Errorf("%s opens a datastar stream directly instead of through "+
			"render.NewSSE. The headers would not be primed before a compression "+
			"middleware sees the body, and the write deadline would not be "+
			"cleared — so the handler returns 200, the database write lands, and "+
			"the browser applies no patches with nothing logged anywhere", path)
	}
}
