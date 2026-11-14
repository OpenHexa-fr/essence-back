package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// cors : API publique en lecture seule (données ouvertes, pas de cookies ni de
// session) : CORS permissif, nécessaire puisque le frontend est servi sur une
// origine distincte. Même comportement que le CORSMiddleware de l'API Python
// (origines `*`, méthode GET, en-têtes quelconques).
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if r.Method == http.MethodOptions && requestedMethod != "" {
			// Requête de pré-vérification (preflight).
			if requestedMethod != http.MethodGet {
				http.Error(w, "Disallowed CORS method", http.StatusBadRequest)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET")
			w.Header().Set("Access-Control-Max-Age", "600")
			if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
				w.Header().Set("Access-Control-Allow-Headers", headers)
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// accessLog journalise chaque requête (méthode, chemin, statut, durée).
func accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		slog.Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"query", strings.TrimSpace(r.URL.RawQuery),
		)
	})
}
