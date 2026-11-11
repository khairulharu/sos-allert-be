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
	"sort"
	"strings"
	"testing"
	"time"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/storage"
	"golang-clean-architecture/internal/usecase"

	"github.com/go-playground/validator/v10"
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

func (s *memAlerts) ListResponses(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertResponse, error) {
	s.init()
	return append([]*entity.AlertResponse(nil), s.responses[alertID]...), nil
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

// historyStatusMatch mirrors the repository's status-group helper.
func historyStatusMatch(status, filter string) bool {
	switch filter {
	case "":
		return true
	case "active":
		return status == entity.AlertStatusTriggered || status == entity.AlertStatusAcknowledged
	case "closed":
		return status == entity.AlertStatusResolved || status == entity.AlertStatusCancelled || status == entity.AlertStatusExpired
	default:
		return status == filter
	}
}

func (s *memAlerts) historyList(req *model.SearchAlertHistoryRequest, match func(*entity.Alert) bool) ([]*entity.Alert, int64, error) {
	s.init()
	var rows []*entity.Alert
	for _, alert := range s.alerts {
		if !match(alert) || !historyStatusMatch(alert.Status, req.Status) {
			continue
		}
		if req.FromMs != nil && alert.TriggeredAt < *req.FromMs {
			continue
		}
		if req.ToMs != nil && alert.TriggeredAt > *req.ToMs {
			continue
		}
		cp := *alert
		rows = append(rows, &cp)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TriggeredAt != rows[j].TriggeredAt {
			return rows[i].TriggeredAt > rows[j].TriggeredAt
		}
		return rows[i].ID > rows[j].ID
	})
	total := int64(len(rows))
	start := (req.Page - 1) * req.Size
	if start < 0 {
		start = 0
	}
	if start > len(rows) {
		start = len(rows)
	}
	end := start + req.Size
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], total, nil
}

func (s *memAlerts) isRecipient(alertID, userID string) bool {
	for _, id := range s.recipients[alertID] {
		if id == userID {
			return true
		}
	}
	return false
}

func (s *memAlerts) ListHistoryBySender(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	return s.historyList(req, func(a *entity.Alert) bool { return a.SenderID == req.UserID })
}

func (s *memAlerts) ListHistoryForRecipient(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	return s.historyList(req, func(a *entity.Alert) bool { return s.isRecipient(a.ID, req.UserID) })
}

func (s *memAlerts) CountBySenderStatus(ctx context.Context, db *gorm.DB, senderID string) (map[string]int64, error) {
	s.init()
	out := map[string]int64{}
	for _, alert := range s.alerts {
		if alert.SenderID == senderID {
			out[alert.Status]++
		}
	}
	return out, nil
}

func (s *memAlerts) CountByRecipientStatus(ctx context.Context, db *gorm.DB, userID string) (map[string]int64, error) {
	s.init()
	out := map[string]int64{}
	for alertID, alert := range s.alerts {
		if s.isRecipient(alertID, userID) {
			out[alert.Status]++
		}
	}
	return out, nil
}

func (s *memAlerts) CountResponded(ctx context.Context, db *gorm.DB, userID string) (int64, int64, error) {
	s.init()
	total := map[string]struct{}{}
	resolved := map[string]struct{}{}
	for alertID, responses := range s.responses {
		for _, r := range responses {
			if r.ResponderID != userID {
				continue
			}
			total[alertID] = struct{}{}
			if alert, ok := s.alerts[alertID]; ok && alert.Status == entity.AlertStatusResolved {
				resolved[alertID] = struct{}{}
			}
		}
	}
	return int64(len(total)), int64(len(resolved)), nil
}

func (s *memAlerts) CountRespondersByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]int, error) {
	s.init()
	out := map[string]int{}
	for _, alertID := range alertIDs {
		seen := map[string]struct{}{}
		for _, r := range s.responses[alertID] {
			seen[r.ResponderID] = struct{}{}
		}
		out[alertID] = len(seen)
	}
	return out, nil
}

func (s *memAlerts) LatestBreadcrumbsByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]*entity.AlertBreadcrumb, error) {
	return map[string]*entity.AlertBreadcrumb{}, nil
}

