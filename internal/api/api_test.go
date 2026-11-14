package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openhexa-fr/core/estest"
)

type fakeRefresher struct{ triggers atomic.Int32 }

func (f *fakeRefresher) TriggerIfStale() { f.triggers.Add(1) }

func newServer(t *testing.T, es http.HandlerFunc) (*httptest.Server, *estest.Recorder, *fakeRefresher) {
	t.Helper()
	client, recorder := estest.New(t, es)
	refresher := &fakeRefresher{}
	server := httptest.NewServer((&Server{ES: client, Refresher: refresher, Index: "openhexa-stations"}).Handler())
	t.Cleanup(server.Close)
	return server, recorder, refresher
}

func get(t *testing.T, url string, headers ...string) (*http.Response, string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, strings.TrimSpace(string(body))
}

func searchResult(hits ...map[string]any) http.HandlerFunc {
	items := make([]any, len(hits))
	for i, h := range hits {
		items[i] = h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 200, map[string]any{"hits": map[string]any{
			"hits": items, "total": map[string]any{"value": len(items), "relation": "eq"},
		}})
	}
}

func TestSearchReturnsTheFastAPIShapedResponse(t *testing.T) {
	server, _, _ := newServer(t, searchResult(map[string]any{
		"_id": "abc", "sort": []any{1.85, 7},
		"_source": map[string]any{
			"station_id": "12345678", "ville": "Paris", "gazole": 1.85, "autoroute": true,
			"location": map[string]any{"lat": 48.8697, "lon": 2.3079},
		},
	}))

	response, body := get(t, server.URL+"/api/v1/stations/search?carburant=gazole&tri=prix")

	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("status=%d type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"items", "next_cursor", "next_search_after", "total", "total_relation"}
	var keys []string
	for k := range got {
		keys = append(keys, k)
	}
	if len(keys) != 5 {
		t.Fatalf("clés %v, attendu %v", keys, wantKeys)
	}
	if got["total"] != float64(1) || got["total_relation"] != "eq" || got["next_search_after"] != nil {
		t.Errorf("enveloppe : %s", body)
	}
	if got["next_cursor"] == nil || got["next_cursor"] == "" {
		t.Error("next_cursor doit être renseigné quand la page n'est pas vide")
	}
	station := got["items"].([]any)[0].(map[string]any)
	wantStation := map[string]any{
		"station_id": "12345678", "nom": nil, "adresse": nil, "ville": "Paris", "code_postal": nil,
		"location": map[string]any{"lat": 48.8697, "lon": 2.3079},
		"sp95":     nil, "sp98": nil, "e10": nil, "e85": nil, "gazole": 1.85, "gplc": nil,
		"mise_a_jour": nil, "autoroute": true,
	}
	if !reflect.DeepEqual(station, wantStation) {
		t.Errorf("station :\n got %v\nwant %v", station, wantStation)
	}
}

func TestSearchOnAnEmptyIndexReturnsEmptyItemsAndNoCursor(t *testing.T) {
	server, _, _ := newServer(t, searchResult())

	_, body := get(t, server.URL+"/api/v1/stations/search")

	want := `{"items":[],"total":0,"total_relation":"eq","next_cursor":null,"next_search_after":null}`
	if body != want {
		t.Errorf("\n got %s\nwant %s", body, want)
	}
}

func TestSearchTriggersTheOnDemandRefresh(t *testing.T) {
	server, _, refresher := newServer(t, searchResult())

	get(t, server.URL+"/api/v1/stations/search")
	get(t, server.URL+"/api/v1/stations/search")

	if refresher.triggers.Load() != 2 {
		t.Errorf("%d déclenchements, attendu 1 par recherche", refresher.triggers.Load())
	}
}

func TestSearchPassesTheParametersToElasticsearch(t *testing.T) {
	server, recorder, _ := newServer(t, searchResult())

	get(t, server.URL+"/api/v1/stations/search?carburant=sans_plomb&lat=45.75&lon=4.85&radius_km=5&tri=distance&prix_max=1.9&size=50&search_after=12.5&search_after=abc")

	body := recorder.Find("POST", "/openhexa-stations/_search")[0].JSONBody(t)
	if body["size"] != float64(50) {
		t.Errorf("size = %v", body["size"])
	}
	if !reflect.DeepEqual(body["search_after"], []any{"12.5", "abc"}) {
		t.Errorf("search_after = %v", body["search_after"])
	}
	query := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)
	if len(query) != 3 { // famille, plafond de prix, rayon
		t.Errorf("filtres : %v", query)
	}
	sort := body["sort"].([]any)[0].(map[string]any)
	if _, ok := sort["_geo_distance"]; !ok {
		t.Errorf("sort = %v", sort)
	}
}

func TestSearchUsesTheDefaultsOfThePythonAPI(t *testing.T) {
	server, recorder, _ := newServer(t, searchResult())

	get(t, server.URL+"/api/v1/stations/search?lat=45&lon=4")

	body := recorder.Requests()[0].JSONBody(t)
	if body["size"] != float64(20) {
		t.Errorf("size par défaut = %v, attendu 20", body["size"])
	}
	geo := body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[0].(map[string]any)["geo_distance"].(map[string]any)
	if geo["distance"] != "10km" {
		t.Errorf("rayon par défaut = %v, attendu 10 km", geo["distance"])
	}
}

