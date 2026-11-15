package stations

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openhexa-fr/core/es"
	"github.com/openhexa-fr/core/estest"
)

func TestParseHistoryBuildsOneDocumentPerFuel(t *testing.T) {
	documents := ParseHistory([]Record{record(t, sampleRecord)})

	if len(documents) != 2 {
		t.Fatalf("%d relevés, 2 attendus (gazole, sp95) : %v", len(documents), documents)
	}
	byFuel := map[any]es.Document{}
	for _, doc := range documents {
		byFuel[doc["carburant"]] = doc
	}
	gazole := byFuel["gazole"]
	if gazole["@timestamp"] != "2026-07-10T05:25:45+00:00" || gazole["prix"] != 1.850 ||
		gazole["station_id"] != "12345678" || gazole["ville"] != "Paris" || gazole["autoroute"] != true {
		t.Errorf("relevé gazole : %v", gazole)
	}
	if byFuel["sp95"]["@timestamp"] != "2026-07-10T08:00:00+00:00" {
		t.Errorf("chaque carburant garde sa propre date : %v", byFuel["sp95"])
	}
}

func TestParseHistoryIDIsStablePerStationFuelAndDate(t *testing.T) {
	first := ParseHistory([]Record{record(t, sampleRecord)})
	again := ParseHistory([]Record{record(t, sampleRecord)})
	changed := ParseHistory([]Record{withRecord(t, map[string]any{
		"gazole_prix": json.Number("1.799"), "gazole_maj": "2026-07-11T06:00:00+00:00",
	})})

	ids := func(docs []es.Document) map[any]any {
		out := map[any]any{}
		for _, d := range docs {
			out[d["carburant"]] = d["_id"]
		}
		return out
	}
	if ids(first)["gazole"] != ids(again)["gazole"] {
		t.Error("le même relevé doit garder le même _id")
	}
	if ids(first)["gazole"] == ids(changed)["gazole"] {
		t.Error("un nouveau prix (nouvelle date) doit produire un nouveau _id")
	}
	if ids(first)["sp95"] != ids(changed)["sp95"] {
		t.Error("un carburant inchangé doit garder son _id")
	}
}

func TestParseHistorySkipsFuelsWithoutPriceOrDate(t *testing.T) {
	documents := ParseHistory([]Record{withRecord(t, map[string]any{
		"gazole_maj": nil, "e10_prix": json.Number("1.7"),
	})})

	if len(documents) != 1 || documents[0]["carburant"] != "sp95" {
		t.Errorf("seul sp95 a un prix et une date : %v", documents)
	}
}

func TestParseHistorySkipsMalformedStations(t *testing.T) {
	documents := ParseHistory([]Record{
		withRecord(t, map[string]any{"gazole_prix": "n/a"}),
		withRecord(t, map[string]any{"id": json.Number("2")}),
		withRecord(t, map[string]any{"id": nil}),
	})

	for _, doc := range documents {
		if doc["station_id"] != "2" {
			t.Errorf("seule la station 2 est valide : %v", doc)
		}
	}
	if len(documents) != 2 {
		t.Errorf("%d relevés", len(documents))
	}
}

// historyServer simule Elasticsearch : `known` sont les _id déjà présents dans
// l'historique ; chaque action create reçoit un 201.
func historyServer(known ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_search"):
			hits := []any{}
			for _, id := range known {
				hits = append(hits, map[string]any{"_id": id})
			}
			estest.JSON(w, 200, map[string]any{"hits": map[string]any{"hits": hits}})
		case r.URL.Path == "/_bulk":
			body, _ := io.ReadAll(r.Body)
			var items []any
			for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
				var action map[string]map[string]any
				if json.Unmarshal([]byte(line), &action) != nil {
					continue
				}
				for name, meta := range action {
					if name == "create" || name == "index" {
						items = append(items, map[string]any{name: map[string]any{"_id": meta["_id"], "status": 201}})
					}
				}
			}
			estest.JSON(w, 200, map[string]any{"errors": false, "items": items})
		default:
			estest.JSON(w, 404, map[string]any{})
		}
	}
}

func TestRecordHistorySkipsKnownReadings(t *testing.T) {
	documents := ParseHistory([]Record{record(t, sampleRecord)})
	var knownID string
	for _, doc := range documents {
		if doc["carburant"] == "gazole" {
			knownID = doc["_id"].(string)
		}
	}
	handler := historyServer(knownID)
	client, recorder := estest.New(t, handler)

	created, known, failed, err := RecordHistory(t.Context(), client, "openhexa-essence-prix-historique", documents)

	if err != nil || created != 1 || known != 1 || failed != 0 {
		t.Fatalf("created=%d known=%d failed=%d err=%v", created, known, failed, err)
	}
	bulk := recorder.Find("POST", "/_bulk")
	if len(bulk) != 1 || strings.Contains(string(bulk[0].Body), knownID) {
		t.Errorf("le relevé connu ne doit pas être renvoyé : %+v", bulk)
	}
	if !strings.Contains(string(bulk[0].Body), `"create"`) {
		t.Errorf("un data stream n'accepte que l'action create : %s", bulk[0].Body)
	}
}

func TestIngestRecordsHistory(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[` + sampleRecord + `]`))
	}))
	defer source.Close()
	handler := historyServer()
	client, recorder := estest.New(t, handler)

	success, failed, err := Ingest(t.Context(), client,
		"openhexa-essence-stations", "openhexa-essence-prix-historique", source.URL)

	if err != nil || success != 1 || failed != 0 {
		t.Fatalf("success=%d failed=%d err=%v", success, failed, err)
	}
	var stationsBulk, historyBulk int
	for _, bulk := range recorder.Find("POST", "/_bulk") {
		body := string(bulk.Body)
		stationsBulk += strings.Count(body, `"_index":"openhexa-essence-stations"`)
		historyBulk += strings.Count(body, `"_index":"openhexa-essence-prix-historique"`)
	}
	if stationsBulk != 1 || historyBulk != 2 {
		t.Errorf("stations=%d relevés=%d", stationsBulk, historyBulk)
	}
}

func TestIngestSurvivesHistoryFailure(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[` + sampleRecord + `]`))
	}))
	defer source.Close()
	handler := historyServer()
	client, _ := estest.New(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/_search") {
			estest.JSON(w, 403, map[string]any{"error": "security_exception"})
			return
		}
		handler(w, r)
	})

	success, _, err := Ingest(t.Context(), client,
		"openhexa-essence-stations", "openhexa-essence-prix-historique", source.URL)

	if err != nil || success != 1 {
		t.Errorf("l'échec de l'historique ne doit pas faire échouer les stations : success=%d err=%v", success, err)
	}
}
