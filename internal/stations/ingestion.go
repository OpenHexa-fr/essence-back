package stations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"

	"github.com/openhexa-fr/core/es"
)

// Ingestion des prix carburants (source data.economie.gouv.fr) dans Elasticsearch.
//
// Reprend la logique du client gouvernemental du projet Pump-price
// (https://github.com/Draclest/Pump-price, `app/services/gov_client.py`) : le
// flux "instantané v2" expose un enregistrement JSON par station avec les prix
// déjà aplatis par carburant (`gazole_prix`, `sp95_prix`, ...) et les
// coordonnées en degrés décimaux, contrairement au flux ZIP/XML
// `donnees.roulez-eco.fr` utilisé auparavant, qui exigeait un rescaling manuel
// des coordonnées et une conversion de format de date non-ISO.

var fuelKeys = []string{"sp95", "sp98", "e10", "e85", "gazole", "gplc"}

const fetchTimeout = 90 * time.Second

// Record est un enregistrement brut du flux : un objet JSON, nombres en json.Number.
type Record = map[string]any

// FetchRecords télécharge l'export JSON du flux instantané depuis `sourceURL`.
func FetchRecords(ctx context.Context, sourceURL string) ([]Record, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("stations: %s a répondu %d", sourceURL, response.StatusCode)
	}
	return decodeRecords(response.Body)
}

// decodeRecords lit un export : soit une liste d'enregistrements, soit un
// objet portant la liste sous la clé `results`.
func decodeRecords(body io.Reader) ([]Record, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()

	if trimmed := bytes.TrimLeft(payload, " \t\r\n"); len(trimmed) > 0 && trimmed[0] == '{' {
		var wrapper struct {
			Results []Record `json:"results"`
		}
		if err := decoder.Decode(&wrapper); err != nil {
			return nil, fmt.Errorf("stations: export illisible : %w", err)
		}
		return wrapper.Results, nil
	}

	var records []Record
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("stations: export illisible : %w", err)
	}
	return records, nil
}

// truthy reproduit la valeur de vérité Python, sur laquelle repose le parsing
// historique (`record.get("id") or ""`, `if maj and ...`).
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case bool:
		return v
	case json.Number:
		f, err := v.Float64()
		return err != nil || f != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func pyText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "True"
		}
		return "False"
	}
	return fmt.Sprint(value)
}

// toFloat reproduit `float(value)` : nombres, chaînes numériques, booléens.
func toFloat(value any) (float64, error) {
	switch v := value.(type) {
	case json.Number:
		return v.Float64()
	case string:
		return strconv.ParseFloat(strings.TrimSpace(v), 64)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("valeur non numérique : %v", value)
}

// parseRecord convertit un enregistrement en document indexable, ou (nil, nil)
// si la station n'a pas d'identifiant.
func parseRecord(record Record) (es.Document, error) {
	var stationID string
	if id := record["id"]; truthy(id) {
		stationID = strings.TrimSpace(pyText(id))
	}
	if stationID == "" {
		return nil, nil
	}

	var location any
	if geom, ok := record["geom"].(map[string]any); ok {
		latitude, longitude := geom["lat"], geom["lon"]
		if latitude != nil && longitude != nil {
			lat, err := toFloat(latitude)
			if err != nil {
				return nil, err
			}
			lon, err := toFloat(longitude)
			if err != nil {
				return nil, err
			}
			location = map[string]any{"lat": lat, "lon": lon}
		}
	}

	prices := map[string]float64{}
	var miseAJour *string
	for _, fuel := range fuelKeys {
		if value := record[fuel+"_prix"]; value != nil {
			price, err := toFloat(value)
			if err != nil {
				return nil, err
			}
			prices[fuel] = price
		}
		if maj := record[fuel+"_maj"]; truthy(maj) {
			text, ok := maj.(string)
			if !ok {
				return nil, fmt.Errorf("date de mise à jour non textuelle : %v", maj)
			}
			if miseAJour == nil || text > *miseAJour {
				miseAJour = &text
			}
		}
	}

	document := es.Document{
		"_id":         es.MakeDocumentID(stationID),
		"station_id":  stationID,
		"adresse":     record["adresse"],
		"ville":       record["ville"],
		"code_postal": record["cp"],
		"location":    location,
		// Le flux JSON publie déjà des dates ISO-8601 avec offset (ex:
		// "2026-07-10T05:25:45+00:00"), directement compatibles avec le mapping
		// ES `date` — pas de conversion de format nécessaire (contrairement à
		// l'ancien flux XML roulez-eco.fr).
		"mise_a_jour": miseAJour,
		"autoroute":   record["pop"] == "A",
	}
	for fuel, price := range prices {
		document[fuel] = price
	}
	return document, nil
}

// ParseRecords parse les enregistrements du flux instantané en documents prêts à indexer.
//
// Une station dont les attributs sont malformés est ignorée plutôt que de faire
// échouer l'ingestion des ~9800 autres stations du flux.
func ParseRecords(records []Record) []es.Document {
	documents := make([]es.Document, 0, len(records))
	for _, record := range records {
		document, err := parseRecord(record)
		if err != nil {
			slog.Warn("station_parse_skipped", "station_id", record["id"], "error", err.Error())
			continue
		}
		if document != nil {
			documents = append(documents, document)
		}
	}
	return documents
}

// Ingest télécharge, parse et indexe les stations depuis `sourceURL`.
func Ingest(
	ctx context.Context, client *elasticsearch.Client, indexAlias, sourceURL string,
) (success, failed int, err error) {
	records, err := FetchRecords(ctx, sourceURL)
	if err != nil {
		return 0, 0, err
	}
	documents := ParseRecords(records)

	success, failed, err = es.BulkIndex(ctx, client, indexAlias, slices.Values(documents))
	if err == nil {
		slog.Info("stations_ingestion_completed", "success", success, "errors", failed)
	}
	return success, failed, err
}
