package stations

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/openhexa-fr/core/es"
	"github.com/openhexa-fr/core/estest"
)

func ptr[T any](v T) *T { return &v }

func filtersOf(t *testing.T, query map[string]any) []any {
	t.Helper()
	return query["bool"].(map[string]any)["filter"].([]any)
}

func contains(filters []any, want map[string]any) bool {
	for _, f := range filters {
		if reflect.DeepEqual(f, any(want)) {
			return true
		}
	}
	return false
}

func TestBuildQueryReturnsMatchAllWithoutFilters(t *testing.T) {
	got := buildQuery(SearchParams{})

	if !reflect.DeepEqual(got, map[string]any{"match_all": map[string]any{}}) {
		t.Errorf("query = %v", got)
	}
}

func TestBuildQueryFiltersOnCarburantExistence(t *testing.T) {
	query := buildQuery(SearchParams{Carburant: "gplc"})

	if !contains(filtersOf(t, query), map[string]any{"exists": map[string]any{"field": "gplc"}}) {
		t.Errorf("query = %v", query)
	}
}

func TestBuildQueryFiltersOnGeoDistance(t *testing.T) {
	query := buildQuery(SearchParams{Lat: ptr(45.75), Lon: ptr(4.85), RadiusKm: 5})

	want := map[string]any{"geo_distance": map[string]any{
		"distance": "5km", "location": map[string]any{"lat": 45.75, "lon": 4.85},
	}}
	if !contains(filtersOf(t, query), want) {
		t.Errorf("query = %v", query)
	}
}

func TestBuildQueryIgnoresPositionWithoutBothCoordinates(t *testing.T) {
	if got := buildQuery(SearchParams{Lat: ptr(45.75), RadiusKm: 5}); !reflect.DeepEqual(got, map[string]any{"match_all": map[string]any{}}) {
		t.Errorf("query = %v", got)
	}
}

func TestBuildQueryFiltersOnFuelFamilyWithShouldClause(t *testing.T) {
	query := buildQuery(SearchParams{Carburant: "sans_plomb"})

	should := filtersOf(t, query)[0].(map[string]any)["bool"].(map[string]any)
	if should["minimum_should_match"] != 1 {
		t.Errorf("minimum_should_match = %v", should["minimum_should_match"])
	}
	clauses := should["should"].([]map[string]any)
	want := []map[string]any{
		{"exists": map[string]any{"field": "e10"}},
		{"exists": map[string]any{"field": "sp95"}},
	}
	if !reflect.DeepEqual(clauses, want) {
		t.Errorf("should = %v", clauses)
	}
}

func TestBuildQueryFiltersOnFuelFamilyPrixMaxWithShouldClause(t *testing.T) {
	query := buildQuery(SearchParams{Carburant: "sans_plomb", PrixMax: ptr(1.8)})

	clauses := filtersOf(t, query)[1].(map[string]any)["bool"].(map[string]any)["should"].([]map[string]any)
	want := []map[string]any{
		{"range": map[string]any{"e10": map[string]any{"lte": 1.8}}},
		{"range": map[string]any{"sp95": map[string]any{"lte": 1.8}}},
	}
	if !reflect.DeepEqual(clauses, want) {
		t.Errorf("should = %v", clauses)
	}
}

func TestBuildQueryFiltersOnPrixMaxWhenCarburantSet(t *testing.T) {
	query := buildQuery(SearchParams{Carburant: "sp95", PrixMax: ptr(1.8)})

	if !contains(filtersOf(t, query), map[string]any{"range": map[string]any{"sp95": map[string]any{"lte": 1.8}}}) {
		t.Errorf("query = %v", query)
	}
}

func TestBuildQueryIgnoresPrixMaxWithoutCarburant(t *testing.T) {
	got := buildQuery(SearchParams{PrixMax: ptr(1.8)})

	if !reflect.DeepEqual(got, map[string]any{"match_all": map[string]any{}}) {
		t.Errorf("query = %v", got)
	}
}

func TestBuildSortDefaultsToStableOrder(t *testing.T) {
	if got := buildSort(SearchParams{}); !reflect.DeepEqual(got, []map[string]any{{"_seq_no": "asc"}}) {
		t.Errorf("sort = %v", got)
	}
}

