// Package api expose l'API HTTP de l'application Essence : routes
// stations-service, état de disponibilité des données, CORS et logs d'accès.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/elastic/go-elasticsearch/v8"
)

// Refresher déclenche, si nécessaire, un rafraîchissement des données.
type Refresher interface{ TriggerIfStale() }

// Server regroupe les dépendances des handlers.
type Server struct {
	ES        *elasticsearch.Client
	Refresher Refresher
	// Index est l'alias de l'index des stations (ex. "openhexa-stations").
	Index string
}

// Handler retourne le routeur complet, middlewares inclus.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Les routes sont enregistrées dans le même préfixe que l'API Python (/api/v1).
	mux.HandleFunc("GET /api/v1/stations/search", s.search)
	mux.HandleFunc("GET /api/v1/stations/{station_id}", s.getByID)
	mux.HandleFunc("GET /api/v1/status", s.status)

	return accessLog(cors(mux))
}

// writeJSON écrit une réponse JSON.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("response_encoding_failed", "error", err.Error())
	}
}

// writeDetail écrit une erreur au format `{"detail": ...}` de FastAPI, que les
// clients de l'API Python savent déjà lire.
func writeDetail(w http.ResponseWriter, status int, detail any) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request_failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
	writeDetail(w, http.StatusInternalServerError, "Internal Server Error")
}
