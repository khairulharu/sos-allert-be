package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type fakeIdempotency struct {
	mu          sync.Mutex
	values      map[string]string
	releases    int
	releaseKeys []string
	releaseErr  error
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

// Release mirrors the Redis compare-and-delete: only a pending marker goes.
func (f *fakeIdempotency) Release(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases++
	f.releaseKeys = append(f.releaseKeys, key)
	if f.releaseErr != nil {
		return f.releaseErr
	}
	if f.values[key] == "pending" {
		delete(f.values, key)
	}
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

func (f *fakeUserReader) ListByIDs(ctx context.Context, ids []string) (map[string]*entity.User, error) {
	out := make(map[string]*entity.User, len(ids))
	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
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
	uc.Deps.RateLimitPerHour = 10
	_, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("error = %v, want ErrRateLimited", err)
	}
	if !strings.Contains(err.Error(), "max 10 triggers per hour") {
		t.Fatalf("error = %q, want the configured limit in the message", err)
	}
	if alertRepo.createCalled || notifier.notifyCalled {
		t.Fatal("rate-limited trigger must not create or notify")
	}
}

func TestTriggerSOSRateLimitDisabledSkipsLimiter(t *testing.T) {
	uc, _, alertRepo, notifier := triggerSetup(t)
	uc.Deps.RateLimiter = &fakeRateLimiter{allow: false}
	uc.Deps.RateLimitDisabled = true

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, ""); err != nil {
		t.Fatalf("disabled limit must not block the trigger, got %v", err)
	}
	if !alertRepo.createCalled || !notifier.notifyCalled {
		t.Fatal("trigger must create and dispatch when the limit is disabled")
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
	uc.Deps.Users = &fakeUserReader{users: map[string]*entity.User{
		"sender": {ID: "sender", Name: "Jane Doe"},
		"user-b": {ID: "user-b", Name: "Budi"},
	}}
	hub := &fakeHub{}
	uc.Deps.Hub = hub
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
	// A user who was never notified cannot self-enroll as a responder.
	if _, err := uc.Respond(context.Background(), "stranger", alertID, &model.RespondSOSRequest{ResponderLatitude: &lat, ResponderLongitude: &lng}); !errors.Is(err, model.ErrNotAlertParticipant) {
		t.Fatalf("stranger respond error = %v, want ErrNotAlertParticipant", err)
	}
	resp, err := uc.Respond(context.Background(), "user-b", alertID, &model.RespondSOSRequest{ResponderLatitude: &lat, ResponderLongitude: &lng})
	if err != nil {
		t.Fatalf("respond error = %v", err)
	}
	if resp.ETASeconds <= 0 || resp.VictimDistanceMeters <= 0 {
		t.Fatalf("respond result = %+v", resp)
	}

	// The responder-arrival frame tells open views who is coming.
	var added *model.ResponderAddedMessage
	for _, payload := range hub.published[alertID] {
		var msg model.ResponderAddedMessage
		if json.Unmarshal(payload, &msg) == nil && msg.Type == "RESPONDER_ADDED" {
			added = &msg
		}
	}
	if added == nil || added.ResponderID != "user-b" || added.Name != "Budi" {
		t.Fatalf("RESPONDER_ADDED frame = %+v", added)
	}
	if added.DistanceMeters <= 0 || added.ETASeconds <= 0 {
		t.Fatalf("RESPONDER_ADDED distance/eta = %+v", added)
	}

	// Detail for the responder shows exact coords, the count and the list.
	detail, err := uc.GetAlert(context.Background(), "user-b", alertID)
	if err != nil {
		t.Fatalf("get detail error = %v", err)
	}
	if detail.Masked || detail.Status != entity.AlertStatusAcknowledged || detail.RespondersEnRoute != 1 {
		t.Fatalf("responder detail = %+v", detail)
	}
	if len(detail.Responders) != 1 || detail.Responders[0].UserID != "user-b" || detail.Responders[0].Name != "Budi" {
		t.Fatalf("responder detail list = %+v", detail.Responders)
	}

	// The sender sees who is coming, with distance and ETA.
	senderDetail, err := uc.GetAlert(context.Background(), "sender", alertID)
	if err != nil {
		t.Fatalf("sender detail error = %v", err)
	}
	if len(senderDetail.Responders) != 1 {
		t.Fatalf("sender responders = %+v, want one", senderDetail.Responders)
	}
	if r := senderDetail.Responders[0]; r.Name != "Budi" || r.DistanceMeters <= 0 || r.ETASeconds <= 0 {
		t.Fatalf("sender responder entry = %+v", r)
	}

	// A stranger gets 404, not a masked payload, so alert ids stay
	// unenumerable.
	if _, err := uc.GetAlert(context.Background(), "stranger", alertID); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("stranger detail error = %v, want ErrAlertNotFound", err)
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
	published := hub.published[alertID]
	if len(published) != 2 {
		t.Fatalf("hub publishes after resolve = %d, want 2 (breadcrumb + status)", len(published))
	}
	var status model.AlertStatusMessage
	if err := json.Unmarshal(published[1], &status); err != nil {
		t.Fatalf("status payload decode = %v", err)
	}
	if status.Type != "ALERT_STATUS" || status.AlertID != alertID || status.Status != entity.AlertStatusResolved {
		t.Fatalf("status payload = %+v, want ALERT_STATUS/RESOLVED", status)
	}
}

func TestSOSInboxListsNotifiedAlerts(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	inbox, err := uc.ListInbox(context.Background(), "user-b")
	if err != nil {
		t.Fatalf("ListInbox error = %v", err)
	}
	if len(inbox) != 1 || inbox[0].AlertID != created.Alert.ID {
		t.Fatalf("inbox = %+v, want the triggered alert", inbox)
	}
	empty, err := uc.ListInbox(context.Background(), "user-outside")
	if err != nil || len(empty) != 0 {
		t.Fatalf("inbox for unrelated user = %+v, err = %v", empty, err)
	}
}

func TestMediaUploadListAndDownload(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	store := &fakeMediaStorage{}
	uc.Deps.MediaStore = store
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID
	up, err := uc.UploadMedia(context.Background(), "sender", alertID,
		"panic.m4a", "audio/mp4", int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.URL != "/api/sos/"+alertID+"/media/"+up.ID {
		t.Fatalf("media url = %q", up.URL)
	}
	list, err := uc.ListAlertMedia(context.Background(), "sender", alertID)
	if err != nil || len(list) != 1 || list[0].ID != up.ID {
		t.Fatalf("list = %+v, err = %v", list, err)
	}
	if _, _, err := uc.OpenAlertMedia(context.Background(), "user-outside", alertID, up.ID); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("outsider open error = %v, want ErrAlertNotFound (same as an unknown id)", err)
	}
	lat, lng := -6.21, 106.85
	if _, err := uc.Respond(context.Background(), "user-b", alertID, &model.RespondSOSRequest{
		ResponderLatitude: &lat, ResponderLongitude: &lng,
	}); err != nil {
		t.Fatalf("respond error = %v", err)
	}
	media, reader, err := uc.OpenAlertMedia(context.Background(), "user-b", alertID, up.ID)
	if err != nil {
		t.Fatalf("responder open error = %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil || string(body) != "voice" || media.ContentType != "audio/mp4" {
		t.Fatalf("media body = %q, content type = %q, err = %v", body, media.ContentType, err)
	}
	if _, _, err := uc.OpenAlertMedia(context.Background(), "user-b", alertID, "missing"); !errors.Is(err, model.ErrMediaNotFound) {
		t.Fatalf("missing media error = %v, want ErrMediaNotFound", err)
	}
}

// The read-only tracking paths (breadcrumbs, live stream auth) must answer
// like an unknown id for a non-participant, so they cannot be used to probe
// which alert ids exist.
func TestReadPathsHideForeignAlerts(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	if _, err := uc.ListBreadcrumbs(context.Background(), "stranger", alertID); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("stranger breadcrumbs error = %v, want ErrAlertNotFound", err)
	}
	if _, err := uc.ListBreadcrumbs(context.Background(), "stranger", "no-such-alert"); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("unknown-id breadcrumbs error = %v, want ErrAlertNotFound", err)
	}
	if err := uc.AuthorizeTracking(context.Background(), "stranger", alertID); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("stranger tracking auth error = %v, want ErrAlertNotFound", err)
	}
	if err := uc.AuthorizeTracking(context.Background(), "stranger", "no-such-alert"); !errors.Is(err, model.ErrAlertNotFound) {
		t.Fatalf("unknown-id tracking auth error = %v, want ErrAlertNotFound", err)
	}
	// The participant happy paths are unchanged.
	if err := uc.AuthorizeTracking(context.Background(), "sender", alertID); err != nil {
		t.Fatalf("sender tracking auth error = %v, want nil", err)
	}
	if _, err := uc.ListBreadcrumbs(context.Background(), "sender", alertID); err != nil {
		t.Fatalf("sender breadcrumbs error = %v, want nil", err)
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

// A contact phone becomes a billable SMS target, so only real E.164
// numbers may be stored.
func TestEmergencyContactRejectsInvalidPhoneNumbers(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	contacts := &fakeEmergencyContacts{}
	uc.Deps.EmergencyContacts = contacts

	create := func(phone string) error {
		_, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
			UserID: "sender", ContactName: "Ibu", PhoneNumber: phone, Relationship: "Mother",
		})
		return err
	}

	for _, phone := range []string{
		"abc",               // not a number at all
		"1234",              // premium short code
		"081234567890",      // national format, no country code
		"+123",              // too short
		"+6281234567890123", // too long (17 digits)
		"++6281234567890",   // double plus
	} {
		if err := create(phone); !errors.Is(err, model.ErrInvalidPhoneNumber) {
			t.Fatalf("create(%q) error = %v, want ErrInvalidPhoneNumber", phone, err)
		}
	}
	if len(contacts.contacts) != 0 {
		t.Fatalf("invalid numbers were persisted: %+v", contacts.contacts)
	}

	// A human-formatted number is accepted and stored canonically.
	first, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
		UserID: "sender", ContactName: "Ibu", PhoneNumber: "+62 812-3456-7890", Relationship: "Mother",
	})
	if err != nil {
		t.Fatalf("create formatted number error = %v", err)
	}
	if first.PhoneNumber != "+6281234567890" {
		t.Fatalf("stored phone = %q, want canonical E.164", first.PhoneNumber)
	}

	// The same number in another format is a duplicate for this user.
	if err := create("+62 812 3456 7890"); !errors.Is(err, model.ErrDuplicateEmergencyContact) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateEmergencyContact", err)
	}
}

