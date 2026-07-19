package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
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

	store, cleanup, err := loadStore(ctx, cfg, lg)
	if err != nil {
		log.Fatalf("failed to load transit data: %v", err)
	}
	defer cleanup()

	stadiaClient := stadia.NewHTTPClient(cfg.StadiaAPIKey).WithLogger(lg)
	isoChainer := isochrone.New(stadiaClient, store, lg)

	srv := server.New(cfg, store, isoChainer, lg)

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
// local dev works without a database. The returned cleanup closes any DB pool.
func loadStore(ctx context.Context, cfg config.Config, lg *internlog.Logger) (*transit.Store, func(), error) {
	noop := func() {}

	if cfg.DatabaseURL == "" {
		lg.Printf("DATABASE_URL not set; using read-only embedded store")
		store, err := transit.NewStore()
		return store, noop, err
	}

	if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
		return nil, noop, err
	}

	repo, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return nil, noop, err
	}

	seeded, err := transit.SeedIfEmpty(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, noop, err
	}
	if seeded {
		lg.Printf("seeded embedded scenario data into empty database")
	}

	store, err := transit.LoadStore(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, noop, err
	}
	return store, repo.Close, nil
}
