package config

import (
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func NewDatabase(viper *viper.Viper, log *logrus.Logger) *gorm.DB {
	idleConnection := poolValue(log, viper.GetInt("database.pool.idle"), defaultPoolIdle, "database.pool.idle")
	maxConnection := poolValue(log, viper.GetInt("database.pool.max"), defaultPoolMax, "database.pool.max")
	maxLifeTimeConnection := poolValue(log, viper.GetInt("database.pool.lifetime"), defaultPoolLifetime, "database.pool.lifetime")

	dsn := databaseDSN(viper, log)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
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

// databaseDSN builds a PostgreSQL URL. The URL form is used instead of the
// keyword/value form so credentials with reserved characters (`@`, `#`, `/`)
// are escaped by url.UserPassword; a managed provider (Supabase, Neon, Aiven)
// generates passwords that contain them, and an unescaped one would make the
// driver parse part of the password as the host.
//
// TLS is controlled by database.sslmode (default "require"): Supabase refuses
// plaintext connections, so "disable" is only for a local server. The optional
// database.params key is appended as extra query parameters; the one case that
// needs it today is `default_query_exec_mode=simple_protocol` when the app is
// pointed at a transaction-mode pooler (port 6543), which cannot reuse
// prepared statements.
func databaseDSN(v *viper.Viper, log *logrus.Logger) string {
	query := url.Values{}
	query.Set("sslmode", sslMode(v.GetString("database.sslmode")))

	port := v.GetInt("database.port")
	if port == 0 {
		port = 5432
	}

	if extra := v.GetString("database.params"); extra != "" {
		parsed, err := url.ParseQuery(extra)
		if err != nil {
			if log != nil {
				log.Warnf("database.params is not a valid query string (%v); ignoring it", err)
			}
		} else {
			for key, values := range parsed {
				for _, value := range values {
					query.Add(key, value)
				}
			}
		}
	}

	return (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(v.GetString("database.username"), v.GetString("database.password")),
		Host:     net.JoinHostPort(v.GetString("database.host"), strconv.Itoa(port)),
		Path:     "/" + v.GetString("database.name"),
		RawQuery: query.Encode(),
	}).String()
}

// sslMode falls back to "require" so a config that predates the PostgreSQL
// switch does not silently drop to a plaintext connection.
func sslMode(configured string) string {
	if configured == "" {
		return "require"
	}
	return configured
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
