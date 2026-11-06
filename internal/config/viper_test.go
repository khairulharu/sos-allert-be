package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestNormalizeLegacyKeys(t *testing.T) {
	config := viper.New()
	config.Set("database.database", "defaultdb")
	config.Set("redis.host", "redis.example.com")
	config.Set("redis.port", 15164)
	config.Set("redis.password", "secret")
	config.Set("redis.username", "avnadmin")
	config.Set("app.port", 3001)

	normalizeLegacyKeys(config)

	if got := config.GetString("database.name"); got != "defaultdb" {
		t.Fatalf("database.name = %q, want defaultdb", got)
	}
	if got := config.GetString("redis.addr"); got != "redis.example.com:15164" {
		t.Fatalf("redis.addr = %q, want redis.example.com:15164", got)
	}
	if got := config.GetString("redis.pass"); got != "secret" {
		t.Fatalf("redis.pass = %q, want secret", got)
	}
	if got := config.GetString("redis.user"); got != "avnadmin" {
		t.Fatalf("redis.user = %q, want avnadmin", got)
	}
	if got := config.GetInt("web.port"); got != 3001 {
		t.Fatalf("web.port = %d, want 3001", got)
	}
	if got := config.GetInt("log.level"); got != 4 {
		t.Fatalf("log.level = %d, want 4", got)
	}
}

func TestNormalizeLegacyKeysPreservesNewKeys(t *testing.T) {
	config := viper.New()
	config.Set("database.name", "newdb")
	config.Set("database.database", "olddb")
	config.Set("redis.addr", "new-redis:6379")
	config.Set("redis.host", "old-redis")
	config.Set("redis.port", 15164)
	config.Set("redis.pass", "new-secret")
	config.Set("redis.password", "old-secret")
	config.Set("web.port", 3000)
	config.Set("app.port", 4000)
	config.Set("log.level", 6)

	normalizeLegacyKeys(config)

	if got := config.GetString("database.name"); got != "newdb" {
		t.Fatalf("database.name = %q, want newdb", got)
	}
	if got := config.GetString("redis.addr"); got != "new-redis:6379" {
		t.Fatalf("redis.addr = %q, want new-redis:6379", got)
	}
	if got := config.GetString("redis.pass"); got != "new-secret" {
		t.Fatalf("redis.pass = %q, want new-secret", got)
	}
	if got := config.GetInt("web.port"); got != 3000 {
		t.Fatalf("web.port = %d, want 3000", got)
	}
	if got := config.GetInt("log.level"); got != 6 {
		t.Fatalf("log.level = %d, want 6", got)
	}
}
