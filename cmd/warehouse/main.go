// Command warehouse runs the warehouse and listing application.
//
// One binary, one SQLite file, one directory of photographs. It migrates its own
// schema on start-up, so deploying is copying the binary and restarting it.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/auth"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/blob"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/cart"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/export"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/fx"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/location"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/offer"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/sequence"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/settings"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/submission"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/taxonomy"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run wires the application and serves until interrupted.
//
// It is separate from main so that every failure path returns an error instead
// of calling os.Exit from somewhere deep, which would skip the deferred closes.
func run(log *slog.Logger) error {
	// Before the configuration is read, not after: loadConfig reads the process
	// environment, so a .env that arrived later would have no effect at all.
	//
	// ENV_FILE names another path, and is itself read from the real environment
	// because a setting that says where to find the settings cannot live in the
	// file it points at.
	envFile := defaultEnvFile
	if v, ok := os.LookupEnv("ENV_FILE"); ok && strings.TrimSpace(v) != "" {
		envFile = v
	}
	if err := loadDotEnv(envFile); err != nil {
		return err
	}
	// Say so when a file was used. "Which config is this process actually
	// running on" is the first question of every deployment problem, and a
	// silently-loaded file is the reason it is hard to answer.
	if _, err := os.Stat(envFile); err == nil {
		log.Info("configuration file loaded", "path", envFile,
			"note", "it fills gaps only — a real environment variable wins over it")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		return err
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Migrations run on the WRITER handle. The reader carries query_only(1) and
	// would be refused by the driver, which is the guarantee working.
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		return err
	}
	if v, err := store.Version(db.Write); err == nil {
		log.Info("schema ready", "version", v)
	}

	blobs, err := blob.NewDir(cfg.PhotosDir)
	if err != nil {
		return err
	}

	// Repositories. Each takes both handles: reads go to one, writes to the
	// other, and the reader is read-only at the driver.
	users := auth.NewRepo(db.Read, db.Write)
	offers := offer.NewRepo(db.Read, db.Write)
	locations := location.NewRepo(db.Read, db.Write)
	taxonomies := taxonomy.NewRepo(db.Read, db.Write)
	rates := fx.NewRepo(db.Read, db.Write)
	carts := cart.NewRepo(db.Read, db.Write)
	subs := submission.NewRepo(db.Read, db.Write)
	conf := settings.NewRepo(db.Read, db.Write)

	// Services own the write rules.
	authSvc := auth.NewService(users)
	// One allocator, shared by both places that mint a reference, so intake and a
	// converted submission draw from the same counter and their labels are
	// indistinguishable.
	seq := sequence.NewRepo(db.Write)
	offerSvc := offer.NewService(offers, blobs, seq)
	locationSvc := location.NewService(locations)
	taxonomySvc := taxonomy.NewService(taxonomies)
	cartSvc := cart.NewService(carts)
	fxSvc := fx.NewService(rates, fx.NewClient(nil, ""))
	// The offer REPOSITORY is handed to the submission service, not the offer
	// service. Conversion is the one place these two aggregates touch, and the
	// submission package states that collaboration as a two-method interface the
	// repo already satisfies — so the dependency is exactly as wide as the
	// collaboration rather than the whole of the offer write side.
	submissionSvc := submission.NewService(subs, offers, blobs, seq)

	if cfg.FetchRates {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		stop := fxSvc.StartDaily(ctx)
		defer stop()
	} else {
		log.Info("daily rate refresh disabled")
	}

	app := &web.App{
		Sessions: web.NewSessions(db.Write, cfg.SecureCookies),
		Log:      log,
		Cfg: web.Config{
			PublicBaseURL: cfg.PublicBaseURL,
			Export: export.Options{
				Currency:    "EUR",
				Category:    cfg.ExportCategory,
				ConditionID: cfg.ExportCondition,
				Location:    cfg.ExportLocation,
			},
		},
		Users:       users,
		Offers:      offers,
		Locations:   locations,
		Taxonomies:  taxonomies,
		Rates:       rates,
		Carts:       carts,
		Submissions: subs,
		Settings:    conf,
		Auth:        authSvc,
		Offer:       offerSvc,
		Location:    locationSvc,
		Taxonomy:    taxonomySvc,
		Cart:        cartSvc,
		Submission:  submissionSvc,
		Blobs:       blobs,
		Bus:         web.NewBus(),
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Routes(),
		ReadHeaderTimeout: web.ReadHeaderTimeout,
		// ⚠ WriteTimeout is deliberately ZERO. It applies to the whole response,
		// and an SSE stream is a response that lasts as long as the page is open —
		// so any non-zero value kills every healthy stream at that mark, silently.
		// Per-request read deadlines guard slow bodies instead.
	}

	// Report the bootstrap state at start-up. On a fresh install the operator
	// needs to know the registration page is open, and that it closes for good
	// once used.
	if open, err := authSvc.BootstrapOpen(context.Background()); err == nil && open {
		log.Info("no accounts yet — the first account created at /bootstrap becomes the administrator",
			"url", cfg.PublicBaseURL+"/bootstrap")
	}

	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr, "public", cfg.PublicBaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errs:
		return err
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
	}

	// Give in-flight requests a moment. Open SSE streams end as soon as their
	// request context is cancelled, which Shutdown does, so this is bounded by
	// the ordinary requests rather than by the streams.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
