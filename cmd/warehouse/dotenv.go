package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// defaultEnvFile is the file loadDotEnv reads when nothing names another.
const defaultEnvFile = ".env"

// loadDotEnv reads KEY=VALUE lines from path into the process environment.
//
// ⚠ IT NEVER OVERWRITES A VARIABLE THAT IS ALREADY SET. A real environment
// variable — from systemd, a container, a CI job — is a deliberate act by
// whoever deployed this. A `.env` on disk may be a leftover from somebody's
// laptop that got copied along with the binary. So the file FILLS GAPS and never
// wins, which also means an operator can override one value for a single run
// (`EBAY_CATEGORY=1234 ./warehouse`) without editing the file.
//
// The resulting order of precedence, lowest first:
//
//	code default  <  .env  <  real environment  <  the settings table
//
// The last step is the one ADR-014 and ADR-016 are about: anything an
// administrator can change at Settings beats all three, because a value that can
// only be fixed by restarting the process is a value that stays wrong.
//
// A MISSING FILE IS NOT AN ERROR. `.env` is a convenience for running from a
// checkout; a deployment that sets real variables should not have one, and
// demanding the file would make the ordinary production case the exceptional
// one.
//
// What it deliberately does NOT support, because each is a way for a config file
// to mean something other than it appears to:
//
//   - variable interpolation (`FOO=$BAR`) — the value is taken literally
//   - multi-line values
//   - anything after a well-formed line on the same line
//
// A line it cannot parse is reported rather than skipped: a typo in a config
// file should not boot a service with a setting silently missing.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		key, value, ok, err := parseEnvLine(sc.Text())
		if err != nil {
			return fmt.Errorf("%s line %d: %w", path, line, err)
		}
		if !ok {
			continue // blank or a comment
		}
		// The gap-filling rule. os.Setenv would clobber.
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s line %d: set %s: %w", path, line, key, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseEnvLine splits one line into a key and a value.
//
// ok is false for a blank line or a comment. An error means the line has content
// that is not a KEY=VALUE pair, which is reported rather than ignored.
func parseEnvLine(raw string) (key, value string, ok bool, err error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return "", "", false, nil
	}

	// `export FOO=bar` is what someone pastes from a shell session, and
	// refusing it would be pedantry about a line whose meaning is unambiguous.
	s = strings.TrimPrefix(s, "export ")

	eq := strings.Index(s, "=")
	if eq < 0 {
		return "", "", false, fmt.Errorf("expected KEY=VALUE, got %q", raw)
	}
	key = strings.TrimSpace(s[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("empty key in %q", raw)
	}
	value = strings.TrimSpace(s[eq+1:])

	switch {
	case len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`):
		// Double quotes: the two escapes anyone actually types.
		value = value[1 : len(value)-1]
		value = strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\"`, `"`, `\\`, `\`).Replace(value)
	case len(value) >= 2 && strings.HasPrefix(value, `'`) && strings.HasSuffix(value, `'`):
		// Single quotes: entirely literal, so a value containing a # or a
		// backslash needs no thought.
		value = value[1 : len(value)-1]
	default:
		// Unquoted: a ` #` starts a trailing comment. The space is required, so
		// a value that is itself a fragment like "a#b" survives intact.
		if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
	}
	return key, value, true, nil
}
