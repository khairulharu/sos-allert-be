package usecase

import (
	"context"
	"errors"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"io"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func noopLogger() *logrus.Logger {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return log
}

// fakeLocationRepository satisfies LocationRepository without Redis.
type fakeLocationRepository struct {
	sender    *entity.LocationCache
	senderErr error
	nearby    []string
	nearbyErr error

	// lastSeenMs keyed by userId; zero value means "not found" → returns error
	lastSeenMs map[string]int64

	gotSenderID    string
	gotLongitude   float64
	gotLatitude    float64
	gotRadius      float64
	geoSearchCalls int
}

func (f *fakeLocationRepository) Get(ctx context.Context, userId string) (*entity.LocationCache, error) {
	f.gotSenderID = userId
	if f.senderErr != nil {
		return nil, f.senderErr
	}
	return f.sender, nil
}

func (f *fakeLocationRepository) GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64) ([]string, error) {
	f.geoSearchCalls++
	f.gotLongitude = longitude
	f.gotLatitude = latitude
	f.gotRadius = radiusMeters
	if f.nearbyErr != nil {
		return nil, f.nearbyErr
	}
	return f.nearby, nil
}

func (f *fakeLocationRepository) GetLastSeen(ctx context.Context, userId string) (int64, error) {
	if f.lastSeenMs == nil {
		return 0, errors.New("last_seen not found")
	}
	ts, ok := f.lastSeenMs[userId]
	if !ok {
		return 0, errors.New("last_seen not found")
	}
	return ts, nil
}

// fakeAlertRepository satisfies AlertSaver without a real DB.
type fakeAlertRepository struct {
	createErr    error
	createCalled bool
	lastAlert    *entity.Alert
}

func (f *fakeAlertRepository) Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error {
	f.createCalled = true
	f.lastAlert = alert
	return f.createErr
}

// fakeNotifier satisfies Notifier without real push infra.
type fakeNotifier struct {
	lastRecipients []string
	lastAlert      model.Alert
	notifyErr      error
	notifyCalled   bool
}

func (f *fakeNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	f.notifyCalled = true
	f.lastRecipients = recipientIDs
	f.lastAlert = alert
	return f.notifyErr
}

// freshLastSeen returns a last_seen timestamp that is well within the stale threshold.
func freshLastSeen() int64 {
	return time.Now().UnixMilli() - 30_000 // 30 seconds ago
}

// staleLastSeen returns a last_seen timestamp that is beyond the stale threshold.
func staleLastSeen() int64 {
	return time.Now().UnixMilli() - 3*60*1000 // 3 minutes ago
}

func newUseCase(locRepo LocationRepository, alertRepo AlertSaver, notifier Notifier) *TriggerSOSUseCase {
	return NewTriggerSOSUseCase(nil, noopLogger(), locRepo, alertRepo, notifier)
}

