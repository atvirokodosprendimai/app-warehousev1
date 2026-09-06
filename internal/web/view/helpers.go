package view

import (
	"encoding/json"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// jsString renders s as a JavaScript string literal for embedding inside a
// datastar expression attribute.
//
// It goes through encoding/json rather than hand-quoting because a title
// containing an apostrophe, a quote, a newline or a lone backslash would
// otherwise break out of the literal and produce an expression datastar cannot
// parse — silently, since a malformed data-signals attribute does not raise
// anything the server can see. templ escapes the result again for the attribute
// context, which is correct and not double-escaping: the browser unescapes the
// HTML layer before datastar ever reads the value.
func jsString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		// json.Marshal only fails here on invalid UTF-8, which cannot reach a
		// signal in a useful form anyway. An empty literal is the safe fallback:
		// it renders a blank field rather than a broken page.
		return `""`
	}
	return string(b)
}

// decimalOrEmpty renders an amount for a text input, or "" when it is unset.
//
// An unset price renders EMPTY rather than "0.00", because a zero in the box is
// a claim that the item is free, and it is the value that would be saved if the
// operator pressed the button without noticing.
func decimalOrEmpty(m core.Money) string {
	if m.IsZero() {
		return ""
	}
	s, err := m.Decimal()
	if err != nil {
		return ""
	}
	return s
}

// currencyOr returns the money's currency, or def when it has none.
func currencyOr(m core.Money, def string) string {
	if m.Currency == "" {
		return def
	}
	return m.Currency
}

// today returns the current UTC date for a date input's default value.
func today() string { return time.Now().UTC().Format("2006-01-02") }