func TestEmergencyContactRejectsEleventhContact(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	contacts := &fakeEmergencyContacts{}
	uc.Deps.EmergencyContacts = contacts

	for i := 0; i < maxEmergencyContactsPerUser; i++ {
		if _, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
			UserID: "sender", ContactName: fmt.Sprintf("Contact %d", i),
			PhoneNumber: fmt.Sprintf("+62812345%04d", i), Relationship: "Friend",
		}); err != nil {
			t.Fatalf("create contact %d error = %v", i, err)
		}
	}
	if _, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
		UserID: "sender", ContactName: "Eleventh", PhoneNumber: "+628999999999", Relationship: "Friend",
	}); !errors.Is(err, model.ErrTooManyEmergencyContacts) {
		t.Fatalf("eleventh contact error = %v, want ErrTooManyEmergencyContacts", err)
	}
	if len(contacts.contacts) != maxEmergencyContactsPerUser {
		t.Fatalf("stored contacts = %d, want %d", len(contacts.contacts), maxEmergencyContactsPerUser)
	}

	// The cap is per user, not global.
	if _, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
		UserID: "other", ContactName: "Ibu", PhoneNumber: "+628123450000", Relationship: "Mother",
	}); err != nil {
		t.Fatalf("contact for another user error = %v", err)
	}
}