func TestBuildSortByPriceUsesTheFuelField(t *testing.T) {
	for _, tri := range []Sort{SortPrix, SortScore} { // "score" se comporte comme "prix"
		got := buildSort(SearchParams{Carburant: "sp95", Tri: tri})

		want := []map[string]any{{"sp95": "asc"}, {"_seq_no": "asc"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tri %s : sort = %v", tri, got)
		}
	}
}

func TestBuildSortByPriceFallsBackWithoutCarburant(t *testing.T) {
	got := buildSort(SearchParams{Tri: SortPrix})

	if !reflect.DeepEqual(got, []map[string]any{{"_seq_no": "asc"}}) {
		t.Errorf("sort = %v", got)
	}
}

func TestBuildSortByFuelFamilyUsesScriptSort(t *testing.T) {
	got := buildSort(SearchParams{Carburant: "sans_plomb", Tri: SortPrix})

	script := got[0]["_script"].(map[string]any)["script"].(map[string]any)
	source := script["source"].(string)
	if !strings.Contains(source, "doc['e10']") || !strings.Contains(source, "doc['sp95']") {
		t.Errorf("source du script : %s", source)
	}
	if script["lang"] != "painless" || got[0]["_script"].(map[string]any)["order"] != "asc" {
		t.Errorf("script : %v", got[0])
	}
	if !reflect.DeepEqual(got[1], map[string]any{"_seq_no": "asc"}) {
		t.Errorf("tie-breaker : %v", got[1])
	}
}

func TestFamilyScriptIsExactlyThePythonOne(t *testing.T) {
	want := "double best = Double.MAX_VALUE;\n" +
		"if (doc['e10'].size() > 0 && doc['e10'].value < best) { best = doc['e10'].value; }\n" +
		"if (doc['sp95'].size() > 0 && doc['sp95'].value < best) { best = doc['sp95'].value; }\n" +
		"return best;"

	got := familyMinPriceSort([]string{"e10", "sp95"})["_script"].(map[string]any)["script"].(map[string]any)["source"]

	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestBuildSortByRecentNeedsNeitherCarburantNorLocation(t *testing.T) {
	got := buildSort(SearchParams{Tri: SortRecent})

	if !reflect.DeepEqual(got[0], map[string]any{"mise_a_jour": "desc"}) {
		t.Errorf("sort = %v", got)
	}
}

func TestBuildSortByDistanceRequiresLocation(t *testing.T) {
	got := buildSort(SearchParams{Lat: ptr(45.75), Lon: ptr(4.85), Tri: SortDistance})

	want := map[string]any{"_geo_distance": map[string]any{
		"location": map[string]any{"lat": 45.75, "lon": 4.85}, "order": "asc", "unit": "km",
	}}
	if !reflect.DeepEqual(got[0], want) {
		t.Errorf("sort = %v", got[0])
	}
	if fallback := buildSort(SearchParams{Tri: SortDistance}); !reflect.DeepEqual(fallback[0], map[string]any{"_seq_no": "asc"}) {
		t.Errorf("sans position : sort = %v", fallback)
	}
}

func TestSearchSendsTheBuiltQueryToElasticsearch(t *testing.T) {
	client, recorder := estest.New(t, func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 200, map[string]any{"hits": map[string]any{"hits": []any{}, "total": map[string]any{"value": 0}}})
	})

	page, err := Search(t.Context(), client, "openhexa-stations", SearchParams{Carburant: "sp95"}, []any{"1.9", "5"}, 20)

	if err != nil || page.Total != 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	searches := recorder.Find("POST", "/openhexa-stations/_search")
	if len(searches) != 1 {
		t.Fatalf("requêtes : %+v", recorder.Requests())
	}
	body := searches[0].JSONBody(t)
	if body["size"] != float64(20) || !reflect.DeepEqual(body["search_after"], []any{"1.9", "5"}) {
		t.Errorf("corps : %s", searches[0].Body)
	}
}

func TestGetByIDReturnsTheStationWhenFound(t *testing.T) {
	client, recorder := estest.New(t, func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 200, map[string]any{"found": true, "_source": map[string]any{
			"station_id": "12345678", "gazole": 1.85, "autoroute": true, "champ_inconnu": 1,
		}})
	})

	station, err := GetByID(t.Context(), client, "openhexa-stations", "12345678")

	if err != nil {
		t.Fatal(err)
	}
	if station.StationID != "12345678" || station.Gazole == nil || *station.Gazole != 1.85 || !station.Autoroute {
		t.Errorf("station = %+v", station)
	}
	// L'`_id` ES est celui de l'ingestion : sha256(station_id)[:16].
	if len(recorder.Find("GET", "/openhexa-stations/_doc/ef797c8118f02dfb")) != 1 {
		t.Errorf("requêtes : %+v", recorder.Requests())
	}
}

func TestGetByIDReturnsNotFoundWhenMissing(t *testing.T) {
	client, _ := estest.New(t, func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 404, map[string]any{"found": false})
	})

	_, err := GetByID(t.Context(), client, "openhexa-stations", "unknown")

	if !errors.Is(err, es.ErrNotFound) {
		t.Errorf("erreur %v, attendu es.ErrNotFound", err)
	}
}

func TestParseSort(t *testing.T) {
	for _, valid := range []string{"", "score", "prix", "distance", "recent"} {
		if _, ok := ParseSort(valid); !ok {
			t.Errorf("%q doit être valide", valid)
		}
	}
	if _, ok := ParseSort("alphabetique"); ok {
		t.Error("un tri inconnu doit être refusé")
	}
}
