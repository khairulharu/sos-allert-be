package config

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
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