func (s *memAlerts) ListRespondedAlertIDs(ctx context.Context, db *gorm.DB, userID string, alertIDs []string) (map[string]bool, error) {
	s.init()
	out := map[string]bool{}
	for _, alertID := range alertIDs {
		for _, r := range s.responses[alertID] {
			if r.ResponderID == userID {
				out[alertID] = true
				break
			}
		}
	}
	return out, nil
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

func (l *memLocation) GetLastSeenBatch(ctx context.Context, userIDs []string) (map[string]int64, error) {
	out := make(map[string]int64, len(userIDs))
	for _, id := range userIDs {
		out[id] = time.Now().UnixMilli() - 1000
	}
	return out, nil
}

// memHub captures tracking frames published by the use case.
type memHub struct{ frames map[string][][]byte }

func (h *memHub) Publish(alertID string, payload []byte) {
	if h.frames == nil {
		h.frames = map[string][][]byte{}
	}
	h.frames[alertID] = append(h.frames[alertID], payload)
}

func (h *memHub) Subscribe(alertID string) (<-chan []byte, func()) {
	ch := make(chan []byte)
	return ch, func() { close(ch) }
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
	hub := &memHub{}
	uc := usecase.NewTriggerSOSUseCase(nil, log, usecase.TriggerSOSDeps{
		Location:    &memLocation{lat: -6.2088, lng: 106.8456},
		Alerts:      alerts,
		Notifier:    memNotifier{},
		MediaStore:  store,
		Triage:      usecase.NewAITriage(log, nil, usecase.NewOfflineClassifier()),
		Transcriber: &stubTranscriber{text: "ada orang pingsan tidak sadar"},
		Hub:         hub,
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
	app.Post("/api/sos/:alertId/respond", ctl.Respond)
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

	// A responder answers, then the incident detail lists who is coming.
	respondReq := httptest.NewRequest("POST", "/api/sos/"+alertID+"/respond",
		strings.NewReader(`{"responder_latitude":-6.2100,"responder_longitude":106.8470}`))
	respondReq.Header.Set("Content-Type", "application/json")
	respondReq.Header.Set("X-Test-User", "user-b")
	respondResp, err := app.Test(respondReq, -1)
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	respondBody, _ := io.ReadAll(respondResp.Body)
	respondResp.Body.Close()
	if respondResp.StatusCode != http.StatusOK {
		t.Fatalf("respond status = %d body = %s", respondResp.StatusCode, respondBody)
	}

	resp, body = do("GET", "/api/sos/"+alertID, "user-b", "")
	var forResponder model.WebResponse[*model.AlertDetailResponse]
	if err := json.Unmarshal(body, &forResponder); err != nil {
		t.Fatalf("responder detail decode: %v (%s)", err, body)
	}
	if len(forResponder.Data.Responders) != 1 {
		t.Fatalf("responder detail list = %+v", forResponder.Data.Responders)
	}
	if r := forResponder.Data.Responders[0]; r.UserID != "user-b" || r.DistanceMeters <= 0 || r.ETASeconds <= 0 {
		t.Fatalf("responder entry = %+v", r)
	}

	resp, body = do("GET", "/api/sos/"+alertID, "user-x", "")
	var forStranger model.WebResponse[*model.AlertDetailResponse]
	if err := json.Unmarshal(body, &forStranger); err != nil {
		t.Fatalf("stranger detail decode: %v (%s)", err, body)
	}
	if len(forStranger.Data.Responders) != 0 {
		t.Fatalf("stranger must not see responder identities: %+v", forStranger.Data.Responders)
	}

	var sawResponderAdded bool
	for _, payload := range hub.frames[alertID] {
		var msg model.ResponderAddedMessage
		if json.Unmarshal(payload, &msg) == nil && msg.Type == "RESPONDER_ADDED" && msg.ResponderID == "user-b" {
			sawResponderAdded = true
		}
	}
	if !sawResponderAdded {
		t.Fatal("RESPONDER_ADDED frame not published")
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

// TestSOSHistoryAndSummaryAPI covers the paged history, the summary cards,
// the error mapping, and route ordering against /api/sos/:alertId.
func TestSOSHistoryAndSummaryAPI(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)

	alerts := &memAlerts{}
	alerts.init()
	resolvedAt := int64(2000)
	alerts.alerts["a-resolved"] = &entity.Alert{
		ID: "a-resolved", SenderID: "sender", Status: entity.AlertStatusResolved,
		EmergencyType: entity.EmergencyTypeMedical, Latitude: -6.2091, Longitude: 106.8461,
		TriggeredAt: 2000, ResolvedAt: &resolvedAt,
	}
	alerts.alerts["a-active"] = &entity.Alert{
		ID: "a-active", SenderID: "sender", Status: entity.AlertStatusTriggered,
		EmergencyType: entity.EmergencyTypeDanger, Latitude: -6.2091, Longitude: 106.8461,
		TriggeredAt: 3000,
	}
	alerts.recipients["a-active"] = []string{"user-b"}
	alerts.responses["a-active"] = []*entity.AlertResponse{
		{AlertID: "a-active", ResponderID: "user-b", RespondedAt: 3100},
	}

	uc := usecase.NewTriggerSOSUseCase(nil, log, usecase.TriggerSOSDeps{
		Alerts:   alerts,
		Validate: validator.New(),
	})

	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error {
		user := c.Get("X-Test-User")
		if user == "" {
			return fiber.ErrUnauthorized
		}
		c.Locals("auth", &model.Auth{ID: user})
		return c.Next()
	})
	ctl := deliveryhttp.NewSOSController(uc, log)
	app.Get("/api/sos/history", ctl.ListHistory)
	app.Get("/api/sos/summary", ctl.Summary)
	app.Get("/api/sos/:alertId", ctl.Get)

	do := func(path, user string) (int, []byte) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		if user != "" {
			req.Header.Set("X-Test-User", user)
		}
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, body
	}

	// History: 200 + paging, and it must not be swallowed by :alertId
	// (which would answer 404 "alert not found").
	status, body := do("/api/sos/history?role=sent", "sender")
	if status != http.StatusOK {
		t.Fatalf("history status = %d body = %s", status, body)
	}
	var history struct {
		Data   []*model.AlertDetailResponse `json:"data"`
		Paging *model.PageMetadata          `json:"paging"`
	}
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("history decode: %v (%s)", err, body)
	}
	if history.Paging == nil || history.Paging.Page != 1 || history.Paging.Size != 20 ||
		history.Paging.TotalItem != 2 || history.Paging.TotalPage != 1 {
		t.Fatalf("paging = %+v", history.Paging)
	}
	if len(history.Data) != 2 || history.Data[0].AlertID != "a-active" || history.Data[1].AlertID != "a-resolved" {
		t.Fatalf("history data = %+v", history.Data)
	}
	if history.Data[1].ResolvedAt == nil {
		t.Fatal("resolved_at missing from sent history")
	}

	// Received history only lists what the user was notified for, and a
	// responder sees exact coordinates.
	status, body = do("/api/sos/history?role=received", "user-b")
	if status != http.StatusOK {
		t.Fatalf("received status = %d body = %s", status, body)
	}
	if err := json.Unmarshal(body, &history); err != nil {
		t.Fatalf("received decode: %v (%s)", err, body)
	}
	if history.Paging == nil || history.Paging.TotalItem != 1 || len(history.Data) != 1 ||
		history.Data[0].AlertID != "a-active" || history.Data[0].Masked {
		t.Fatalf("received history = %+v", history)
	}

	// Invalid params -> 400.
	for _, path := range []string{
		"/api/sos/history?role=foo",
		"/api/sos/history?role=sent&status=foo",
		"/api/sos/history?role=sent&size=500",
		"/api/sos/history?role=sent&page=0",
		"/api/sos/history?role=sent&from_ms=abc",
		"/api/sos/history?role=sent&from_ms=2000&to_ms=1000",
	} {
		if code, respBody := do(path, "sender"); code != http.StatusBadRequest {
			t.Fatalf("%s status = %d body = %s, want 400", path, code, respBody)
		}
	}

	// Missing token -> 401.
	if code, _ := do("/api/sos/history", ""); code != http.StatusUnauthorized {
		t.Fatalf("no-token status = %d, want 401", code)
	}

	// Summary counts every status, not just the active ones.
	status, body = do("/api/sos/summary", "sender")
	if status != http.StatusOK {
		t.Fatalf("summary status = %d body = %s", status, body)
	}
	var summary model.WebResponse[*model.AlertSummaryResponse]
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("summary decode: %v (%s)", err, body)
	}
	if summary.Data.Sent != (model.AlertStatusCounts{Total: 2, Active: 1, Resolved: 1}) {
		t.Fatalf("sent summary = %+v", summary.Data.Sent)
	}

	status, body = do("/api/sos/summary", "user-b")
	if status != http.StatusOK {
		t.Fatalf("summary status = %d body = %s", status, body)
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("summary decode: %v (%s)", err, body)
	}
	if summary.Data.Received != (model.AlertStatusCounts{Total: 1, Active: 1}) {
		t.Fatalf("received summary = %+v", summary.Data.Received)
	}
	if summary.Data.Responded != (model.AlertRespondedCounts{Total: 1}) {
		t.Fatalf("responded summary = %+v", summary.Data.Responded)
	}
}
