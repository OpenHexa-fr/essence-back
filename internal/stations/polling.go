package stations

import (
	"context"
	"log/slog"
	"time"

	"github.com/elastic/go-elasticsearch/v8"

	"github.com/openhexa-fr/core/es"
)

// IndexHasData indique si `indexAlias` contient déjà au moins un document.
//
// Avec `min-replicas: 0` (scale-to-zero), le process redémarre à chaque cold
// start : sans ce check, l'ingestion initiale repartirait de zéro à chaque fois
// alors que les données sont déjà indexées (le rafraîchissement à la demande
// couvre déjà la fraîcheur). Si le comptage échoue, on retente l'ingestion par
// prudence plutôt que de la sauter à tort.
func IndexHasData(ctx context.Context, client *elasticsearch.Client, indexAlias string) bool {
	n, err := es.Count(ctx, client, indexAlias)
	return err == nil && n > 0
}

// PollingLoop est la synchronisation de fond, filet de sécurité en complément
// du rafraîchissement à la demande déclenché par les recherches. Elle ne
// s'arrête que lorsque `ctx` est annulé, jamais sur une erreur d'ingestion.
func PollingLoop(
	ctx context.Context, interval time.Duration,
	hasData func(context.Context) bool, ingest IngestFunc,
) {
	run := func() {
		if _, _, err := ingest(ctx); err != nil && ctx.Err() == nil {
			slog.Error("stations_polling_failed", "error", err.Error())
		}
	}

	if hasData(ctx) {
		slog.Info("stations_polling_initial_run_skipped", "reason", "index_already_populated")
	} else {
		run()
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
