// Package stations contient le domaine stations-service : schémas, mapping
// Elasticsearch, ingestion du flux des prix, recherche et rafraîchissement à la demande.
package stations

// Sort est un critère de tri exposé en recherche.
type Sort string

// Critères de tri.
const (
	SortScore    Sort = "score"
	SortPrix     Sort = "prix"
	SortDistance Sort = "distance"
	SortRecent   Sort = "recent"
)

// ParseSort valide un critère de tri ; la chaîne vide est valide (aucun tri demandé).
func ParseSort(raw string) (Sort, bool) {
	switch s := Sort(raw); s {
	case "", SortScore, SortPrix, SortDistance, SortRecent:
		return s, true
	}
	return "", false
}

// FuelFamilies regroupe des carburants exposés en recherche (ex. "Sans-plomb"
// combine E10 et SP95, deux carburants distincts mais considérés comme une même
// famille par les comparateurs grand public). Purement une abstraction de
// requête : rien n'est stocké ni réindexé, `Station` garde un champ par
// carburant réel.
var FuelFamilies = map[string][]string{
	"sans_plomb": {"e10", "sp95"},
}

// GeoPoint est une position en degrés décimaux.
type GeoPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Station est une fiche station-service telle qu'exposée par l'API.
type Station struct {
	StationID  string    `json:"station_id"`
	Nom        *string   `json:"nom"`
	Adresse    *string   `json:"adresse"`
	Ville      *string   `json:"ville"`
	CodePostal *string   `json:"code_postal"`
	Location   *GeoPoint `json:"location"`
	SP95       *float64  `json:"sp95"`
	SP98       *float64  `json:"sp98"`
	E10        *float64  `json:"e10"`
	E85        *float64  `json:"e85"`
	Gazole     *float64  `json:"gazole"`
	GPLc       *float64  `json:"gplc"`
	MiseAJour  *string   `json:"mise_a_jour"`
	Autoroute  bool      `json:"autoroute"`
}

// SearchParams sont les critères d'une recherche de stations.
type SearchParams struct {
	// Un carburant réel (ex. "sp98") ou une clé de FuelFamilies (ex. "sans_plomb").
	Carburant string
	Lat, Lon  *float64
	// RadiusKm est le rayon autour de (Lat, Lon), 10 km par défaut côté API.
	RadiusKm float64
	Tri      Sort
	// PrixMax est ignoré sans Carburant (pas de plafond de prix sans savoir
	// quel prix comparer).
	PrixMax *float64
}
