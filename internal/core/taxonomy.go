package core

import (
	"fmt"
	"strings"
)

// FieldKind is what sort of answer a question expects.
//
// There are five and no more, chosen against the alternative of text-and-choice
// only. [KindNumber] is a separate kind precisely so a mileage can be
// range-filtered and a unit converted later — which a string can never be — even
// though nothing filters on it yet.
type FieldKind string

const (
	// FieldText is a single line, e.g. an engine code.
	FieldText FieldKind = "text"
	// FieldLongText is a paragraph, e.g. what is wrong with the part.
	FieldLongText FieldKind = "longtext"
	// FieldNumber is a quantity, optionally carrying a unit.
	FieldNumber FieldKind = "number"
	// FieldChoice is one of a list the operator writes.
	FieldChoice FieldKind = "choice"
	// FieldBool is yes or no.
	FieldBool FieldKind = "bool"
)

// FieldKinds lists every kind this application defines.
func FieldKinds() []FieldKind {
	return []FieldKind{FieldText, FieldLongText, FieldNumber, FieldChoice, FieldBool}
}

// Valid reports whether k is a kind this application defines.
func (k FieldKind) Valid() bool {
	for _, x := range FieldKinds() {
		if x == k {
			return true
		}
	}
	return false
}

// Label returns a human-readable name for the kind, for a picker.
func (k FieldKind) Label() string {
	switch k {
	case FieldText:
		return "Text"
	case FieldLongText:
		return "Long text"
	case FieldNumber:
		return "Number"
	case FieldChoice:
		return "Choice"
	case FieldBool:
		return "Yes / no"
	default:
		return string(k)
	}
}

// ParseFieldKind validates a kind arriving from outside.
//
// A write of an unknown kind is refused here. That is deliberately NOT symmetric
// with reading: a renderer treats a kind it does not recognise as text rather
// than failing the page, because a row written by a newer binary and read by an
// older one should degrade, not break. Refusing on the way in is what keeps that
// case rare enough to degrade gracefully.
func ParseFieldKind(s string) (FieldKind, error) {
	k := FieldKind(strings.ToLower(strings.TrimSpace(s)))
	if !k.Valid() {
		return "", fmt.Errorf("%w: unknown field kind %q", ErrInvalid, s)
	}
	return k, nil
}

// Category is one node in the operator's own tree of what a thing IS.
//
// ⚠ This is NOT [Offer.Categories], which is where to list an item on each
// marketplace (ADR-016). This tree is the operator's vocabulary and changes when
// they say so; a marketplace's taxonomy is that marketplace's and changes when
// it says so. [Offer.CategoryID] points here.
type Category struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// ParentID is the enclosing node, empty for a root.
	ParentID string
	// Code is the operator's short handle, unique among its siblings and used in
	// the path.
	Code string
	// Path is the materialised address, slash-separated from the root down, e.g.
	// "CAR/ENGINE/TURBO". Unique across the whole tree, and path order is tree
	// order.
	Path string
	// Name is what a human reads, e.g. "Turbocharger".
	Name string
	// Position orders this node among its siblings.
	Position int
}

// Validate checks the rules that hold for any node in the tree.
func (c *Category) Validate() error {
	if strings.TrimSpace(c.Code) == "" {
		return fmt.Errorf("%w: a category needs a code", ErrInvalid)
	}
	if strings.Contains(c.Code, "/") {
		return fmt.Errorf("%w: a category code cannot contain %q, because the path is "+
			"slash-separated and the code would split it", ErrInvalid, "/")
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%w: a category needs a name", ErrInvalid)
	}
	return nil
}

// ChildPath returns the materialised path a child with the given code would have.
func (c *Category) ChildPath(code string) string {
	if c == nil || c.Path == "" {
		return code
	}
	return c.Path + "/" + code
}

// Depth returns how many levels down the tree this node sits, 0 for a root.
func (c *Category) Depth() int {
	if c == nil || c.Path == "" {
		return 0
	}
	return strings.Count(c.Path, "/")
}

// CategoryField is one question a category asks, inherited by every descendant.
type CategoryField struct {
	// ID is the immutable internal identifier (a UUID). Answers are keyed by it,
	// so renaming a field never orphans what people have already typed.
	ID string
	// CategoryID is the node this question hangs off. Every descendant of that
	// node inherits it.
	CategoryID string
	// CategoryPath is the path of that node, carried alongside so a resolved
	// field can say which level it came from without a second read. It is not
	// stored: it is filled in by the read.
	CategoryPath string
	// Code is the stable internal key.
	Code string
	// Label is what an operator and a marketplace both see. ⚠ Renaming it
	// changes a published column name; Code is what never moves.
	Label string
	// Kind is what sort of answer this expects.
	Kind FieldKind
	// Unit is shown beside a number, e.g. "km". Meaningless for other kinds.
	Unit string
	// Options is the newline-separated list a choice picks from. Meaningless for
	// other kinds.
	Options string
	// Required is a hint to the operator, never a gate — ADR-004 and ADR-019
	// both establish that a value the operator does not have yet must not be
	// demanded at the shelf.
	Required bool
	// Export says whether this field leaves the building in a CSV. False by
	// default: a private note is not something to publish.
	Export bool
	// Position orders this field among the others on the same category.
	Position int
}

// Validate checks the rules that hold for any field.
func (f *CategoryField) Validate() error {
	if strings.TrimSpace(f.Code) == "" {
		return fmt.Errorf("%w: a field needs a code", ErrInvalid)
	}
	if strings.TrimSpace(f.Label) == "" {
		return fmt.Errorf("%w: a field needs a label", ErrInvalid)
	}
	if !f.Kind.Valid() {
		return fmt.Errorf("%w: unknown field kind %q", ErrInvalid, f.Kind)
	}
	if f.Kind == FieldChoice && len(f.OptionList()) == 0 {
		return fmt.Errorf("%w: a choice field needs at least one option, or nobody can "+
			"answer it", ErrInvalid)
	}
	return nil
}

// OptionList splits Options into the choices an operator wrote, dropping blank
// lines so a stray newline does not become an empty option nobody can pick.
func (f *CategoryField) OptionList() []string {
	out := []string{}
	for _, line := range strings.Split(f.Options, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// OfferField is one resolved question together with this offer's answer to it.
//
// The embedded [CategoryField] is the question as it was defined on whichever
// ancestor defined it; Value is what was typed against it, empty when nothing
// has been.
type OfferField struct {
	CategoryField
	// Value is the answer, stored as text whatever the kind. A bool is "1" or
	// empty; a number is the digits the operator typed.
	Value string
}

// Answered reports whether anything has been typed against this question.
func (f OfferField) Answered() bool { return strings.TrimSpace(f.Value) != "" }

// Bool reads the value as a yes/no answer.
func (f OfferField) Bool() bool { return f.Value == "1" }

// Display renders the answer for a listing or an export cell, appending the unit
// where there is one so "180000" reads as "180000 km".
func (f OfferField) Display() string {
	v := strings.TrimSpace(f.Value)
	if v == "" {
		return ""
	}
	if f.Kind == FieldBool {
		if f.Bool() {
			return "Yes"
		}
		return "No"
	}
	if f.Kind == FieldNumber && strings.TrimSpace(f.Unit) != "" {
		return v + " " + strings.TrimSpace(f.Unit)
	}
	return v
}
