package messagebus

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.uber.org/atomic"

	"golang.org/x/sync/errgroup"

	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type TestMsg struct {
	ID string `bson:"ID"`
}

func TestMessageBus(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connStr, cleanup, err := startMongodb()
	Expect(err).To(BeNil())
	defer cleanup()

	broker, err := NewMessageBus[TestMsg](ctx, logger.NewLogger(), connStr, "messagebus")
	Expect(err).To(BeNil())

	sessionID := lo.RandomString(5, lo.LowerCaseLettersCharset)

	errG, ctx := errgroup.WithContext(ctx)

	receivedWrongEvent := atomic.NewBool(false)
	receivedCorrectEvent := atomic.NewBool(false)
	// expect to receive my id
	errG.Go(func() error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return broker.OnMessage(ctx, sessionID,
			func() {
				// spam with different ids
				lo.ForEach(lo.Range(10), func(idx int, _ int) {
					err = broker.Send(ctx, fmt.Sprintf("not-my-id-%d", idx), TestMsg{
						ID: fmt.Sprintf("not-my-id-%d", idx),
					})
					Expect(err).To(BeNil())
				})
				// send my id
				err = broker.Send(ctx, sessionID, TestMsg{
					ID: "test-id",
				})
				Expect(err).To(BeNil())
			},
			func(evt TestMsg) {
				if evt.ID != "test-id" {
					receivedWrongEvent.Store(true)
				} else {
					receivedCorrectEvent.Store(true)
				}
			})
	})
	_ = errG.Wait()
	Expect(receivedWrongEvent.Load()).To(BeFalse())
	Expect(receivedCorrectEvent.Load()).To(BeTrue())
}

// TestMessageBusPerformance tests the performance improvements with sessionID filtering
func TestMessageBusPerformance(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connStr, cleanup, err := startMongodb()
	Expect(err).To(BeNil())
	defer cleanup()

	broker, err := NewMessageBus[TestMsg](ctx, logger.NewLogger(), connStr, "message")
	Expect(err).To(BeNil())

	targetSessionID := "performance-test-session"
	noiseSessionPrefix := "noise-session"
	numNoiseSessions := 50
	messagesPerSession := 20

	// Create a lot of noise in different sessions
	for i := 0; i < numNoiseSessions; i++ {
		for j := 0; j < messagesPerSession; j++ {
			err = broker.Send(ctx, fmt.Sprintf("%s-%d", noiseSessionPrefix, i), TestMsg{
				ID: fmt.Sprintf("noise-%d-%d", i, j),
			})
			Expect(err).To(BeNil())
		}
	}

	// Test that we only receive messages for our target session
	receivedMessages := make([]TestMsg, 0)
	var mu sync.Mutex

	errG, ctx := errgroup.WithContext(ctx)

	// Start listening for messages
	errG.Go(func() error {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return broker.OnMessage(ctx, targetSessionID,
			func() {
				// Send target messages after subscription starts
				for i := 0; i < 5; i++ {
					err := broker.Send(ctx, targetSessionID, TestMsg{
						ID: fmt.Sprintf("target-%d", i),
					})
					Expect(err).To(BeNil())
				}
			},
			func(evt TestMsg) {
				mu.Lock()
				receivedMessages = append(receivedMessages, evt)
				mu.Unlock()
			})
	})

	_ = errG.Wait()

	// Verify we only received messages for our target session
	mu.Lock()
	Expect(len(receivedMessages)).To(Equal(5))
	for i, msg := range receivedMessages {
		Expected := fmt.Sprintf("target-%d", i)
		Expect(msg.ID).To(Equal(Expected))
	}
	mu.Unlock()
}

// TestMessageBusIndexUsage verifies that the sessionId index is being used
func TestMessageBusIndexUsage(t *testing.T) {
	RegisterTestingT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connStr, cleanup, err := startMongodb()
	Expect(err).To(BeNil())
	defer cleanup()

	broker, err := NewMessageBus[TestMsg](ctx, logger.NewLogger(), connStr, "messagebus")
	Expect(err).To(BeNil())

	// Access the underlying MongoDB client to check indexes
	mongoBroker := broker.(*mongodbMessageBus[TestMsg])

	// List indexes on the messages collection
	indexes, err := mongoBroker.messages.Indexes().List(ctx)
	Expect(err).To(BeNil())

	// Check that sessionId index exists
	foundSessionIdIndex := false
	for indexes.Next(ctx) {
		var index bson.M
		err := indexes.Decode(&index)
		Expect(err).To(BeNil())

		if name, ok := index["name"].(string); ok && name == "sessionId_1" {
			foundSessionIdIndex = true
			break
		}
	}

	Expect(foundSessionIdIndex).To(BeTrue(), "sessionId_1 index should exist")
}

// BenchmarkMessageBusWithFiltering benchmarks the performance with database-level filtering
func BenchmarkMessageBusWithFiltering(b *testing.B) {
	ctx := context.Background()
	connStr, cleanup, err := startMongodb()
	if err != nil {
		b.Fatalf("Failed to start MongoDB: %v", err)
	}
	defer cleanup()

	broker, err := NewMessageBus[TestMsg](ctx, logger.NewLogger(), connStr, "messagebus")
	if err != nil {
		b.Fatalf("Failed to create message bus: %v", err)
	}

	targetSession := "benchmark-session"

	// Pre-populate with noise data
	for i := 0; i < 100; i++ {
		for j := 0; j < 10; j++ {
			_ = broker.Send(ctx, fmt.Sprintf("noise-%d", i), TestMsg{
				ID: fmt.Sprintf("noise-%d-%d", i, j),
			})
		}
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		messageReceived := make(chan bool, 1)

		go func() {
			_ = broker.OnMessage(ctx, targetSession,
				func() {
					_ = broker.Send(ctx, targetSession, TestMsg{
						ID: fmt.Sprintf("benchmark-%d", i),
					})
				},
				func(evt TestMsg) {
					select {
					case messageReceived <- true:
					default:
					}
				})
		}()

		select {
		case <-messageReceived:
			// Message received successfully
		case <-time.After(1 * time.Second):
			b.Errorf("Timeout waiting for message %d", i)
		}

		cancel()
	}
}

func startMongodb() (string, func(), error) {
	ctx := context.Background()
	mongodbContainer, err := mongodb.Run(ctx, "mongo:6", mongodb.WithReplicaSet("test"))
	if err != nil {
		return "", nil, err
	}
	connStr, err := mongodbContainer.ConnectionString(ctx)
	if err != nil {
		return "", nil, err
	}
	urlObj, err := url.Parse(connStr)
	if err != nil {
		return "", nil, err
	}

	urlObj.Path = "/" + lo.RandomString(5, lo.LowerCaseLettersCharset)
	return connStr, func() {
		_ = mongodbContainer.Terminate(ctx)
	}, err
}
