package core

import "testing"

func TestParseMoneyAcceptsBothDecimalSeparators(t *testing.T) {
	// A Lithuanian keyboard produces the comma form; rejecting it would be a
	// data-entry trap rather than a validation.
	for _, in := range []string{"12.50", "12,50", " 12.50 "} {
		got, err := ParseMoney(in, "EUR")
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", in, err)
		}
		if got.Minor != 1250 || got.Currency != "EUR" {
			t.Errorf("ParseMoney(%q) = %+v, want 1250 EUR", in, got)
		}
	}
}

func TestParseMoneyRefusesExcessPrecision(t *testing.T) {
	// Silently rounding a price the operator typed is a change they did not make,
	// and it would ship to a marketplace.
	if _, err := ParseMoney("12.505", "EUR"); err == nil {
		t.Fatal("ParseMoney accepted three decimals for EUR; the extra digit would be " +
			"silently dropped into a published price")
	}
	// JPY has no minor unit at all, so even one decimal is too many.
	if _, err := ParseMoney("100.5", "JPY"); err == nil {
		t.Fatal("ParseMoney accepted a decimal for JPY, which has no minor unit")
	}
	if _, err := ParseMoney("100", "JPY"); err != nil {
		t.Fatalf("ParseMoney rejected a whole number of yen: %v", err)
	}
}

func TestParseMoneyRefusesUnknownCurrency(t *testing.T) {
	if _, err := ParseMoney("10.00", "XYZ"); err == nil {
		t.Fatal("ParseMoney accepted an unknown currency; its exponent would be guessed " +
			"and the exported price mis-scaled")
	}
}

func TestDecimalPadsSubUnitAmounts(t *testing.T) {
	cases := map[string]Money{
		"0.05":   {Minor: 5, Currency: "EUR"},
		"0.50":   {Minor: 50, Currency: "EUR"},
		"12.50":  {Minor: 1250, Currency: "EUR"},
		"-12.50": {Minor: -1250, Currency: "EUR"},
		"1000":   {Minor: 1000, Currency: "JPY"},
	}
	for want, m := range cases {
		got, err := m.Decimal()
		if err != nil {
			t.Fatalf("Decimal(%+v): %v", m, err)
		}
		if got != want {
			t.Errorf("Decimal(%+v) = %q, want %q", m, got, want)
		}
	}
}

func TestParseMoneyRoundTrips(t *testing.T) {
	// A formatter and a parser that disagree produce a value that changes every
	// time a form is re-saved, which is invisible until it has drifted.
	for _, in := range []string{"0.01", "0.99", "1.00", "1999.95"} {
		m, err := ParseMoney(in, "EUR")
		if err != nil {
			t.Fatalf("ParseMoney(%q): %v", in, err)
		}
		out, err := m.Decimal()
		if err != nil {
			t.Fatalf("Decimal: %v", err)
		}
		if out != in {
			t.Errorf("round trip %q -> %d -> %q", in, m.Minor, out)
		}
	}
}
