package messagebus

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// Verifies the change-stream bus against a MongoDB reached from the host,
// which is the arrangement the README documents for local development: the
// database in Compose, the service run directly with `go run ./cmd/baas`.
func TestBusAgainstHostReachableMongo(t *testing.T) {
	uri := os.Getenv("PROBE_MONGO_URI")
	if uri == "" {
		t.Skip("set PROBE_MONGO_URI to run this probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	bus, err := NewMessageBus[string](ctx, logger.NewLogger(), uri, "probe_msgs")
	if err != nil {
		t.Fatalf("NewMessageBus: %v", err)
	}

	got := make(chan string, 1)
	started := make(chan struct{})
	go func() {
		_ = bus.OnMessage(ctx, "probe-session", func() { close(started) }, func(evt string) {
			select {
			case got <- evt:
			default:
			}
		})
	}()

	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("change stream never started")
	}

	if err := bus.Send(ctx, "probe-session", "hello-from-host"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case v := <-got:
		if v != "hello-from-host" {
			t.Fatalf("got %q", v)
		}
		t.Log("change stream delivered the message over the host connection")
	case <-time.After(30 * time.Second):
		t.Fatal("no change-stream delivery within 30s")
	}
}