// Rows stored before the E.164 rule must stay readable and must not block
// a new valid contact.
func TestEmergencyContactsKeepLegacyRowsReadable(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	contacts := &fakeEmergencyContacts{contacts: []*entity.EmergencyContact{
		{ID: "legacy", UserID: "sender", ContactName: "Old", PhoneNumber: "0812-ABC", Relationship: "Friend"},
	}}
	uc.Deps.EmergencyContacts = contacts

	list, err := uc.ListEmergencyContacts(context.Background(), "sender")
	if err != nil || len(list) != 1 || list[0].PhoneNumber != "0812-ABC" {
		t.Fatalf("legacy list = %+v, err = %v", list, err)
	}
	if _, err := uc.CreateEmergencyContact(context.Background(), &model.CreateEmergencyContactRequest{
		UserID: "sender", ContactName: "Ibu", PhoneNumber: "+6281234567890", Relationship: "Mother",
	}); err != nil {
		t.Fatalf("create alongside a legacy row error = %v", err)
	}
	if list, err = uc.ListEmergencyContacts(context.Background(), "sender"); err != nil || len(list) != 2 {
		t.Fatalf("list after create = %+v, err = %v", list, err)
	}
}

type fakeMediaStorage struct {
	files   map[string][]byte
	deleted []string
	saveErr error
}

func (f *fakeMediaStorage) Save(ctx context.Context, filename string, r io.Reader) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	path := "/media/" + filename
	f.files[path] = b
	return path, nil
}

func (f *fakeMediaStorage) Open(ctx context.Context, path string) (io.ReadCloser, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, errors.New("media file not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (f *fakeMediaStorage) Delete(ctx context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	delete(f.files, path)
	return nil
}

func TestAcknowledgeRejectsOutsider(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}

	// Only participants may move someone else's incident.
	if _, err := uc.Acknowledge(context.Background(), "stranger", created.Alert.ID); !errors.Is(err, model.ErrNotAlertParticipant) {
		t.Fatalf("outsider acknowledge error = %v, want ErrNotAlertParticipant", err)
	}

	// A notified recipient may acknowledge but still sees masked coords
	// until they respond.
	outsider, err := uc.Acknowledge(context.Background(), "user-b", created.Alert.ID)
	if err != nil {
		t.Fatalf("acknowledge error = %v", err)
	}
	if !outsider.Masked {
		t.Fatalf("recipient acknowledge must be masked: %+v", outsider)
	}
	if outsider.LatestLocation.Latitude == created.Alert.Location.Latitude {
		t.Fatal("masked latitude equals exact latitude")
	}

	// The sender still sees exact coordinates when re-reading the state.
	sender, err := uc.Acknowledge(context.Background(), "sender", created.Alert.ID)
	if err != nil {
		t.Fatalf("sender acknowledge error = %v", err)
	}
	if sender.Masked {
		t.Fatalf("sender acknowledge must not be masked: %+v", sender)
	}
}

type fakeTranscriber struct {
	text           string
	err            error
	gotFilename    string
	gotContentType string
	gotAudio       string
	calls          int
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, filename, contentType string, r io.Reader) (string, error) {
	f.calls++
	f.gotFilename = filename
	f.gotContentType = contentType
	b, _ := io.ReadAll(r)
	f.gotAudio = string(b)
	return f.text, f.err
}

