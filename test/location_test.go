package test

import (
	"context"
	"golang-clean-architecture/internal/entity"
	"testing"
)

func TestSave(t *testing.T) {
	dummyUser := entity.LocationCache{
		UserId:    "user-A",
		Longitude: 108.8272,
		Latitude:  -6.1754,
	}

	if err := locationCacheRepository.Save(context.Background(), &dummyUser); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
}

func TestGet(t *testing.T) {
	result, err := locationCacheRepository.Get(context.Background(), "user-B")
	if err != nil {
		t.Fatalf("failed to get user-B: %v", err)
	}

	if result.UserId != "user-B" {
		t.Fatalf("wrong user location, %s", result.UserId)
	}

	t.Log(result)
}

func TestGetAll(t *testing.T) {
	results, err := locationCacheRepository.GetAll(context.Background())
	if err != nil {
		t.Fatalf("failed to get all user location: %v", err)
	}

	if len(results) <= 0 {
		t.Fatalf("failed gett all data must be at least one there: %v", err)
	}

	t.Log(len(results), cap(results))

	for i := range results {
		t.Logf("user: %s, long: %f, lat: %f", results[i].UserId, results[i].Longitude, results[i].Latitude)
	}
}

func TestDelete(t *testing.T) {
	result, err := locationCacheRepository.Delete(context.Background(), "user-B")
	if err != nil {
		t.Fatalf("failed to delete user-A; %v", err)
	}

	if result == false {
		t.Fatalf("failed to delete user-A; %b", err)
	}
}
