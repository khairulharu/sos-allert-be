package config

import (
	"context"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/viper"
)

func NewRedisClient(v *viper.Viper) *redis.Client {
	redisClient := redis.NewClient(&redis.Options{
		Addr:     v.GetString("redis.addr"),
		Username: v.GetString("redis.user"),
		Password: v.GetString("redis.pass"),
		DB:       v.GetInt("redis.db"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := redisClient.Ping(ctx).Err(); err != nil {
		_ = redisClient.Close()
		log.Fatalf("Failed to connect to redis client: %v", err)
	}
	return redisClient
}