func TestUploadMediaTranscribesAndTriages(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{text: "ada orang pingsan tidak sadar"}
	uc.Deps.Triage = NewAITriage(noopLogger(), nil, NewOfflineClassifier())
	hub := &fakeHub{}
	uc.Deps.Hub = hub

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	up, err := uc.UploadMedia(context.Background(), "sender", alertID,
		"panic.webm", "audio/webm", int64(len("voice-bytes")), strings.NewReader("voice-bytes"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.TranscriptionStatus != "completed" {
		t.Fatalf("status = %q, want completed", up.TranscriptionStatus)
	}
	if up.Transcript != "ada orang pingsan tidak sadar" {
		t.Fatalf("transcript = %q", up.Transcript)
	}
	if up.Triage == nil || up.Triage.EmergencyType != entity.EmergencyTypeMedical || up.Triage.Severity <= 0 {
		t.Fatalf("triage = %+v", up.Triage)
	}
	if up.TranscribedAt == nil {
		t.Fatal("TranscribedAt is nil")
	}
	if !alertRepo.triageUpdated || alertRepo.triageType != entity.EmergencyTypeMedical {
		t.Fatalf("triage update = %+v", alertRepo)
	}
	if alertRepo.alerts[alertID].Summary == nil || *alertRepo.alerts[alertID].Summary == "" {
		t.Fatal("alert summary was not updated")
	}
	if len(alertRepo.media[alertID]) != 1 || alertRepo.media[alertID][0].Transcript == "" {
		t.Fatalf("media = %+v", alertRepo.media[alertID])
	}

	var foundTriage bool
	for _, payload := range hub.published[alertID] {
		var msg model.AlertTriageMessage
		if json.Unmarshal(payload, &msg) == nil && msg.Type == "ALERT_TRIAGE" {
			foundTriage = true
			if msg.EmergencyType != entity.EmergencyTypeMedical || msg.Summary == "" {
				t.Fatalf("triage message = %+v", msg)
			}
		}
	}
	if !foundTriage {
		t.Fatal("ALERT_TRIAGE websocket message not published")
	}
}

func TestUploadMediaTranscriptionFailureKeepsMedia(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{err: errors.New("stt down")}

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"panic.webm", "audio/webm", int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.TranscriptionStatus != "failed" || up.Transcript != "" {
		t.Fatalf("media = %+v", up)
	}
	if alertRepo.triageUpdated {
		t.Fatal("triage must not update on STT failure")
	}
	if len(alertRepo.media[created.Alert.ID]) != 1 {
		t.Fatal("media must still be stored")
	}
}

func TestUploadMediaAudioOverTranscriptionLimit(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{text: "should not be used"}

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"big.webm", "audio/webm", maxTranscriptionBytes+1, strings.NewReader("small"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.TranscriptionStatus != "too_large" {
		t.Fatalf("status = %q, want too_large", up.TranscriptionStatus)
	}
}

// A transcript triage may legitimately score 0 (Jev's lowest rubric level).
// That must reach the repository as a real score, not be skipped as "no
// triage" and leave alerts.severity NULL (#55).
func TestUploadMediaStoresZeroSeverity(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{text: "tidak ada bahaya"}
	uc.Deps.Triage = &fakeTriageEngine{res: TriageResult{
		EmergencyType: entity.EmergencyTypeDanger,
		Severity:      0,
		Summary:       "Potensi bahaya atau ancaman",
	}}

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"calm.webm", "audio/webm", int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.Triage == nil || up.Triage.Severity != 0 || up.Triage.EmergencyType != entity.EmergencyTypeDanger {
		t.Fatalf("response triage = %+v, want DANGER with severity 0", up.Triage)
	}
	if !alertRepo.triageUpdated || !alertRepo.triageSeveritySet || alertRepo.triageSeverity != 0 {
		t.Fatalf("repository update = updated:%v set:%v severity:%v, want a written 0",
			alertRepo.triageUpdated, alertRepo.triageSeveritySet, alertRepo.triageSeverity)
	}
	alert := alertRepo.alerts[created.Alert.ID]
	if alert.Severity == nil || *alert.Severity != 0 {
		t.Fatalf("stored severity = %v, want 0", alert.Severity)
	}
}

// A classifier answer without an emergency type is not an answer (the same
// rule AITriage applies). It must not overwrite the alert with a summary and
// leave whatever type the alert already had (#55).
func TestUploadMediaIgnoresTriageWithoutEmergencyType(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{text: "terima kasih telah menonton"}
	uc.Deps.Triage = &fakeTriageEngine{res: TriageResult{
		Severity: 0.4,
		Summary:  "ringkasan tanpa tipe",
	}}

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{
		EmergencyType: entity.EmergencyTypeMedical,
	}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"silence.webm", "audio/webm", int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.Triage != nil {
		t.Fatalf("triage = %+v, want no signal for a type-less answer", up.Triage)
	}
	if alertRepo.triageUpdated {
		t.Fatal("UpdateTriage must not run for a type-less answer")
	}
	if got := alertRepo.alerts[created.Alert.ID].EmergencyType; got != entity.EmergencyTypeMedical {
		t.Fatalf("emergency_type = %q, want the request's MEDICAL kept", got)
	}
}

// audioEscalationSetup wires a use case whose geo search returns a wider
// set at 2 km than at 1 km, so a severity raise must widen the fan-out.
func audioEscalationSetup(t *testing.T) (*TriggerSOSUseCase, *fakeAlertRepository, *fakeNotifier) {
	t.Helper()
	uc, locRepo, alertRepo, notifier := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{
		1000: {"sender", "user-b", "user-c", "user-d"},
		2000: {"sender", "user-b", "user-c", "user-d", "user-e"},
		5000: {"sender", "user-b", "user-c", "user-d", "user-e", "user-f"},
	}
	locRepo.lastSeenMs["user-e"] = freshLastSeen()
	locRepo.lastSeenMs["user-f"] = freshLastSeen()
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{text: "ada orang pingsan tidak sadar"}
	uc.Deps.Triage = NewAITriage(noopLogger(), nil, NewOfflineClassifier())
	return uc, alertRepo, notifier
}

func TestUploadMediaEscalationWidensFanout(t *testing.T) {
	uc, alertRepo, notifier := audioEscalationSetup(t)

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if created.RadiusMeters != 1000 {
		t.Fatalf("initial radius = %v, want 1000", created.RadiusMeters)
	}
	if len(created.Recipients) != 3 {
		t.Fatalf("initial recipients = %v", created.Recipients)
	}

	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"panic.webm", "audio/webm", int64(len("voice-bytes")), strings.NewReader("voice-bytes"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if !up.Escalated || up.RecipientsAdded != 2 {
		t.Fatalf("escalation = escalated:%v added:%d, want true/2", up.Escalated, up.RecipientsAdded)
	}
	if got := alertRepo.recipients[created.Alert.ID]; len(got) != 5 {
		t.Fatalf("recorded recipients = %v, want the original 3 plus user-e/user-f", got)
	}
	if !notifier.recipientNotifyCalled || len(notifier.recipientSets) != 1 ||
		len(notifier.recipientSets[0]) != 2 {
		t.Fatalf("escalation notify = %+v, want the two new responders only", notifier.recipientSets)
	}
	for _, id := range notifier.recipientSets[0] {
		if id == "user-b" || id == "user-c" || id == "user-d" {
			t.Fatalf("escalation re-notified an original recipient: %v", notifier.recipientSets[0])
		}
	}
	if notifier.escalatedAlert.Severity <= 0 || notifier.escalatedAlert.EmergencyType != entity.EmergencyTypeMedical {
		t.Fatalf("escalated payload = %+v", notifier.escalatedAlert)
	}
}

// The escalation event must be marked push-only: the worker dispatch path
// would otherwise text the sender's emergency contacts a second time.
func TestEscalationEventIsPushOnly(t *testing.T) {
	uc, _, _ := audioEscalationSetup(t)
	publisher := &fakePublisher{}
	uc.Deps.Publisher = publisher

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if _, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"panic.webm", "audio/webm", int64(len("voice-bytes")), strings.NewReader("voice-bytes")); err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}

	if len(publisher.events) != 2 {
		t.Fatalf("published events = %d, want the trigger plus the escalation", len(publisher.events))
	}
	if publisher.events[0].PushOnly {
		t.Fatal("the first dispatch must stay a normal event (contacts still get the SMS)")
	}
	escalated := publisher.events[1]
	if !escalated.PushOnly {
		t.Fatal("the escalation event must be push-only, or the worker texts the contacts twice")
	}
	if escalated.EventType != "sos.dispatched" {
		t.Fatalf("event type = %q, want the same topic event type", escalated.EventType)
	}
	if len(escalated.Recipients) != 2 {
		t.Fatalf("escalation recipients = %v, want only the added responders", escalated.Recipients)
	}
}

func TestUploadMediaNoEscalationWhenSeverityUnchanged(t *testing.T) {
	uc, alertRepo, notifier := audioEscalationSetup(t)
	// "orang sakit" classifies as MEDICAL 0.6 with no urgent keyword,
	// matching the explicit pre-trigger severity below.
	uc.Deps.Transcriber = &fakeTranscriber{text: "orang sakit"}

	// The 0.6 severity comes from the server-produced classification, not
	// from the request body.
	uc.Deps.Classify = &fakeClassifyStore{info: &model.TriageInfo{EmergencyType: entity.EmergencyTypeMedical, Severity: 0.6}}
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if created.RadiusMeters != 2000 {
		t.Fatalf("initial radius = %v, want 2000", created.RadiusMeters)
	}
	before := len(alertRepo.recipients[created.Alert.ID])

	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"panic.webm", "audio/webm", int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.Escalated || up.RecipientsAdded != 0 {
		t.Fatalf("escalation = escalated:%v added:%d, want none", up.Escalated, up.RecipientsAdded)
	}
	if notifier.recipientNotifyCalled {
		t.Fatal("must not re-notify when the severity did not raise the radius")
	}
	if len(alertRepo.recipients[created.Alert.ID]) != before {
		t.Fatal("recipient set must not change without an escalation")
	}
}

func TestTriggerSOSPendingAudioTriageWidensFirstFanout(t *testing.T) {
	uc, locRepo, _, notifier := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{
		1000: {"sender", "user-b", "user-c", "user-d"},
		5000: {"sender", "user-b", "user-c", "user-d", "user-e", "user-f"},
	}
	locRepo.lastSeenMs["user-e"] = freshLastSeen()
	locRepo.lastSeenMs["user-f"] = freshLastSeen()

	uc.Deps.Classify = &fakeClassifyStore{info: &model.TriageInfo{EmergencyType: entity.EmergencyTypeMedical, Severity: 0.9}}
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if result.RadiusMeters != 5000 {
		t.Fatalf("radius = %v, want 5000 (server triage must drive the fan-out)", result.RadiusMeters)
	}
	if len(result.Recipients) != 5 {
		t.Fatalf("recipients = %v, want the full 5 km set", result.Recipients)
	}
	if result.Triage == nil || result.Triage.Severity != 0.9 {
		t.Fatalf("triage = %+v, want severity 0.9", result.Triage)
	}
	if uc.Deps.Classify.(*fakeClassifyStore).taken == 0 {
		t.Fatal("the pending classification must be consumed, not reused")
	}
	if len(notifier.lastRecipients) != 5 {
		t.Fatalf("notified = %v, want the wider set in the first wave", notifier.lastRecipients)
	}
}

func TestClassifyAudio(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Transcriber = &fakeTranscriber{text: "ada orang pingsan tidak sadar"}
	uc.Deps.Triage = NewAITriage(noopLogger(), nil, NewOfflineClassifier())

	res, err := uc.ClassifyAudio(context.Background(), "sender", "clip.webm", "audio/webm",
		int64(len("voice")), strings.NewReader("voice"))
	if err != nil {
		t.Fatalf("ClassifyAudio error = %v", err)
	}
	if res.TranscriptionStatus != "completed" || res.Transcript != "ada orang pingsan tidak sadar" {
		t.Fatalf("classify = %+v", res)
	}
	if res.EmergencyType != entity.EmergencyTypeMedical || res.Severity <= 0 {
		t.Fatalf("classification = %+v", res)
	}
	store := &fakeClassifyStore{}
	uc.Deps.Classify = store
	if _, err := uc.ClassifyAudio(context.Background(), "sender", "clip.webm", "audio/webm",
		int64(len("voice")), strings.NewReader("voice")); err != nil {
		t.Fatalf("second ClassifyAudio error = %v", err)
	}
	if store.saves != 1 || store.info == nil || store.info.Severity != res.Severity {
		t.Fatalf("pending triage = %+v saves = %d, want the server result stored", store.info, store.saves)
	}
}

func TestClassifyAudioErrorsAndEmpty(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)

	if _, err := uc.ClassifyAudio(context.Background(), "sender", "clip.txt", "text/plain",
		5, strings.NewReader("hello")); !errors.Is(err, fiber.ErrBadRequest) {
		t.Fatalf("non-audio error = %v, want bad request", err)
	}
	if _, err := uc.ClassifyAudio(context.Background(), "sender", "clip.webm", "audio/webm",
		5, strings.NewReader("hello")); !errors.Is(err, model.ErrTranscriptionNotConfigured) {
		t.Fatalf("unconfigured error = %v, want transcription not configured", err)
	}

	uc.Deps.Transcriber = &fakeTranscriber{err: errors.New("stt down")}
	if _, err := uc.ClassifyAudio(context.Background(), "sender", "clip.webm", "audio/webm",
		5, strings.NewReader("hello")); !errors.Is(err, model.ErrTranscriptionFailed) {
		t.Fatalf("provider error = %v, want transcription failed", err)
	}

	uc.Deps.Transcriber = &fakeTranscriber{text: ""}
	res, err := uc.ClassifyAudio(context.Background(), "sender", "clip.webm", "audio/webm",
		5, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("empty transcript error = %v", err)
	}
	if res.TranscriptionStatus != "empty" || res.EmergencyType != "" {
		t.Fatalf("empty transcript result = %+v", res)
	}
}

// A silent clip is a normal outcome, not a provider failure (#54): classify
// answers "empty" (no 502) and stores no pending triage.
func TestClassifyAudioSilentClipReturnsEmpty(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Transcriber = &fakeTranscriber{err: ErrNoSpeechDetected}
	uc.Deps.Triage = NewAITriage(noopLogger(), nil, NewOfflineClassifier())
	store := &fakeClassifyStore{}
	uc.Deps.Classify = store

	res, err := uc.ClassifyAudio(context.Background(), "sender", "silence.webm", "audio/webm",
		int64(len("noise")), strings.NewReader("noise"))
	if err != nil {
		t.Fatalf("ClassifyAudio error = %v, want the silent clip treated as empty", err)
	}
	if res.TranscriptionStatus != "empty" || res.Transcript != "" || res.EmergencyType != "" {
		t.Fatalf("classify = %+v, want an empty result", res)
	}
	if store.saves != 0 {
		t.Fatalf("pending triage saves = %d, want none for a silent clip", store.saves)
	}
}

// The same silent clip uploaded as evidence keeps the media row but never
// produces a transcript or a triage update (#54).
func TestUploadMediaSilentClipKeepsMediaEmpty(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.MediaStore = &fakeMediaStorage{}
	uc.Deps.Transcriber = &fakeTranscriber{err: ErrNoSpeechDetected}

	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	up, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"silence.webm", "audio/webm", int64(len("noise")), strings.NewReader("noise"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.TranscriptionStatus != "empty" || up.Transcript != "" {
		t.Fatalf("upload = %+v, want empty transcript", up)
	}
	if up.Triage != nil || alertRepo.triageUpdated {
		t.Fatal("a silent clip must not update the incident triage")
	}
	if len(alertRepo.media[created.Alert.ID]) != 1 {
		t.Fatal("the audio evidence must still be stored")
	}
}

// The detail response must distinguish "never classified" from a real 0
// score: nil/absent versus an explicit 0 (#55).
func TestAlertDetailBaseKeepsZeroSeverity(t *testing.T) {
	zero := 0.0
	withScore := alertDetailBase(&entity.Alert{ID: "alert-a", Severity: &zero})
	if withScore.Severity == nil || *withScore.Severity != 0 {
		t.Fatalf("severity = %v, want an explicit 0", withScore.Severity)
	}
	unclassified := alertDetailBase(&entity.Alert{ID: "alert-b"})
	if unclassified.Severity != nil {
		t.Fatalf("severity = %v, want nil for an unclassified alert", *unclassified.Severity)
	}
}

// gaugeMetrics records SetActiveSOS so the refresh loop can be asserted.
type gaugeMetrics struct {
	mu     sync.Mutex
	active int64
	calls  int
}

func (g *gaugeMetrics) ObserveDispatchDuration(float64) {}
func (g *gaugeMetrics) IncPushFailures()                {}
func (g *gaugeMetrics) IncFanoutFailures()              {}

func (g *gaugeMetrics) SetActiveSOS(n int64) {
	g.mu.Lock()
	g.active = n
	g.calls++
	g.mu.Unlock()
}

func (g *gaugeMetrics) value() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// callCount reports how many times the gauge was refreshed.
func (g *gaugeMetrics) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func TestRunActiveSOSGaugeRecomputesFromStore(t *testing.T) {
	alerts := &fakeAlertRepository{alerts: map[string]*entity.Alert{
		"a1": {ID: "a1", Status: entity.AlertStatusTriggered},
		"a2": {ID: "a2", Status: entity.AlertStatusAcknowledged},
		"a3": {ID: "a3", Status: entity.AlertStatusResolved},
	}}
	met := &gaugeMetrics{}
	uc := newUseCaseWithDeps(TriggerSOSDeps{Alerts: alerts, Metrics: met})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go uc.RunActiveSOSGauge(ctx, time.Millisecond)

	deadline := time.Now().Add(time.Second)
	for met.value() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := met.value(); got != 2 {
		t.Fatalf("active_sos_count = %d, want 2", got)
	}
}

// A multipart filename is client-controlled: path separators must not
// reach the storage layer and an over-long name must not fail the
// VARCHAR(255) insert (which surfaced as a 500).
func TestUploadMediaSanitizesFilename(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	store := &fakeMediaStorage{}
	uc.Deps.MediaStore = store
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	up, err := uc.UploadMedia(context.Background(), "sender", alertID,
		"../../etc/passwd\x00.wav", "audio/wav", 4, strings.NewReader("data"))
	if err != nil {
		t.Fatalf("UploadMedia error = %v", err)
	}
	if up.Filename != "passwd.wav" {
		t.Fatalf("stored filename = %q, want passwd.wav", up.Filename)
	}

	longName := strings.Repeat("a", 300) + ".m4a"
	up, err = uc.UploadMedia(context.Background(), "sender", alertID,
		longName, "audio/mp4", 4, strings.NewReader("data"))
	if err != nil {
		t.Fatalf("UploadMedia with a long name error = %v", err)
	}
	runes := []rune(up.Filename)
	if len(runes) != maxMediaFilenameRunes {
		t.Fatalf("stored filename has %d runes, want %d", len(runes), maxMediaFilenameRunes)
	}
	if !strings.HasSuffix(up.Filename, ".m4a") {
		t.Fatalf("stored filename = %q, want the extension kept", up.Filename)
	}
	if got := len(alertRepo.media[alertID]); got != 2 {
		t.Fatalf("stored media rows = %d, want 2", got)
	}
}

func TestUploadMediaRejectsUnusableFilename(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	store := &fakeMediaStorage{}
	uc.Deps.MediaStore = store
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}

	for _, name := range []string{"", "   ", "/", "..", "\x00"} {
		_, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
			name, "audio/wav", 4, strings.NewReader("data"))
		if !errors.Is(err, fiber.ErrBadRequest) {
			t.Fatalf("filename %q error = %v, want 400", name, err)
		}
	}
	if len(store.files) != 0 {
		t.Fatalf("nothing may be written for an invalid filename: %v", store.files)
	}
	if len(alertRepo.media[created.Alert.ID]) != 0 {
		t.Fatal("no media row may be created for an invalid filename")
	}
}

