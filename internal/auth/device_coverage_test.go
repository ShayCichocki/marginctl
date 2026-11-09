package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDevicePollSlowDownCompoundsAndDefaultsApply(t *testing.T) {
	var n int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			_, _ = w.Write([]byte(`{"status":"slow_down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"authorized","authorization_code":"c","code_verifier":"v"}`))
	}))
	defer issuer.Close()
	var waits []time.Duration
	_, err := mustOAuthClient(t, issuer.URL).PollDeviceWithOptions(context.Background(), DeviceStart{DeviceAuthID: "d"}, DevicePollOptions{
		InitialInterval: 4 * time.Second, SlowDownIncrement: 3 * time.Second,
		Wait: func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(waits) != 2 || waits[0] != 7*time.Second || waits[1] != 10*time.Second {
		t.Fatalf("waits = %v, want 7s then 10s", waits)
	}

	d := DevicePollOptions{}.withDefaults()
	if d.InitialInterval <= 0 || d.SlowDownIncrement <= 0 || d.Timeout <= 0 || d.Wait == nil {
		t.Fatalf("defaults not applied: %+v", d)
	}
}

func TestWaitDevicePollInterval(t *testing.T) {
	if err := waitDevicePollInterval(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitDevicePollInterval(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
