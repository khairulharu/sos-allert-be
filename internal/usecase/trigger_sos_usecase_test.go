package usecase

import (
	"context"
	"errors"
	"fmt"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"io"
	"sort"
	"sync"
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

	// nearbyDetailed is returned by GeoSearchWithDistance.
	nearbyDetailed []entity.GeoLocation

	// nearbyByRadius, when set, overrides nearby per requested radius.
	nearbyByRadius map[float64][]string

	// lastSeenMs keyed by userId; zero value means "not found" → returns error
	lastSeenMs map[string]int64

	// lastSeenBatchErr makes GetLastSeenBatch fail (freshness unknown).
	lastSeenBatchErr error

	gotSenderID    string
	gotLongitude   float64
	gotLatitude    float64
	gotRadius      float64
	gotCounts      []int
	radii          []float64
	geoSearchCalls int
	saved          []*entity.LocationCache

	// GetLastSeen call counters: batching means single calls must stay at
	// zero for the nearby list and the SOS fan-out.
	lastSeenSingleCalls int
	lastSeenBatchCalls  int
	// lastSeenBatchMax is the largest candidate set passed to
	// GetLastSeenBatch, so the candidate cap can be asserted.
	lastSeenBatchMax int
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

func (f *fakeLocationRepository) GeoSearch(ctx context.Context, longitude float64, latitude float64, radiusMeters float64, count int) ([]string, error) {
	f.geoSearchCalls++
	f.gotLongitude = longitude
	f.gotLatitude = latitude
	f.gotRadius = radiusMeters
	f.gotCounts = append(f.gotCounts, count)
	f.radii = append(f.radii, radiusMeters)
	if f.nearbyErr != nil {
		return nil, f.nearbyErr
	}
	var list []string
	if byRadius, ok := f.nearbyByRadius[radiusMeters]; ok {
		list = byRadius
	} else {
		list = f.nearby
	}
	// Emulate Redis GEOSEARCH COUNT: nearest first, so truncating keeps
	// the closest candidates.
	if count > 0 && len(list) > count {
		list = list[:count]
	}
	return list, nil
}

func (f *fakeLocationRepository) GeoSearchWithDistance(ctx context.Context, longitude float64, latitude float64, radiusMeters float64, count int) ([]entity.GeoLocation, error) {
	f.geoSearchCalls++
	f.gotLongitude = longitude
	f.gotLatitude = latitude
	f.gotRadius = radiusMeters
	f.radii = append(f.radii, radiusMeters)
	if f.nearbyErr != nil {
		return nil, f.nearbyErr
	}
	return f.nearbyDetailed, nil
}

func (f *fakeLocationRepository) GetLastSeen(ctx context.Context, userId string) (int64, error) {
	f.lastSeenSingleCalls++
	if f.lastSeenMs == nil {
		return 0, errors.New("last_seen not found")
	}
	ts, ok := f.lastSeenMs[userId]
	if !ok {
		return 0, errors.New("last_seen not found")
	}
	return ts, nil
}

func (f *fakeLocationRepository) GetLastSeenBatch(ctx context.Context, userIds []string) (map[string]int64, error) {
	f.lastSeenBatchCalls++
	if f.lastSeenBatchErr != nil {
		return nil, f.lastSeenBatchErr
	}
	if len(userIds) > f.lastSeenBatchMax {
		f.lastSeenBatchMax = len(userIds)
	}
	out := make(map[string]int64, len(userIds))
	for _, id := range userIds {
		if ts, ok := f.lastSeenMs[id]; ok {
			out[id] = ts
		}
	}
	return out, nil
}

// fakeAlertRepository satisfies AlertStore with in-memory state.
type fakeAlertRepository struct {
	createErr    error
	createCalled bool
	lastAlert    *entity.Alert
	alerts       map[string]*entity.Alert
	breadcrumbs  map[string][]*entity.AlertBreadcrumb
	responses    map[string][]*entity.AlertResponse
	media        map[string][]*entity.AlertMedia
	recipients   map[string][]string

	// users lets the fake roster query resolve display names the way the
	// real LEFT JOIN users does.
	users *fakeUserReader

	// responderSummariesErr fails the batched roster read.
	responderSummariesErr error

	triageUpdated     bool
	triageType        string
	triageSeverity    float64
	triageSeveritySet bool
	triageSummary     string
	triageErr         error

	createMediaErr error

	// batch call counters guard against per-row queries in history and the
	// lifecycle list endpoints.
	countRespondersBatchCalls    int
	latestBreadcrumbsBatchCalls  int
	responderSummariesBatchCalls int
	listRespondedBatchCalls      int

	// single call counters prove the list paths never fall back to the
	// per-row detail reads.
	countRespondersCalls  int
	latestBreadcrumbCalls int
	isResponderCalls      int
	listResponsesCalls    int
	listRecipientsCalls   int

	// Durable outbox (#40) knobs: dueListErr fails the due scan,
	// recipientsErr fails the fan-out recipient lookup, afterListDue runs
	// once a scan list was built (simulates a cancel racing the scan).
	dueListErr     error
	recipientsErr  error
	markDispatchFn func(alertID string, atMs int64) (bool, error)
	afterListDue   func()

	// Housekeeping knobs (#42): expiry/purge call counters and results.
	expireCalls    int
	expireBeforeMs int64
	expired        int64
	expireErr      error
	purgeCalls     int
	purgeBeforeMs  int64
	purged         int64
	purgeErr       error
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
	if f.media == nil {
		f.media = make(map[string][]*entity.AlertMedia)
	}
	if f.recipients == nil {
		f.recipients = make(map[string][]string)
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
	f.ensure()
	f.expireCalls++
	f.expireBeforeMs = beforeMs
	if f.expireErr != nil {
		return 0, f.expireErr
	}
	return f.expired, nil
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
	f.latestBreadcrumbCalls++
	list := f.breadcrumbs[alertID]
	if len(list) == 0 {
		return nil, nil
	}
	return list[len(list)-1], nil
}

func (f *fakeAlertRepository) CreateResponse(ctx context.Context, db *gorm.DB, r *entity.AlertResponse) error {
	f.ensure()
	// Mirror the unique (alert_id, responder_id) index: a repeat answer is
	// a no-op reported as ErrDuplicateResponse.
	for _, existing := range f.responses[r.AlertID] {
		if existing.ResponderID == r.ResponderID {
			return model.ErrDuplicateResponse
		}
	}
	f.responses[r.AlertID] = append(f.responses[r.AlertID], r)
	return nil
}

func (f *fakeAlertRepository) ListResponses(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertResponse, error) {
	f.ensure()
	f.listResponsesCalls++
	return append([]*entity.AlertResponse(nil), f.responses[alertID]...), nil
}

// ListResponderSummariesByAlertIDs mirrors the joined roster query: the
// responses of each alert ordered by responded_at, with display names.
func (f *fakeAlertRepository) ListResponderSummariesByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string][]model.ResponderSummary, error) {
	f.ensure()
	f.responderSummariesBatchCalls++
	if f.responderSummariesErr != nil {
		return nil, f.responderSummariesErr
	}
	out := make(map[string][]model.ResponderSummary, len(alertIDs))
	for _, alertID := range alertIDs {
		responses := append([]*entity.AlertResponse(nil), f.responses[alertID]...)
		sort.SliceStable(responses, func(i, j int) bool {
			return responses[i].RespondedAt < responses[j].RespondedAt
		})
		for _, r := range responses {
			name := ""
			if f.users != nil {
				if user, ok := f.users.users[r.ResponderID]; ok {
					name = user.Name
				}
			}
			out[alertID] = append(out[alertID], model.ResponderSummary{
				UserID:         r.ResponderID,
				Name:           name,
				DistanceMeters: int(r.DistanceMeters + 0.5),
				ETASeconds:     r.ETASeconds,
				RespondedAt:    r.RespondedAt,
			})
		}
	}
	return out, nil
}

