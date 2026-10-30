package usecase

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type fakeIdempotency struct {
	mu     sync.Mutex
	values map[string]string
}

func (f *fakeIdempotency) Claim(ctx context.Context, key string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]string{}
	}
	if _, ok := f.values[key]; ok {
		return false, nil
	}
	f.values[key] = "pending"
	return true, nil
}

func (f *fakeIdempotency) Get(ctx context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.values[key]
	if v == "pending" {
		return "", nil
	}
	return v, nil
}

func (f *fakeIdempotency) Set(ctx context.Context, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.values == nil {
		f.values = map[string]string{}
	}
	f.values[key] = value
	return nil
}

type fakeRateLimiter struct {
	allow bool
	err   error
	calls int
}

func (f *fakeRateLimiter) Allow(ctx context.Context, userID string, limit int, window time.Duration) (bool, error) {
	f.calls++
	return f.allow, f.err
}

type fakeUserReader struct {
	users map[string]*entity.User
}

func (f *fakeUserReader) GetByID(ctx context.Context, id string) (*entity.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, errors.New("user not found")
}

func userWithPIN(id, pin string) *entity.User {
	hash, _ := bcrypt.GenerateFromPassword([]byte(pin), bcrypt.MinCost)
	h := string(hash)
	return &entity.User{ID: id, Name: "Sender", SafePINHash: &h}
}

type fakePublisher struct {
	events []model.SOSEvent
	err    error
}

func (f *fakePublisher) Send(ctx context.Context, event model.SOSEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, event)
	return nil
}

type fakeHub struct {
	mu        sync.Mutex
	published map[string][][]byte
}

func (f *fakeHub) Publish(alertID string, payload []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.published == nil {
		f.published = map[string][][]byte{}
	}
	f.published[alertID] = append(f.published[alertID], payload)
}

func (f *fakeHub) Subscribe(alertID string) (<-chan []byte, func()) {
	ch := make(chan []byte)
	return ch, func() { close(ch) }
}

type fakeEmergencyContacts struct {
	contacts []*entity.EmergencyContact
}

func (f *fakeEmergencyContacts) Create(ctx context.Context, db *gorm.DB, c *entity.EmergencyContact) error {
	f.contacts = append(f.contacts, c)
	return nil
}

