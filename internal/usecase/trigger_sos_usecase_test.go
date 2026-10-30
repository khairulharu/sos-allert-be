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
	radii          []float64
	geoSearchCalls int
	saved          []*entity.LocationCache
}

func (f *fakeLocationRepository) Get(ctx context.Context, userId string) (*entity.LocationCache, error) {
	f.gotSenderID = userId
	if f.senderErr != nil {
		return nil, f.senderErr
	}
	return f.sender, nil
}

func (f *fakeLocationRepository) Save(ctx context.Context, loc *entity.LocationCache) error {
	f.saved = append(f.saved, loc)
	return nil
}

func (f *fakeLocationRepository) GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64) ([]string, error) {
	f.geoSearchCalls++
	f.gotLongitude = longitude
	f.gotLatitude = latitude
	f.gotRadius = radiusMeters
	f.radii = append(f.radii, radiusMeters)
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

// fakeAlertRepository satisfies AlertStore with in-memory state.
type fakeAlertRepository struct {
	createErr    error
	createCalled bool
	lastAlert    *entity.Alert
	alerts       map[string]*entity.Alert
	breadcrumbs  map[string][]*entity.AlertBreadcrumb
	responses    map[string][]*entity.AlertResponse
}

func (f *fakeAlertRepository) ensure() {
	if f.alerts == nil {
		f.alerts = make(map[string]*entity.Alert)
	}
	if f.breadcrumbs == nil {
		f.breadcrumbs = make(map[string][]*entity.AlertBreadcrumb)
	}
	if f.responses == nil {
		f.responses = make(map[string][]*entity.AlertResponse)
	}
}

func (f *fakeAlertRepository) Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error {
	f.createCalled = true
	f.lastAlert = alert
	if f.createErr != nil {
		return f.createErr
	}
	f.ensure()
	cp := *alert
	f.alerts[alert.ID] = &cp
	return nil
}

func (f *fakeAlertRepository) GetByID(ctx context.Context, db *gorm.DB, id string) (*entity.Alert, error) {
	f.ensure()
	if a, ok := f.alerts[id]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, model.ErrAlertNotFound
}

func (f *fakeAlertRepository) ListActiveBySender(ctx context.Context, db *gorm.DB, senderID string, limit int) ([]*entity.Alert, error) {
	f.ensure()
	var out []*entity.Alert
	for _, a := range f.alerts {
		if a.SenderID == senderID && a.IsActive() {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakeAlertRepository) Transition(ctx context.Context, db *gorm.DB, id string, fromStatuses []string, updates map[string]any) (bool, error) {
	f.ensure()
	a, ok := f.alerts[id]
	if !ok {
		return false, nil
	}
	allowed := false
	for _, s := range fromStatuses {
		if a.Status == s {
			allowed = true
		}
	}
	if !allowed {
		return false, nil
	}
	if v, ok := updates["status"]; ok {
		a.Status = v.(string)
	}
	if v, ok := updates["cancellation_reason"]; ok {
		s := v.(string)
		a.CancellationReason = &s
	}
	if v, ok := updates["cancelled_at"]; ok {
		n := v.(int64)
		a.CancelledAt = &n
	}
	if v, ok := updates["resolved_at"]; ok {
		n := v.(int64)
		a.ResolvedAt = &n
	}
	return true, nil
}

func (f *fakeAlertRepository) ExpireOlderThan(ctx context.Context, db *gorm.DB, beforeMs int64, expiredAtMs int64) (int64, error) {
	return 0, nil
}

func (f *fakeAlertRepository) CreateBreadcrumb(ctx context.Context, db *gorm.DB, b *entity.AlertBreadcrumb) error {
	f.ensure()
	f.breadcrumbs[b.AlertID] = append(f.breadcrumbs[b.AlertID], b)
	return nil
}

func (f *fakeAlertRepository) ListBreadcrumbs(ctx context.Context, db *gorm.DB, alertID string, limit int) ([]*entity.AlertBreadcrumb, error) {
	f.ensure()
	return f.breadcrumbs[alertID], nil
}

func (f *fakeAlertRepository) LatestBreadcrumb(ctx context.Context, db *gorm.DB, alertID string) (*entity.AlertBreadcrumb, error) {
	f.ensure()
	list := f.breadcrumbs[alertID]
	if len(list) == 0 {
		return nil, nil
	}
	return list[len(list)-1], nil
}

func (f *fakeAlertRepository) CreateResponse(ctx context.Context, db *gorm.DB, r *entity.AlertResponse) error {
	f.ensure()
	f.responses[r.AlertID] = append(f.responses[r.AlertID], r)
	return nil
}

func (f *fakeAlertRepository) CountResponders(ctx context.Context, db *gorm.DB, alertID string) (int, error) {
	f.ensure()
	seen := map[string]struct{}{}
	for _, r := range f.responses[alertID] {
		seen[r.ResponderID] = struct{}{}
	}
	return len(seen), nil
}

func (f *fakeAlertRepository) IsResponder(ctx context.Context, db *gorm.DB, alertID, userID string) (bool, error) {
	f.ensure()
	for _, r := range f.responses[alertID] {
		if r.ResponderID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeAlertRepository) CreateMedia(ctx context.Context, db *gorm.DB, m *entity.AlertMedia) error {
	return nil
}

func (f *fakeAlertRepository) PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error) {
	return 0, nil
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

func newUseCase(locRepo LocationRepository, alertRepo AlertStore, notifier Notifier) *TriggerSOSUseCase {
	return NewTriggerSOSUseCase(nil, noopLogger(), TriggerSOSDeps{
		Location: locRepo, Alerts: alertRepo, Notifier: notifier,
	})
}

func newUseCaseWithDeps(deps TriggerSOSDeps) *TriggerSOSUseCase {
	return NewTriggerSOSUseCase(nil, noopLogger(), deps)
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
			// Adaptive radius: first probe is always 1 km; with fewer
			// than 3 recipients it expands through all radii.
			if len(locRepo.radii) == 0 || locRepo.radii[0] != 1000 {
				t.Errorf("first GeoSearch radius = %v, want 1000", locRepo.radii)
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
			if alertRepo.lastAlert.Status != entity.AlertStatusTriggered {
				t.Errorf("alert.Status = %s, want TRIGGERED", alertRepo.lastAlert.Status)
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
