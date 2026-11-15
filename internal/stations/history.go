package stations

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/elastic/go-elasticsearch/v8"

	"github.com/openhexa-fr/core/es"
)

// Historique des prix carburants.
//
// L'index des stations ne garde que le dernier prix connu : chaque ingestion
// l'écrase. L'historique conserve, dans un data stream, un document par
// relevé de prix, c'est-à-dire par (station, carburant, date de mise à jour
// publiée par la station). `@timestamp` est cette date de mise à jour, et non
// la date d'ingestion : la courbe d'un prix suit donc ses changements réels.
//
// Le flux source est republié toutes les 10 minutes mais un prix ne change
// que quelques fois par jour : la plupart des relevés reçus sont déjà
// enregistrés. L'`_id` est déterministe (station, carburant, date) et les
// relevés connus sont écartés avant écriture (voir RecordHistory) : réingérer
// le même flux n'ajoute rien.

// HistoryMapping retourne le mapping Elasticsearch du data stream de l'historique.
func HistoryMapping() map[string]any {
	return map[string]any{
		"dynamic": "strict",
		"properties": map[string]any{
			"@timestamp":  es.Date(),
			"station_id":  es.Keyword(),
			"carburant":   es.Keyword(),
			"prix":        es.Float(),
			"ville":       es.Keyword(),
			"code_postal": es.Keyword(),
			"location":    es.GeoPoint(),
			"autoroute":   es.Boolean(),
		},
	}
}

// parseHistory retourne les relevés de prix d'un enregistrement : un document
// par carburant ayant à la fois un prix et une date de mise à jour.
func parseHistory(record Record) ([]es.Document, error) {
	stationID := recordStationID(record)
	if stationID == "" {
		return nil, nil
	}
	location, err := parseLocation(record)
	if err != nil {
		return nil, err
	}

	var documents []es.Document
	for _, fuel := range fuelKeys {
		value, maj := record[fuel+"_prix"], record[fuel+"_maj"]
		if value == nil || !truthy(maj) {
			continue
		}
		price, err := toFloat(value)
		if err != nil {
			return nil, err
		}
		timestamp, ok := maj.(string)
		if !ok {
			return nil, fmt.Errorf("date de mise à jour non textuelle : %v", maj)
		}
		documents = append(documents, es.Document{
			"_id":         es.MakeDocumentID(stationID, fuel, timestamp),
			"@timestamp":  timestamp,
			"station_id":  stationID,
			"carburant":   fuel,
			"prix":        price,
			"ville":       record["ville"],
			"code_postal": record["cp"],
			"location":    location,
			"autoroute":   record["pop"] == "A",
		})
	}
	return documents, nil
}

// ParseHistory extrait les relevés de prix du flux instantané.
//
// Une station dont les attributs sont malformés est ignorée, comme dans ParseRecords.
func ParseHistory(records []Record) []es.Document {
	var documents []es.Document
	for _, record := range records {
		parsed, err := parseHistory(record)
		if err != nil {
			slog.Warn("station_history_parse_skipped", "station_id", record["id"], "error", err.Error())
			continue
		}
		documents = append(documents, parsed...)
	}
	return documents
}

// RecordHistory ajoute au data stream `stream` les relevés de `documents` qui
// n'y sont pas encore.
//
// Les relevés déjà présents sont écartés en amont (ExistingIDs, sur tous les
// index sous-jacents) : dans un data stream, l'action `create` ne détecte un
// doublon que dans l'index d'écriture courant.
func RecordHistory(
	ctx context.Context, client *elasticsearch.Client, stream string, documents []es.Document,
) (created, known, failed int, err error) {
	ids := make([]string, len(documents))
	for i, doc := range documents {
		ids[i] = doc["_id"].(string)
	}
	existing, err := es.ExistingIDs(ctx, client, stream, ids)
	if err != nil {
		return 0, 0, 0, err
	}
	fresh := slices.DeleteFunc(slices.Clone(documents), func(doc es.Document) bool {
		return existing[doc["_id"].(string)]
	})

	created, duplicates, failed, err := es.BulkCreate(ctx, client, stream, slices.Values(fresh))
	return created, len(existing) + duplicates, failed, err
}
