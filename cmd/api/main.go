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

	"github.com/andrewcgraves/sparks-effect-api/internal/account"
	"github.com/andrewcgraves/sparks-effect-api/internal/auth"
	"github.com/andrewcgraves/sparks-effect-api/internal/compile"
	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/ids"
	internlog "github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/routing"
	"github.com/andrewcgraves/sparks-effect-api/internal/server"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
	"github.com/joho/godotenv"
)

func main() {
	startedAt := time.Now()
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

	store, repo, cleanup, err := loadStore(ctx, cfg, startedAt, lg)
	if err != nil {
		lg.Error("failed to load transit data", "error", err)
		os.Exit(1)
	}
	defer cleanup()

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

	var publisher routing.Publisher
	if cfg.AMQPURL != "" {
		amqpPublisher := routing.NewAMQPPublisher(cfg.AMQPURL, cfg.RoutingQueue, lg)
		defer amqpPublisher.Close()
		publisher = amqpPublisher
		lg.Info("routing jobs will be published", "queue", cfg.RoutingQueue)
	} else {
		lg.Info("AMQP_URL not set; the isochrone endpoints will answer 503")
	}

	if cfg.DatabaseURL != "" && cfg.WorkerToken == "" {
		lg.Info("WORKER_TOKEN not set; the routing worker endpoints will answer 503")
	}

	compiles := compile.NewRunner(deps, cfg.BoardingWait)
	srv := server.New(cfg, store, deps, publisher, compiles, lg)

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

	// Handlers first, so no request can enqueue a compile after the drain has
	// begun; then the compiles they enqueued, inside the same grace period.
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		lg.Error("graceful shutdown failed", "error", shutdownErr)
	}
	if abandoned := compiles.Drain(shutdownCtx); len(abandoned) > 0 {
		lg.Warn("abandoned in-flight compiles at shutdown; the next boot fails them",
			"count", len(abandoned), "job_ids", abandoned)
	}
	if shutdownErr != nil {
		os.Exit(1)
	}
}

func loadStore(ctx context.Context, cfg config.Config, startedAt time.Time, lg *slog.Logger) (*transit.Store, *postgres.Repo, func(), error) {
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

	// A deploy or crash mid-compile leaves its job queued or running with
	// nothing left to finish it (SPA-431). Swept before the seeded compile
	// below enqueues jobs of its own. Not fatal: the stuck rows only mislead
	// whoever polls them.
	if n, err := repo.FailInterruptedJobs(ctx, startedAt); err != nil {
		lg.Error("could not fail interrupted compile jobs", "error", err)
	} else if n > 0 {
		lg.Warn("failed compile jobs interrupted by a restart", "count", n)
	}

	// YAML is the source of truth for seeded rows. SeedIfEmpty only fills an
	// empty database; ReconcileSeed upserts embedded rows whose content no
	// longer matches, so a YAML correction reaches a deployed database without
	// a data-correction migration (SPA-285). Authored rows are out of scope.
	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	if seeded {
		lg.Info("seeded embedded scenario data into empty database")
	}
	reconciled, err := transit.ReconcileSeed(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	if reconciled > 0 {
		lg.Info("reconciled embedded seed", "rows", reconciled)
	}

	// Compile what was seeded, so a freshly deployed environment can answer the
	// public isochrone without an admin triggering a compile by hand (SPA-181).
	// A scenario that already has a compiled graph is skipped, so a restart
	// against a populated database does no work here. A reconcile that changed
	// source rows makes sameCompiledGraph fail, so the next line recompiles.
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
	// would never reach a deployed one (ReconcileSeed does not insert
	// prerendered isochrones); idempotency comes from the stable id in
	// each seed file instead. It must run after the compile above, so the
	// service membership it snapshots is the scenario's settled one.
	if err := transit.SeedPrerenderedIsochronesFromEmbedded(ctx, repo); err != nil {
		repo.Close()
		return nil, nil, noop, err
	}

	store, err := transit.LoadStore(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, nil, noop, err
	}
	return store, repo, repo.Close, nil
}

// bootstrapUserStore is the persistence bootstrapAdmin needs. *postgres.Repo
// satisfies it; tests pass a fake so a rejected password never needs a database.
type bootstrapUserStore interface {
	GetUserByEmail(ctx context.Context, email string) (account.User, bool, error)
	CreateUser(ctx context.Context, u account.User, passwordHash string) error
}

func bootstrapAdmin(ctx context.Context, cfg config.Config, repo bootstrapUserStore, lg *slog.Logger) error {
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

	if err := auth.ValidatePassword(cfg.BootstrapAdminPassword, email); err != nil {
		return err
	}

	hash, err := auth.NewHasher(cfg.PasswordHashCost).Hash(cfg.BootstrapAdminPassword)
	if err != nil {
		return err
	}
	id, err := ids.NewUUID()
	if err != nil {
		return err
	}

	if err := repo.CreateUser(ctx, account.User{
		ID: id, Email: email, Name: "Bootstrap Admin", IsAdmin: true,
	}, hash); err != nil {
		return err
	}
	// The password is never logged, only the address it was applied to.
	lg.Info("provisioned bootstrap admin account", "email", email)
	return nil
}
