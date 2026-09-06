package core

import (
	"fmt"
	"math/big"
	"time"
)

// Rate is one day's exchange rate from [BaseCurrency] into Quote.
//
// The rate is held as a decimal string rather than a float because it is
// persisted, re-read and multiplied against money; a float would round-trip
// through the database differently on different platforms, and a report that
// disagrees with itself between runs is worse than one that is slightly stale.
type Rate struct {
	// AsOf is the day the rate applies to, in YYYY-MM-DD.
	AsOf string
	// Quote is the ISO code being priced, e.g. "USD".
	Quote string
	// Rate is how many units of Quote one unit of BaseCurrency buys.
	Rate string
	// FetchedAt records when it was retrieved, so a stale table is visible.
	FetchedAt time.Time
}

// Convert converts m into target using r, rounding half-up to target's exponent.
//
// It converts only between [BaseCurrency] and r.Quote, in either direction. A
// cross rate (say PLN to USD) would need two rates and a decision about which
// day's pair to use, and doing that implicitly would hide the assumption inside
// a total nobody can reproduce.
func (r Rate) Convert(m Money, target string) (Money, error) {
	if m.Currency == target {
		return m, nil
	}
	rate, ok := new(big.Rat).SetString(r.Rate)
	if !ok || rate.Sign() <= 0 {
		return Money{}, fmt.Errorf("%w: rate %q is not a positive number", ErrBadMoney, r.Rate)
	}

	var factor *big.Rat
	switch {
	case m.Currency == BaseCurrency && target == r.Quote:
		factor = rate
	case m.Currency == r.Quote && target == BaseCurrency:
		factor = new(big.Rat).Inv(rate)
	default:
		return Money{}, fmt.Errorf("%w: rate %s/%s cannot convert %s to %s",
			ErrBadMoney, BaseCurrency, r.Quote, m.Currency, target)
	}

	fromExp, err := Money{Currency: m.Currency}.Exponent()
	if err != nil {
		return Money{}, err
	}
	toExp, err := Money{Currency: target}.Exponent()
	if err != nil {
		return Money{}, err
	}

	// Work in exact rationals: minor -> major -> converted major -> target minor.
	v := new(big.Rat).SetInt64(m.Minor)
	v.Quo(v, ratPow10(fromExp))
	v.Mul(v, factor)
	v.Mul(v, ratPow10(toExp))

	return Money{Minor: roundHalfUp(v), Currency: target}, nil
}

// ratPow10 returns 10^n as a rational.
func ratPow10(n int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	return new(big.Rat).SetInt(p)
}

// roundHalfUp rounds a rational to the nearest integer, halves away from zero.
//
// Half-up is the convention an invoice reader expects; banker's rounding is
// defensible statistically and surprises people reading a single line.
func roundHalfUp(v *big.Rat) int64 {
	neg := v.Sign() < 0
	abs := new(big.Rat).Abs(v)
	abs.Add(abs, big.NewRat(1, 2))

	q := new(big.Int).Quo(abs.Num(), abs.Denom())
	n := q.Int64()
	if neg {
		n = -n
	}
	return n
}
