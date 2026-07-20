package main

import (
	"context"
	"errors"
	"log"
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
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	internlog "github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/persistence/postgres"
	"github.com/andrewcgraves/sparks-effect-api/internal/server"
	"github.com/andrewcgraves/sparks-effect-api/internal/stadia"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()
	if cfg.StadiaAPIKey == "" {
		log.Fatal("STADIA_API_KEY must be set")
	}

	lg := internlog.Default(cfg.Debug)
	if cfg.Debug {
		lg.Printf("debug logging enabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, repo, cleanup, err := loadStore(ctx, cfg, lg)
	if err != nil {
		log.Fatalf("failed to load transit data: %v", err)
	}
	defer cleanup()

	// deps stays nil (not a typed nil) when there is no database, so the server
	// can detect the database-less case and register the auth routes as 503s.
	var deps server.AuthDeps
	if repo != nil {
		deps = repo
		if err := bootstrapAdmin(ctx, cfg, repo, lg); err != nil {
			log.Fatalf("failed to provision bootstrap admin: %v", err)
		}
	}

	stadiaClient := stadia.NewHTTPClient(cfg.StadiaAPIKey).WithLogger(lg)
	isoChainer := isochrone.New(stadiaClient, store, lg)

	srv := server.New(cfg, store, deps, isoChainer, lg)

	go func() {
		log.Printf("listening on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
}

// loadStore builds the compiled transit Store. When DATABASE_URL is set it runs
// migrations, seeds the embedded scenario data on first boot, and loads rows
// from Postgres. Otherwise it falls back to the read-only embedded YAML store so
// local dev works without a database, and the returned repo is nil.
// The returned cleanup closes any DB pool.
func loadStore(ctx context.Context, cfg config.Config, lg *internlog.Logger) (*transit.Store, *postgres.Repo, func(), error) {
	noop := func() {}

	if cfg.DatabaseURL == "" {
		lg.Printf("DATABASE_URL not set; using read-only embedded store (authentication disabled)")
		store, err := transit.NewStore()
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
		lg.Printf("seeded embedded scenario data into empty database")
	}

	store, err := transit.LoadStore(ctx, repo)
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
func bootstrapAdmin(ctx context.Context, cfg config.Config, repo *postgres.Repo, lg *internlog.Logger) error {
	email := strings.ToLower(strings.TrimSpace(cfg.BootstrapAdminEmail))
	if email == "" || cfg.BootstrapAdminPassword == "" {
		return nil
	}

	if _, exists, err := repo.GetUserByEmail(ctx, email); err != nil {
		return err
	} else if exists {
		lg.Printf("bootstrap admin %s already exists; leaving it unchanged", email)
		return nil
	}

	hash, err := auth.HashPassword(cfg.BootstrapAdminPassword)
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
	log.Printf("provisioned bootstrap admin account %s", email)
	return nil
}
