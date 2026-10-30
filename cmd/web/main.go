package main

import (
	"fmt"
	"golang-clean-architecture/internal/config"
)

func main() {
	viperConfig := config.NewViper()
	log := config.NewLogger(viperConfig)
	db := config.NewDatabase(viperConfig, log)
	validate := config.NewValidator(viperConfig)
	app := config.NewFiber(viperConfig)
	producer := config.NewKafkaProducer(viperConfig, log)
	redisClient := config.NewRedisClient(viperConfig)

	config.Bootstrap(&config.BootstrapConfig{
		DB:          db,
		App:         app,
		Log:         log,
		Validate:    validate,
		Config:      viperConfig,
		Producer:    producer,
		RedisClient: redisClient,
	})

	// Port precedence: web.port, then the README example's app.port,
	// then 3000. Without the fallback, a config that only sets
	// app.port would start on a random port (":0").
	webPort := viperConfig.GetInt("web.port")
	if webPort == 0 {
		webPort = viperConfig.GetInt("app.port")
	}
	if webPort == 0 {
		webPort = 3000
	}
	err := app.Listen(fmt.Sprintf(":%d", webPort))
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