// The bytes are written before the row; a failed insert must not leave
// an orphan file on disk.
func TestUploadMediaDeletesFileWhenInsertFails(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	store := &fakeMediaStorage{}
	uc.Deps.MediaStore = store
	alertRepo.createMediaErr = errors.New("insert failed")
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}

	if _, err := uc.UploadMedia(context.Background(), "sender", created.Alert.ID,
		"panic.m4a", "audio/mp4", 5, strings.NewReader("voice")); err == nil {
		t.Fatal("a failed media insert must be reported")
	}
	if len(store.deleted) != 1 {
		t.Fatalf("deleted = %v, want the written file removed", store.deleted)
	}
	if len(store.files) != 0 {
		t.Fatalf("stored files = %v, want none left behind", store.files)
	}
}

// A client timestamp far in the future would sort above every later
// breadcrumb and pin latest_location to it forever.
func TestAddBreadcrumbClampsFutureTimestamp(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID
	lat, lng := -6.2092, 106.8462

	future := time.Now().Add(6 * time.Hour).UnixMilli()
	crumb, err := uc.AddBreadcrumb(context.Background(), "sender", alertID, &model.BreadcrumbRequest{
		Latitude: &lat, Longitude: &lng, Timestamp: &future,
	})
	if err != nil {
		t.Fatalf("future timestamp error = %v", err)
	}
	now := time.Now().UnixMilli()
	if crumb.RecordedAt > now+breadcrumbFutureSkewMs {
		t.Fatalf("RecordedAt = %d, want it clamped to ~now (%d)", crumb.RecordedAt, now)
	}

	// A later heartbeat must become the latest location, not the clamp.
	lat2, lng2 := -6.2100, 106.8470
	later := now
	if _, err := uc.AddBreadcrumb(context.Background(), "sender", alertID, &model.BreadcrumbRequest{
		Latitude: &lat2, Longitude: &lng2, Timestamp: &later,
	}); err != nil {
		t.Fatalf("second breadcrumb error = %v", err)
	}
	latest, err := alertRepo.LatestBreadcrumb(context.Background(), nil, alertID)
	if err != nil {
		t.Fatalf("LatestBreadcrumb error = %v", err)
	}
	if latest == nil || latest.Latitude != lat2 {
		t.Fatalf("latest breadcrumb = %+v, want the newer heartbeat", latest)
	}
}

