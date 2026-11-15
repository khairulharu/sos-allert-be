package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang-clean-architecture/internal/config"
)

// shutdownTimeout bounds the drain: in-flight SOS requests get 10 s to
// finish before the listener is abandoned.
const shutdownTimeout = 10 * time.Second

func main() {
	viperConfig := config.NewViper()
	log := config.NewLogger(viperConfig)
	db := config.NewDatabase(viperConfig, log)
	validate := config.NewValidator(viperConfig)
	app := config.NewFiber(viperConfig)
	producer := config.NewKafkaProducer(viperConfig, log)
	redisClient := config.NewRedisClient(viperConfig)

	// Graceful shutdown: SIGINT/SIGTERM cancels the background loops
	// (active-SOS gauge, durable grace-period dispatch outbox) and drains
	// the HTTP listener instead of dropping every in-flight request. The
	// SOS fan-out itself is durable, so a restart inside the grace period
	// no longer loses it (#40).
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	config.Bootstrap(&config.BootstrapConfig{
		DB:          db,
		App:         app,
		Log:         log,
		Validate:    validate,
		Config:      viperConfig,
		Producer:    producer,
		RedisClient: redisClient,
		Context:     rootCtx,
	})

	webPort := viperConfig.GetInt("web.port")
	// Listen blocks, so it runs in its own goroutine and the main
	// goroutine waits for a signal.
	listenErr := make(chan error, 1)
	go func() {
		listenErr <- app.Listen(fmt.Sprintf(":%d", webPort))
	}()

	select {
	case err := <-listenErr:
		// A startup failure (port taken, TLS misconfig) stays fatal.
		if err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	case <-rootCtx.Done():
		log.Info("shutdown signal received, draining the server")
		// Restore the default signal handling so a second Ctrl-C kills the
		// process immediately instead of waiting out the timeout.
		stop()
		if err := app.ShutdownWithTimeout(shutdownTimeout); err != nil {
			log.Errorf("Graceful shutdown failed: %v", err)
		}
		log.Info("server stopped")
	}
}
