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

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/server"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

func main() {
	cfg := config.Load()

	store, err := transit.NewStore()
	if err != nil {
		log.Fatalf("failed to load transit data: %v", err)
	}

	srv := server.New(cfg, store)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