func (f *fakeAlertRepository) CountResponders(ctx context.Context, db *gorm.DB, alertID string) (int, error) {
	f.ensure()
	f.countRespondersCalls++
	seen := map[string]struct{}{}
	for _, r := range f.responses[alertID] {
		seen[r.ResponderID] = struct{}{}
	}
	return len(seen), nil
}

func (f *fakeAlertRepository) IsResponder(ctx context.Context, db *gorm.DB, alertID, userID string) (bool, error) {
	f.ensure()
	f.isResponderCalls++
	for _, r := range f.responses[alertID] {
		if r.ResponderID == userID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeAlertRepository) CreateMedia(ctx context.Context, db *gorm.DB, m *entity.AlertMedia) error {
	if f.createMediaErr != nil {
		return f.createMediaErr
	}
	f.ensure()
	f.media[m.AlertID] = append(f.media[m.AlertID], m)
	return nil
}

func (f *fakeAlertRepository) UpdateTriage(ctx context.Context, db *gorm.DB, alertID, emergencyType string, severity *float64, summary string) error {
	if f.triageErr != nil {
		return f.triageErr
	}
	f.triageUpdated = true
	f.triageType = emergencyType
	f.triageSeveritySet = severity != nil
	if severity != nil {
		f.triageSeverity = *severity
	}
	f.triageSummary = summary
	if alert, ok := f.alerts[alertID]; ok {
		if emergencyType != "" {
			alert.EmergencyType = emergencyType
		}
		if severity != nil {
			v := *severity
			alert.Severity = &v
		}
		if summary != "" {
			v := summary
			alert.Summary = &v
		}
	}
	return nil
}

func (f *fakeAlertRepository) ListMedia(ctx context.Context, db *gorm.DB, alertID string) ([]*entity.AlertMedia, error) {
	f.ensure()
	return f.media[alertID], nil
}

func (f *fakeAlertRepository) GetMedia(ctx context.Context, db *gorm.DB, alertID, mediaID string) (*entity.AlertMedia, error) {
	f.ensure()
	for _, m := range f.media[alertID] {
		if m.ID == mediaID {
			return m, nil
		}
	}
	return nil, model.ErrMediaNotFound
}

func (f *fakeAlertRepository) CreateRecipients(ctx context.Context, db *gorm.DB, alertID string, userIDs []string, notifiedAt int64) error {
	f.ensure()
	f.recipients[alertID] = append(f.recipients[alertID], userIDs...)
	return nil
}

func (f *fakeAlertRepository) ListRecipients(ctx context.Context, db *gorm.DB, alertID string) ([]string, error) {
	f.ensure()
	f.listRecipientsCalls++
	if f.recipientsErr != nil {
		return nil, f.recipientsErr
	}
	return append([]string(nil), f.recipients[alertID]...), nil
}

// ListDueDispatch mirrors the SQL due scan: TRIGGERED, due, not dispatched.
func (f *fakeAlertRepository) ListDueDispatch(ctx context.Context, db *gorm.DB, dueBeforeMs int64, limit int) ([]*entity.Alert, error) {
	f.ensure()
	if f.dueListErr != nil {
		return nil, f.dueListErr
	}
	var out []*entity.Alert
	for _, a := range f.alerts {
		if a.Status != entity.AlertStatusTriggered || a.DispatchDueAt == nil || a.DispatchedAt != nil {
			continue
		}
		if *a.DispatchDueAt > dueBeforeMs {
			continue
		}
		cp := *a
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return *out[i].DispatchDueAt < *out[j].DispatchDueAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if f.afterListDue != nil {
		f.afterListDue()
	}
	return out, nil
}

// MarkDispatched mirrors the conditional update: only the first call wins.
func (f *fakeAlertRepository) MarkDispatched(ctx context.Context, db *gorm.DB, alertID string, atMs int64) (bool, error) {
	f.ensure()
	if f.markDispatchFn != nil {
		return f.markDispatchFn(alertID, atMs)
	}
	a, ok := f.alerts[alertID]
	if !ok || a.DispatchedAt != nil {
		return false, nil
	}
	ts := atMs
	a.DispatchedAt = &ts
	return true, nil
}

func (f *fakeAlertRepository) ListInbox(ctx context.Context, db *gorm.DB, userID string, limit int) ([]*entity.Alert, error) {
	f.ensure()
	var out []*entity.Alert
	for alertID, users := range f.recipients {
		for _, u := range users {
			if u == userID {
				if a, ok := f.alerts[alertID]; ok && a.IsActive() {
					cp := *a
					out = append(out, &cp)
				}
				break
			}
		}
	}
	return out, nil
}

func (f *fakeAlertRepository) PurgeBreadcrumbs(ctx context.Context, db *gorm.DB, beforeMs int64) (int64, error) {
	f.purgeCalls++
	f.purgeBeforeMs = beforeMs
	if f.purgeErr != nil {
		return 0, f.purgeErr
	}
	return f.purged, nil
}

func (f *fakeAlertRepository) CountActive(ctx context.Context, db *gorm.DB) (int64, error) {
	f.ensure()
	var n int64
	for _, alert := range f.alerts {
		if alert.IsActive() {
			n++
		}
	}
	return n, nil
}

// historyFilters mirrors alertStatusesFor for the in-memory store.
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

// historyList applies the same filters/paging as the SQL implementation so
// the usecase tests exercise real ordering and windowing.
func (f *fakeAlertRepository) historyList(req *model.SearchAlertHistoryRequest, match func(*entity.Alert) bool) ([]*entity.Alert, int64, error) {
	f.ensure()
	var rows []*entity.Alert
	for _, alert := range f.alerts {
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

func (f *fakeAlertRepository) isRecipient(alertID, userID string) bool {
	for _, id := range f.recipients[alertID] {
		if id == userID {
			return true
		}
	}
	return false
}

func (f *fakeAlertRepository) ListHistoryBySender(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	return f.historyList(req, func(a *entity.Alert) bool { return a.SenderID == req.UserID })
}

func (f *fakeAlertRepository) ListHistoryForRecipient(ctx context.Context, db *gorm.DB, req *model.SearchAlertHistoryRequest) ([]*entity.Alert, int64, error) {
	return f.historyList(req, func(a *entity.Alert) bool { return f.isRecipient(a.ID, req.UserID) })
}

// SummaryCounts mirrors the single-statement aggregate: sent per status,
// received per status, and distinct responded/resolved alerts.
func (f *fakeAlertRepository) SummaryCounts(ctx context.Context, db *gorm.DB, userID string) (sent, received map[string]int64, respondedTotal, respondedResolved int64, err error) {
	f.ensure()
	sent = map[string]int64{}
	received = map[string]int64{}
	for alertID, alert := range f.alerts {
		if alert.SenderID == userID {
			sent[alert.Status]++
		}
		if f.isRecipient(alertID, userID) {
			received[alert.Status]++
		}
	}
	total := map[string]struct{}{}
	resolved := map[string]struct{}{}
	for alertID, responses := range f.responses {
		for _, r := range responses {
			if r.ResponderID != userID {
				continue
			}
			total[alertID] = struct{}{}
			if alert, ok := f.alerts[alertID]; ok && alert.Status == entity.AlertStatusResolved {
				resolved[alertID] = struct{}{}
			}
		}
	}
	return sent, received, int64(len(total)), int64(len(resolved)), nil
}

func (f *fakeAlertRepository) CountRespondersByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]int, error) {
	f.ensure()
	f.countRespondersBatchCalls++
	out := map[string]int{}
	for _, alertID := range alertIDs {
		seen := map[string]struct{}{}
		for _, r := range f.responses[alertID] {
			seen[r.ResponderID] = struct{}{}
		}
		out[alertID] = len(seen)
	}
	return out, nil
}

func (f *fakeAlertRepository) LatestBreadcrumbsByAlertIDs(ctx context.Context, db *gorm.DB, alertIDs []string) (map[string]*entity.AlertBreadcrumb, error) {
	f.ensure()
	f.latestBreadcrumbsBatchCalls++
	out := map[string]*entity.AlertBreadcrumb{}
	for _, alertID := range alertIDs {
		var latest *entity.AlertBreadcrumb
		for _, crumb := range f.breadcrumbs[alertID] {
			if latest == nil || crumb.RecordedAt > latest.RecordedAt {
				latest = crumb
			}
		}
		if latest != nil {
			out[alertID] = latest
		}
	}
	return out, nil
}

func (f *fakeAlertRepository) ListRespondedAlertIDs(ctx context.Context, db *gorm.DB, userID string, alertIDs []string) (map[string]bool, error) {
	f.ensure()
	f.listRespondedBatchCalls++
	out := map[string]bool{}
	for _, alertID := range alertIDs {
		for _, r := range f.responses[alertID] {
			if r.ResponderID == userID {
				out[alertID] = true
				break
			}
		}
	}
	return out, nil
}

// fakeNotifier satisfies Notifier without real push infra.
type fakeNotifier struct {
	lastRecipients []string
	lastAlert      model.Alert
	notifyErr      error
	notifyCalled   bool
	notifyCalls    int

	// notifiedCh, when set, is closed after the first Notify call so the
	// grace-period tests can wait for the deferred dispatch without
	// polling (or racing on) the fields above.
	notifiedCh chan struct{}
	notifyOnce sync.Once

	recipientNotifyCalled bool
	recipientNotifyCalls  int
	recipientSets         [][]string
	escalatedAlert        model.Alert
}

func (f *fakeNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	f.notifyCalled = true
	f.notifyCalls++
	f.lastRecipients = recipientIDs
	f.lastAlert = alert
	if f.notifiedCh != nil {
		f.notifyOnce.Do(func() { close(f.notifiedCh) })
	}
	return f.notifyErr
}

// NotifyRecipients records the push-only escalation fan-out.
func (f *fakeNotifier) NotifyRecipients(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	f.recipientNotifyCalled = true
	f.recipientNotifyCalls++
	f.recipientSets = append(f.recipientSets, append([]string(nil), recipientIDs...))
	f.escalatedAlert = alert
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
	// Wire the fake roster query to the fake user directory so its
	// joined-name behaviour mirrors the real SQL.
	if repo, ok := deps.Alerts.(*fakeAlertRepository); ok {
		if users, ok := deps.Users.(*fakeUserReader); ok {
			repo.users = users
		}
	}
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

func TestTriggerSOSFanoutReadsLastSeenInOneBatch(t *testing.T) {
	uc, locRepo, _, _ := triggerSetup(t)

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, ""); err != nil {
		t.Fatalf("trigger error = %v", err)
	}
	if locRepo.lastSeenBatchCalls != 1 || locRepo.lastSeenSingleCalls != 0 {
		t.Fatalf("fan-out last_seen lookups = batch:%d single:%d, want 1/0 (no per-candidate HGET)",
			locRepo.lastSeenBatchCalls, locRepo.lastSeenSingleCalls)
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

// Alert persistence is the only 5xx-worthy failure: no alert means nothing
// to dispatch, so the notifier must not run (#37).
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
	if !alertRepo.createCalled {
		t.Error("AlertRepository.Create() was not attempted")
	}
	if notifier.notifyCalled {
		t.Error("Notifier.Notify() must not run when the alert could not be persisted")
	}
	if locRepo.geoSearchCalls != 0 {
		t.Errorf("GeoSearch() called %d times, want 0 after save failure", locRepo.geoSearchCalls)
	}
}

// fakeClassifyStore stands in for the Redis handoff of the audio triage.
type fakeClassifyStore struct {
	info    *model.TriageInfo
	saves   int
	taken   int
	saveErr error
	takeErr error
}

func (f *fakeClassifyStore) Save(ctx context.Context, userID string, info model.TriageInfo) error {
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	f.info = &info
	return nil
}

func (f *fakeClassifyStore) Take(ctx context.Context, userID string) (*model.TriageInfo, error) {
	f.taken++
	if f.takeErr != nil {
		return nil, f.takeErr
	}
	info := f.info
	f.info = nil
	return info, nil
}

// A client-supplied severity/summary is only a claim: it must not widen the
// fan-out, must not become the stored triage, and must be labelled as the
// sender's claim in the notification.
func TestTriggerSOSClientClaimIsNotTrustedAsTriage(t *testing.T) {
	uc, locRepo, _, notifier := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{
		1000: {"sender", "user-b", "user-c", "user-d"},
		5000: {"sender", "user-b", "user-c", "user-d", "user-e", "user-f"},
	}
	locRepo.lastSeenMs["user-e"] = freshLastSeen()
	locRepo.lastSeenMs["user-f"] = freshLastSeen()

	claimSeverity := 0.9
	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{
		ClientSeverity: &claimSeverity,
		ClientSummary:  "TRIAGE-OVERRIDE-WITNESS",
	}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if result.RadiusMeters != 1000 {
		t.Fatalf("radius = %v, want 1000 (a claim must not widen the fan-out)", result.RadiusMeters)
	}
	if result.Triage != nil {
		t.Fatalf("triage = %+v, want no server triage for an empty description", result.Triage)
	}
	if notifier.lastAlert.Severity != 0 || notifier.lastAlert.Summary != "" {
		t.Fatalf("stored triage = %v/%q, want empty", notifier.lastAlert.Severity, notifier.lastAlert.Summary)
	}
	if notifier.lastAlert.ClientSeverity == nil || *notifier.lastAlert.ClientSeverity != claimSeverity {
		t.Fatalf("client claim = %v, want it recorded as a claim", notifier.lastAlert.ClientSeverity)
	}
	if notifier.lastAlert.ClientSummary != "TRIAGE-OVERRIDE-WITNESS" {
		t.Fatalf("client summary = %q, want the claim kept verbatim", notifier.lastAlert.ClientSummary)
	}
	if result.Alert.ClientSummary != "TRIAGE-OVERRIDE-WITNESS" {
		t.Fatalf("result claim = %q, want it echoed to the client", result.Alert.ClientSummary)
	}
}

// A server triage out of range is clamped before it reaches the radius picker.
func TestTriggerSOSClampsServerSeverity(t *testing.T) {
	uc, locRepo, _, _ := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{5000: {"sender", "user-b"}}
	uc.Deps.Classify = &fakeClassifyStore{info: &model.TriageInfo{Severity: 4.2}}

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if result.Triage == nil || result.Triage.Severity != 1 {
		t.Fatalf("triage = %+v, want severity clamped to 1", result.Triage)
	}
}

// Jev's lowest rubric level is a legitimate score of 0 (#55). A triage that
// carries a type and severity 0 must be persisted as 0 — not left NULL as if
// the alert had never been classified — while still starting at the closest
// radius.
func TestTriggerSOSStoresZeroSeverity(t *testing.T) {
	uc, locRepo, alertRepo, _ := triggerSetup(t)
	locRepo.nearbyByRadius = map[float64][]string{1000: {"sender", "user-b", "user-c", "user-d"}}
	uc.Deps.Triage = &fakeTriageEngine{res: TriageResult{
		EmergencyType: entity.EmergencyTypeDanger,
		Severity:      0,
		Summary:       "Potensi bahaya atau ancaman",
	}}

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	if alertRepo.lastAlert.Severity == nil {
		t.Fatal("stored severity = NULL, want a persisted 0")
	}
	if *alertRepo.lastAlert.Severity != 0 {
		t.Fatalf("stored severity = %v, want 0", *alertRepo.lastAlert.Severity)
	}
	if result.Triage == nil || result.Triage.Severity != 0 || result.Triage.EmergencyType != entity.EmergencyTypeDanger {
		t.Fatalf("triage = %+v, want DANGER with severity 0", result.Triage)
	}
	if result.RadiusMeters != 1000 {
		t.Fatalf("radius = %v, want the closest 1000 m ring", result.RadiusMeters)
	}
}

// dispatchLatencyMetrics records ObserveDispatchDuration samples.
type dispatchLatencyMetrics struct {
	mu      sync.Mutex
	samples []float64
}

func (m *dispatchLatencyMetrics) ObserveDispatchDuration(ms float64) {
	m.mu.Lock()
	m.samples = append(m.samples, ms)
	m.mu.Unlock()
}

func (m *dispatchLatencyMetrics) IncPushFailures()   {}
func (m *dispatchLatencyMetrics) IncFanoutFailures() {}
func (m *dispatchLatencyMetrics) SetActiveSOS(int64) {}

func (m *dispatchLatencyMetrics) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.samples)
}

func (m *dispatchLatencyMetrics) first() (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.samples) == 0 {
		return 0, false
	}
	return m.samples[0], true
}

