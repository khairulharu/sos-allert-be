package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"golang-clean-architecture/internal/model"

	"github.com/redis/go-redis/v9"
)

// ClassifyStoreRepository keeps the result of the most recent audio
// classification per user, so POST /api/sos can reuse the server-computed
// triage instead of trusting a severity the client sends. Single-use: the
// trigger consumes (and deletes) it, and the short TTL keeps a stale clip
// from classifying a much later alert.
type ClassifyStoreRepository struct {
	Client *redis.Client
	TTL    time.Duration
}

func NewClassifyStoreRepository(client *redis.Client, ttl time.Duration) *ClassifyStoreRepository {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return &ClassifyStoreRepository{Client: client, TTL: ttl}
}

// Save stores the classification for a user, replacing an older one.
func (r *ClassifyStoreRepository) Save(ctx context.Context, userID string, info model.TriageInfo) error {
	if r == nil || r.Client == nil {
		return nil
	}
	payload, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode classify result: %w", err)
	}
	if err := r.Client.Set(ctx, classifyStoreKey+userID, payload, r.TTL).Err(); err != nil {
		return fmt.Errorf("store classify result: %w", err)
	}
	return nil
}

// Take returns the pending classification and deletes it, so one clip can
// only classify one alert.
func (r *ClassifyStoreRepository) Take(ctx context.Context, userID string) (*model.TriageInfo, error) {
	if r == nil || r.Client == nil {
		return nil, nil
	}
	key := classifyStoreKey + userID
	raw, err := r.Client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, fmt.Errorf("read classify result: %w", err)
	}
	if err := r.Client.Del(ctx, key).Err(); err != nil {
		return nil, fmt.Errorf("consume classify result: %w", err)
	}
	var info model.TriageInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("decode classify result: %w", err)
	}
	return &info, nil
}
