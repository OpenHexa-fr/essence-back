package stations

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsStaleBeforeAnyRefresh(t *testing.T) {
	refresher := NewRefresher(t.Context(), 10*time.Minute, nil)

	if !refresher.IsStale() {
		t.Error("jamais rafraîchi : doit être périmé")
	}
}

func TestTriggerIfStaleRunsIngestionInBackground(t *testing.T) {
	var calls atomic.Int32
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 10, 0, nil
	})

	refresher.TriggerIfStale()
	refresher.Wait()

	if calls.Load() != 1 {
		t.Errorf("%d ingestions, attendu 1", calls.Load())
	}
	if refresher.IsStale() {
		t.Error("juste après un rafraîchissement, les données ne sont pas périmées")
	}
}

func TestTriggerIfStaleIsNoopOnceFresh(t *testing.T) {
	var calls atomic.Int32
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 10, 0, nil
	})
	refresher.TriggerIfStale()
	refresher.Wait()

	refresher.TriggerIfStale()
	refresher.Wait()

	if calls.Load() != 1 {
		t.Errorf("%d ingestions, attendu 1", calls.Load())
	}
}

func TestTriggerIfStaleDoesNotStartASecondRefreshWhileInFlight(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return 10, 0, nil
	})

	refresher.TriggerIfStale()
	<-started
	// Une rafale de recherches simultanées ne doit lancer aucun téléchargement de plus.
	for range 20 {
		refresher.TriggerIfStale()
	}
	close(release)
	refresher.Wait()

	if calls.Load() != 1 {
		t.Errorf("%d ingestions concurrentes, attendu 1", calls.Load())
	}
}

func TestRefreshBecomesStaleAgainAfterTheTTL(t *testing.T) {
	now := time.Now()
	var calls atomic.Int32
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 1, 0, nil
	})
	refresher.now = func() time.Time { return now }

	refresher.TriggerIfStale()
	refresher.Wait()
	now = now.Add(9 * time.Minute)
	if refresher.IsStale() {
		t.Error("9 min < TTL de 10 min")
	}
	now = now.Add(2 * time.Minute)
	if !refresher.IsStale() {
		t.Error("11 min > TTL de 10 min")
	}
	refresher.TriggerIfStale()
	refresher.Wait()

	if calls.Load() != 2 {
		t.Errorf("%d ingestions, attendu 2", calls.Load())
	}
}

// Un flux en panne ne doit pas être re-téléchargé à chaque recherche : l'échec
// compte comme une tentative et ne fait pas remonter d'erreur à l'appelant.
func TestAFailedRefreshStillCountsAsAnAttempt(t *testing.T) {
	var calls atomic.Int32
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 0, 0, errors.New("source en panne")
	})

	refresher.TriggerIfStale()
	refresher.Wait()
	refresher.TriggerIfStale()
	refresher.Wait()

	if calls.Load() != 1 {
		t.Errorf("%d tentatives, attendu 1", calls.Load())
	}
}

func TestAPanickingRefreshDoesNotCrashTheProcess(t *testing.T) {
	refresher := NewRefresher(t.Context(), 10*time.Minute, func(context.Context) (int, int, error) {
		panic("boom")
	})

	refresher.TriggerIfStale()
	refresher.Wait()

	if refresher.IsStale() {
		t.Error("la panique doit être absorbée et compter comme une tentative")
	}
}

// Le rafraîchissement doit survivre à la requête qui l'a déclenché : il reçoit
// le contexte de l'application, pas celui de la requête.
func TestRefreshUsesTheApplicationContext(t *testing.T) {
	appCtx, cancel := context.WithCancel(t.Context())
	var got atomic.Value
	refresher := NewRefresher(appCtx, 10*time.Minute, func(ctx context.Context) (int, int, error) {
		got.Store(ctx)
		return 0, 0, nil
	})

	refresher.TriggerIfStale()
	refresher.Wait()
	cancel()

	if got.Load().(context.Context) != appCtx {
		t.Error("le rafraîchissement doit utiliser le contexte de l'application")
	}
}
