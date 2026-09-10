package core

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// SettingPublicBaseURL is the stored key for the public origin.
const SettingPublicBaseURL = "public_base_url"

// The stored keys for eBay's per-run configuration.
//
// They are namespaced by profile because there will be more than one
// marketplace, and an unprefixed "category" would have to mean eBay's number,
// Allegro's category name and Shopify's product type at once.
const (
	SettingEbayCategory    = "ebay.category"
	SettingEbayConditionID = "ebay.condition_id"
	SettingEbayLocation    = "ebay.location"
)

// KnownExportProfiles names every marketplace this application can produce a
// file for, lower case, as the export package registers them.
//
// ⚠ It lives in core rather than in internal/export because the offer aggregate
// has to validate a profile name and no package here imports a sibling. That
// makes two places that know the list, which is a drift risk — so
// internal/export carries a test asserting its registry matches this exactly.
// Add a profile in both, or that test goes red.
func KnownExportProfiles() []string {
	return []string{"ebay", "recar", "shopify"}
}

// ValidExportProfile reports whether name is a profile this application knows.
//
// A typo'd profile is refused rather than stored: it would mint a category row
// that no export will ever read, and nothing would report it — the operator
// would simply see their category ignored for ever.
func ValidExportProfile(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, p := range KnownExportProfiles() {
		if p == name {
			return true
		}
	}
	return false
}

// Marketplace is the per-profile configuration an export needs beyond the
// offers themselves.
//
// It is a value rather than three loose strings so that resolving stored
// settings against start-up defaults is one operation with one rule, rather
// than three call sites each free to get the precedence subtly different.
type Marketplace struct {
	// Category is the marketplace's own category identifier. eBay's is a number
	// from its taxonomy; Allegro's is a category NAME. Stored as text so the
	// schema has no opinion about which.
	Category string
	// ConditionID is eBay's numeric condition code.
	ConditionID string
	// Location is the city an item ships from, which eBay shows on the listing
	// and uses to quote postage.
	Location string
}

// Resolve returns m with any empty field taken from def.
//
// ⚠ PER FIELD, not whole-value. An administrator correcting one wrong setting
// must not have to restate the two that are already right, and a whole-value
// fallback would blank them the moment any single field was stored.
func (m Marketplace) Resolve(def Marketplace) Marketplace {
	if strings.TrimSpace(m.Category) == "" {
		m.Category = def.Category
	}
	if strings.TrimSpace(m.ConditionID) == "" {
		m.ConditionID = def.ConditionID
	}
	if strings.TrimSpace(m.Location) == "" {
		m.Location = def.Location
	}
	return m
}

// Settings are the deployment values an administrator can change while the
// application is running.
//
// They live in the database rather than only in the environment because the one
// that matters is discovered to be wrong AFTER an export has gone out, and the
// person who notices is rarely the person who can restart the process.
type Settings struct {
	// PublicBaseURL is the origin a marketplace fetches photographs from, e.g.
	// "https://warehouse.example.com". Empty means "fall back to the environment".
	PublicBaseURL string
	// Ebay is eBay's per-run configuration. Any empty field means "fall back to
	// the environment", exactly as PublicBaseURL does.
	Ebay Marketplace
	// UpdatedAt is when it last changed, in UTC.
	UpdatedAt time.Time
}

// ValidatePublicBaseURL checks a proposed origin and returns it normalised.
//
// It is strict about the shape it can check and honest about the one it cannot:
// absoluteness is decidable from the string, reachability is not.
func ValidatePublicBaseURL(s string) (string, error) {
	s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "/"))
	if s == "" {
		return "", fmt.Errorf("%w: a public address is required — it is what a "+
			"marketplace fetches photographs from", ErrInvalid)
	}

	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%w: %q must be an absolute address starting with "+
			"https:// or http://, because a marketplace fetches images from its own "+
			"servers and a relative address there fails silently", ErrInvalid, s)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%w: %q should be the bare origin, with no path — "+
			"photo addresses are built by appending to it", ErrInvalid, s)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%w: %q should be the bare origin, with no query or "+
			"fragment", ErrInvalid, s)
	}
	return u.Scheme + "://" + u.Host, nil
}

// LocalOnlyHosts are addresses that are absolute, and valid, and still cannot be
// reached by anybody but this machine.
var localOnlyHosts = []string{"localhost", "127.0.0.1", "::1", "0.0.0.0"}

// ReachablePublicly reports whether an origin could plausibly be fetched from
// outside this machine.
//
// ⚠ This is a WARNING, not a validation, and the difference matters. Somebody
// running the app on their own laptop to try it out has a perfectly correct
// localhost address; refusing it would break the ordinary first-run case to
// guard against a mistake that only bites at export time. So the address is
// accepted and the export screen says plainly that a marketplace will not be
// able to fetch from it — the only place where the fact becomes consequential.
func ReachablePublicly(s string) bool {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	for _, h := range localOnlyHosts {
		if strings.EqualFold(host, h) {
			return false
		}
	}
	// A bare hostname with no dot is a machine name on a local network, not a
	// domain a marketplace can resolve.
	return strings.Contains(host, ".")
}
