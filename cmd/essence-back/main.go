// Commande essence-back : point d'entrée de l'API OpenHexa Essence.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openhexa-fr/core/config"
	"github.com/openhexa-fr/core/es"

	"github.com/openhexa-fr/essence-back/internal/api"
	appconfig "github.com/openhexa-fr/essence-back/internal/config"
	"github.com/openhexa-fr/essence-back/internal/stations"
)

const shutdownTimeout = 15 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("essence_api_failed", "error", err.Error())
		os.Exit(1)
	}
}

func run() error {
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	settings, err := appconfig.Load()
	if err != nil {
		return err
	}

	// SIGTERM : arrêt demandé par l'orchestrateur (scale-to-zero, nouvelle révision).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Bloque jusqu'à ce que le cluster réponde (backoff exponentiel, ~100 s max).
	client, err := es.Connect(ctx, settings.ES)
	if err != nil {
		return err
	}

	alias := settings.StationsAlias()
	indexName := alias + "-000001"
	if _, err := es.CreateIndex(ctx, client, indexName, stations.Mapping(), nil); err != nil {
		return err
	}
	if _, err := es.EnsureAlias(ctx, client, alias, indexName); err != nil {
		return err
	}

	ingest := func(ctx context.Context) (int, int, error) {
		return stations.Ingest(ctx, client, alias, settings.DataGouvLiveURL)
	}
	refresher := stations.NewRefresher(ctx, settings.SearchRefreshTTL, ingest)

	var background sync.WaitGroup
	background.Add(1)
	go func() {
		defer background.Done()
		stations.PollingLoop(ctx, settings.PollingInterval,
			func(ctx context.Context) bool { return stations.IndexHasData(ctx, client, alias) }, ingest)
	}()

	server := &http.Server{
		Addr:              settings.Addr,
		Handler:           (&api.Server{ES: client, Refresher: refresher, Index: alias}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	slog.Info("essence_api_started", "addr", settings.Addr)

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			stop()
			background.Wait()
			return err
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = server.Shutdown(shutdownCtx)
	stop() // arrête le polling et les rafraîchissements en cours
	background.Wait()
	refresher.Wait()
	slog.Info("essence_api_stopped")
	return err
}
