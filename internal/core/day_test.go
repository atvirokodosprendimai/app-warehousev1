package core

import (
	"testing"
	"time"
)

// TestNormalizeDayRejectsAnUnpaddedDate is the whole reason NormalizeDay exists.
//
// A rate lookup bounds as_of with a TEXT comparison in SQL, so "2026-9-6" sorts
// AFTER "2026-09-06" — meaning a query asked for "on or before 2026-9-6" would
// happily match rows from October and return the wrong rate rather than fail.
// The failure is silent and produces a plausible number, which is the worst
// shape a money bug can have.
func TestNormalizeDayRejectsAnUnpaddedDate(t *testing.T) {
	for _, bad := range []string{"2026-9-6", "2026-09-6", "2026-9-06"} {
		if _, err := NormalizeDay(bad); err == nil {
			t.Errorf("NormalizeDay(%q) was accepted; as a text bound it sorts after a "+
				"zero-padded date and would select the wrong rate", bad)
		}
	}

	// The property the test above is really about: string ordering disagrees with
	// date ordering for these two spellings of the same day. If this ever stops
	// being true the guard is unnecessary — and this line says so out loud rather
	// than leaving the guard as folklore.
	if !("2026-9-6" > "2026-09-06") {
		t.Fatal("the premise no longer holds: an unpadded date no longer sorts after a " +
			"padded one, so NormalizeDay's justification needs rewriting")
	}
}

func TestNormalizeDayRejectsNonDates(t *testing.T) {
	for _, bad := range []string{"", "today", "2026/09/06", "06-09-2026", "2026-13-01", "2026-09-06T00:00:00Z"} {
		if _, err := NormalizeDay(bad); err == nil {
			t.Errorf("NormalizeDay(%q) was accepted", bad)
		}
	}
}

func TestNormalizeDayAcceptsCanonical(t *testing.T) {
	got, err := NormalizeDay("2026-09-06")
	if err != nil {
		t.Fatalf("NormalizeDay: %v", err)
	}
	if got != "2026-09-06" {
		t.Errorf("got %q", got)
	}
}

func TestDayIsUTCAndCanonical(t *testing.T) {
	// A timestamp late in the day in a positive-offset zone is the case that
	// separates "format it" from "convert it first": 2026-09-06 23:30 +03:00 is
	// still 2026-09-06 in UTC, but 2026-09-07 01:30 +03:00 is the day before.
	zone := time.FixedZone("EEST", 3*60*60)
	when := time.Date(2026, 9, 7, 1, 30, 0, 0, zone)

	if got := Day(when); got != "2026-09-06" {
		t.Errorf("Day = %q, want 2026-09-06 — the rate day is a UTC day, and taking the "+
			"local date would attribute a sale to the wrong day's rate", got)
	}
	if _, err := NormalizeDay(Day(when)); err != nil {
		t.Errorf("Day produced something NormalizeDay rejects: %v", err)
	}
}
