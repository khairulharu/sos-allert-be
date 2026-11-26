package config

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// database/sql reads 0 as unlimited, so a zero pool setting must fall
// back to a sane default and say so.
func TestPoolValueFallsBackWhenZero(t *testing.T) {
	var out bytes.Buffer
	log := logrus.New()
	log.SetOutput(&out)

	if got := poolValue(log, 0, 10, "database.pool.idle"); got != 10 {
		t.Fatalf("poolValue(0) = %d, want 10", got)
	}
	if got := poolValue(log, -5, 100, "database.pool.max"); got != 100 {
		t.Fatalf("poolValue(-5) = %d, want 100", got)
	}
	if got := poolValue(log, 25, 10, "database.pool.idle"); got != 25 {
		t.Fatalf("poolValue(25) = %d, want 25", got)
	}
	if msg := out.String(); !strings.Contains(msg, "database.pool.idle") || !strings.Contains(msg, "database.pool.max") {
		t.Fatalf("expected warnings for the zero pool settings, got %q", msg)
	}
}

func TestPoolValueWithoutLoggerDoesNotPanic(t *testing.T) {
	if got := poolValue(nil, 0, 10, "database.pool.idle"); got != 10 {
		t.Fatalf("poolValue(nil, 0) = %d, want 10", got)
	}
}

func testDatabaseViper() *viper.Viper {
	v := viper.New()
	v.Set("database.username", "postgres.lzggqmotqnzubrsgfszx")
	v.Set("database.password", "p@ss#word/1")
	v.Set("database.host", "aws-1-ap-northeast-1.pooler.supabase.com")
	v.Set("database.port", 5432)
	v.Set("database.name", "postgres")
	return v
}

// The DSN is a URL, so a managed-provider password with reserved characters
// must survive the round trip instead of being parsed as part of the host.
func TestDatabaseDSNEscapesReservedPasswordCharacters(t *testing.T) {
	dsn := databaseDSN(testDatabaseViper(), nil)

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if parsed.Scheme != "postgres" {
		t.Fatalf("scheme = %q, want postgres", parsed.Scheme)
	}
	password, _ := parsed.User.Password()
	if got := parsed.User.Username(); got != "postgres.lzggqmotqnzubrsgfszx" {
		t.Errorf("username = %q", got)
	}
	if password != "p@ss#word/1" {
		t.Errorf("password = %q, want the original value", password)
	}
	if got := parsed.Host; got != "aws-1-ap-northeast-1.pooler.supabase.com:5432" {
		t.Errorf("host = %q", got)
	}
	if got := parsed.Path; got != "/postgres" {
		t.Errorf("path = %q", got)
	}
}

// An unset sslmode must default to require: a config carried over from the
// MySQL era has no TLS key, and silently using plaintext against Supabase
// fails at connect time with a much less obvious error.
func TestDatabaseDSNDefaultsToRequireTLS(t *testing.T) {
	parsed, err := url.Parse(databaseDSN(testDatabaseViper(), nil))
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if got := parsed.Query().Get("sslmode"); got != "require" {
		t.Errorf("sslmode = %q, want require", got)
	}
}

func TestDatabaseDSNAppendsExtraParams(t *testing.T) {
	v := testDatabaseViper()
	v.Set("database.sslmode", "disable")
	v.Set("database.params", "default_query_exec_mode=simple_protocol")

	parsed, err := url.Parse(databaseDSN(v, nil))
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("sslmode"); got != "disable" {
		t.Errorf("sslmode = %q, want disable", got)
	}
	if got := query.Get("default_query_exec_mode"); got != "simple_protocol" {
		t.Errorf("default_query_exec_mode = %q", got)
	}
}

// A malformed database.params must not take the process down; the driver was
// always able to connect before the key existed.
func TestDatabaseDSNIgnoresMalformedParams(t *testing.T) {
	v := testDatabaseViper()
	v.Set("database.params", "default_query_exec_mode=%zz")

	parsed, err := url.Parse(databaseDSN(v, nil))
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if got := parsed.Query().Get("default_query_exec_mode"); got != "" {
		t.Errorf("malformed params leaked into the DSN: %q", got)
	}
	if got := parsed.Query().Get("sslmode"); got != "require" {
		t.Errorf("sslmode = %q, want require", got)
	}
}

// A missing port must fall back to the PostgreSQL default instead of
// producing host:0.
func TestDatabaseDSNDefaultsPort(t *testing.T) {
	v := testDatabaseViper()
	v.Set("database.port", 0)

	parsed, err := url.Parse(databaseDSN(v, nil))
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if got := parsed.Host; got != "aws-1-ap-northeast-1.pooler.supabase.com:5432" {
		t.Errorf("host = %q, want the default port appended", got)
	}
}
