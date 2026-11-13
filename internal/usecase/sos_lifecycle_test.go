package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if _, _, err := uc.OpenAlertMedia(context.Background(), "user-outside", alertID, up.ID); !errors.Is(err, model.ErrNotAlertParticipant) {
		t.Fatalf("outsider open error = %v, want ErrNotAlertParticipant", err)
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

type fakeMediaStorage struct {
	files map[string][]byte
}

func (f *fakeMediaStorage) Save(ctx context.Context, filename string, r io.Reader) (string, error) {
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

func TestUploadMediaNoEscalationWhenSeverityUnchanged(t *testing.T) {
	uc, alertRepo, notifier := audioEscalationSetup(t)
	// "orang sakit" classifies as MEDICAL 0.6 with no urgent keyword,
	// matching the explicit pre-trigger severity below.
	uc.Deps.Transcriber = &fakeTranscriber{text: "orang sakit"}

	severity := 0.6
	created, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{Severity: &severity}, "")
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

func TestTriggerSOSExplicitSeverityWidensFirstFanout(t *testing.T) {
	uc, locRepo, _, notifier := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{
		1000: {"sender", "user-b", "user-c", "user-d"},
		5000: {"sender", "user-b", "user-c", "user-d", "user-e", "user-f"},
	}
	locRepo.lastSeenMs["user-e"] = freshLastSeen()
	locRepo.lastSeenMs["user-f"] = freshLastSeen()

	severity := 0.9
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{Severity: &severity}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if result.RadiusMeters != 5000 {
		t.Fatalf("radius = %v, want 5000 (explicit severity must drive the fan-out)", result.RadiusMeters)
	}
	if len(result.Recipients) != 5 {
		t.Fatalf("recipients = %v, want the full 5 km set", result.Recipients)
	}
	if result.Triage == nil || result.Triage.Severity != severity {
		t.Fatalf("triage = %+v, want severity %v", result.Triage, severity)
	}
	if len(notifier.lastRecipients) != 5 {
		t.Fatalf("notified = %v, want the wider set in the first wave", notifier.lastRecipients)
	}
}

func TestClassifyAudio(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	uc.Deps.Transcriber = &fakeTranscriber{text: "ada orang pingsan tidak sadar"}
	uc.Deps.Triage = NewAITriage(noopLogger(), nil, NewOfflineClassifier())

	res, err := uc.ClassifyAudio(context.Background(), "clip.webm", "audio/webm",
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
}

func TestClassifyAudioErrorsAndEmpty(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)

	if _, err := uc.ClassifyAudio(context.Background(), "clip.txt", "text/plain",
		5, strings.NewReader("hello")); !errors.Is(err, fiber.ErrBadRequest) {
		t.Fatalf("non-audio error = %v, want bad request", err)
	}
	if _, err := uc.ClassifyAudio(context.Background(), "clip.webm", "audio/webm",
		5, strings.NewReader("hello")); !errors.Is(err, model.ErrTranscriptionNotConfigured) {
		t.Fatalf("unconfigured error = %v, want transcription not configured", err)
	}

	uc.Deps.Transcriber = &fakeTranscriber{err: errors.New("stt down")}
	if _, err := uc.ClassifyAudio(context.Background(), "clip.webm", "audio/webm",
		5, strings.NewReader("hello")); !errors.Is(err, model.ErrTranscriptionFailed) {
		t.Fatalf("provider error = %v, want transcription failed", err)
	}

	uc.Deps.Transcriber = &fakeTranscriber{text: ""}
	res, err := uc.ClassifyAudio(context.Background(), "clip.webm", "audio/webm",
		5, strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("empty transcript error = %v", err)
	}
	if res.TranscriptionStatus != "empty" || res.EmergencyType != "" {
		t.Fatalf("empty transcript result = %+v", res)
	}
}

// gaugeMetrics records SetActiveSOS so the refresh loop can be asserted.
type gaugeMetrics struct {
	mu     sync.Mutex
	active int64
}

func (g *gaugeMetrics) ObserveDispatchDuration(float64) {}
func (g *gaugeMetrics) IncPushFailures()                {}

func (g *gaugeMetrics) SetActiveSOS(n int64) {
	g.mu.Lock()
	g.active = n
	g.mu.Unlock()
}

func (g *gaugeMetrics) value() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
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
