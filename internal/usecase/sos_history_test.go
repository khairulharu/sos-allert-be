package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

// historySetup builds a use case backed by the in-memory alert store and
// seeds a small mixed-status fixture.
func historySetup(t *testing.T) (*TriggerSOSUseCase, *fakeAlertRepository) {
	t.Helper()
	repo := &fakeAlertRepository{}
	repo.ensure()
	users := &fakeUserReader{users: map[string]*entity.User{
		"sender": {ID: "sender", Name: "Sender"},
		"other":  {ID: "other", Name: "Other"},
	}}
	uc := newUseCaseWithDeps(TriggerSOSDeps{
		Alerts:   repo,
		Users:    users,
		Validate: validator.New(),
	})

	resolvedAt := int64(2600)
	seed := func(id, sender, status string, triggeredAt int64, resolved *int64) {
		repo.alerts[id] = &entity.Alert{
			ID: id, SenderID: sender, Status: status, EmergencyType: entity.EmergencyTypeDanger,
			Latitude: -6.2091234, Longitude: 106.8461234, TriggeredAt: triggeredAt, ResolvedAt: resolved,
		}
	}
	seed("a1", "sender", entity.AlertStatusTriggered, 1000, nil)
	seed("a2", "sender", entity.AlertStatusResolved, 2000, &resolvedAt)
	seed("a3", "sender", entity.AlertStatusCancelled, 3000, nil)
	seed("a4", "sender", entity.AlertStatusExpired, 1500, nil)
	seed("a5", "sender", entity.AlertStatusAcknowledged, 5000, nil)
	seed("other-1", "other", entity.AlertStatusResolved, 2500, &resolvedAt)

	repo.recipients["a1"] = []string{"user-b"}
	repo.recipients["a2"] = []string{"user-b", "user-c"}
	repo.recipients["other-1"] = []string{"user-b"}
	repo.responses["a2"] = []*entity.AlertResponse{
		{AlertID: "a2", ResponderID: "user-b", RespondedAt: 2100},
		// duplicate answer: counts must stay distinct per alert
		{AlertID: "a2", ResponderID: "user-b", RespondedAt: 2200},
	}
	repo.responses["other-1"] = []*entity.AlertResponse{
		{AlertID: "other-1", ResponderID: "user-c", RespondedAt: 2600},
	}
	return uc, repo
}

func historyIDs(items []*model.AlertDetailResponse) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.AlertID)
	}
	return out
}

func TestHistorySentIncludesResolved(t *testing.T) {
	uc, _ := historySetup(t)

	items, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("ListHistory error = %v", err)
	}
	if total != 5 || len(items) != 5 {
		t.Fatalf("total = %d, items = %d, want 5/5", total, len(items))
	}
	// Newest first.
	want := []string{"a5", "a3", "a2", "a4", "a1"}
	got := historyIDs(items)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	var resolved *model.AlertDetailResponse
	for _, item := range items {
		if item.AlertID == "a2" {
			resolved = item
		}
	}
	if resolved == nil || resolved.Status != entity.AlertStatusResolved || resolved.ResolvedAt == nil {
		t.Fatalf("resolved alert missing from sent history: %+v", resolved)
	}
	if resolved.RespondersEnRoute != 1 {
		t.Fatalf("responders_en_route = %d, want 1 (duplicates deduped)", resolved.RespondersEnRoute)
	}
}

func TestHistoryStatusGroups(t *testing.T) {
	uc, _ := historySetup(t)

	closed, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Status: "closed", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("closed history error = %v", err)
	}
	if total != 3 || len(closed) != 3 {
		t.Fatalf("closed = %v (total %d), want a3/a2/a4", historyIDs(closed), total)
	}
	for _, item := range closed {
		if item.Status != entity.AlertStatusResolved &&
			item.Status != entity.AlertStatusCancelled &&
			item.Status != entity.AlertStatusExpired {
			t.Fatalf("closed history contains %s", item.Status)
		}
	}

	active, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Status: "active", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("active history error = %v", err)
	}
	if total != 2 || len(active) != 2 {
		t.Fatalf("active = %v (total %d), want a5/a1", historyIDs(active), total)
	}
}

