package api

import (
	"errors"
	"net/http"

	"github.com/openhexa-fr/core/es"
	"github.com/openhexa-fr/core/models"

	"github.com/openhexa-fr/essence-back/internal/stations"
)

// search : recherche des stations-service par carburant disponible et rayon
// autour d'une position.
//
// Déclenche, si nécessaire, un rafraîchissement à la demande depuis le flux
// "instantané" (republié toutes les 10 min côté gouvernement) : la recherche en
// cours répond avec les données actuelles, mais garantit qu'une donnée trop
// ancienne ne le reste pas si des recherches ont lieu régulièrement.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	request, problems := parseSearch(r.URL.Query())
	if len(problems) > 0 {
		writeDetail(w, http.StatusUnprocessableEntity, problems)
		return
	}

	s.Refresher.TriggerIfStale()
	page, err := stations.Search(r.Context(), s.ES, s.Index, request.Params, request.SearchAfter, request.Size)
	if err != nil {
		internalError(w, r, err)
		return
	}

	response, err := models.FromPage[stations.Station](page)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// getByID retourne la fiche d'une station par son identifiant.
func (s *Server) getByID(w http.ResponseWriter, r *http.Request) {
	station, err := stations.GetByID(r.Context(), s.ES, s.Index, r.PathValue("station_id"))
	switch {
	case errors.Is(err, es.ErrNotFound):
		writeDetail(w, http.StatusNotFound, "Station introuvable")
	case err != nil:
		internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, station)
	}
}

// domainStatus indique si les données de chaque domaine sont disponibles.
type domainStatus struct {
	Stations bool `json:"stations"`
}

// status : route d'état de disponibilité des données.
//
// Utilisée par le frontend pour masquer un bandeau "synchronisation en cours"
// une fois que l'index stations a reçu au moins un document.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	count, err := es.Count(r.Context(), s.ES, s.Index)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, domainStatus{Stations: count > 0})
}
