package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	internlog "github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/server"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	// Installed as the process-wide default too, so code with no logger
	// threaded to it still emits the same leveled JSON through slog's
	// package-level functions (see internal/logger.Init).
	internlog.Init(cfg.LogLevel)
	lg := slog.Default()
	lg.Debug("debug logging enabled")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, repo, cleanup, err := loadStore(ctx, cfg, lg)
	if err != nil {
		lg.Error("failed to load transit data", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	// deps stays nil (not a typed nil) when there is no database, so the server
	// can detect the database-less case and register the auth routes as 503s.
	var deps server.AuthDeps
	if repo != nil {
		deps = repo
		if err := bootstrapAdmin(ctx, cfg, repo, lg); err != nil {
			lg.Error("failed to provision bootstrap admin", "error", err)
			os.Exit(1)
		}
		// Lapsed sessions can no longer authenticate (GetSessionUser filters on
		// expiry), so this is housekeeping, not a security control — it just
		// keeps the table from growing without bound. On boot is enough given
		// the deploy cadence; a periodic reaper would be the next step if the
		// process ever ran for months at a time.
		if n, err := repo.DeleteExpiredSessions(ctx); err != nil {
			// Not fatal: the API is perfectly usable with stale rows present.
			lg.Error("could not prune expired sessions", "error", err)
		} else if n > 0 {
			lg.Info("pruned expired sessions", "count", n)
		}
	}

	// publisher stays nil (not a typed nil) with no broker configured, so the
	// server can register the isochrone routes as 503s. It connects lazily, so
	// constructing one here does not require the broker to be up yet.
	var publisher routing.Publisher
	if cfg.AMQPURL != "" {
		amqpPublisher := routing.NewAMQPPublisher(cfg.AMQPURL, cfg.RoutingQueue, lg)
		defer amqpPublisher.Close()
		publisher = amqpPublisher
		lg.Info("routing jobs will be published", "queue", cfg.RoutingQueue)
	} else {
		lg.Info("AMQP_URL not set; the isochrone endpoints will answer 503")
	}

	srv := server.New(cfg, store, deps, publisher, lg)

	go func() {
		lg.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			lg.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	lg.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		lg.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

// loadStore builds the compiled transit Store. When DATABASE_URL is set it runs
// migrations, seeds the embedded scenario data on first boot, compiles what it
// seeded, and loads rows from Postgres. Otherwise it falls back to the
// read-only embedded YAML store so local dev works without a database, and the
// returned repo is nil — in which case there are no compile jobs and the
// isochrone routes answer 503 (see server.registerCompileRoutes).
// The returned cleanup closes any DB pool.
func loadStore(ctx context.Context, cfg config.Config, lg *slog.Logger) (*transit.Store, *postgres.Repo, func(), error) {
	noop := func() {}

	if cfg.DatabaseURL == "" {
		lg.Info("DATABASE_URL not set; using read-only embedded store (authentication disabled)")
		store, err := transit.NewStore(cfg.BoardingWait)
		return store, nil, noop, err
	}

	if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, nil, noop, err
	}

	repo, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, nil, noop, err
	}

	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	if seeded {
		lg.Info("seeded embedded scenario data into empty database")
	}

	// Compile what was seeded, so a freshly deployed environment can answer the
	// public isochrone without an admin triggering a compile by hand (SPA-181).
	// A scenario that already has a compiled graph is skipped, so a restart
	// against a populated database does no work here.
	//
	// Changing BOARDING_WAIT_POLICY makes every stored ServiceGraph compare
	// unequal (WaitPolicy / WaitSecs), so the first boot after a policy change
	// recompiles once — expected, not a fault (SPA-236).
	compiled, err := transit.CompileSeededIfNeeded(ctx, repo, cfg.BoardingWait)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	if compiled > 0 {
		lg.Info("compiled seeded scenarios", "count", compiled)
	}

	// Curated isochrones ship as repo seed data, so every environment has them
	// with no manual post-deploy step. This runs unconditionally rather than
	// under SeedIfEmpty, which returns early on any populated database and so
	// would never reach a deployed one; idempotency comes from the stable id in
	// each seed file instead. It must run after the compile above, so the
	// service membership it snapshots is the scenario's settled one.
	if err := transit.SeedPrerenderedIsochronesFromEmbedded(ctx, repo); err != nil {
		repo.Close()
		return nil, nil, noop, err
	}

	store, err := transit.LoadStore(ctx, repo, cfg.BoardingWait)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	return store, repo, repo.Close, nil
}

// bootstrapAdmin provisions the first admin account from the environment.
//
// The API is invite-only: accounts exist only because an admin created them,
// which leaves no way to create the first admin. This closes that loop. It is a
// no-op unless both variables are set, and never overwrites an existing
// account — so leaving the variables in place across deploys cannot silently
// reset a password, and rotating one means deleting the account first.
func bootstrapAdmin(ctx context.Context, cfg config.Config, repo *postgres.Repo, lg *slog.Logger) error {
	email := strings.ToLower(strings.TrimSpace(cfg.BootstrapAdminEmail))
	if email == "" || cfg.BootstrapAdminPassword == "" {
		return nil
	}

	if _, exists, err := repo.GetUserByEmail(ctx, email); err != nil {
		return err
	} else if exists {
		lg.Info("bootstrap admin already exists; leaving it unchanged", "email", email)
		return nil
	}

	hash, err := auth.NewHasher(cfg.PasswordHashCost).Hash(cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	id, err := ids.NewUUID()
	if err != nil {
		return err
	}

	if err := repo.CreateUser(ctx, transit.User{
		ID: id, Email: email, Name: "Bootstrap Admin", IsAdmin: true,
	}, hash); err != nil {
		return err
	}
	// The password is never logged, only the address it was applied to.
	lg.Info("provisioned bootstrap admin account", "email", email)
	return nil
}
