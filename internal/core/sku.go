package core

import (
	"fmt"
	"strconv"
	"strings"
)

// SequenceOfferSKU names the counter that offer references are drawn from.
const SequenceOfferSKU = "offer_sku"

// SKUPrefix is what every generated reference starts with.
const SKUPrefix = "WH"

// skuDigits is the zero-padded width of a generated reference.
//
// Seven is chosen for the hand: WH0000001 is nine characters, which fits on a
// small label, survives being read aloud, and groups naturally when scanned down
// a column. It is a MINIMUM rather than a cap — the millionth item simply gets a
// longer reference rather than a wrapped one, because silently reusing a number
// that is already written on a box is far worse than an uneven column.
const skuDigits = 7

// FormatSKU renders a sequence value as an offer reference.
//
// It is here rather than in the offer package because the submission aggregate
// also mints references when it converts a proposal, and two packages
// formatting the same identifier independently is exactly how two formats end up
// in one warehouse.
func FormatSKU(n int64) string {
	return fmt.Sprintf("%s%0*d", SKUPrefix, skuDigits, n)
}

// ParseSKU returns the sequence number in a generated reference.
//
// It exists so that an operator typing "1", "WH1" or "wh0000001" into a search
// box finds WH0000001. Somebody reading a number off a box will not reproduce the
// padding, and a search that only matches the exact stored string makes the short
// reference no easier to use than the long one it replaced.
func ParseSKU(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	digits := strings.TrimPrefix(strings.ToUpper(s), SKUPrefix)
	if digits == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// NormalizeSKUQuery expands what somebody typed into the canonical reference,
// when it looks like one, and returns "" when it does not.
//
// "42" becomes "WH0000042"; "brass lamp" stays empty and is searched as text.
func NormalizeSKUQuery(s string) string {
	n, ok := ParseSKU(s)
	if !ok {
		return ""
	}
	return FormatSKU(n)
}
