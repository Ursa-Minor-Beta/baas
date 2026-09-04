package service

import (
	"context"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/Ursa-Minor-Beta/baas/pkg/dto"
)

func (s *Server) getReadabilityFromCache(ctx context.Context, hash string) (*dto.ReadabilityResult, error) {
	foundCache := s.readabilityCache.FindOne(ctx, bson.M{
		"_id": hash,
		"cachedAt": bson.M{
			"$gt": time.Now().Add(-s.cacheTTl),
		},
	})
	if foundCache.Err() != nil {
		return nil, errors.Wrapf(foundCache.Err(), "failed to find cached value")
	}
	var cachedRes dto.ReadabilityCachedResult
	err := foundCache.Decode(&cachedRes)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to decode cached result")
	}
	var res dto.ReadabilityResult

	err = json.Unmarshal([]byte(cachedRes.ResultJson), &res)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to unmarshal cached result json")
	}
	return &res, nil
}

func (s *Server) addToCache(ctx context.Context, hash string, res *dto.ReadabilityResult) error {
	resJson, err := json.Marshal(res)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal json")
	}
	cachedResult := dto.ReadabilityCachedResult{
		ID:         hash,
		CachedAt:   time.Now(),
		ResultJson: string(resJson),
	}
	updateRes, err := s.readabilityCache.UpdateOne(ctx, bson.M{"_id": hash}, bson.M{
		"$set": cachedResult,
	}, &options.UpdateOptions{
		Upsert: lo.ToPtr(true),
	})
	if err != nil {
		return errors.Wrapf(err, "failed to update cache")
	}
	if updateRes.MatchedCount == 0 && updateRes.ModifiedCount == 0 && updateRes.UpsertedCount == 0 {
		return errors.Errorf("cache wasn't updated")
	}
	return nil
}
