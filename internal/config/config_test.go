package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ES_URL", "http://localhost:9200")
	for _, key := range []string{"PORT", "DATA_GOUV_LIVE_URL", "POLLING_INTERVAL_SECONDS", "SEARCH_REFRESH_TTL_SECONDS", "ES_INDEX_PREFIX"} {
		t.Setenv(key, "")
	}

	settings, err := Load()

	if err != nil {
		t.Fatal(err)
	}
	if settings.Addr != ":8001" || settings.PollingInterval != 24*time.Hour || settings.SearchRefreshTTL != 10*time.Minute {
		t.Errorf("settings = %+v", settings)
	}
	if settings.StationsAlias() != "openhexa-stations" {
		t.Errorf("alias = %s", settings.StationsAlias())
	}
	if want := "https://data.economie.gouv.fr/api/explore/v2.1/catalog/datasets/prix-des-carburants-en-france-flux-instantane-v2/exports/json"; settings.DataGouvLiveURL != want {
		t.Errorf("URL = %s", settings.DataGouvLiveURL)
	}
}

func TestLoadReadsTheEnvironment(t *testing.T) {
	t.Setenv("ES_URL", "http://es:9200")
	t.Setenv("ES_INDEX_PREFIX", "prod")
	t.Setenv("PORT", "9000")
	t.Setenv("DATA_GOUV_LIVE_URL", "http://source/export")
	t.Setenv("POLLING_INTERVAL_SECONDS", "60")
	t.Setenv("SEARCH_REFRESH_TTL_SECONDS", "30")

	settings, err := Load()

	if err != nil {
		t.Fatal(err)
	}
	if settings.Addr != ":9000" || settings.DataGouvLiveURL != "http://source/export" ||
		settings.PollingInterval != time.Minute || settings.SearchRefreshTTL != 30*time.Second ||
		settings.StationsAlias() != "prod-stations" {
		t.Errorf("settings = %+v", settings)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("ES_URL", "http://localhost:9200")
	t.Setenv("POLLING_INTERVAL_SECONDS", "souvent")
	if _, err := Load(); err == nil {
		t.Error("intervalle non numérique : erreur attendue")
	}

	t.Setenv("POLLING_INTERVAL_SECONDS", "")
	t.Setenv("ES_URL", "")
	if _, err := Load(); err == nil {
		t.Error("ES_URL manquant : erreur attendue")
	}
}
