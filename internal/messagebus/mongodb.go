package messagebus

import (
	"context"
	"embed"
	"encoding/json"
	"net/url"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/mongodb"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

//go:embed embed/migrations
var migrations embed.FS

const (
	defaultDatabaseName = "baas"
)

type mongodbMessageBus[T any] struct {
	log          logger.Logger
	databaseName string
	mongo        *mongo.Client
	database     *mongo.Database
	messages     *mongo.Collection
	cancels      []func()
}

type Broker[T any] interface {
	Send(ctx context.Context, sessionID string, evt T) error
	OnMessage(ctx context.Context, sessionID string, onSubStarted func(), onMsgCallback func(evt T)) error
}

type storedMessage struct {
	Id        primitive.ObjectID `bson:"_id"`
	SessionID string             `bson:"sessionId"`
	Message   string             `bson:"message"`
}

func NewMessageBus[T any](ctx context.Context, log logger.Logger, mongoUriString string, colName string) (Broker[T], error) {
	s := &mongodbMessageBus[T]{
		log: log,
	}
	mongoUri, err := url.Parse(mongoUriString)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to parse mongo uri")
	} else if mongoUri != nil {
		s.databaseName = strings.TrimPrefix(mongoUri.Path, "/")
		if s.databaseName == "" {
			s.databaseName = defaultDatabaseName
		}
		if client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoUriString)); err != nil {
			return nil, errors.Wrapf(err, "failed to conect to Mongo")
		} else {
			s.mongo = client
		}
	}

	if source, err := iofs.New(migrations, "embed/migrations"); err != nil {
		return nil, errors.Wrapf(err, "failed to read migrations for database")
	} else if migrationsDb, err := mongodb.WithInstance(s.mongo, &mongodb.Config{DatabaseName: s.databaseName}); err != nil {
		return nil, errors.Wrapf(err, "failed to open migrations connection")
	} else if m, err := migrate.NewWithInstance("iofs", source, s.databaseName, migrationsDb); err != nil {
		return nil, errors.Wrapf(err, "failed to init migrations")
	} else if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return nil, errors.Wrapf(err, "failed to migrate database")
	} else {
		s.database = s.mongo.Database(s.databaseName)
		s.messages = s.database.Collection(colName)
		s.cancels = append(s.cancels, func() {
			_ = s.mongo.Disconnect(ctx)
		})
	}

	return s, nil
}

func (m *mongodbMessageBus[T]) Send(ctx context.Context, sessionID string, evt T) error {
	msgBytes, err := json.Marshal(evt)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal event")
	}
	res, err := m.messages.InsertOne(ctx, storedMessage{
		Id:        primitive.NewObjectID(),
		SessionID: sessionID,
		Message:   string(msgBytes),
	})
	if err != nil {
		return err
	}
	if res.InsertedID == nil {
		return errors.Errorf("didn't return inserted id")
	}
	return nil
}

func (m *mongodbMessageBus[T]) OnMessage(ctx context.Context, sessionID string, onSubStarted func(), onMsgCallback func(evt T)) error {
	opts := options.ChangeStream().SetFullDocument(options.UpdateLookup)

	// Optimized pipeline: filter by sessionID at database level
	cs, err := m.messages.Watch(ctx, mongo.Pipeline{
		bson.D{bson.E{Key: "$match", Value: bson.D{
			bson.E{Key: "operationType", Value: "insert"},
			bson.E{Key: "fullDocument.sessionId", Value: sessionID},
		}}},
		bson.D{
			bson.E{Key: "$project", Value: bson.M{"_id": 1, "fullDocument": 1, "ns": 1, "documentKey": 1}},
		},
	}, opts)
	if err != nil {
		return err
	}

	defer func() {
		if closeErr := cs.Close(context.Background()); closeErr != nil {
			m.log.Errorf(m.log.WithValue(ctx, "error", closeErr.Error()), "failed to close change stream")
		}
	}()

	onSubStarted()

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		default:
			if cs.Next(ctx) {
				re := cs.Current.Index(1)
				var message storedMessage
				if err := bson.Unmarshal(re.Value().Value, &message); err != nil {
					m.log.Errorf(m.log.WithValue(ctx, "error", err.Error()), "failed to unmarshal stored message")
					continue
				}

				// Since we're filtering at database level, we know this message is for our session
				var evt T
				if err := json.Unmarshal([]byte(message.Message), &evt); err != nil {
					m.log.Errorf(m.log.WithValue(ctx, "error", err.Error()), "failed to unmarshal event")
				} else {
					onMsgCallback(evt)
				}
			} else if err := cs.Err(); err != nil {
				return errors.Wrapf(err, "change stream error for session %s", sessionID)
			}
		}
	}
}