func TestTriggerSOSUseCaseExecute(t *testing.T) {
	sender := &entity.LocationCache{
		UserId:    "sender",
		Longitude: 108.8272,
		Latitude:  -6.1754,
	}

	tests := []struct {
		name   string
		nearby []string
		want   []string
	}{
		{
			name:   "excludes sender from nearby users",
			nearby: []string{"sender", "user-b", "user-c"},
			want:   []string{"user-b", "user-c"},
		},
		{
			name:   "only sender is nearby",
			nearby: []string{"sender"},
			want:   []string{},
		},
		{
			name:   "no nearby users",
			nearby: []string{},
			want:   []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lastSeen := map[string]int64{}
			for _, id := range tc.nearby {
				lastSeen[id] = freshLastSeen()
			}
			locRepo := &fakeLocationRepository{sender: sender, nearby: tc.nearby, lastSeenMs: lastSeen}
			alertRepo := &fakeAlertRepository{}
			notifier := &fakeNotifier{}
			useCase := newUseCase(locRepo, alertRepo, notifier)

			result, err := useCase.Execute(context.Background(), sender.UserId)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			if locRepo.gotSenderID != sender.UserId {
				t.Errorf("Get() userId = %s, want %s", locRepo.gotSenderID, sender.UserId)
			}
			if locRepo.gotLongitude != sender.Longitude || locRepo.gotLatitude != sender.Latitude {
				t.Errorf("GeoSearch() center = (%v, %v), want (%v, %v)",
					locRepo.gotLongitude, locRepo.gotLatitude, sender.Longitude, sender.Latitude)
			}
			if locRepo.gotRadius != sosRadiusMeters {
				t.Errorf("GeoSearch() radius = %v, want %v", locRepo.gotRadius, sosRadiusMeters)
			}
			if !alertRepo.createCalled {
				t.Error("AlertRepository.Create() was not called")
			}
			if alertRepo.lastAlert.SenderID != sender.UserId {
				t.Errorf("alert.SenderID = %s, want %s", alertRepo.lastAlert.SenderID, sender.UserId)
			}
			if alertRepo.lastAlert.Longitude != sender.Longitude || alertRepo.lastAlert.Latitude != sender.Latitude {
				t.Errorf("alert location = (%v, %v), want (%v, %v)",
					alertRepo.lastAlert.Longitude, alertRepo.lastAlert.Latitude, sender.Longitude, sender.Latitude)
			}
			if !notifier.notifyCalled {
				t.Error("Notifier.Notify() was not called")
			}

			got := result.Recipients
			if len(got) != len(tc.want) {
				t.Fatalf("Execute() recipients = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("recipients[%d] = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestTriggerSOSUseCaseStaleFiltering(t *testing.T) {
	sender := &entity.LocationCache{UserId: "sender", Longitude: 108.8272, Latitude: -6.1754}

	locRepo := &fakeLocationRepository{
		sender: sender,
		nearby: []string{"sender", "fresh-user", "stale-user"},
		lastSeenMs: map[string]int64{
			"sender":     freshLastSeen(),
			"fresh-user": freshLastSeen(),
			"stale-user": staleLastSeen(),
		},
	}
	alertRepo := &fakeAlertRepository{}
	notifier := &fakeNotifier{}
	useCase := newUseCase(locRepo, alertRepo, notifier)

	result, err := useCase.Execute(context.Background(), sender.UserId)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(result.Recipients) != 1 || result.Recipients[0] != "fresh-user" {
		t.Errorf("recipients = %v, want [fresh-user]", result.Recipients)
	}
}

func TestTriggerSOSUseCaseExecuteGetError(t *testing.T) {
	repoErr := errors.New("sender location not found")
	locRepo := &fakeLocationRepository{senderErr: repoErr}
	alertRepo := &fakeAlertRepository{}
	notifier := &fakeNotifier{}
	useCase := newUseCase(locRepo, alertRepo, notifier)

	result, err := useCase.Execute(context.Background(), "sender")

	if !errors.Is(err, repoErr) {
		t.Fatalf("Execute() error = %v, want %v", err, repoErr)
	}
	if result != nil {
		t.Errorf("Execute() result = %v, want nil", result)
	}
	if alertRepo.createCalled {
		t.Error("AlertRepository.Create() must not be called when Get fails")
	}
	if locRepo.geoSearchCalls != 0 {
		t.Errorf("GeoSearch() called %d times, want 0", locRepo.geoSearchCalls)
	}
}

func TestTriggerSOSUseCaseExecuteGeoSearchError(t *testing.T) {
	repoErr := errors.New("redis unavailable")
	locRepo := &fakeLocationRepository{
		sender:    &entity.LocationCache{UserId: "sender", Longitude: 108.8272, Latitude: -6.1754},
		nearbyErr: repoErr,
	}
	alertRepo := &fakeAlertRepository{}
	notifier := &fakeNotifier{}
	useCase := newUseCase(locRepo, alertRepo, notifier)

	result, err := useCase.Execute(context.Background(), "sender")

	if !errors.Is(err, repoErr) {
		t.Fatalf("Execute() error = %v, want %v", err, repoErr)
	}
	if result != nil {
		t.Errorf("Execute() result = %v, want nil", result)
	}
}

func TestTriggerSOSUseCaseExecuteAlertSaveError(t *testing.T) {
	saveErr := errors.New("db write failed")
	locRepo := &fakeLocationRepository{
		sender: &entity.LocationCache{UserId: "sender", Longitude: 108.8272, Latitude: -6.1754},
	}
	alertRepo := &fakeAlertRepository{createErr: saveErr}
	notifier := &fakeNotifier{}
	useCase := newUseCase(locRepo, alertRepo, notifier)

	result, err := useCase.Execute(context.Background(), "sender")

	if !errors.Is(err, saveErr) {
		t.Fatalf("Execute() error = %v, want %v", err, saveErr)
	}
	if result != nil {
		t.Errorf("Execute() result = %v, want nil", result)
	}
	if locRepo.geoSearchCalls != 0 {
		t.Errorf("GeoSearch() called %d times, want 0 after save failure", locRepo.geoSearchCalls)
	}
}
