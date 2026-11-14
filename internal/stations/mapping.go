package stations

import "github.com/openhexa-fr/core/es"

// Mapping retourne le mapping Elasticsearch explicite du domaine stations-service.
func Mapping() map[string]any {
	return map[string]any{
		"properties": map[string]any{
			"station_id":  es.Keyword(),
			"nom":         es.Keyword(),
			"adresse":     es.Keyword(),
			"ville":       es.Keyword(),
			"code_postal": es.Keyword(),
			"location":    es.GeoPoint(),
			"sp95":        es.Float(),
			"sp98":        es.Float(),
			"e10":         es.Float(),
			"e85":         es.Float(),
			"gazole":      es.Float(),
			"gplc":        es.Float(),
			"mise_a_jour": es.Date(),
			"autoroute":   es.Boolean(),
		},
	}
}
