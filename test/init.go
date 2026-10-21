package test

import (
	"golang-clean-architecture/internal/config"
	"golang-clean-architecture/internal/repository"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

var viperConfig *viper.Viper

var redisClient *redis.Client

var log *logrus.Logger

var locationCacheRepository *repository.LocationCacheRepository

func init() {
	viperConfig = config.NewViper()
	redisClient = config.NewRedisClient(viperConfig)
	log = config.NewLogger(viperConfig)

	locationCacheRepository = repository.NewLocationCacheRepository(redisClient, log)
}
