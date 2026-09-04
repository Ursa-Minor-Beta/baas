package sessions

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"

	"github.com/Ursa-Minor-Beta/baas/internal/tempfiles"
	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

const (
	sessionsCollection = "active_sessions"
)

type IsActive struct {
	Active bool `json:"active"`
}

type ActiveSession struct {
	ID          string          `json:"ID" bson:"_id"`
	BrowserOpts dto.BrowserOpts `json:"browserOpts" bson:"browserOpts"`
	CreatedAt   time.Time       `json:"createdAt" bson:"createdAt"`
	ExpiresAt   time.Time       `json:"expiresAt" bson:"expiresAt"`
	TtlSeconds  int64           `json:"ttlSeconds" bson:"ttlSeconds"`
}

type Registry interface {
	IsSessionActive(ctx context.Context, sessionID string) (bool, error)
	GetSession(ctx context.Context, sessionID string) (*ActiveSession, error)
	GetActiveSessions(ctx context.Context) ([]ActiveSession, error)
	UpsertActiveSession(ctx context.Context, sessionID string, opts dto.BrowserOpts) error
	RemoveActiveSession(ctx context.Context, sessionID string) error
	RemoveExpiredSessions(ctx context.Context) error
	RecordTempFile(ctx context.Context, sessionID, filePath string) error
}

type noopRegistry struct{}

func (n noopRegistry) RecordTempFile(ctx context.Context, sessionID, filePath string) error {
	return nil
}

func (n noopRegistry) IsSessionActive(ctx context.Context, sessionID string) (bool, error) {
	return false, nil
}

func (n noopRegistry) GetSession(ctx context.Context, sessionID string) (*ActiveSession, error) {
	return nil, nil
}

func (n noopRegistry) GetActiveSessions(ctx context.Context) ([]ActiveSession, error) {
	return nil, nil
}

func (n noopRegistry) UpsertActiveSession(ctx context.Context, sessionID string, opts dto.BrowserOpts) error {
	return nil
}

func (n noopRegistry) RemoveActiveSession(ctx context.Context, sessionID string) error {
	return nil
}

func (n noopRegistry) RemoveExpiredSessions(ctx context.Context) error {
	return nil
}

func NewNoopRegistry() Registry {
	return &noopRegistry{}
}

func NewRegistry(log logger.Logger, db *mongo.Database, tempFiles tempfiles.Manager) Registry {
	if tempFiles == nil {
		tempFiles = tempfiles.NewNoopManager()
	}
	return &registry{
		log:       log,
		sessions:  db.Collection(sessionsCollection),
		tempFiles: tempFiles,
	}
}

type registry struct {
	log       logger.Logger
	sessions  *mongo.Collection
	tempFiles tempfiles.Manager
}

func (r *registry) RecordTempFile(ctx context.Context, sessionID, filePath string) error {
	if r.tempFiles == nil {
		return nil
	}
	return r.tempFiles.Record(ctx, sessionID, filePath)
}

func (r *registry) IsSessionActive(ctx context.Context, sessionID string) (bool, error) {
	_, err := r.GetSession(ctx, sessionID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	return err == nil, err
}

func (r *registry) GetSession(ctx context.Context, sessionID string) (*ActiveSession, error) {
	res := r.sessions.FindOne(ctx, bson.M{"_id": sessionID})
	if res.Err() != nil && !errors.Is(res.Err(), mongo.ErrNoDocuments) {
		return nil, errors.Wrapf(res.Err(), "failed to find session")
	}
	if errors.Is(res.Err(), mongo.ErrNoDocuments) {
		return nil, res.Err()
	}
	var session ActiveSession
	if err := res.Decode(&session); err != nil {
		return nil, errors.Wrapf(err, "failed to decode session")
	}
	return &session, nil
}

func (r *registry) GetActiveSessions(ctx context.Context) ([]ActiveSession, error) {
	cur, err := r.sessions.Find(ctx, bson.M{})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to find active sessions")
	}
	var res []ActiveSession
	if err := cur.All(ctx, &res); err != nil {
		return nil, errors.Wrapf(err, "failed to decode active sessions")
	}
	return res, nil
}

func (r *registry) UpsertActiveSession(ctx context.Context, sessionID string, opts dto.BrowserOpts) error {
	sessionTimeout, err := time.ParseDuration(opts.Timeout)
	if err != nil {
		return errors.Wrapf(err, "failed to parse session duration of %q", opts.Timeout)
	}
	if sessionID == "" {
		return errors.Errorf("sessionID cannot be empty")
	}
	session := ActiveSession{
		ID:          sessionID,
		BrowserOpts: opts,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(sessionTimeout),
		TtlSeconds:  int64(sessionTimeout.Seconds()),
	}
	res, err := r.sessions.UpdateOne(ctx, bson.M{"_id": sessionID}, bson.M{"$set": session}, options.Update().SetUpsert(true))
	if err != nil {
		return errors.Wrapf(err, "failed to upsert session")
	}
	if res.UpsertedCount == 0 && res.ModifiedCount == 0 {
		return errors.Errorf("failed to upsert session: modified count is 0")
	}
	return nil
}

func (r *registry) RemoveActiveSession(ctx context.Context, sessionID string) error {
	if r.tempFiles != nil && sessionID != "" {
		if err := r.tempFiles.Cleanup(ctx, sessionID); err != nil {
			r.log.Warnf(r.log.WithValue(ctx, "error", err.Error()), "failed to cleanup temp files for session")
		}
	}
	_, err := r.sessions.DeleteOne(ctx, bson.M{"_id": sessionID})
	if err != nil {
		return errors.Wrapf(err, "failed to delete session")
	}
	return nil
}

func (r *registry) RemoveExpiredSessions(ctx context.Context) error {
	sessions, err := r.getExpiredSessions(ctx)
	if err != nil {
		return errors.Wrapf(err, "failed to collect expired sessions")
	}
	if r.tempFiles != nil {
		for _, session := range sessions {
			if err := r.tempFiles.Cleanup(ctx, session.ID); err != nil {
				r.log.Warnf(r.log.WithValue(ctx, "error", err.Error()), "failed to cleanup temp files for expired session")
			}
		}
	}
	_, err = r.sessions.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": lo.Map(sessions, func(s ActiveSession, _ int) string {
		return s.ID
	})}})
	return err
}

func (r *registry) getExpiredSessions(ctx context.Context) ([]ActiveSession, error) {
	cursor, err := r.sessions.Aggregate(ctx, []bson.M{
		{
			"$project": bson.M{
				"_id":       1,
				"createdAt": 1,
				"expiresAt": bson.M{
					"$dateAdd": bson.M{
						"startDate": "$createdAt",
						"unit":      "second",
						"amount":    "$ttlSeconds",
					},
				},
			},
		},
		{
			"$match": bson.M{
				"expiresAt": bson.M{
					"$lte": time.Now(),
				},
			},
		},
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to execute aggregation")
	}
	var expiredSessions []ActiveSession
	err = cursor.All(ctx, &expiredSessions)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal cursor")
	}
	return expiredSessions, nil
}
