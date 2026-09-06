package core

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrBadMoney reports a money value that cannot be represented or parsed.
var ErrBadMoney = errors.New("core: invalid money")

// Money is an exact amount in a single currency.
//
// The amount is held in the currency's MINOR units (cents for EUR and USD, whole
// yen for JPY) as an integer. Prices are compared, summed and exported, and a
// binary float cannot represent 0.10 exactly, so a float here would drift by a
// cent under addition — which a customer notices when they add up a column and
// disagree with the total.
type Money struct {
	// Minor is the amount in the currency's smallest unit.
	Minor int64
	// Currency is the uppercase ISO 4217 code, e.g. "EUR".
	Currency string
}

// currencyExponents holds the number of minor-unit digits for the currencies
// this application knows how to format.
//
// It is a small closed table rather than a full ISO 4217 dataset because an
// unknown currency should be a loud parse failure at the edge, not a silently
// mis-scaled price in an export a marketplace will act on.
var currencyExponents = map[string]int{
	"EUR": 2,
	"USD": 2,
	"GBP": 2,
	"PLN": 2,
	"SEK": 2,
	"DKK": 2,
	"NOK": 2,
	"CHF": 2,
	"CZK": 2,
	"JPY": 0,
}

// BaseCurrency is the currency the business keeps its books in. Reports convert
// into it, and it is the base of every stored FX rate.
const BaseCurrency = "EUR"

// KnownCurrencies returns the supported ISO codes, for populating a form.
func KnownCurrencies() []string {
	out := make([]string, 0, len(currencyExponents))
	for c := range currencyExponents {
		out = append(out, c)
	}
	// Sorted so the form's option order is stable between renders; an order that
	// shuffles per request makes a select box unusable with the keyboard.
	sortStrings(out)
	return out
}

// sortStrings is an insertion sort, adequate for a list this size and avoiding a
// sort import for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Exponent returns how many decimal places m's currency has.
func (m Money) Exponent() (int, error) {
	e, ok := currencyExponents[m.Currency]
	if !ok {
		return 0, fmt.Errorf("%w: unknown currency %q", ErrBadMoney, m.Currency)
	}
	return e, nil
}

// IsZero reports whether the amount is zero. A zero Money with no currency is
// the natural "unset price", so callers use this rather than comparing structs.
func (m Money) IsZero() bool { return m.Minor == 0 }

// String renders the amount with its currency, e.g. "12.50 EUR".
func (m Money) String() string {
	s, err := m.Decimal()
	if err != nil {
		return fmt.Sprintf("?%d %s", m.Minor, m.Currency)
	}
	return s + " " + m.Currency
}

// Decimal renders the bare amount with the currency's own number of decimal
// places and a "." separator — the form every marketplace CSV expects, with no
// thousands separator and no currency symbol.
func (m Money) Decimal() (string, error) {
	exp, err := m.Exponent()
	if err != nil {
		return "", err
	}
	neg := m.Minor < 0
	v := m.Minor
	if neg {
		v = -v
	}
	digits := strconv.FormatInt(v, 10)
	if exp == 0 {
		if neg {
			return "-" + digits, nil
		}
		return digits, nil
	}
	// Left-pad so that 5 minor units renders as "0.05" rather than ".5".
	for len(digits) <= exp {
		digits = "0" + digits
	}
	out := digits[:len(digits)-exp] + "." + digits[len(digits)-exp:]
	if neg {
		out = "-" + out
	}
	return out, nil
}

// ParseMoney reads a human-typed decimal amount in the named currency.
//
// It accepts "12.50", "12,50" and "12" — a Lithuanian keyboard produces the
// comma form, and rejecting it would be a data-entry trap rather than a
// validation. It refuses more decimal places than the currency has, because
// silently rounding a price the operator typed is a change they did not make.
func ParseMoney(s, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	exp, ok := currencyExponents[currency]
	if !ok {
		return Money{}, fmt.Errorf("%w: unknown currency %q", ErrBadMoney, currency)
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return Money{}, fmt.Errorf("%w: empty amount", ErrBadMoney)
	}
	s = strings.ReplaceAll(s, ",", ".")
	s = strings.ReplaceAll(s, " ", "")

	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "+")

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if !allDigits(whole) || (hasFrac && !allDigits(frac)) {
		return Money{}, fmt.Errorf("%w: %q is not a number", ErrBadMoney, s)
	}
	if len(frac) > exp {
		return Money{}, fmt.Errorf("%w: %s has at most %d decimal place(s), got %q",
			ErrBadMoney, currency, exp, s)
	}
	for len(frac) < exp {
		frac += "0"
	}

	minor, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrBadMoney, err)
	}
	if neg {
		minor = -minor
	}
	return Money{Minor: minor, Currency: currency}, nil
}

// allDigits reports whether s is non-empty and entirely ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
