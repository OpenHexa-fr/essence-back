package stations

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Rafraîchissement à la demande des prix carburants, déclenché par une recherche.
//
// Le gouvernement republie le flux "instantané" toutes les 10 minutes (voir la
// description du jeu de données data.gouv.fr "Prix des carburants en France -
// Flux instantané - v2" : "Le flux de données instantané est mis à jour toutes
// les 10 minutes"). Retélécharger le fichier national à chaque recherche serait
// à la fois inutile (la source ne change pas plus vite) et coûteux (~12 Mo,
// ~10 000 documents à réindexer) : Refresher ne déclenche donc un nouveau cycle
// d'ingestion que si le dernier remonte à plus de `ttl`, et seulement si aucun
// rafraîchissement n'est déjà en cours.
//
// Le rafraîchissement tourne en tâche de fond (fire-and-forget) pour ne pas
// pénaliser la latence de la recherche qui le déclenche : cette recherche voit
// les données actuelles, la suivante bénéficiera des données fraîchement
// réindexées. Ceci s'ajoute à la synchronisation régulière (boucle de polling,
// par défaut quotidienne) démarrée au lancement de l'application, qui reste le
// filet de sécurité si aucune recherche n'a lieu pendant une longue période.

// IngestFunc exécute un cycle d'ingestion complet.
type IngestFunc func(ctx context.Context) (success, failed int, err error)

// Refresher est un garde-fou stateful : au plus un rafraîchissement en vol, au
// plus un par `ttl`.
type Refresher struct {
	ttl    time.Duration
	ingest IngestFunc
	// baseCtx borne la durée de vie des rafraîchissements : il doit survivre à
	// la requête HTTP qui en déclenche un (le contexte de la requête est annulé
	// dès que la réponse est écrite), et s'arrêter à l'extinction du service.
	baseCtx context.Context
	now     func() time.Time

	mu            sync.Mutex
	lastRefreshAt time.Time
	hasRefreshed  bool
	running       bool
	wg            sync.WaitGroup
}

// NewRefresher crée un Refresher. `baseCtx` est le contexte de l'application.
func NewRefresher(baseCtx context.Context, ttl time.Duration, ingest IngestFunc) *Refresher {
	return &Refresher{ttl: ttl, ingest: ingest, baseCtx: baseCtx, now: time.Now}
}

// IsStale indique si les données sont périmées : jamais rafraîchies, ou
// rafraîchies il y a plus de `ttl`.
func (r *Refresher) IsStale() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isStaleLocked()
}

func (r *Refresher) isStaleLocked() bool {
	if !r.hasRefreshed {
		return true
	}
	return r.now().Sub(r.lastRefreshAt) > r.ttl
}

// TriggerIfStale programme un rafraîchissement en tâche de fond si les données
// sont périmées. Ne bloque jamais.
//
// No-op si un rafraîchissement est déjà en cours : évite qu'une rafale de
// recherches simultanées ne déclenche autant de téléchargements concurrents du
// fichier national.
func (r *Refresher) TriggerIfStale() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.isStaleLocked() || r.running {
		return
	}
	r.running = true
	r.wg.Add(1)
	go r.refresh()
}

// Wait bloque jusqu'à la fin du rafraîchissement en cours, s'il y en a un.
func (r *Refresher) Wait() { r.wg.Wait() }

func (r *Refresher) refresh() {
	defer r.wg.Done()
	defer func() {
		// L'horodatage est posé même en cas d'échec : un flux en panne ne doit
		// pas être re-téléchargé à chaque recherche.
		r.mu.Lock()
		r.lastRefreshAt, r.hasRefreshed, r.running = r.now(), true, false
		r.mu.Unlock()
	}()
	defer func() {
		// Ne doit jamais faire remonter d'erreur à l'appelant, ni faire tomber le process.
		if recovered := recover(); recovered != nil {
			slog.Error("stations_search_refresh_failed", "error", fmt.Sprint(recovered))
		}
	}()

	if _, _, err := r.ingest(r.baseCtx); err != nil {
		if r.baseCtx.Err() != nil {
			// Extinction du service en cours : interruption normale, pas une panne.
			slog.Info("stations_search_refresh_interrupted")
			return
		}
		slog.Error("stations_search_refresh_failed", "error", err.Error())
	}
}
