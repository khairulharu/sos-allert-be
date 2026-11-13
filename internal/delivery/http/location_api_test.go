package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
)

type fakeLocationStore struct {
	location       *entity.LocationCache
	lastSeen       int64
	lastSeenByUser map[string]int64
	nearby         []entity.GeoLocation
	getErr         error
}

func (f *fakeLocationStore) Save(ctx context.Context, loc *entity.LocationCache) error {
	f.location = loc
	return nil
}

func (f *fakeLocationStore) Get(ctx context.Context, userId string) (*entity.LocationCache, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.location, nil
}

func (f *fakeLocationStore) GetLastSeen(ctx context.Context, userId string) (int64, error) {
	if f.lastSeenByUser != nil {
		ts, ok := f.lastSeenByUser[userId]
		if !ok {
			return 0, errors.New("last_seen not found")
		}
		return ts, nil
	}
	if f.lastSeen == 0 {
		return 0, errors.New("last_seen not found")
	}
	return f.lastSeen, nil
}

func (f *fakeLocationStore) GetLastSeenBatch(ctx context.Context, userIds []string) (map[string]int64, error) {
	out := make(map[string]int64, len(userIds))
	for _, id := range userIds {
		if f.lastSeenByUser != nil {
			if ts, ok := f.lastSeenByUser[id]; ok {
				out[id] = ts
			}
			continue
		}
		if f.lastSeen != 0 {
			out[id] = f.lastSeen
		}
	}
	return out, nil
}

func (f *fakeLocationStore) GeoSearch(ctx context.Context, longitude, latitude, radiusMeters float64) ([]string, error) {
	return nil, nil
}

func (f *fakeLocationStore) GeoSearchWithDistance(ctx context.Context, longitude, latitude, radiusMeters float64, count int) ([]entity.GeoLocation, error) {
	return f.nearby, nil
}

func newLocationTestApp(store usecase.LocationRepository) *fiber.App {
	uc := usecase.NewLocationUseCase(store, nil, validator.New(), logrus.New())
	controller := deliveryhttp.NewLocationController(uc, logrus.New())

	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return ctx.Status(code).JSON(fiber.Map{"errors": err.Error()})
		},
	})
	app.Use(func(ctx *fiber.Ctx) error {
		ctx.Locals("auth", &model.Auth{ID: "user-1"})
		return ctx.Next()
	})
	app.Get("/api/location", controller.Get)
	app.Get("/api/location/nearby", controller.ListNearby)
	return app
}

func TestGetLocationHTTP(t *testing.T) {
	now := time.Now().UnixMilli()
	store := &fakeLocationStore{
		location: &entity.LocationCache{UserId: "user-1", Latitude: -6.2088, Longitude: 106.8456},
		lastSeen: now,
	}
	app := newLocationTestApp(store)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/location", nil))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body model.WebResponse[*model.LocationStatusResponse]
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if body.Data == nil || body.Data.UserId != "user-1" || body.Data.Stale {
		t.Fatalf("unexpected response: %+v", body.Data)
	}
}

func TestGetLocationHTTPNotFound(t *testing.T) {
	app := newLocationTestApp(&fakeLocationStore{getErr: model.ErrUserLocationNotFound})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/location", nil))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestListNearbyHTTP(t *testing.T) {
	now := time.Now().UnixMilli()
	store := &fakeLocationStore{
		location: &entity.LocationCache{UserId: "user-1", Latitude: -6.2, Longitude: 106.8},
		lastSeenByUser: map[string]int64{
			"user-1": now,
			"user-2": now,
		},
		nearby: []entity.GeoLocation{
			{UserId: "user-1", Latitude: -6.2, Longitude: 106.8, DistanceMeters: 0},
			{UserId: "user-2", Latitude: -6.2088, Longitude: 106.8456, DistanceMeters: 123.4},
		},
	}
	app := newLocationTestApp(store)

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/location/nearby?radius_m=2000", nil))
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read error = %v", err)
	}
	// Issue #11: the distance must not be finer than the coordinate masking.
	if bytes.Contains(raw, []byte(`"distance_m":123`)) {
		t.Fatalf("nearby payload leaks a precise distance: %s", raw)
	}

	var body model.WebResponse[*model.NearbyLocationResponse]
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if body.Data == nil || body.Data.Count != 1 || body.Data.RadiusMeters != 2000 || !body.Data.Masked {
		t.Fatalf("unexpected response: %+v", body.Data)
	}
	responder := body.Data.Responders[0]
	if responder.UserId != "user-2" || responder.Latitude != -6.209 || responder.Longitude != 106.846 || responder.DistanceMeters != 100 {
		t.Fatalf("unexpected responder: %+v", responder)
	}
}

func TestListNearbyHTTPInvalidRadius(t *testing.T) {
	app := newLocationTestApp(&fakeLocationStore{location: &entity.LocationCache{UserId: "user-1"}})

	for _, query := range []string{"?radius_m=abc", "?radius_m=50", "?radius_m=5001"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/location/nearby"+query, nil))
		if err != nil {
			t.Fatalf("request error = %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", query, resp.StatusCode)
		}
	}
}
