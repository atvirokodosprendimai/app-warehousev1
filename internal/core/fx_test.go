package core

import "testing"

func TestConvertBothDirections(t *testing.T) {
	r := Rate{AsOf: "2026-09-05", Quote: "USD", Rate: "1.1000"}

	// 100.00 EUR at 1.10 is 110.00 USD.
	got, err := r.Convert(Money{Minor: 10000, Currency: "EUR"}, "USD")
	if err != nil {
		t.Fatalf("EUR->USD: %v", err)
	}
	if got.Minor != 11000 || got.Currency != "USD" {
		t.Errorf("EUR->USD = %+v, want 11000 USD", got)
	}

	// And back again.
	back, err := r.Convert(got, "EUR")
	if err != nil {
		t.Fatalf("USD->EUR: %v", err)
	}
	if back.Minor != 10000 || back.Currency != "EUR" {
		t.Errorf("USD->EUR = %+v, want 10000 EUR", back)
	}
}

func TestConvertRoundsHalfUp(t *testing.T) {
	// 0.01 EUR at 1.005 is 1.005 cents; an invoice reader expects 0.01, not the
	// banker's-rounding 0.00.
	r := Rate{Quote: "USD", Rate: "1.005"}
	got, err := r.Convert(Money{Minor: 1, Currency: "EUR"}, "USD")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1 {
		t.Errorf("got %d minor units, want 1", got.Minor)
	}

	// 0.005 EUR worth exactly half a cent must round away from zero.
	r2 := Rate{Quote: "USD", Rate: "0.5"}
	got2, err := r2.Convert(Money{Minor: 1, Currency: "EUR"}, "USD")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got2.Minor != 1 {
		t.Errorf("half rounded to %d, want 1 (half-up)", got2.Minor)
	}
}

func TestConvertRefusesACrossRate(t *testing.T) {
	// PLN to USD needs two rates and a decision about which day's pair to use.
	// Doing it implicitly would hide that assumption inside an unreproducible total.
	r := Rate{Quote: "USD", Rate: "1.10"}
	if _, err := r.Convert(Money{Minor: 100, Currency: "PLN"}, "USD"); err == nil {
		t.Fatal("a EUR/USD rate was used to convert PLN to USD")
	}
}

func TestConvertIsANoOpForTheSameCurrency(t *testing.T) {
	r := Rate{Quote: "USD", Rate: "1.10"}
	in := Money{Minor: 999, Currency: "EUR"}
	got, err := r.Convert(in, "EUR")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != in {
		t.Errorf("same-currency convert changed the value: %+v -> %+v", in, got)
	}
}

func TestConvertRejectsANonsenseRate(t *testing.T) {
	for _, bad := range []string{"", "abc", "0", "-1.1"} {
		r := Rate{Quote: "USD", Rate: bad}
		if _, err := r.Convert(Money{Minor: 100, Currency: "EUR"}, "USD"); err == nil {
			t.Errorf("rate %q was accepted", bad)
		}
	}
}

func TestConvertHandlesDifferingExponents(t *testing.T) {
	// JPY has no minor unit, so the scale changes as well as the value.
	r := Rate{Quote: "JPY", Rate: "160"}
	got, err := r.Convert(Money{Minor: 1000, Currency: "EUR"}, "JPY") // 10.00 EUR
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 1600 || got.Currency != "JPY" {
		t.Errorf("10.00 EUR at 160 = %+v, want 1600 JPY", got)
	}
}
