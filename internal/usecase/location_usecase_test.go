package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

func newTestLocationUseCase(repo LocationRepository) *LocationUsecase {
	return NewLocationUseCase(repo, nil, validator.New(), logrus.New())
}

func TestGetLocationReturnsLastHeartbeat(t *testing.T) {
	now := time.Now().UnixMilli()
	repo := &fakeLocationRepository{
		sender:     &entity.LocationCache{UserId: "user-1", Latitude: -6.2088, Longitude: 106.8456},
		lastSeenMs: map[string]int64{"user-1": now},
	}
	uc := newTestLocationUseCase(repo)

	got, err := uc.GetLocation(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetLocation error = %v", err)
	}
	if repo.gotSenderID != "user-1" {
		t.Fatalf("looked up %q, want user-1", repo.gotSenderID)
	}
	if got.UserId != "user-1" || got.Latitude != -6.2088 || got.Longitude != 106.8456 {
		t.Fatalf("unexpected location: %+v", got)
	}
	if got.LastSeen != now || got.Stale {
		t.Fatalf("last_seen/stale = %d/%v, want %d/false", got.LastSeen, got.Stale, now)
	}
}

func TestGetLocationMarksStaleHeartbeat(t *testing.T) {
	repo := &fakeLocationRepository{
		sender:     &entity.LocationCache{UserId: "user-1", Latitude: -6.2088, Longitude: 106.8456},
		lastSeenMs: map[string]int64{"user-1": time.Now().Add(-3 * time.Minute).UnixMilli()},
	}
	uc := newTestLocationUseCase(repo)

	got, err := uc.GetLocation(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("GetLocation error = %v", err)
	}
	if !got.Stale {
		t.Fatal("Stale = false, want true for a heartbeat older than 2 minutes")
	}
}

func TestGetLocationNotFound(t *testing.T) {
	repo := &fakeLocationRepository{senderErr: model.ErrUserLocationNotFound}
	uc := newTestLocationUseCase(repo)

	if _, err := uc.GetLocation(context.Background(), "missing"); !errors.Is(err, fiber.ErrNotFound) {
		t.Fatalf("GetLocation error = %v, want fiber.ErrNotFound", err)
	}
}

type fakeLocationHistory struct {
	userID    string
	longitude float64
	latitude  float64
	err       error
	calls     int
}

func (f *fakeLocationHistory) Upsert(ctx context.Context, userId string, longitude, latitude float64) error {
	f.calls++
	f.userID = userId
	f.longitude = longitude
	f.latitude = latitude
	return f.err
}

func TestListNearbyMasksAndFilters(t *testing.T) {
	now := time.Now().UnixMilli()
	repo := &fakeLocationRepository{
		sender: &entity.LocationCache{UserId: "user-1", Latitude: -6.2, Longitude: 106.8},
		lastSeenMs: map[string]int64{
			"user-1": now,
			"user-2": now,
			"user-3": now - 3*60*1000, // stale, must be dropped
			"user-4": now,
		},
		nearbyDetailed: []entity.GeoLocation{
			{UserId: "user-1", Latitude: -6.2, Longitude: 106.8, DistanceMeters: 0},
			{UserId: "user-2", Latitude: -6.2088, Longitude: 106.8456, DistanceMeters: 123.4},
			{UserId: "user-3", Latitude: -6.21, Longitude: 106.85, DistanceMeters: 200},
			{UserId: "user-4", Latitude: -6.25, Longitude: 106.9, DistanceMeters: 900},
		},
	}
	uc := newTestLocationUseCase(repo)

	got, err := uc.ListNearby(context.Background(), "user-1", 0)
	if err != nil {
		t.Fatalf("ListNearby error = %v", err)
	}
	if got.RadiusMeters != defaultNearbyRadiusMeters {
		t.Fatalf("radius = %v, want %v", got.RadiusMeters, defaultNearbyRadiusMeters)
	}
	if !got.Masked || got.Count != 2 || len(got.Responders) != 2 {
		t.Fatalf("unexpected response: %+v", got)
	}
	first := got.Responders[0]
	if first.UserId != "user-2" || first.DistanceMeters != 123 {
		t.Fatalf("unexpected first responder: %+v", first)
	}
	if first.Latitude != -6.209 || first.Longitude != 106.846 {
		t.Fatalf("coordinates not masked: %+v", first)
	}
	if got.Responders[1].UserId != "user-4" {
		t.Fatalf("unexpected second responder: %+v", got.Responders[1])
	}
}

func TestListNearbyRequiresHeartbeat(t *testing.T) {
	uc := newTestLocationUseCase(&fakeLocationRepository{senderErr: model.ErrUserLocationNotFound})

	if _, err := uc.ListNearby(context.Background(), "user-1", 1000); !errors.Is(err, fiber.ErrNotFound) {
		t.Fatalf("ListNearby error = %v, want fiber.ErrNotFound", err)
	}
}

func TestListNearbyRejectsInvalidRadius(t *testing.T) {
	repo := &fakeLocationRepository{sender: &entity.LocationCache{UserId: "user-1"}}
	uc := newTestLocationUseCase(repo)

	for _, radius := range []float64{99, 5001} {
		if _, err := uc.ListNearby(context.Background(), "user-1", radius); !errors.Is(err, fiber.ErrBadRequest) {
			t.Fatalf("radius %v error = %v, want fiber.ErrBadRequest", radius, err)
		}
	}
}

func TestUpdateLocationUpsertsHistory(t *testing.T) {
	repo := &fakeLocationRepository{}
	history := &fakeLocationHistory{}
	uc := NewLocationUseCase(repo, history, validator.New(), logrus.New())

	longitude, latitude := 106.8456, -6.2088
	ok, err := uc.UpdateLocation(context.Background(), &model.UpdateLocationRequest{
		UserId:    "user-1",
		Longitude: &longitude,
		Latitude:  &latitude,
	})
	if err != nil || !ok {
		t.Fatalf("UpdateLocation = %v, %v", ok, err)
	}
	if len(repo.saved) != 1 || repo.saved[0].Latitude != latitude {
		t.Fatalf("redis save missing: %+v", repo.saved)
	}
	if history.calls != 1 || history.userID != "user-1" || history.longitude != longitude || history.latitude != latitude {
		t.Fatalf("history upsert = %+v", history)
	}
}