// With a grace period the dispatch runs seconds after the request (now via
// the durable outbox, #40); the latency metric must describe the
// notification send, not the countdown.
func TestDispatchLatencyExcludesGracePeriod(t *testing.T) {
	uc, _, _, _ := triggerSetup(t)
	met := &dispatchLatencyMetrics{}
	uc.Deps.Metrics = met
	uc.Deps.GracePeriod = 100 * time.Millisecond

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, ""); err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	if _, err := uc.DispatchDueAlerts(context.Background()); err != nil {
		t.Fatalf("DispatchDueAlerts error = %v", err)
	}
	sample, ok := met.first()
	if !ok {
		t.Fatal("dispatch latency was never recorded")
	}
	if sample >= float64(uc.Deps.GracePeriod.Milliseconds()) {
		t.Fatalf("dispatch latency = %v ms, want the send time only (grace period = %v ms)",
			sample, uc.Deps.GracePeriod.Milliseconds())
	}
}

// findRecipients used to pull every user in the radius and run an HMGET
// over all of them: the candidate set must be bounded (MaxRecipients*4)
// both at Redis (GEOSEARCH COUNT) and before the last-seen batch, while
// still returning the closest fresh recipients.
func TestFindRecipientsCapsCandidates(t *testing.T) {
	uc, locRepo, _, _ := triggerSetup(t)

	nearby := []string{"sender"}
	lastSeen := map[string]int64{"sender": freshLastSeen()}
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("u%03d", i)
		nearby = append(nearby, id)
		if i < 60 {
			// Stale users still occupy candidate slots; the cap must not
			// hide the fresh users behind them.
			lastSeen[id] = staleLastSeen()
			continue
		}
		lastSeen[id] = freshLastSeen()
	}
	locRepo.nearby = nearby
	locRepo.lastSeenMs = lastSeen

	result, err := uc.ExecuteWithRequest(context.Background(), "sender", &model.TriggerSOSRequest{}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest error = %v", err)
	}

	wantCandidates := uc.Deps.MaxRecipients * 4
	if len(locRepo.gotCounts) == 0 {
		t.Fatal("GeoSearch was not called")
	}
	if got := locRepo.gotCounts[0]; got != wantCandidates+1 {
		t.Fatalf("GeoSearch count = %d, want %d (cap + sender slot)", got, wantCandidates+1)
	}
	if locRepo.lastSeenBatchMax > wantCandidates {
		t.Fatalf("GetLastSeenBatch got %d candidates, want at most %d", locRepo.lastSeenBatchMax, wantCandidates)
	}
	if len(result.Recipients) != uc.Deps.MaxRecipients {
		t.Fatalf("recipients = %d, want the cap %d", len(result.Recipients), uc.Deps.MaxRecipients)
	}
	// Nearest first, stale skipped: the first 60 candidates are stale.
	if result.Recipients[0] != "u060" {
		t.Fatalf("first recipient = %q, want u060 (closest fresh candidate)", result.Recipients[0])
	}
	if last := result.Recipients[len(result.Recipients)-1]; last != "u109" {
		t.Fatalf("last recipient = %q, want u109", last)
	}
}
