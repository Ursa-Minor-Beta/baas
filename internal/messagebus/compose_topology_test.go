package messagebus

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/docker/docker/api/types/container"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// composeMember is the name docker-compose.yaml has the replica set advertise.
// It resolves inside the Compose network and nowhere else.
const composeMember = "mongodb"

// withComposeReplicaSet reproduces docker-compose.yaml: the member advertises
// composeMember:27017, which only resolves inside the container.
func withComposeReplicaSet() testcontainers.CustomizeRequestOption {
	return func(req *testcontainers.GenericContainerRequest) error {
		req.Cmd = append(req.Cmd, "--replSet", "baas")
		if err := testcontainers.WithHostConfigModifier(func(hc *container.HostConfig) {
			hc.ExtraHosts = append(hc.ExtraHosts, composeMember+":127.0.0.1")
		})(req); err != nil {
			return err
		}
		initiate := fmt.Sprintf(
			"rs.initiate({_id:'baas',members:[{_id:0,host:'%s:27017'}]}); "+
				"while (!db.hello().isWritablePrimary) { sleep(200) }", composeMember)
		req.LifecycleHooks = append(req.LifecycleHooks, testcontainers.ContainerLifecycleHooks{
			PostStarts: []testcontainers.ContainerHook{
				func(ctx context.Context, c testcontainers.Container) error {
					return wait.ForExec([]string{"mongosh", "--quiet", "--eval", initiate}).
						WithStartupTimeout(60*time.Second).WaitUntilReady(ctx, c)
				},
			},
		})
		return nil
	}
}

// The README's source-run setup: MongoDB from docker-compose.yaml, BaaS on the
// host through the published port. That only works with directConnection=true,
// which is why .env.example carries it.
func TestMessageBus_hostAgainstComposeReplicaSet(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	RegisterTestingT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	mongoC, err := mongodb.Run(ctx, "mongo:7.0", withComposeReplicaSet())
	Expect(err).To(BeNil())
	t.Cleanup(func() { _ = mongoC.Terminate(context.Background()) })

	endpoint, err := mongoC.ConnectionString(ctx)
	Expect(err).To(BeNil())
	db := lo.RandomString(8, lo.LowerCaseLettersCharset)

	if _, err := net.LookupHost(composeMember); err != nil {
		t.Run("without directConnection the driver chases the advertised name", func(t *testing.T) {
			RegisterTestingT(t)
			client, err := mongo.Connect(ctx, options.Client().
				ApplyURI(endpoint+"/"+db).
				SetServerSelectionTimeout(3*time.Second))
			Expect(err).To(BeNil())
			defer func() { _ = client.Disconnect(context.Background()) }()
			Expect(client.Ping(ctx, nil)).NotTo(Succeed())
		})
	}

	uri := endpoint + "/" + db + "?directConnection=true"
	bus, err := NewMessageBus[TestMsg](ctx, logger.NewLogger(), uri, "messagebus")
	Expect(err).To(BeNil())

	sessionID := lo.RandomString(8, lo.LowerCaseLettersCharset)
	started := make(chan struct{})
	got := make(chan TestMsg, 1)
	errc := make(chan error, 1)
	go func() {
		errc <- bus.OnMessage(ctx, sessionID, func() { close(started) }, func(m TestMsg) {
			select {
			case got <- m:
			default:
			}
		})
	}()

	select {
	case <-started:
	case err := <-errc:
		t.Fatalf("change stream did not start: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("change stream did not start within 30s")
	}

	Expect(bus.Send(ctx, sessionID, TestMsg{ID: "from-host"})).To(Succeed())
	select {
	case m := <-got:
		Expect(m.ID).To(Equal("from-host"))
	case err := <-errc:
		t.Fatalf("change stream ended before delivery: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("no change-stream delivery within 30s")
	}
}
