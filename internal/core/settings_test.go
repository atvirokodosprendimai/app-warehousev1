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
