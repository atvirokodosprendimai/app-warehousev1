package core

import (
	"errors"
	"strings"
	"testing"
)

// TestAnUnknownFieldKindIsRefused pins the asymmetry between writing and reading
// a kind.
//
// A write of an unrecognised kind is refused here. A READ of one is not — the
// renderer degrades it to text rather than failing the page — because a row
// written by a newer binary and read by an older one should not take a screen
// down. Refusing on the way in is what keeps that case rare enough to degrade.
func TestAnUnknownFieldKindIsRefused(t *testing.T) {
	for _, s := range []string{"text", "LongText", " number ", "choice", "BOOL"} {
		if _, err := ParseFieldKind(s); err != nil {
			t.Errorf("ParseFieldKind(%q) = %v, want it accepted — the parse trims and "+
				"lowercases, so a kind arriving off a picker is not case-sensitive", s, err)
		}
	}
	for _, s := range []string{"", "date", "money", "richtext", "integer"} {
		_, err := ParseFieldKind(s)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseFieldKind(%q) = %v, want ErrInvalid — five kinds and no more, "+
				"because a sixth stored today is a kind no released renderer knows", s, err)
		}
	}
}

// TestAChoiceFieldNeedsAtLeastOneOption refuses a question nobody can answer.
func TestAChoiceFieldNeedsAtLeastOneOption(t *testing.T) {
	f := CategoryField{Code: "grade", Label: "Grade", Kind: FieldChoice}
	if err := f.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a choice with no options validated as %v, want ErrInvalid", err)
	}

	f.Options = "\n  \n"
	if err := f.Validate(); !errors.Is(err, ErrInvalid) {
		t.Errorf("a choice whose options are only blank lines validated as %v, want "+
			"ErrInvalid — blank lines are dropped, so this really is no options", err)
	}

	f.Options = "A\n\nB\n C \n"
	if err := f.Validate(); err != nil {
		t.Fatalf("a choice with options was refused: %v", err)
	}
	got := f.OptionList()
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("OptionList() = %q, want %q — blank lines dropped and each option "+
			"trimmed, so a stray newline does not become an option nobody can pick",
			got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("option %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestACategoryCodeCannotSplitThePath refuses the one character that would make
// a materialised address ambiguous.
func TestACategoryCodeCannotSplitThePath(t *testing.T) {
	c := Category{Code: "CAR/ENGINE", Name: "Sneaky"}
	err := c.Validate()
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("a code containing a slash validated as %v, want ErrInvalid", err)
	}
	if !strings.Contains(err.Error(), "slash-separated") {
		t.Errorf("the refusal says %q; it should say WHY a slash is refused, because "+
			"the reason is not obvious from the field alone", err)
	}
}

// TestOfferFieldDisplayAppendsAUnit checks the one place a stored value and what
// a human reads differ.
func TestOfferFieldDisplayAppendsAUnit(t *testing.T) {
	km := OfferField{
		CategoryField: CategoryField{Label: "Mileage", Kind: FieldNumber, Unit: "km"},
		Value:         "180000",
	}
	if got := km.Display(); got != "180000 km" {
		t.Errorf("Display() = %q, want %q — the unit is stored beside the number and "+
			"not inside it, so that the number stays a number", got, "180000 km")
	}

	// A unit on a non-number is meaningless and must not be appended.
	note := OfferField{
		CategoryField: CategoryField{Label: "Note", Kind: FieldText, Unit: "km"},
		Value:         "scratched",
	}
	if got := note.Display(); got != "scratched" {
		t.Errorf("Display() = %q, want %q", got, "scratched")
	}

	yes := OfferField{CategoryField: CategoryField{Kind: FieldBool}, Value: "1"}
	if got := yes.Display(); got != "Yes" {
		t.Errorf("Display() of a true bool = %q, want %q", got, "Yes")
	}
	no := OfferField{CategoryField: CategoryField{Kind: FieldBool}, Value: ""}
	if got := no.Display(); got != "" {
		t.Errorf("Display() of an UNANSWERED bool = %q, want empty — an unanswered "+
			"question is not the same as an answer of No, and rendering it as No is "+
			"how a blank becomes a claim", got)
	}
}