func TestHistoryReceivedOnlyRecipients(t *testing.T) {
	uc, _ := historySetup(t)

	items, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "user-b", Role: "received", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("received history error = %v", err)
	}
	if total != 3 || len(items) != 3 {
		t.Fatalf("received = %v (total %d), want a2/other-1/a1", historyIDs(items), total)
	}
	want := map[string]bool{"a1": true, "a2": true, "other-1": true}
	for _, item := range items {
		if !want[item.AlertID] {
			t.Fatalf("unexpected alert %s in received history", item.AlertID)
		}
	}

	// A user who did not receive anything gets an empty page, not an error.
	empty, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "stranger", Role: "received", Page: 1, Size: 20,
	})
	if err != nil || total != 0 || len(empty) != 0 {
		t.Fatalf("stranger received = %v (total %d, err %v)", empty, total, err)
	}
}

func TestHistoryPagingStable(t *testing.T) {
	uc, repo := historySetup(t)
	// Same timestamp exercises the id DESC tie-break.
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("p%02d", i)
		repo.alerts[id] = &entity.Alert{
			ID: id, SenderID: "sender", Status: entity.AlertStatusResolved,
			TriggeredAt: 10_000, Latitude: -6.2, Longitude: 106.8,
		}
	}

	page1, total, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("page 1 error = %v", err)
	}
	page2, total2, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Page: 2, Size: 20,
	})
	if err != nil {
		t.Fatalf("page 2 error = %v", err)
	}
	if total != 30 || total2 != 30 {
		t.Fatalf("totals = %d/%d, want 30", total, total2)
	}
	if len(page1) != 20 || len(page2) != 10 {
		t.Fatalf("page sizes = %d/%d, want 20/10", len(page1), len(page2))
	}
	seen := map[string]bool{}
	for _, item := range append(page1, page2...) {
		if seen[item.AlertID] {
			t.Fatalf("duplicate alert %s across pages", item.AlertID)
		}
		seen[item.AlertID] = true
	}
	if len(seen) != 30 {
		t.Fatalf("unique alerts = %d, want 30", len(seen))
	}
	// Tie-break: descending id within the same triggered_at.
	if page1[0].AlertID != "p24" || page1[1].AlertID != "p23" {
		t.Fatalf("tie-break order = %s,%s; want p24,p23", page1[0].AlertID, page1[1].AlertID)
	}
}

func TestHistoryMaskingFollowsParticipants(t *testing.T) {
	uc, _ := historySetup(t)

	sender, _, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Status: "RESOLVED", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("sender history error = %v", err)
	}
	if len(sender) != 1 || sender[0].Masked {
		t.Fatalf("sender view should not be masked: %+v", sender)
	}
	if sender[0].InitialLocation.Latitude != -6.2091234 {
		t.Fatalf("sender latitude = %v, want exact", sender[0].InitialLocation.Latitude)
	}

	received, _, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "user-b", Role: "received", Status: "RESOLVED", Page: 1, Size: 20,
	})
	if err != nil {
		t.Fatalf("received history error = %v", err)
	}
	byID := map[string]*model.AlertDetailResponse{}
	for _, item := range received {
		byID[item.AlertID] = item
	}
	// user-b responded to a2 -> participant -> exact coordinates.
	if a2 := byID["a2"]; a2 == nil || a2.Masked || a2.InitialLocation.Latitude != -6.2091234 {
		t.Fatalf("responder detail should be unmasked: %+v", a2)
	}
	// user-b did not respond to other-1 -> masked ~3 decimals.
	other := byID["other-1"]
	if other == nil || !other.Masked {
		t.Fatalf("non-responder detail should be masked: %+v", other)
	}
	wantLat, wantLng := model.MaskCoordinates(-6.2091234, 106.8461234)
	if other.InitialLocation.Latitude != wantLat || other.InitialLocation.Longitude != wantLng {
		t.Fatalf("masked coords = %+v, want %v/%v", other.InitialLocation, wantLat, wantLng)
	}

	// History never carries the responder roster or breadcrumbs.
	for _, item := range received {
		if len(item.Responders) != 0 || len(item.Breadcrumbs) != 0 {
			t.Fatalf("history item leaked responders/breadcrumbs: %+v", item)
		}
	}
}

