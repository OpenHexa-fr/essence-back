package stations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/openhexa-fr/core/estest"
)

func record(t *testing.T, raw string) Record {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var r Record
	if err := decoder.Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

const sampleRecord = `{
	"id": 12345678, "adresse": "1 rue de la Paix", "ville": "Paris", "cp": "75001", "pop": "A",
	"geom": {"lat": 48.8697, "lon": 2.3079},
	"gazole_prix": 1.850, "gazole_maj": "2026-07-10T05:25:45+00:00",
	"sp95_prix": 1.950, "sp95_maj": "2026-07-10T08:00:00+00:00"
}`

func withRecord(t *testing.T, overrides map[string]any) Record {
	r := record(t, sampleRecord)
	for k, v := range overrides {
		r[k] = v
	}
	return r
}

func TestParseRecordsBuildsDocumentsWithDecimalCoordinates(t *testing.T) {
	documents := ParseRecords([]Record{record(t, sampleRecord)})

	if len(documents) != 1 {
		t.Fatalf("%d documents", len(documents))
	}
	doc := documents[0]
	if doc["station_id"] != "12345678" {
		t.Errorf("station_id = %v", doc["station_id"])
	}
	if !reflect.DeepEqual(doc["location"], map[string]any{"lat": 48.8697, "lon": 2.3079}) {
		t.Errorf("location = %v", doc["location"])
	}
	if doc["gazole"] != 1.850 || doc["sp95"] != 1.950 {
		t.Errorf("prix : gazole=%v sp95=%v", doc["gazole"], doc["sp95"])
	}
	if doc["autoroute"] != true {
		t.Errorf("autoroute = %v", doc["autoroute"])
	}
	if id, _ := doc["_id"].(string); len(id) != 16 {
		t.Errorf("_id = %v", doc["_id"])
	}
	if doc["adresse"] != "1 rue de la Paix" || doc["ville"] != "Paris" || doc["code_postal"] != "75001" {
		t.Errorf("champs texte : %v", doc)
	}
}

// Le `_id` doit rester celui que produisait la version Python (sha256("12345678")[:16]),
// sans quoi une ré-ingestion dupliquerait toutes les stations déjà indexées.
func TestParseRecordsKeepsThePythonDocumentID(t *testing.T) {
	documents := ParseRecords([]Record{record(t, sampleRecord)})

	if documents[0]["_id"] != "ef797c8118f02dfb" {
		t.Errorf("_id = %v", documents[0]["_id"])
	}
}

func TestParseRecordsKeepsISO8601DatesUnchanged(t *testing.T) {
	// Le flux JSON publie déjà des dates ISO-8601 avec offset : aucune
	// conversion de format n'est nécessaire. La plus récente des dates de prix l'emporte.
	documents := ParseRecords([]Record{record(t, sampleRecord)})

	got := documents[0]["mise_a_jour"].(*string)
	if got == nil || *got != "2026-07-10T08:00:00+00:00" {
		t.Errorf("mise_a_jour = %v", got)
	}
}

func TestParseRecordsSkipsRecordWithoutID(t *testing.T) {
	for name, id := range map[string]any{"nil": nil, "vide": "", "espaces": "  ", "zéro": json.Number("0")} {
		if documents := ParseRecords([]Record{withRecord(t, map[string]any{"id": id})}); len(documents) != 0 {
			t.Errorf("id %s : %d documents, attendu 0", name, len(documents))
		}
	}
}

func TestParseRecordsOmitsLocationWhenGeomMissing(t *testing.T) {
	for name, geom := range map[string]any{
		"nil": nil, "vide": map[string]any{}, "sans lon": map[string]any{"lat": json.Number("1")},
	} {
		documents := ParseRecords([]Record{withRecord(t, map[string]any{"geom": geom})})

		if len(documents) != 1 {
			t.Fatalf("%s : %d documents", name, len(documents))
		}
		if documents[0]["location"] != nil {
			t.Errorf("%s : location = %v", name, documents[0]["location"])
		}
	}
}

func TestParseRecordsSkipsMalformedStationWithoutFailingOthers(t *testing.T) {
	bad := withRecord(t, map[string]any{"id": json.Number("1"), "gazole_prix": "not-a-number"})
	good := withRecord(t, map[string]any{"id": json.Number("2")})

	documents := ParseRecords([]Record{bad, good})

	if len(documents) != 1 || documents[0]["station_id"] != "2" {
		t.Errorf("documents : %v", documents)
	}
}

func TestParseRecordsAcceptsNumericStrings(t *testing.T) {
	r := withRecord(t, map[string]any{"gazole_prix": "1.75", "geom": map[string]any{"lat": "48.5", "lon": json.Number("2")}})

	documents := ParseRecords([]Record{r})

	if documents[0]["gazole"] != 1.75 {
		t.Errorf("gazole = %v", documents[0]["gazole"])
	}
	if !reflect.DeepEqual(documents[0]["location"], map[string]any{"lat": 48.5, "lon": 2.0}) {
		t.Errorf("location = %v", documents[0]["location"])
	}
}

func TestParseRecordsLeavesAbsentFuelsOutOfTheDocument(t *testing.T) {
	documents := ParseRecords([]Record{record(t, sampleRecord)})

	for _, fuel := range []string{"sp98", "e10", "e85", "gplc"} {
		if _, present := documents[0][fuel]; present {
			t.Errorf("%s ne doit pas figurer dans le document", fuel)
		}
	}
	if documents[0]["autoroute"] != true {
		t.Error("pop == A : autoroute")
	}
	if ParseRecords([]Record{withRecord(t, map[string]any{"pop": "R"})})[0]["autoroute"] != false {
		t.Error("pop != A : pas autoroute")
	}
}

func TestDecodeRecordsAcceptsListOrResultsObject(t *testing.T) {
	for name, body := range map[string]string{
		"liste":  `[{"id": 1}, {"id": 2}]`,
		"objet":  `{"results": [{"id": 1}, {"id": 2}]}`,
		"espace": "\n  [{\"id\": 1}, {\"id\": 2}]",
	} {
		records, err := decodeRecords(strings.NewReader(body))
		if err != nil || len(records) != 2 {
			t.Errorf("%s : %d enregistrements, err=%v", name, len(records), err)
		}
	}
	if _, err := decodeRecords(strings.NewReader("pas du json")); err == nil {
		t.Error("JSON invalide : erreur attendue")
	}
}

func TestDecodeRecordsKeepsNumbersExact(t *testing.T) {
	records, err := decodeRecords(strings.NewReader(`[{"id": 12345678901234567890}]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := pyText(records[0]["id"]); got != "12345678901234567890" {
		t.Errorf("id = %s : les identifiants numériques ne doivent pas passer par un flottant", got)
	}
}

func TestIngestFetchesParsesAndBulkIndexes(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[` + sampleRecord + `, {"id": null}]`))
	}))
	defer source.Close()
	client, recorder := estest.New(t, func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 200, map[string]any{
			"errors": false,
			"items":  []any{map[string]any{"index": map[string]any{"_id": "x", "status": 201}}},
		})
	})

	success, failed, err := Ingest(t.Context(), client, "openhexa-stations", source.URL)

	if err != nil || success != 1 || failed != 0 {
		t.Fatalf("success=%d failed=%d err=%v", success, failed, err)
	}
	bulk := recorder.Find("POST", "/_bulk")
	if len(bulk) != 1 || !strings.Contains(string(bulk[0].Body), `"_index":"openhexa-stations"`) {
		t.Errorf("bulk : %+v", recorder.Requests())
	}
	if !strings.Contains(string(bulk[0].Body), `"station_id":"12345678"`) {
		t.Errorf("document absent du bulk : %s", bulk[0].Body)
	}
}

func TestIngestReportsSourceFailures(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "indisponible", http.StatusServiceUnavailable)
	}))
	defer source.Close()
	client, recorder := estest.New(t, func(w http.ResponseWriter, r *http.Request) {})

	_, _, err := Ingest(t.Context(), client, "openhexa-stations", source.URL)

	if err == nil {
		t.Error("une source en 503 doit produire une erreur")
	}
	if len(recorder.Requests()) != 0 {
		t.Error("rien ne doit être écrit quand le téléchargement échoue")
	}
}

func TestIngestFollowsRedirects(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()

	records, err := FetchRecords(t.Context(), redirect.URL)

	if err != nil || len(records) != 0 {
		t.Errorf("records=%v err=%v", records, err)
	}
}