func TestAddBreadcrumbAcceptsStaleButValidTimestamp(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	lat, lng := -6.2092, 106.8462
	stale := time.Now().Add(-5 * time.Minute).UnixMilli()

	crumb, err := uc.AddBreadcrumb(context.Background(), "sender", created.Alert.ID, &model.BreadcrumbRequest{
		Latitude: &lat, Longitude: &lng, Timestamp: &stale,
	})
	if err != nil {
		t.Fatalf("stale timestamp error = %v", err)
	}
	if crumb.RecordedAt != stale {
		t.Fatalf("RecordedAt = %d, want the client timestamp %d kept", crumb.RecordedAt, stale)
	}
	crumbs := alertRepo.breadcrumbs[created.Alert.ID]
	if len(crumbs) != 1 || crumbs[0].RecordedAt != stale {
		t.Fatalf("stored breadcrumbs = %+v, want the client timestamp", crumbs)
	}

	// Within the tolerated skew a slightly fast client clock is kept.
	skewed := time.Now().Add(30 * time.Second).UnixMilli()
	if _, err := uc.AddBreadcrumb(context.Background(), "sender", created.Alert.ID, &model.BreadcrumbRequest{
		Latitude: &lat, Longitude: &lng, Timestamp: &skewed,
	}); err != nil {
		t.Fatalf("slightly future timestamp error = %v", err)
	}
}