func TestHistoryUsesBatchedQueries(t *testing.T) {
	uc, repo := historySetup(t)

	if _, _, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Page: 1, Size: 20,
	}); err != nil {
		t.Fatalf("ListHistory error = %v", err)
	}
	if repo.countRespondersBatchCalls != 1 || repo.latestBreadcrumbsBatchCalls != 1 {
		t.Fatalf("batch calls = %d/%d, want 1/1 (no per-row queries)",
			repo.countRespondersBatchCalls, repo.latestBreadcrumbsBatchCalls)
	}

	// A bigger page must not add queries.
	repo.countRespondersBatchCalls = 0
	repo.latestBreadcrumbsBatchCalls = 0
	if _, _, err := uc.ListHistory(context.Background(), &model.SearchAlertHistoryRequest{
		UserID: "sender", Role: "sent", Page: 1, Size: 100,
	}); err != nil {
		t.Fatalf("ListHistory (size 100) error = %v", err)
	}
	if repo.countRespondersBatchCalls != 1 || repo.latestBreadcrumbsBatchCalls != 1 {
		t.Fatalf("batch calls for size=100 = %d/%d, want 1/1",
			repo.countRespondersBatchCalls, repo.latestBreadcrumbsBatchCalls)
	}
}

func TestSummaryCounts(t *testing.T) {
	uc, _ := historySetup(t)

	senderSummary, err := uc.Summary(context.Background(), "sender")
	if err != nil {
		t.Fatalf("Summary(sender) error = %v", err)
	}
	wantSent := model.AlertStatusCounts{Total: 5, Active: 2, Resolved: 1, Cancelled: 1, Expired: 1}
	if senderSummary.Sent != wantSent {
		t.Fatalf("sent = %+v, want %+v", senderSummary.Sent, wantSent)
	}
	if senderSummary.Received != (model.AlertStatusCounts{}) {
		t.Fatalf("received = %+v, want zeroes", senderSummary.Received)
	}

	responderSummary, err := uc.Summary(context.Background(), "user-b")
	if err != nil {
		t.Fatalf("Summary(user-b) error = %v", err)
	}
	wantReceived := model.AlertStatusCounts{Total: 3, Active: 1, Resolved: 2}
	if responderSummary.Received != wantReceived {
		t.Fatalf("received = %+v, want %+v", responderSummary.Received, wantReceived)
	}
	if responderSummary.Responded != (model.AlertRespondedCounts{Total: 1, Resolved: 1}) {
		t.Fatalf("responded = %+v, want 1/1 (duplicates deduped)", responderSummary.Responded)
	}
}

func TestHistoryValidation(t *testing.T) {
	uc, _ := historySetup(t)

	from := int64(2000)
	to := int64(1000)
	cases := []struct {
		name string
		req  *model.SearchAlertHistoryRequest
	}{
		{"unknown role", &model.SearchAlertHistoryRequest{UserID: "sender", Role: "foo", Page: 1, Size: 20}},
		{"unknown status", &model.SearchAlertHistoryRequest{UserID: "sender", Role: "sent", Status: "nope", Page: 1, Size: 20}},
		{"page zero", &model.SearchAlertHistoryRequest{UserID: "sender", Role: "sent", Page: 0, Size: 20}},
		{"size over max", &model.SearchAlertHistoryRequest{UserID: "sender", Role: "sent", Page: 1, Size: 500}},
		{"empty window start", &model.SearchAlertHistoryRequest{UserID: "sender", Role: "sent", Page: 1, Size: 20, FromMs: &from, ToMs: &to}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := uc.ListHistory(context.Background(), tc.req); !errors.Is(err, fiber.ErrBadRequest) {
				t.Fatalf("err = %v, want fiber.ErrBadRequest", err)
			}
		})
	}
}
