package repository

import (
	"context"
	"testing"
	"time"

	"golang-clean-architecture/internal/model"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestClassifyStoreTakeIsSingleUse(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewClassifyStoreRepository(client, 2*time.Minute)
	ctx := context.Background()

	if info, err := store.Take(ctx, "user-a"); err != nil || info != nil {
		t.Fatalf("empty Take = %+v err = %v, want nil", info, err)
	}

	want := model.TriageInfo{EmergencyType: "MEDICAL", Severity: 0.8, Summary: "Kemungkinan darurat medis"}
	if err := store.Save(ctx, "user-a", want); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if got := server.TTL(classifyStoreKey + "user-a"); got != 2*time.Minute {
		t.Fatalf("TTL = %v, want 2m", got)
	}

	info, err := store.Take(ctx, "user-a")
	if err != nil || info == nil {
		t.Fatalf("Take = %+v err = %v, want the stored triage", info, err)
	}
	if *info != want {
		t.Fatalf("Take = %+v, want %+v", *info, want)
	}
	if info, err := store.Take(ctx, "user-a"); err != nil || info != nil {
		t.Fatalf("second Take = %+v err = %v, want it consumed", info, err)
	}

	// One clip must not classify another user's alert.
	if err := store.Save(ctx, "user-a", want); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	if info, err := store.Take(ctx, "user-b"); err != nil || info != nil {
		t.Fatalf("Take for other user = %+v err = %v, want nil", info, err)
	}
}
