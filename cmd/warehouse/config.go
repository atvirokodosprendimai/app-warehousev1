package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// config is everything the binary needs from its environment.
//
// It is read once at start-up and validated there, so a misconfiguration is a
// refusal to boot rather than a failure discovered later by a marketplace that
// could not fetch an image.
type config struct {
	// Addr is the listen address.
	Addr string
	// DBPath is the SQLite file. Its directory is created if missing.
	DBPath string
	// PhotosDir is where photo blobs live.
	PhotosDir string
	// PublicBaseURL is the origin a marketplace fetches photos from. It is the
	// one setting that cannot be guessed from the request, because the export is
	// generated for a third party to act on later.
	PublicBaseURL string
	// SecureCookies marks the session cookie Secure. It must be true behind
	// HTTPS and false on a local machine, because a Secure cookie is simply not
	// sent over plain HTTP and every sign-in would appear to fail.
	SecureCookies bool
	// ExportCategory and ExportCondition are eBay's required item fields, which
	// are marketplace-specific and have no sensible universal default.
	ExportCategory  string
	ExportCondition string
	// ExportLocation is the item location eBay shows to buyers.
	ExportLocation string
	// FetchRates turns the daily ECB rate refresh on. Off in tests and in any
	// environment without outbound network access.
	FetchRates bool
}

// loadConfig reads the environment and validates it.
func loadConfig() (config, error) {
	c := config{
		Addr:            env("ADDR", ":8080"),
		DBPath:          env("DB_PATH", "data/warehouse.db"),
		PhotosDir:       env("PHOTOS_DIR", "data/photos"),
		PublicBaseURL:   env("PUBLIC_BASE_URL", "http://localhost:8080"),
		SecureCookies:   envBool("SECURE_COOKIES", false),
		ExportCategory:  env("EBAY_CATEGORY", ""),
		ExportCondition: env("EBAY_CONDITION_ID", "3000"),
		ExportLocation:  env("EBAY_LOCATION", ""),
		FetchRates:      envBool("FETCH_RATES", true),
	}

	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	u, err := url.Parse(c.PublicBaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return config{}, fmt.Errorf(
			"PUBLIC_BASE_URL must be an absolute http(s) URL, got %q — a marketplace "+
				"fetches photos from its own servers and reports nothing when the URL "+
				"does not resolve", c.PublicBaseURL)
	}
	return c, nil
}

// env reads a variable or returns a default.
func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return def
}

// envBool reads a boolean variable. An unparseable value falls back to the
// default rather than failing, because a stray quote in a deployment file should
// not stop the service from starting.
func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return b
}
