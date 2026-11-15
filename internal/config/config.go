// Package config lit la configuration de l'API Essence : paramètres
// Elasticsearch (via core) + polling.
//
// Source des prix : l'API JSON `data.economie.gouv.fr` (dataset "Prix des
// carburants en France - Flux instantané - v2"), reprise du client
// gouvernemental du projet Pump-price (https://github.com/Draclest/Pump-price)
// plutôt que le flux ZIP/XML `donnees.roulez-eco.fr` utilisé auparavant : un
// enregistrement par station, prix déjà aplatis par carburant (`gazole_prix`,
// `sp95_prix`, ...) et coordonnées en degrés décimaux, sans les écueils du flux
// XML (coordonnées à rescaler, dates non-ISO). Le jeu de données est republié
// toutes les 10 minutes côté gouvernement : c'est cette cadence que
// `SEARCH_REFRESH_TTL_SECONDS` exploite pour le rafraîchissement à la demande
// (voir `stations.Refresher`).
package config

import (
	"time"

	core "github.com/openhexa-fr/core/config"
)

const defaultDataGouvLiveURL = "https://data.economie.gouv.fr/api/explore/v2.1/catalog/datasets/" +
	"prix-des-carburants-en-france-flux-instantane-v2/exports/json"

// Settings est la configuration complète de l'API.
type Settings struct {
	ES core.ESSettings

	// Addr est l'adresse d'écoute HTTP (variable PORT, 8001 par défaut).
	Addr string

	DataGouvLiveURL string

	// PollingInterval est l'intervalle de la synchronisation régulière de fond
	// (filet de sécurité) : quotidienne par défaut, car le rafraîchissement à
	// la demande (déclenché par les recherches) couvre déjà la fraîcheur
	// intra-journée, alignée sur les 10 min du flux source.
	PollingInterval time.Duration
	// SearchRefreshTTL est l'âge à partir duquel une recherche déclenche un
	// rafraîchissement à la demande.
	SearchRefreshTTL time.Duration

	// HistoryRetention est la durée de conservation de l'historique des prix
	// (variable HISTORY_RETENTION, au format Elasticsearch : "730d", "5y"...).
	// Vide par défaut : conservation illimitée.
	HistoryRetention string
}

// Load lit la configuration depuis l'environnement.
func Load() (Settings, error) {
	es, err := core.LoadESSettings()
	if err != nil {
		return Settings{}, err
	}
	polling, err := core.Int("POLLING_INTERVAL_SECONDS", 24*3600)
	if err != nil {
		return Settings{}, err
	}
	ttl, err := core.Int("SEARCH_REFRESH_TTL_SECONDS", 600)
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		ES:               es,
		Addr:             ":" + core.String("PORT", "8001"),
		DataGouvLiveURL:  core.String("DATA_GOUV_LIVE_URL", defaultDataGouvLiveURL),
		PollingInterval:  time.Duration(polling) * time.Second,
		SearchRefreshTTL: time.Duration(ttl) * time.Second,
		HistoryRetention: core.String("HISTORY_RETENTION", ""),
	}, nil
}

// StationsAlias est l'alias de l'index des stations pour le préfixe donné.
func (s Settings) StationsAlias() string { return s.ES.IndexPrefix + "-stations" }

// HistoryStream est le data stream de l'historique des prix pour le préfixe donné.
func (s Settings) HistoryStream() string { return s.ES.IndexPrefix + "-prix-historique" }
