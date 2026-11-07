package http_test

// Integration tests for the SOS HTTP surface with in-memory stores, so
// routing, request decoding, status codes and media streaming are
// covered without MySQL/Redis/Kafka.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/storage"
	"golang-clean-architecture/internal/usecase"

	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type memAlerts struct {
	alerts     map[string]*entity.Alert
	media      map[string][]*entity.AlertMedia
	recipients map[string][]string
	responses  map[string][]*entity.AlertResponse
}

func (s *memAlerts) init() {
	if s.alerts == nil {
		s.alerts = map[string]*entity.Alert{}
		s.media = map[string][]*entity.AlertMedia{}
		s.recipients = map[string][]string{}
		s.responses = map[string][]*entity.AlertResponse{}
	}
}

func (s *memAlerts) Create(ctx context.Context, db *gorm.DB, a *entity.Alert) error {
	s.init()
	cp := *a
	s.alerts[a.ID] = &cp
	return nil
}

func (s *memAlerts) GetByID(ctx context.Context, db *gorm.DB, id string) (*entity.Alert, error) {
	s.init()
	a, ok := s.alerts[id]
	if !ok {
		return nil, model.ErrAlertNotFound
	}
	cp := *a
	return &cp, nil
}

func (s *memAlerts) ListActiveBySender(ctx context.Context, db *gorm.DB, sender string, limit int) ([]*entity.Alert, error) {
	s.init()
	var out []*entity.Alert
	for _, a := range s.alerts {
		if a.SenderID == sender && a.IsActive() {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *memAlerts) Transition(ctx context.Context, db *gorm.DB, id string, from []string, updates map[string]any) (bool, error) {
	s.init()
	a, ok := s.alerts[id]
	if !ok {
		return false, nil
	}
	for _, f := range from {
		if a.Status == f {
			if v, ok := updates["status"]; ok {
				a.Status = v.(string)
			}
			return true, nil
		}
	}
	return false, nil
}

func (s *memAlerts) ExpireOlderThan(ctx context.Context, db *gorm.DB, beforeMs, expiredAtMs int64) (int64, error) {
	return 0, nil
}

func (s *memAlerts) CreateBreadcrumb(ctx context.Context, db *gorm.DB, b *entity.AlertBreadcrumb) error {
	return nil
}

func (s *memAlerts) ListBreadcrumbs(ctx context.Context, db *gorm.DB, alertID string, limit int) ([]*entity.AlertBreadcrumb, error) {
	return nil, nil
}

func (s *memAlerts) LatestBreadcrumb(ctx context.Context, db *gorm.DB, alertID string) (*entity.AlertBreadcrumb, error) {
	return nil, nil
}

func (s *memAlerts) CreateResponse(ctx context.Context, db *gorm.DB, r *entity.AlertResponse) error {
	s.init()
	s.responses[r.AlertID] = append(s.responses[r.AlertID], r)
	return nil
}

func (s *memAlerts) CountResponders(ctx context.Context, db *gorm.DB, alertID string) (int, error) {
	return len(s.responses[alertID]), nil
}

func (s *memAlerts) IsResponder(ctx context.Context, db *gorm.DB, alertID, userID string) (bool, error) {
	for _, r := range s.responses[alertID] {
		if r.ResponderID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (s *memAlerts) CreateMedia(ctx context.Context, db *gorm.DB, m *entity.AlertMedia) error {
	s.init()
	s.media[m.AlertID] = append(s.media[m.AlertID], m)
	return nil
}

func (s *memAlerts) UpdateTriage(ctx context.Context, db *gorm.DB, alertID, emergencyType string, severity float64, summary string) error {
	s.init()
	if a, ok := s.alerts[alertID]; ok {
		if emergencyType != "" {
			a.EmergencyType = emergencyType
		}
		if severity > 0 {
			v := severity
			a.Severity = &v
		}
		if summary != "" {
			v := summary
			a.Summary = &v
		}
	}
	return nil
}

func (s *memAlerts) ListMedia(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertMedia, error) {
	s.init()
	return s.media[alertID], nil
}

func (s *memAlerts) GetMedia(ctx context.Context, db *gorm.DB, alertID, mediaID string) (*entity.AlertMedia, error) {
	s.init()
	for _, m := range s.media[alertID] {
		if m.ID == mediaID {
			return m, nil
		}
	}
	return nil, model.ErrMediaNotFound
}

func (s *memAlerts) CreateRecipients(ctx context.Context, db *gorm.DB, alertID string, users []string, at int64) error {
	s.init()
	s.recipients[alertID] = append(s.recipients[alertID], users...)
	return nil
}

func (s *memAlerts) ListRecipients(ctx context.Context, db *gorm.DB, alertID string) ([]string, error) {
	s.init()
	return append([]string(nil), s.recipients[alertID]...), nil
}

func (s *memAlerts) ListInbox(ctx context.Context, db *gorm.DB, userID string, limit int) ([]*entity.Alert, error) {
	s.init()
	var out []*entity.Alert
	for alertID, users := range s.recipients {
		for _, u := range users {
			if u == userID {
				if a, ok := s.alerts[alertID]; ok && a.IsActive() {
					cp := *a
					out = append(out, &cp)
				}
				break
			}
		}
	}
	return out, nil
}

func (s *memAlerts) PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error) {
	return 0, nil
}

type memLocation struct{ lat, lng float64 }

func (l *memLocation) Get(ctx context.Context, userID string) (*entity.LocationCache, error) {
	return &entity.LocationCache{UserId: userID, Latitude: l.lat, Longitude: l.lng}, nil
}

func (l *memLocation) Save(ctx context.Context, loc *entity.LocationCache) error { return nil }

func (l *memLocation) GeoSearch(ctx context.Context, lng, lat, radius float64) ([]string, error) {
	return []string{"user-b"}, nil
}

func (l *memLocation) GeoSearchWithDistance(ctx context.Context, lng, lat, radius float64, count int) ([]entity.GeoLocation, error) {
	return nil, nil
}

func (l *memLocation) GetLastSeen(ctx context.Context, userID string) (int64, error) {
	return time.Now().UnixMilli() - 1000, nil
}

type memNotifier struct{}

func (memNotifier) Notify(ctx context.Context, ids []string, alert model.Alert) error { return nil }

// stubTranscriber satisfies usecase.Transcriber for the classify endpoint.
type stubTranscriber struct {
	text string
	err  error
}

func (s *stubTranscriber) Transcribe(ctx context.Context, filename, contentType string, r io.Reader) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	_, _ = io.Copy(io.Discard, r)
	return s.text, nil
}

func TestSOSAPIEndToEnd(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	alerts := &memAlerts{}
	store := storage.NewLocalStorage(t.TempDir())
	uc := usecase.NewTriggerSOSUseCase(nil, log, usecase.TriggerSOSDeps{
		Location:    &memLocation{lat: -6.2088, lng: 106.8456},
		Alerts:      alerts,
		Notifier:    memNotifier{},
		MediaStore:  store,
		Triage:      usecase.NewAITriage(log, nil, usecase.NewOfflineClassifier()),
		Transcriber: &stubTranscriber{text: "ada orang pingsan tidak sadar"},
		Radii:       []float64{1000, 2000, 5000},
	})

	app := fiber.New()
	// The real middleware loads the user from MySQL; stub it for the test.
	app.Use(func(c *fiber.Ctx) error {
		c.Locals("auth", &model.Auth{ID: strings.Clone(c.Get("X-Test-User"))})
		return c.Next()
	})
	ctl := deliveryhttp.NewSOSController(uc, log)
	app.Post("/api/sos", ctl.Trigger)
	app.Post("/api/sos/classify", ctl.Classify)
	app.Get("/api/sos/inbox", ctl.ListInbox)
	app.Get("/api/sos/active", ctl.ListActive)
	app.Get("/api/sos/:alertId", ctl.Get)
	app.Get("/api/sos/:alertId/media", ctl.ListMedia)
	app.Get("/api/sos/:alertId/media/:mediaId", ctl.DownloadMedia)
	app.Post("/api/sos/:alertId/media", ctl.UploadMedia)

	do := func(method, path, user, body string) (*http.Response, []byte) {
		t.Helper()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("X-Test-User", user)
		resp, err := app.Test(r, -1)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, b
	}

	resp, body := do("POST", "/api/sos", "sender",
		`{"latitude":-6.2088,"longitude":106.8456,"description":"ada orang pingsan di pantai, tidak sadar"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("trigger status = %d body = %s", resp.StatusCode, body)
	}
	var created model.WebResponse[*model.SOSCreatedResponse]
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("trigger decode: %v (%s)", err, body)
	}
	if created.Data.Triage == nil || created.Data.Triage.EmergencyType != "MEDICAL" || created.Data.Triage.Summary == "" {
		t.Fatalf("trigger triage = %+v", created.Data.Triage)
	}
	alertID := created.Data.AlertID

	resp, body = do("GET", "/api/sos/"+alertID, "user-b", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d body = %s", resp.StatusCode, body)
	}
	var detail model.WebResponse[*model.AlertDetailResponse]
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("detail decode: %v", err)
	}
	if detail.Data.Severity <= 0 || detail.Data.Summary == "" || !detail.Data.Masked {
		t.Fatalf("detail = %+v, want persisted triage + masking for a non-participant", detail.Data)
	}

	resp, body = do("GET", "/api/sos/inbox", "user-b", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), alertID) {
		t.Fatalf("inbox user-b status = %d body = %s", resp.StatusCode, body)
	}
	resp, body = do("GET", "/api/sos/inbox", "user-x", "")
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), alertID) {
		t.Fatalf("inbox user-x status = %d body = %s", resp.StatusCode, body)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="panic.m4a"`)
	partHeader.Set("Content-Type", "audio/mp4")
	part, _ := mw.CreatePart(partHeader)
	_, _ = part.Write([]byte("voice-bytes"))
	_ = mw.Close()

	uploadReq := httptest.NewRequest("POST", "/api/sos/"+alertID+"/media", &buf)
	uploadReq.Header.Set("Content-Type", mw.FormDataContentType())
	uploadReq.Header.Set("X-Test-User", "sender")
	uploadResp, err := app.Test(uploadReq, -1)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	uploadBody, _ := io.ReadAll(uploadResp.Body)
	uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d body = %s", uploadResp.StatusCode, uploadBody)
	}
	var uploaded model.WebResponse[*model.AlertMediaResponse]
	if err := json.Unmarshal(uploadBody, &uploaded); err != nil {
		t.Fatalf("upload decode: %v", err)
	}
	if uploaded.Data.URL == "" {
		t.Fatalf("upload url empty: %s", uploadBody)
	}

	resp, body = do("GET", "/api/sos/"+alertID+"/media", "sender", "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), uploaded.Data.ID) {
		t.Fatalf("media list status = %d body = %s", resp.StatusCode, body)
	}
	resp, body = do("GET", uploaded.Data.URL, "sender", "")
	if resp.StatusCode != http.StatusOK || string(body) != "voice-bytes" ||
		!strings.Contains(resp.Header.Get("Content-Type"), "audio/mp4") {
		t.Fatalf("download status = %d type = %q body = %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	resp, _ = do("GET", uploaded.Data.URL, "user-x", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider download status = %d, want 403", resp.StatusCode)
	}
	resp, _ = do("GET", "/api/sos/does-not-exist", "sender", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown alert status = %d, want 404", resp.StatusCode)
	}

	// POST /api/sos/classify: audio-only, nothing persisted.
	var cbuf bytes.Buffer
	cmw := multipart.NewWriter(&cbuf)
	ch := make(textproto.MIMEHeader)
	ch.Set("Content-Disposition", `form-data; name="file"; filename="clip.m4a"`)
	ch.Set("Content-Type", "audio/mp4")
	cp, _ := cmw.CreatePart(ch)
	_, _ = cp.Write([]byte("voice-bytes"))
	_ = cmw.Close()
	classifyReq := httptest.NewRequest("POST", "/api/sos/classify", &cbuf)
	classifyReq.Header.Set("Content-Type", cmw.FormDataContentType())
	classifyReq.Header.Set("X-Test-User", "sender")
	classifyResp, err := app.Test(classifyReq, -1)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	classifyBody, _ := io.ReadAll(classifyResp.Body)
	classifyResp.Body.Close()
	if classifyResp.StatusCode != http.StatusOK {
		t.Fatalf("classify status = %d body = %s", classifyResp.StatusCode, classifyBody)
	}
	var classified model.WebResponse[*model.ClassifyResponse]
	if err := json.Unmarshal(classifyBody, &classified); err != nil {
		t.Fatalf("classify decode: %v (%s)", err, classifyBody)
	}
	if classified.Data.EmergencyType != "MEDICAL" || classified.Data.Severity <= 0 ||
		classified.Data.TranscriptionStatus != "completed" || classified.Data.Transcript == "" {
		t.Fatalf("classify data = %+v", classified.Data)
	}
}
