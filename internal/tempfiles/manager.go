package tempfiles

import (
	"context"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/pkg/errors"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

const collectionName = "temp_files"

// Manager tracks temporary files attached to browser sessions and cleans them up.
type Manager interface {
	Record(ctx context.Context, sessionID, filePath string) error
	Cleanup(ctx context.Context, sessionID string) error
}

type noopManager struct{}

func NewNoopManager() Manager {
	return noopManager{}
}

func (noopManager) Record(ctx context.Context, sessionID, filePath string) error {
	return nil
}

func (noopManager) Cleanup(ctx context.Context, sessionID string) error {
	return nil
}

type mongoManager struct {
	log        logger.Logger
	collection *mongo.Collection
}

func NewMongoManager(log logger.Logger, db *mongo.Database) (Manager, error) {
	if db == nil {
		return nil, errors.Errorf("database must not be nil")
	}
	collection := db.Collection(collectionName)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = collection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "sessionID", Value: 1}},
	})
	return &mongoManager{log: log, collection: collection}, nil
}

type tempFileDoc struct {
	SessionID string    `bson:"sessionID"`
	FilePath  string    `bson:"filePath"`
	CreatedAt time.Time `bson:"createdAt"`
}

func (m *mongoManager) Record(ctx context.Context, sessionID, filePath string) error {
	if sessionID == "" || filePath == "" {
		return errors.Errorf("sessionID and filePath must be provided")
	}
	doc := tempFileDoc{
		SessionID: sessionID,
		FilePath:  filePath,
		CreatedAt: time.Now(),
	}
	if _, err := m.collection.InsertOne(ctx, doc); err != nil {
		return errors.Wrapf(err, "failed to insert temp file record")
	}
	return nil
}

func (m *mongoManager) Cleanup(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.Errorf("sessionID must be provided")
	}
	cursor, err := m.collection.Find(ctx, bson.M{"sessionID": sessionID})
	if err != nil {
		return errors.Wrapf(err, "failed to find temp files for session")
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc tempFileDoc
		if err := cursor.Decode(&doc); err != nil {
			m.log.Errorf(m.log.WithValue(ctx, "error", err.Error()), "failed to decode temp file document")
			continue
		}
		if err := os.Remove(doc.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.log.Warnf(m.log.WithValue(ctx, "error", err.Error()), "failed to remove temp file")
		}
	}
	if err := cursor.Err(); err != nil {
		return errors.Wrapf(err, "cursor failure during temp file cleanup")
	}
	if _, err := m.collection.DeleteMany(ctx, bson.M{"sessionID": sessionID}); err != nil {
		return errors.Wrapf(err, "failed to delete temp file records")
	}
	return nil
}
