package core

import "testing"

func TestFormatSKUPadsToAFixedWidth(t *testing.T) {
	cases := map[int64]string{
		1:        "WH0000001",
		42:       "WH0000042",
		9999999:  "WH9999999",
		10000000: "WH10000000", // past the width it grows rather than wrapping
	}
	for n, want := range cases {
		if got := FormatSKU(n); got != want {
			t.Errorf("FormatSKU(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestParseSKUAcceptsWhatSomebodyWouldActuallyType is the point of the whole
// format change: a reference is read off a box by a person, and a person does
// not reproduce zero padding.
func TestParseSKUAcceptsWhatSomebodyWouldActuallyType(t *testing.T) {
	for _, in := range []string{"WH0000042", "wh0000042", "WH42", "wh42", "42", " 42 "} {
		got, ok := ParseSKU(in)
		if !ok {
			t.Errorf("ParseSKU(%q) refused a reference somebody would type", in)
			continue
		}
		if got != 42 {
			t.Errorf("ParseSKU(%q) = %d, want 42", in, got)
		}
		if s := NormalizeSKUQuery(in); s != "WH0000042" {
			t.Errorf("NormalizeSKUQuery(%q) = %q, want WH0000042", in, s)
		}
	}
}

// TestParseSKURejectsOrdinaryText keeps the search box working: a query that is
// not a reference must be searched as words, not turned into one.
func TestParseSKURejectsOrdinaryText(t *testing.T) {
	for _, in := range []string{"", "   ", "brass lamp", "WH", "wh", "MARANTZ-2226", "12a", "-1"} {
		if n, ok := ParseSKU(in); ok {
			t.Errorf("ParseSKU(%q) = %d, true — it would be searched as a reference "+
				"instead of as text", in, n)
		}
		if s := NormalizeSKUQuery(in); s != "" {
			t.Errorf("NormalizeSKUQuery(%q) = %q, want empty", in, s)
		}
	}
}

// TestFormatAndParseRoundTrip guards the pair against drifting apart: a
// generated reference must be findable by parsing it back.
func TestFormatAndParseRoundTrip(t *testing.T) {
	for _, n := range []int64{1, 9, 10, 999, 1000, 9999999, 12345678} {
		s := FormatSKU(n)
		got, ok := ParseSKU(s)
		if !ok || got != n {
			t.Errorf("round trip %d -> %q -> (%d, %v)", n, s, got, ok)
		}
	}
}
