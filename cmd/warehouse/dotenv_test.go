package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEnvFile puts contents in a temp file and returns its path.
func writeEnvFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// TestDotEnvNeverOverwritesARealEnvironmentVariable is the rule the whole file
// exists to get right.
//
// A real variable — from systemd, a container, a CI job — is a deliberate act by
// whoever deployed this. A `.env` on disk may be a leftover copied from a
// laptop. If the file won, a one-off override (`EBAY_CATEGORY=1234 ./warehouse`)
// would silently do nothing, which is the kind of failure somebody debugs for an
// hour before checking the file.
func TestDotEnvNeverOverwritesARealEnvironmentVariable(t *testing.T) {
	t.Setenv("WAREHOUSE_ALREADY_SET", "from-the-environment")

	path := writeEnvFile(t, "WAREHOUSE_ALREADY_SET=from-the-file\nWAREHOUSE_ONLY_IN_FILE=filled\n")
	if err := loadDotEnv(path); err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}

	if got := os.Getenv("WAREHOUSE_ALREADY_SET"); got != "from-the-environment" {
		t.Errorf("WAREHOUSE_ALREADY_SET = %q, want the real environment to win", got)
	}
	// ...and the gap IS filled, or the file would do nothing at all.
	if got := os.Getenv("WAREHOUSE_ONLY_IN_FILE"); got != "filled" {
		t.Errorf("WAREHOUSE_ONLY_IN_FILE = %q, want %q", got, "filled")
	}
	t.Cleanup(func() { os.Unsetenv("WAREHOUSE_ONLY_IN_FILE") })
}

// TestAMissingDotEnvIsNotAnError pins the ordinary production case.
//
// A deployment that sets real variables should have no `.env` at all. Treating
// its absence as a failure would make the normal case the exceptional one and
// stop the service from booting.
func TestAMissingDotEnvIsNotAnError(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "nothing-here")); err != nil {
		t.Errorf("loadDotEnv on a missing file = %v, want nil", err)
	}
}

// TestDotEnvParsesTheShapesPeopleActuallyWrite covers the syntax a person
// produces by pasting from a shell or a password manager.
func TestDotEnvParsesTheShapesPeopleActuallyWrite(t *testing.T) {
	tests := []struct {
		name string
		line string
		key  string
		want string
	}{
		{"plain", "ADDR=:8080", "ADDR", ":8080"},
		{"spaces around the equals", "ADDR = :9090", "ADDR", ":9090"},
		{"export prefix, as pasted from a shell", "export ADDR=:7070", "ADDR", ":7070"},
		{"double quoted", `EBAY_LOCATION="Kaunas, Lithuania"`, "EBAY_LOCATION", "Kaunas, Lithuania"},
		{"single quoted is literal", `NOTE='a # b \n c'`, "NOTE", `a # b \n c`},
		{"double quotes unescape", `NOTE="line\none"`, "NOTE", "line\none"},
		{"trailing comment after a space", "ADDR=:8080 # the listen address", "ADDR", ":8080"},
		{"a hash with no space is part of the value", "TAG=blue#2", "TAG", "blue#2"},
		{"empty value is allowed and means empty", "EBAY_CATEGORY=", "EBAY_CATEGORY", ""},
		{"a URL survives intact", "PUBLIC_BASE_URL=https://w.example.com", "PUBLIC_BASE_URL", "https://w.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, value, ok, err := parseEnvLine(tt.line)
			if err != nil {
				t.Fatalf("parseEnvLine(%q) = %v", tt.line, err)
			}
			if !ok {
				t.Fatalf("parseEnvLine(%q) reported no pair", tt.line)
			}
			if key != tt.key || value != tt.want {
				t.Errorf("parseEnvLine(%q) = %q=%q, want %q=%q", tt.line, key, value, tt.key, tt.want)
			}
		})
	}
}

// TestDotEnvSkipsBlanksAndComments — a file people can annotate.
func TestDotEnvSkipsBlanksAndComments(t *testing.T) {
	for _, line := range []string{"", "   ", "# a comment", "   # indented comment"} {
		_, _, ok, err := parseEnvLine(line)
		if err != nil {
			t.Errorf("parseEnvLine(%q) = %v, want it skipped", line, err)
		}
		if ok {
			t.Errorf("parseEnvLine(%q) reported a pair, want it skipped", line)
		}
	}
}

// TestDotEnvReportsALineItCannotParse rather than skipping it.
//
// A typo in a config file must not boot the service with a setting silently
// missing — that is the same silent-misconfiguration failure the public-origin
// validation exists to prevent, one layer lower down.
func TestDotEnvReportsALineItCannotParse(t *testing.T) {
	path := writeEnvFile(t, "ADDR=:8080\nthis line has no equals sign\n")

	err := loadDotEnv(path)
	if err == nil {
		t.Fatal("loadDotEnv accepted a malformed line; want an error naming it")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error does not name the line number, so the operator has to hunt: %v", err)
	}
}

// TestDotEnvRefusesAnEmptyKey — "=value" is a typo, not a setting.
func TestDotEnvRefusesAnEmptyKey(t *testing.T) {
	if _, _, _, err := parseEnvLine("=orphaned"); err == nil {
		t.Error("parseEnvLine accepted an empty key")
	}
}