func (f *fakeEmergencyContacts) ListByUserID(ctx context.Context, db *gorm.DB, userID string) ([]*entity.EmergencyContact, error) {
	var out []*entity.EmergencyContact
	for _, c := range f.contacts {
		if c.UserID == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

func triggerSetup(t *testing.T) (*TriggerSOSUseCase, *fakeLocationRepository, *fakeAlertRepository, *fakeNotifier) {
	t.Helper()
	sender := &entity.LocationCache{UserId: "sender", Longitude: 106.8456, Latitude: -6.2088}
	locRepo := &fakeLocationRepository{
		sender: sender,
		nearby: []string{"sender", "user-b", "user-c", "user-d"},
		lastSeenMs: map[string]int64{
			"sender": freshLastSeen(), "user-b": freshLastSeen(),
			"user-c": freshLastSeen(), "user-d": freshLastSeen(),
		},
	}
	alertRepo := &fakeAlertRepository{}
	notifier := &fakeNotifier{}
	uc := newUseCase(locRepo, alertRepo, notifier)
	return uc, locRepo, alertRepo, notifier
}

func TestTriggerSOSWithBodyCoordinates(t *testing.T) {
	uc, locRepo, alertRepo, notifier := triggerSetup(t)
	lat, lng := -6.2091, 106.8460
	acc := 4.5
	batt := 18
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{
		Latitude: &lat, Longitude: &lng, AccuracyMeters: &acc,
		BatteryPercentage: &batt, EmergencyType: "MEDICAL",
	}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if len(locRepo.saved) != 1 || locRepo.saved[0].Latitude != lat {
		t.Fatalf("Redis location not refreshed from body: %+v", locRepo.saved)
	}
	if alertRepo.lastAlert.EmergencyType != "MEDICAL" || alertRepo.lastAlert.BatteryPercentage == nil || *alertRepo.lastAlert.BatteryPercentage != 18 {
		t.Fatalf("alert telemetry not stored: %+v", alertRepo.lastAlert)
	}
	if result.Alert.EmergencyType != "MEDICAL" || !notifier.notifyCalled {
		t.Fatalf("result = %+v, notifyCalled = %v", result, notifier.notifyCalled)
	}
	// Adaptive radius stops at 1 km once 3 recipients are found.
	if result.RadiusMeters != 1000 || locRepo.geoSearchCalls != 1 {
		t.Fatalf("radius = %v, geoSearchCalls = %d, want 1000/1", result.RadiusMeters, locRepo.geoSearchCalls)
	}
}

func TestTriggerSOSIdempotencyReplay(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	idem := &fakeIdempotency{}
	uc.Deps.Idempotency = idem
	req := &model.TriggerSOSRequest{}
	first, err := uc.ExecuteWithRequest(context.Background(), "sender", req, "key-1")
	if err != nil {
		t.Fatalf("first trigger error = %v", err)
	}
	second, err := uc.ExecuteWithRequest(context.Background(), "sender", req, "key-1")
	if err != nil {
		t.Fatalf("replay error = %v", err)
	}
	if second.Alert.ID != first.Alert.ID {
		t.Fatalf("replay alert id = %s, want %s", second.Alert.ID, first.Alert.ID)
	}
	if len(alertRepo.alerts) != 1 {
		t.Fatalf("alerts created = %d, want 1 (no duplicate)", len(alertRepo.alerts))
	}
}

func TestTriggerSOSRateLimited(t *testing.T) {
	uc, _, alertRepo, notifier := triggerSetup(t)
	uc.Deps.RateLimiter = &fakeRateLimiter{allow: false}
	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
	if alertRepo.createCalled || notifier.notifyCalled {
		t.Fatal("rate-limited trigger must not create or notify")
	}
}

func TestTriggerSOSRateLimiterErrorFailsOpen(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.RateLimiter = &fakeRateLimiter{allow: false, err: errors.New("redis down")}
	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("limiter error should fail open, got %v", err)
	}
	if !alertRepo.createCalled {
		t.Fatal("alert was not created")
	}
}

func TestTriggerSOSPublishesKafkaEvent(t *testing.T) {
	uc, _, _, notifier := triggerSetup(t)
	pub := &fakePublisher{}
	uc.Deps.Publisher = pub
	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if len(pub.events) != 1 || pub.events[0].EventType != "sos.dispatched" {
		t.Fatalf("events = %+v", pub.events)
	}
	if notifier.notifyCalled {
		t.Fatal("direct notify must be skipped when Kafka publishing succeeds")
	}
}

func TestCancelSOS(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.Users = &fakeUserReader{users: map[string]*entity.User{"sender": userWithPIN("sender", "1234")}}
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	// Wrong PIN is rejected and the alert stays active.
	if _, err := uc.Cancel(context.Background(), "sender", alertID, &model.CancelSOSRequest{PIN: "0000", Reason: "FALSE_ALARM"}); !errors.Is(err, model.ErrInvalidPIN) {
		t.Fatalf("wrong PIN error = %v, want ErrInvalidPIN", err)
	}
	if alertRepo.alerts[alertID].Status != entity.AlertStatusTriggered {
		t.Fatal("alert changed after wrong PIN")
	}
	// Non-owner cannot cancel.
	if _, err := uc.Cancel(context.Background(), "other", alertID, &model.CancelSOSRequest{PIN: "1234"}); !errors.Is(err, model.ErrNotAlertOwner) {
		t.Fatalf("non-owner error = %v, want ErrNotAlertOwner", err)
	}
	// Correct PIN cancels.
	res, err := uc.Cancel(context.Background(), "sender", alertID, &model.CancelSOSRequest{PIN: "1234", Reason: "FALSE_ALARM"})
	if err != nil {
		t.Fatalf("cancel error = %v", err)
	}
	if res.Status != entity.AlertStatusCancelled || alertRepo.alerts[alertID].Status != entity.AlertStatusCancelled {
		t.Fatalf("cancel result = %+v", res)
	}
	// Second cancel is an invalid transition.
	if _, err := uc.Cancel(context.Background(), "sender", alertID, &model.CancelSOSRequest{PIN: "1234"}); !errors.Is(err, model.ErrInvalidTransition) {
		t.Fatalf("second cancel error = %v, want ErrInvalidTransition", err)
	}
}

func TestCancelSOSWithoutPINConfigured(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Users = &fakeUserReader{users: map[string]*entity.User{"sender": {ID: "sender"}}}
	created, _ := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	_, err := uc.Cancel(context.Background(), "sender", created.Alert.ID, &model.CancelSOSRequest{PIN: "1234"})
	if !errors.Is(err, model.ErrPINNotConfigured) {
		t.Fatalf("error = %v, want ErrPINNotConfigured", err)
	}
}

func TestRespondSOSAndDetailMasking(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Users = &fakeUserReader{users: map[string]*entity.User{"sender": {ID: "sender", Name: "Jane Doe"}}}
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	// Sender cannot respond to their own alert.
	lat, lng := -6.2100, 106.8470
	if _, err := uc.Respond(context.Background(), "sender", alertID, &model.RespondSOSRequest{ResponderLatitude: &lat, ResponderLongitude: &lng}); err == nil {
		t.Fatal("self-response must fail")
	}
	resp, err := uc.Respond(context.Background(), "user-b", alertID, &model.RespondSOSRequest{ResponderLatitude: &lat, ResponderLongitude: &lng})
	if err != nil {
		t.Fatalf("respond error = %v", err)
	}
	if resp.ETASeconds <= 0 || resp.VictimDistanceMeters <= 0 {
		t.Fatalf("respond result = %+v", resp)
	}
	// Detail for the responder shows exact coords and 1 responder.
	detail, err := uc.GetAlert(context.Background(), "user-b", alertID)
	if err != nil {
		t.Fatalf("get detail error = %v", err)
	}
	if detail.Masked || detail.Status != entity.AlertStatusAcknowledged || detail.RespondersEnRoute != 1 {
		t.Fatalf("responder detail = %+v", detail)
	}
	// Detail for a stranger is masked.
	stranger, err := uc.GetAlert(context.Background(), "stranger", alertID)
	if err != nil {
		t.Fatalf("stranger detail error = %v", err)
	}
	if !stranger.Masked {
		t.Fatalf("stranger detail must be masked: %+v", stranger)
	}
	if stranger.LatestLocation.Latitude == created.Alert.Location.Latitude {
		t.Fatal("masked latitude equals exact latitude")
	}
}

func TestBreadcrumbBroadcastAndResolve(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	hub := &fakeHub{}
	uc.Deps.Hub = hub
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID
	lat, lng := -6.2092, 106.8462
	speed := 4.2
	if _, err := uc.AddBreadcrumb(context.Background(), "sender", alertID, &model.BreadcrumbRequest{Latitude: &lat, Longitude: &lng, SpeedMps: &speed}); err != nil {
		t.Fatalf("breadcrumb error = %v", err)
	}
	if len(hub.published[alertID]) != 1 {
		t.Fatalf("hub publishes = %d, want 1", len(hub.published[alertID]))
	}
	crumbs, err := uc.ListBreadcrumbs(context.Background(), "sender", alertID)
	if err != nil || len(crumbs) != 1 {
		t.Fatalf("breadcrumbs = %+v, err = %v", crumbs, err)
	}
	detail, err := uc.Resolve(context.Background(), "sender", alertID)
	if err != nil {
		t.Fatalf("resolve error = %v", err)
	}
	if detail.Status != entity.AlertStatusResolved {
		t.Fatalf("status = %s, want RESOLVED", detail.Status)
	}
	if _, err := uc.AddBreadcrumb(context.Background(), "sender", alertID, &model.BreadcrumbRequest{Latitude: &lat, Longitude: &lng}); !errors.Is(err, model.ErrInvalidTransition) {
		t.Fatalf("breadcrumb on resolved alert error = %v, want ErrInvalidTransition", err)
	}
}

func TestAlertStatusTransitions(t *testing.T) {
	triggered := &entity.Alert{Status: entity.AlertStatusTriggered}
	if !triggered.CanTransitionTo(entity.AlertStatusCancelled) || triggered.CanTransitionTo(entity.AlertStatusTriggered) {
		t.Fatal("TRIGGERED transitions wrong")
	}
	resolved := &entity.Alert{Status: entity.AlertStatusResolved}
	if resolved.CanTransitionTo(entity.AlertStatusCancelled) {
		t.Fatal("RESOLVED must be terminal")
	}
}

func TestDistanceAndETA(t *testing.T) {
	// ~240 m apart (README respond example).
	d := CalculateDistanceMeters(-6.2088, 106.8456, -6.2100, 106.8470)
	if d < 150 || d > 350 {
		t.Fatalf("distance = %v, want ~240m", d)
	}
	if eta := EstimateETASeconds(240); eta < 120 || eta > 240 {
		t.Fatalf("ETA for 240m = %d", eta)
	}
	if !CalculateDistance(-6.2088, 106.8456, -6.2089, 106.8457) {
		t.Fatal("nearby points must be < 1 km")
	}
}

func TestEmergencyContactCRUD(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	contacts := &fakeEmergencyContacts{}
	uc.Deps.EmergencyContacts = contacts
	created, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
		UserID: "sender", ContactName: "Ibu", PhoneNumber: "+6281234567890", Relationship: "Mother",
	})
	if err != nil {
		t.Fatalf("create contact error = %v", err)
	}
	list, err := uc.ListEmergencyContacts(context.Background(), "sender")
	if err != nil || len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
}
