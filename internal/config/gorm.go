package config

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewDatabase(viper *viper.Viper, log *logrus.Logger) *gorm.DB {
	username := viper.GetString("database.username")
	password := viper.GetString("database.password")
	host := viper.GetString("database.host")
	port := viper.GetInt("database.port")
	database := viper.GetString("database.name")
	idleConnection := poolValue(log, viper.GetInt("database.pool.idle"), defaultPoolIdle, "database.pool.idle")
	maxConnection := poolValue(log, viper.GetInt("database.pool.max"), defaultPoolMax, "database.pool.max")
	maxLifeTimeConnection := poolValue(log, viper.GetInt("database.pool.lifetime"), defaultPoolLifetime, "database.pool.lifetime")

	// TLS is NOT implemented: the DSN below has no tls parameter, so the
	// MySQL connection is plain (or provider-terminated, e.g. Aiven).
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local", username, password, host, port, database)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.New(&logrusWriter{Logger: log}, logger.Config{
			SlowThreshold:             time.Second * 5,
			Colorful:                  false,
			IgnoreRecordNotFoundError: true,
			ParameterizedQueries:      true,
			LogLevel:                  logger.Info,
		}),
	})
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	connection, err := db.DB()
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	connection.SetMaxIdleConns(idleConnection)
	connection.SetMaxOpenConns(maxConnection)
	connection.SetConnMaxLifetime(time.Second * time.Duration(maxLifeTimeConnection))

	return db
}

// Database pool fallbacks. database/sql treats 0 as "unlimited open/idle
// connections", and a 0 lifetime means connections are never recycled, so
// an unset or explicitly zero pool setting is replaced by these values.
const (
	defaultPoolIdle     = 10
	defaultPoolMax      = 100
	defaultPoolLifetime = 300 // seconds
)

// poolValue returns the configured pool setting, warning and falling back
// to the sane default when it is zero (or negative).
func poolValue(log *logrus.Logger, configured, fallback int, key string) int {
	if configured > 0 {
		return configured
	}
	if log != nil {
		log.Warnf("%s is %d; using %d (0 means unlimited connections / no connection recycling)", key, configured, fallback)
	}
	return fallback
}

type logrusWriter struct {
	Logger *logrus.Logger
}

func (l *logrusWriter) Printf(message string, args ...interface{}) {
	l.Logger.Tracef(message, args...)
}
