package core

import "testing"

func TestValidatePublicBaseURLNormalises(t *testing.T) {
	cases := map[string]string{
		"https://warehouse.example.com":    "https://warehouse.example.com",
		"https://warehouse.example.com/":   "https://warehouse.example.com",
		"  https://warehouse.example.com ": "https://warehouse.example.com",
		"http://192.168.1.20:8080":         "http://192.168.1.20:8080",
	}
	for in, want := range cases {
		got, err := ValidatePublicBaseURL(in)
		if err != nil {
			t.Errorf("ValidatePublicBaseURL(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ValidatePublicBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestValidatePublicBaseURLRefusesWhatCannotWork covers the shapes that would
// produce a broken image address rather than a failed save.
func TestValidatePublicBaseURLRefusesWhatCannotWork(t *testing.T) {
	bad := []string{
		"",                                     // nothing
		"warehouse.example.com",                // no scheme: relative, fails silently at the marketplace
		"ftp://warehouse.example.com",          // a scheme no image fetcher speaks
		"https://",                             // no host
		"https://warehouse.example.com/photos", // a path: addresses are built by appending
		"https://warehouse.example.com?x=1",    // a query, same reason
	}
	for _, in := range bad {
		if got, err := ValidatePublicBaseURL(in); err == nil {
			t.Errorf("ValidatePublicBaseURL(%q) was accepted as %q", in, got)
		}
	}
}

// TestReachablePubliclyIsAWarningNotAValidation pins the deliberate asymmetry:
// a localhost address is ACCEPTED (somebody trying the app on their laptop has a
// perfectly correct one) and REPORTED as unreachable, because the fact only
// becomes consequential at export time.
func TestReachablePubliclyIsAWarningNotAValidation(t *testing.T) {
	local := []string{
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
		"http://warehouse", // a machine name on a LAN, not a resolvable domain
	}
	for _, in := range local {
		if _, err := ValidatePublicBaseURL(in); err != nil {
			t.Errorf("ValidatePublicBaseURL(%q) refused a locally-valid address: %v", in, err)
		}
		if ReachablePublicly(in) {
			t.Errorf("ReachablePublicly(%q) = true; a marketplace cannot fetch from it", in)
		}
	}

	public := []string{
		"https://warehouse.example.com",
		"http://192.168.1.20:8080", // routable on a LAN; we cannot know it is not published
	}
	for _, in := range public {
		if !ReachablePublicly(in) {
			t.Errorf("ReachablePublicly(%q) = false", in)
		}
	}
}

// TestMarketplaceResolveTakesTheDefaultOnlyForEmptyFields pins the precedence
// ADR-016 establishes, field by field rather than value by value.
//
// The reported failure was not a missing value — it was that a value could only
// be supplied by restarting the process, and nothing said so. Making the stored
// settings win PER FIELD is what lets an administrator set the one thing that is
// wrong without having to restate the two that are already right.
func TestMarketplaceResolveTakesTheDefaultOnlyForEmptyFields(t *testing.T) {
	env := Marketplace{Category: "9999", ConditionID: "3000", Location: "Vilnius"}

	t.Run("a stored field wins and an empty one falls through", func(t *testing.T) {
		stored := Marketplace{Category: "11450", Location: "Kaunas"}

		got := stored.Resolve(env)

		if got.Category != "11450" {
			t.Errorf("Category = %q, want the stored %q", got.Category, "11450")
		}
		if got.ConditionID != "3000" {
			t.Errorf("ConditionID = %q, want the environment default %q — an unset field must "+
				"fall through, or setting one value would blank the others",
				got.ConditionID, "3000")
		}
		if got.Location != "Kaunas" {
			t.Errorf("Location = %q, want the stored %q", got.Location, "Kaunas")
		}
	})

	t.Run("nothing stored is the fresh-installation case", func(t *testing.T) {
		got := Marketplace{}.Resolve(env)

		if got != env {
			t.Errorf("Resolve on an empty Marketplace = %+v, want the environment default %+v; "+
				"a fresh installation has no rows and must still export", got, env)
		}
	})

	t.Run("resolving against nothing leaves the stored values alone", func(t *testing.T) {
		stored := Marketplace{Category: "11450", ConditionID: "1000", Location: "Kaunas"}

		if got := stored.Resolve(Marketplace{}); got != stored {
			t.Errorf("Resolve against an empty default = %+v, want %+v", got, stored)
		}
	})
}
