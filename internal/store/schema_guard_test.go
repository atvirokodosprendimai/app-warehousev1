package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoTestBuildsItsOwnCopyOfTheSchema is ADR-013 asked of the whole tree
// rather than of the one package that happened to carry its Enforced-by test.
//
// ⚠ ADR-013 SAID "No package holds a copy of any schema statement" WHILE FOUR
// PACKAGES DID. Its enforcing test lives in `internal/offer` and proves only
// that `internal/offer` runs the migrations — structurally blind to `fx`,
// `auth`, `location` and `cart`, which between them carried nine copied
// statements for months. A record can be mutation-verified and still overstate
// its reach, and this is what that looks like from the inside.
//
// ★ WHY A COPY IS WORSE THAN NO TEST AT ALL. Each of those copies came with a
// comment asking whoever changed the schema to keep it in step. `internal/offer`
// and `internal/submission` carried that same comment until their copies
// drifted, and the suite stayed green against a schema production does not have.
// The copy cannot detect its own drift: it DEFINES the table the test then
// measures, so both sides move together, agree with each other perfectly, and
// disagree with the database people actually run. `internal/location`'s copy of
// `offers` had eight columns where the real one has more than twenty.
func TestNoTestBuildsItsOwnCopyOfTheSchema(t *testing.T) {
	// This package is where the exception lives, and it is a narrow one: the
	// handle-split tests create throwaway one-column tables to make the DRIVER
	// prove something (that query_only refuses a write, that _txlock=immediate
	// takes its lock at BEGIN). Those are not the application's schema and no
	// migration could stand in for them.
	const exempt = "store"

	needle := "CREATE " + "TABLE"

	root := ".." // internal/
	var offenders []string
	checked := 0

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) == exempt {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment may say the words — several of these files now explain
			// at length what they used to carry, and saying so must not itself
			// trip the guard.
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if strings.Contains(trimmed, needle) {
				offenders = append(offenders, path+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// ⚠ The positive control. A root that moved or a suffix that stopped
	// matching would produce an empty offender list, byte-identical to the rule
	// holding.
	if checked == 0 {
		t.Fatalf("found no _test.go files under %s at all, so this guard searched "+
			"something other than the source tree and its clean result means "+
			"nothing", root)
	}

	for _, where := range offenders {
		t.Errorf("%s declares a table of its own instead of running the real "+
			"migrations (ADR-013). If this is a throwaway table proving something "+
			"about the DRIVER rather than a copy of the application's schema, it "+
			"belongs in internal/store beside the other two — and if it is a copy, "+
			"it will agree with itself long after it has stopped agreeing with "+
			"production", where)
	}
}

// itoa keeps the failure message readable without pulling strconv in for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