func TestSearchRejectsInvalidParametersWithA422(t *testing.T) {
	server, recorder, refresher := newServer(t, searchResult())

	for query, param := range map[string]string{
		"lat=abc": "lat", "lon=x": "lon", "prix_max=cher": "prix_max", "radius_km=-": "radius_km",
		"tri=alphabetique": "tri", "size=abc": "size", "size=0": "size", "size=1001": "size",
	} {
		response, body := get(t, server.URL+"/api/v1/stations/search?"+query)

		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s : status %d, attendu 422", query, response.StatusCode)
			continue
		}
		var parsed struct {
			Detail []validationError `json:"detail"`
		}
		if err := json.Unmarshal([]byte(body), &parsed); err != nil || len(parsed.Detail) != 1 {
			t.Errorf("%s : corps %s", query, body)
			continue
		}
		if !reflect.DeepEqual(parsed.Detail[0].Loc, []string{"query", param}) {
			t.Errorf("%s : loc %v", query, parsed.Detail[0].Loc)
		}
	}
	if len(recorder.Requests()) != 0 || refresher.triggers.Load() != 0 {
		t.Error("une requête invalide ne doit ni interroger ES ni déclencher de rafraîchissement")
	}
}

func TestSearchReportsEveryInvalidParameter(t *testing.T) {
	server, _, _ := newServer(t, searchResult())

	_, body := get(t, server.URL+"/api/v1/stations/search?lat=a&lon=b")

	if strings.Count(body, `"loc"`) != 2 {
		t.Errorf("corps %s", body)
	}
}

func TestSearchReturns500WhenElasticsearchFails(t *testing.T) {
	server, _, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) { estest.JSON(w, 500, map[string]any{"error": "boom"}) })

	response, body := get(t, server.URL+"/api/v1/stations/search")

	if response.StatusCode != 500 || body != `{"detail":"Internal Server Error"}` {
		t.Errorf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestGetByIDReturnsTheStation(t *testing.T) {
	server, _, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		estest.JSON(w, 200, map[string]any{"found": true, "_source": map[string]any{"station_id": "12345678", "ville": "Paris"}})
	})

	response, body := get(t, server.URL+"/api/v1/stations/12345678")

	if response.StatusCode != 200 || !strings.Contains(body, `"station_id":"12345678"`) || !strings.Contains(body, `"gazole":null`) {
		t.Errorf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestSearchRouteWinsOverTheStationIDRoute(t *testing.T) {
	server, recorder, _ := newServer(t, searchResult())

	get(t, server.URL+"/api/v1/stations/search")

	if len(recorder.Find("POST", "/openhexa-stations/_search")) != 1 {
		t.Errorf("/stations/search doit lancer une recherche : %+v", recorder.Requests())
	}
}

func TestGetByIDReturns404WithTheFrenchMessage(t *testing.T) {
	server, _, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) { estest.JSON(w, 404, map[string]any{"found": false}) })

	response, body := get(t, server.URL+"/api/v1/stations/inconnue")

	if response.StatusCode != 404 || body != `{"detail":"Station introuvable"}` {
		t.Errorf("status=%d body=%s", response.StatusCode, body)
	}
}

func TestStatusReflectsWhetherStationsAreIndexed(t *testing.T) {
	for count, want := range map[int]string{0: `{"stations":false}`, 9800: `{"stations":true}`} {
		server, _, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) { estest.JSON(w, 200, map[string]any{"count": count}) })

		response, body := get(t, server.URL+"/api/v1/status")

		if response.StatusCode != 200 || body != want {
			t.Errorf("count=%d : status=%d body=%s, attendu %s", count, response.StatusCode, body, want)
		}
	}
}

func TestUnknownRoutesAndMethodsAreRejected(t *testing.T) {
	server, _, _ := newServer(t, searchResult())

	if response, _ := get(t, server.URL+"/api/v1/inconnu"); response.StatusCode != 404 {
		t.Errorf("route inconnue : %d", response.StatusCode)
	}
	response, err := http.Post(server.URL+"/api/v1/stations/search", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST : %d, attendu 405 (API en lecture seule)", response.StatusCode)
	}
}

func TestCORSAllowsAnyOriginForSimpleRequests(t *testing.T) {
	server, _, _ := newServer(t, searchResult())

	response, _ := get(t, server.URL+"/api/v1/stations/search", "Origin", "https://openhexa.fr")
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q", got)
	}

	response, _ = get(t, server.URL+"/api/v1/stations/search")
	if got := response.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("sans Origin : Access-Control-Allow-Origin = %q", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	server, recorder, _ := newServer(t, searchResult())
	preflight := func(method string) *http.Response {
		request, _ := http.NewRequest(http.MethodOptions, server.URL+"/api/v1/stations/search", nil)
		request.Header.Set("Origin", "https://openhexa.fr")
		request.Header.Set("Access-Control-Request-Method", method)
		request.Header.Set("Access-Control-Request-Headers", "x-custom, content-type")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response
	}

	ok := preflight("GET")
	if ok.StatusCode != 200 ||
		ok.Header.Get("Access-Control-Allow-Origin") != "*" ||
		ok.Header.Get("Access-Control-Allow-Methods") != "GET" ||
		ok.Header.Get("Access-Control-Allow-Headers") != "x-custom, content-type" {
		t.Errorf("preflight GET : %d %v", ok.StatusCode, ok.Header)
	}
	if refused := preflight("DELETE"); refused.StatusCode != http.StatusBadRequest {
		t.Errorf("preflight DELETE : %d, attendu 400", refused.StatusCode)
	}
	if len(recorder.Requests()) != 0 {
		t.Error("un preflight ne doit pas atteindre Elasticsearch")
	}
}
