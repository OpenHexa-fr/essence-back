package stations

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/elastic/go-elasticsearch/v8"

	"github.com/openhexa-fr/core/es"
)

// fuelFields résout `carburant` en champs ES réels : la famille (FuelFamilies)
// si reconnue, sinon le carburant lui-même tel quel (comportement historique).
func fuelFields(carburant string) []string {
	if fields, ok := FuelFamilies[carburant]; ok {
		return fields
	}
	return []string{carburant}
}

func shouldAny(clauses []map[string]any) map[string]any {
	return map[string]any{"bool": map[string]any{"should": clauses, "minimum_should_match": 1}}
}

// buildQuery construit la clause `query` : carburant (ou famille) disponible,
// plafond de prix et/ou rayon.
//
// Une famille (ex. "sans_plomb") matche si AU MOINS UN de ses carburants est
// disponible (`should`/`minimum_should_match`), pareil pour le plafond de prix
// (au moins un des carburants de la famille sous le plafond) — on ne filtre pas
// sur "tous les carburants de la famille", une station qui ne vend que du SP95
// doit rester éligible au filtre "Sans-plomb".
//
// Le plafond de prix n'a de sens que relativement à un carburant précis :
// ignoré si Carburant n'est pas fourni. Pas de filtre par ville ou service
// (données non extraites de la source actuelle, voir ingestion.go).
func buildQuery(params SearchParams) map[string]any {
	var filters []any

	if params.Carburant != "" {
		fields := fuelFields(params.Carburant)
		if len(fields) == 1 {
			filters = append(filters, map[string]any{"exists": map[string]any{"field": fields[0]}})
			if params.PrixMax != nil {
				filters = append(filters, map[string]any{
					"range": map[string]any{fields[0]: map[string]any{"lte": *params.PrixMax}},
				})
			}
		} else {
			exists := make([]map[string]any, len(fields))
			for i, field := range fields {
				exists[i] = map[string]any{"exists": map[string]any{"field": field}}
			}
			filters = append(filters, shouldAny(exists))
			if params.PrixMax != nil {
				ranges := make([]map[string]any, len(fields))
				for i, field := range fields {
					ranges[i] = map[string]any{"range": map[string]any{field: map[string]any{"lte": *params.PrixMax}}}
				}
				filters = append(filters, shouldAny(ranges))
			}
		}
	}

	if params.Lat != nil && params.Lon != nil {
		filters = append(filters, map[string]any{
			"geo_distance": map[string]any{
				"distance": strconv.FormatFloat(params.RadiusKm, 'f', -1, 64) + "km",
				"location": map[string]any{"lat": *params.Lat, "lon": *params.Lon},
			},
		})
	}

	if len(filters) == 0 {
		return map[string]any{"match_all": map[string]any{}}
	}
	return map[string]any{"bool": map[string]any{"filter": filters}}
}

// familyMinPriceSort trie par le plus bas prix parmi les carburants d'une famille.
//
// Pas de support natif ES pour trier sur le min de plusieurs champs : script
// Painless. `fields` vient uniquement de FuelFamilies (jamais d'une valeur
// utilisateur directe), donc pas de risque d'injection dans la source du
// script — voir fuelFields.
func familyMinPriceSort(fields []string) map[string]any {
	conditions := make([]string, len(fields))
	for i, field := range fields {
		conditions[i] = fmt.Sprintf(
			"if (doc['%[1]s'].size() > 0 && doc['%[1]s'].value < best) { best = doc['%[1]s'].value; }", field)
	}
	source := "double best = Double.MAX_VALUE;\n" + strings.Join(conditions, "\n") + "\nreturn best;"
	return map[string]any{
		"_script": map[string]any{
			"type":   "number",
			"script": map[string]any{"lang": "painless", "source": source},
			"order":  "asc",
		},
	}
}

var stableOrder = map[string]any{"_seq_no": "asc"}

// buildSort construit la clause `sort` : distance, prix, récence, ou "score".
//
//   - "distance" exige une position (lat/lon).
//   - "prix"/"score" exigent un carburant (ou une famille) sélectionné(e) — on
//     ne peut pas trier sur un prix sans savoir lequel. "score" est pour
//     l'instant un simple alias de "prix" côté tri serveur — le vrai score
//     composite (0-100, prix + distance) affiché en badge est calculé côté
//     frontend à partir des résultats déjà triés, sans aller-retour serveur
//     supplémentaire.
//   - "recent" trie par fraîcheur du prix (`mise_a_jour` décroissant), seul tri
//     qui ne requiert ni position ni carburant.
//
// Si les données nécessaires manquent pour le tri demandé, ou si aucun tri
// n'est demandé, on retombe sur l'ordre stable de l'index (`_seq_no`).
func buildSort(params SearchParams) []map[string]any {
	switch {
	case params.Tri == SortDistance && params.Lat != nil && params.Lon != nil:
		return []map[string]any{
			{"_geo_distance": map[string]any{
				"location": map[string]any{"lat": *params.Lat, "lon": *params.Lon},
				"order":    "asc",
				"unit":     "km",
			}},
			stableOrder,
		}
	case params.Tri == SortRecent:
		return []map[string]any{{"mise_a_jour": "desc"}, stableOrder}
	case (params.Tri == SortPrix || params.Tri == SortScore) && params.Carburant != "":
		fields := fuelFields(params.Carburant)
		if len(fields) == 1 {
			return []map[string]any{{fields[0]: "asc"}, stableOrder}
		}
		return []map[string]any{familyMinPriceSort(fields), stableOrder}
	}
	return []map[string]any{stableOrder}
}

// Search recherche des stations selon `params`, paginée par `search_after`.
func Search(
	ctx context.Context, client *elasticsearch.Client, index string,
	params SearchParams, searchAfter []any, size int,
) (es.Page, error) {
	return es.Paginate(ctx, client, es.PaginateParams{
		Index:       index,
		Query:       buildQuery(params),
		Sort:        buildSort(params),
		SearchAfter: searchAfter,
		Size:        size,
	})
}

// GetByID retourne la station `stationID`, ou es.ErrNotFound si elle est absente.
func GetByID(ctx context.Context, client *elasticsearch.Client, index, stationID string) (*Station, error) {
	source, err := es.GetSource(ctx, client, index, es.MakeDocumentID(stationID))
	if err != nil {
		return nil, err
	}
	var station Station
	if err := json.Unmarshal(source, &station); err != nil {
		return nil, fmt.Errorf("stations: station %s illisible : %w", stationID, err)
	}
	return &station, nil
}