func TestAddBreadcrumbRejectsAbsurdlyOldTimestamp(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	lat, lng := -6.2092, 106.8462
	for _, ts := range []int64{0, -1, time.Now().Add(-48 * time.Hour).UnixMilli()} {
		value := ts
		if _, err := uc.AddBreadcrumb(context.Background(), "sender", created.Alert.ID, &model.BreadcrumbRequest{
			Latitude: &lat, Longitude: &lng, Timestamp: &value,
		}); !errors.Is(err, fiber.ErrBadRequest) {
			t.Fatalf("timestamp %d error = %v, want 400", ts, err)
		}
	}
	if len(alertRepo.breadcrumbs[created.Alert.ID]) != 0 {
		t.Fatal("no breadcrumb may be stored for a rejected timestamp")
	}
}

// A responder answering twice (double tap, retried request) must not store a
// second alert_responses row, must not broadcast a second RESPONDER_ADDED and
// must be reported as a conflict instead of a duplicate or a 500 (#42).
func TestRespondIsIdempotentForTheSameResponder(t *testing.T) {
	uc, _, alertRepo, _ := triggerSetup(t)
	uc.Deps.Users = &fakeUserReader{users: map[string]*entity.User{
		"sender": {ID: "sender", Name: "Jane Doe"},
		"user-b": {ID: "user-b", Name: "Budi"},
	}}
	hub := &fakeHub{}
	uc.Deps.Hub = hub
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	alertID := created.Alert.ID

	lat, lng := -6.21, 106.85
	first, err := uc.Respond(context.Background(), "user-b", alertID, &model.RespondSOSRequest{
		ResponderLatitude: &lat, ResponderLongitude: &lng,
	})
	if err != nil {
		t.Fatalf("first respond error = %v", err)
	}
	_, err = uc.Respond(context.Background(), "user-b", alertID, &model.RespondSOSRequest{
		ResponderLatitude: &lat, ResponderLongitude: &lng,
	})
	if !errors.Is(err, model.ErrDuplicateResponse) {
		t.Fatalf("second respond error = %v, want ErrDuplicateResponse", err)
	}

	if got := len(alertRepo.responses[alertID]); got != 1 {
		t.Fatalf("stored responses = %d, want 1", got)
	}
	if count, err := alertRepo.CountResponders(context.Background(), nil, alertID); err != nil || count != 1 {
		t.Fatalf("CountResponders = %d, %v, want 1", count, err)
	}
	var added int
	for _, payload := range hub.published[alertID] {
		var msg model.ResponderAddedMessage
		if json.Unmarshal(payload, &msg) == nil && msg.Type == "RESPONDER_ADDED" {
			added++
		}
	}
	if added != 1 {
		t.Fatalf("RESPONDER_ADDED frames = %d, want exactly 1", added)
	}
	detail, err := uc.GetAlert(context.Background(), "sender", alertID)
	if err != nil {
		t.Fatalf("detail error = %v", err)
	}
	if detail.RespondersEnRoute != 1 {
		t.Fatalf("responders_en_route = %d, want 1", detail.RespondersEnRoute)
	}
	if first.ResponseID == "" {
		t.Fatal("first response id is empty")
	}
}
