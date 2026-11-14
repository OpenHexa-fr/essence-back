package api

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/openhexa-fr/essence-back/internal/stations"
)

const (
	defaultRadiusKm = 10.0
	defaultSize     = 20
	maxSize         = 1000
)

// validationError décrit un paramètre invalide, dans la forme d'une entrée de
// `detail` d'un 422 FastAPI.
type validationError struct {
	Loc  []string `json:"loc"`
	Msg  string   `json:"msg"`
	Type string   `json:"type"`
}

type searchRequest struct {
	Params      stations.SearchParams
	SearchAfter []any
	Size        int
}

// parseSearch lit et valide les paramètres de `GET /stations/search`.
func parseSearch(query url.Values) (searchRequest, []validationError) {
	var problems []validationError
	fail := func(name, msg, kind string) {
		problems = append(problems, validationError{Loc: []string{"query", name}, Msg: msg, Type: kind})
	}
	optionalFloat := func(name string) *float64 {
		raw := query.Get(name)
		if raw == "" {
			return nil
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			fail(name, "Input should be a valid number, unable to parse string as a number", "float_parsing")
			return nil
		}
		return &value
	}

	request := searchRequest{
		Params: stations.SearchParams{
			Carburant: query.Get("carburant"),
			Lat:       optionalFloat("lat"),
			Lon:       optionalFloat("lon"),
			RadiusKm:  defaultRadiusKm,
			PrixMax:   optionalFloat("prix_max"),
		},
		Size: defaultSize,
	}

	if raw := query.Get("radius_km"); raw != "" {
		if value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64); err == nil {
			request.Params.RadiusKm = value
		} else {
			fail("radius_km", "Input should be a valid number, unable to parse string as a number", "float_parsing")
		}
	}

	if raw := query.Get("tri"); raw != "" {
		if tri, ok := stations.ParseSort(raw); ok {
			request.Params.Tri = tri
		} else {
			fail("tri", "Input should be 'score', 'prix', 'distance' or 'recent'", "literal_error")
		}
	}

	if raw := query.Get("size"); raw != "" {
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		switch {
		case err != nil:
			fail("size", "Input should be a valid integer, unable to parse string as an integer", "int_parsing")
		case value < 1 || value > maxSize:
			fail("size", fmt.Sprintf("Input should be between 1 and %d", maxSize), "value_error")
		default:
			request.Size = value
		}
	}

	// `search_after` se répète (`?search_after=a&search_after=b`) : les valeurs
	// restent des chaînes, qu'Elasticsearch coerce vers le type du champ de tri.
	for _, value := range query["search_after"] {
		request.SearchAfter = append(request.SearchAfter, value)
	}

	return request, problems
}
