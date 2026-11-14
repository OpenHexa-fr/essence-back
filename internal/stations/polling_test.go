package stations

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func runPolling(t *testing.T, interval time.Duration, hasData bool, ingest IngestFunc, runFor time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), runFor)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		PollingLoop(ctx, interval, func(context.Context) bool { return hasData }, ingest)
	}()
	select {
	case <-done:
	case <-time.After(runFor + 2*time.Second):
		t.Fatal("PollingLoop ne s'arrête pas à l'annulation du contexte")
	}
}

func TestPollingSkipsTheInitialRunWhenIndexIsPopulated(t *testing.T) {
	var calls atomic.Int32

	runPolling(t, time.Hour, true, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 1, 0, nil
	}, 100*time.Millisecond)

	if calls.Load() != 0 {
		t.Errorf("%d ingestions, attendu 0 (index déjà peuplé, intervalle non écoulé)", calls.Load())
	}
}

func TestPollingRunsInitiallyWhenIndexIsEmpty(t *testing.T) {
	var calls atomic.Int32

	runPolling(t, time.Hour, false, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 1, 0, nil
	}, 100*time.Millisecond)

	if calls.Load() != 1 {
		t.Errorf("%d ingestions, attendu 1", calls.Load())
	}
}

func TestPollingKeepsRunningAfterErrors(t *testing.T) {
	var calls atomic.Int32

	runPolling(t, 10*time.Millisecond, false, func(context.Context) (int, int, error) {
		calls.Add(1)
		return 0, 0, errors.New("réseau")
	}, 200*time.Millisecond)

	if calls.Load() < 3 {
		t.Errorf("%d ingestions : le polling ne doit jamais s'arrêter sur une erreur", calls.Load())
	}
}
