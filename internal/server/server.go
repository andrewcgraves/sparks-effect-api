package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/andrewcgraves/sparks-effect-api/internal/config"
	"github.com/andrewcgraves/sparks-effect-api/internal/handler"
	"github.com/andrewcgraves/sparks-effect-api/internal/isochrone"
	"github.com/andrewcgraves/sparks-effect-api/internal/logger"
	"github.com/andrewcgraves/sparks-effect-api/internal/transit"
)

// New builds an *http.Server with all routes registered, ready to be
// started by the caller.
func New(cfg config.Config, store *transit.Store, chainer isochrone.Chainer, lg *logger.Logger) *http.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handler.Health)

	mux.HandleFunc("GET /api/scenarios", handler.Scenarios(store))
	mux.HandleFunc("GET /api/scenarios/{slug}", handler.ScenarioBySlug(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/routes", handler.ScenarioRoutes(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/services", handler.ScenarioServices(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/stations", handler.ScenarioStations(store))
	mux.HandleFunc("GET /api/scenarios/{slug}/travel-times", handler.ScenarioTravelTimes(store))

	mux.HandleFunc("POST /api/isochrone", handler.Isochrone(chainer, lg))

	h := cors(mux, cfg.AllowLocalhostCORS)

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           logRequests(h),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// allowedOrigins are always permitted for CORS, regardless of the
// ALLOW_LOCALHOST_CORS testing flag.
var allowedOrigins = map[string]bool{
	"https://sparks-effect-website.vercel.app": true,
}

func cors(next http.Handler, allowLocalhost bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowedOrigins[origin] || (allowLocalhost && isLocalhostOrigin(origin)) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Add("Vary", "Origin")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalhostOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	host := origin
	if i := strings.Index(origin, "://"); i >= 0 {
		host = origin[i+3:]
	}
	return strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")
}
