package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// detailBatchSetup builds a use case backed by the in-memory alert store
// and a user directory big enough to resolve sender and responder names.
func detailBatchSetup(t *testing.T) (*TriggerSOSUseCase, *fakeAlertRepository, *fakeUserReader) {
	t.Helper()
	repo := &fakeAlertRepository{}
	repo.ensure()
	users := &fakeUserReader{users: map[string]*entity.User{
		"sender": {ID: "sender", Name: "Sender"},
		"user-b": {ID: "user-b", Name: "Budi"},
		"user-c": {ID: "user-c", Name: "Citra"},
	}}
	uc := newUseCaseWithDeps(TriggerSOSDeps{Alerts: repo, Users: users})
	return uc, repo, users
}

func seedActiveAlert(repo *fakeAlertRepository, id, sender string, triggeredAt int64) *entity.Alert {
	alert := &entity.Alert{
		ID:            id,
		SenderID:      sender,
		Status:        entity.AlertStatusTriggered,
		EmergencyType: entity.EmergencyTypeDanger,
		Latitude:      -6.2091234,
		Longitude:     106.8461234,
		TriggeredAt:   triggeredAt,
	}
	repo.alerts[id] = alert
	return alert
}

// resetDetailCounters clears every detail-read counter so a test can
// assert the exact query shape of one call.
func (f *fakeAlertRepository) resetDetailCounters() {
	f.countRespondersBatchCalls = 0
	f.latestBreadcrumbsBatchCalls = 0
	f.responderSummariesBatchCalls = 0
	f.listRespondedBatchCalls = 0
	f.countRespondersCalls = 0
	f.latestBreadcrumbCalls = 0
	f.isResponderCalls = 0
	f.listResponsesCalls = 0
	f.listRecipientsCalls = 0
}

func assertNoPerRowDetailReads(t *testing.T, repo *fakeAlertRepository) {
	t.Helper()
	if repo.countRespondersCalls != 0 || repo.latestBreadcrumbCalls != 0 ||
		repo.isResponderCalls != 0 || repo.listResponsesCalls != 0 {
		t.Fatalf("per-row detail reads = count:%d crumb:%d responder:%d responses:%d, want 0",
			repo.countRespondersCalls, repo.latestBreadcrumbCalls,
			repo.isResponderCalls, repo.listResponsesCalls)
	}
}

func TestListActiveUsesBatchedDetailLookups(t *testing.T) {
	uc, repo, users := detailBatchSetup(t)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("alert-%02d", i)
		seedActiveAlert(repo, id, "sender", int64(1000+i))
		repo.recipients[id] = []string{"user-b"}
		repo.responses[id] = []*entity.AlertResponse{{
			ID: "resp-" + id, AlertID: id, ResponderID: "user-c",
			DistanceMeters: 42, ETASeconds: 90, RespondedAt: int64(2000 + i),
		}}
		repo.breadcrumbs[id] = []*entity.AlertBreadcrumb{{
			AlertID: id, Latitude: -6.21, Longitude: 106.85, RecordedAt: int64(3000 + i),
		}}
	}
	repo.resetDetailCounters()
	users.listByIDsCalls = 0

	items, err := uc.ListActive(context.Background(), "sender")
	if err != nil {
		t.Fatalf("ListActive error = %v", err)
	}
	if len(items) != 20 {
		t.Fatalf("ListActive returned %d items, want 20", len(items))
	}
	if repo.countRespondersBatchCalls != 1 || repo.latestBreadcrumbsBatchCalls != 1 ||
		repo.responderSummariesBatchCalls != 1 {
		t.Fatalf("batch calls = counts:%d crumbs:%d summaries:%d, want 1/1/1",
			repo.countRespondersBatchCalls, repo.latestBreadcrumbsBatchCalls,
			repo.responderSummariesBatchCalls)
	}
	if repo.listRespondedBatchCalls != 0 {
		t.Fatalf("role=sent ran %d responded lookups, want 0", repo.listRespondedBatchCalls)
	}
	assertNoPerRowDetailReads(t, repo)
	// Sender names only: the roster query already carries responder names.
	if users.listByIDsCalls != 1 {
		t.Fatalf("ListByIDs calls = %d, want 1 (senders)", users.listByIDsCalls)
	}

	for _, item := range items {
		if item.Sender.Name != "Sender" {
			t.Fatalf("sender name = %q, want Sender", item.Sender.Name)
		}
		if item.Masked {
			t.Fatalf("sender view must not be masked: %+v", item)
		}
		if item.RespondersEnRoute != 1 {
			t.Fatalf("responders_en_route = %d, want 1", item.RespondersEnRoute)
		}
		if len(item.Responders) != 1 || item.Responders[0].UserID != "user-c" ||
			item.Responders[0].Name != "Citra" {
			t.Fatalf("responders = %+v, want Citra", item.Responders)
		}
		if item.LatestLocation.UpdatedAt == nil {
			t.Fatalf("latest location missing breadcrumb timestamp: %+v", item.LatestLocation)
		}
	}
}

func TestListInboxUsesBatchedDetailLookups(t *testing.T) {
	uc, repo, users := detailBatchSetup(t)
	seedActiveAlert(repo, "a-responded", "sender", 1000)
	seedActiveAlert(repo, "a-unresponded", "sender", 2000)
	repo.recipients["a-responded"] = []string{"user-b"}
	repo.recipients["a-unresponded"] = []string{"user-b"}
	repo.responses["a-responded"] = []*entity.AlertResponse{
		{ID: "resp-b-late", AlertID: "a-responded", ResponderID: "user-c",
			DistanceMeters: 30, ETASeconds: 70, RespondedAt: 1600},
		// Deliberately out of order: the API must expose responded_at ASC.
		{ID: "resp-b", AlertID: "a-responded", ResponderID: "user-b",
			DistanceMeters: 15, ETASeconds: 40, RespondedAt: 1500},
	}
	repo.breadcrumbs["a-unresponded"] = []*entity.AlertBreadcrumb{{
		AlertID: "a-unresponded", Latitude: -6.22, Longitude: 106.86, RecordedAt: 2500,
	}}
	repo.resetDetailCounters()
	users.listByIDsCalls = 0

	items, err := uc.ListInbox(context.Background(), "user-b")
	if err != nil {
		t.Fatalf("ListInbox error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ListInbox returned %d items, want 2", len(items))
	}
	if repo.countRespondersBatchCalls != 1 || repo.latestBreadcrumbsBatchCalls != 1 ||
		repo.responderSummariesBatchCalls != 1 || repo.listRespondedBatchCalls != 1 {
		t.Fatalf("batch calls = counts:%d crumbs:%d summaries:%d responded:%d, want 1/1/1/1",
			repo.countRespondersBatchCalls, repo.latestBreadcrumbsBatchCalls,
			repo.responderSummariesBatchCalls, repo.listRespondedBatchCalls)
	}
	assertNoPerRowDetailReads(t, repo)
	if users.listByIDsCalls != 1 {
		t.Fatalf("ListByIDs calls = %d, want 1 (senders)", users.listByIDsCalls)
	}

	byID := map[string]bool{}
	for _, item := range items {
		switch item.AlertID {
		case "a-responded":
			if item.Masked {
				t.Fatalf("a responder must see exact coordinates: %+v", item)
			}
			if len(item.Responders) != 2 || item.Responders[0].Name != "Budi" ||
				item.Responders[1].Name != "Citra" {
				t.Fatalf("responder roster = %+v, want Budi then Citra", item.Responders)
			}
			if item.LatestLocation.Latitude != -6.2091234 {
				t.Fatalf("responder latest location = %+v, want exact", item.LatestLocation)
			}
			byID[item.AlertID] = true
		case "a-unresponded":
			if !item.Masked {
				t.Fatalf("a notified non-responder must stay masked: %+v", item)
			}
			wantLat, wantLng := model.MaskCoordinates(-6.2091234, 106.8461234)
			if item.InitialLocation.Latitude != wantLat || item.InitialLocation.Longitude != wantLng {
				t.Fatalf("masked coords = %+v, want %v/%v", item.InitialLocation, wantLat, wantLng)
			}
			if len(item.Responders) != 0 {
				t.Fatalf("non-participant leaked responder identities: %+v", item.Responders)
			}
			if item.RespondersEnRoute != 0 {
				t.Fatalf("responders_en_route = %d, want 0", item.RespondersEnRoute)
			}
			byID[item.AlertID] = true
		}
	}
	if len(byID) != 2 {
		t.Fatalf("inbox ids = %v, want a-responded and a-unresponded", byID)
	}
}

func TestListDetailJSONMatchesSingleAlert(t *testing.T) {
	uc, repo, _ := detailBatchSetup(t)
	seedActiveAlert(repo, "a1", "sender", 1000)
	repo.recipients["a1"] = []string{"user-b"}
	repo.responses["a1"] = []*entity.AlertResponse{{
		ID: "resp-c", AlertID: "a1", ResponderID: "user-c",
		DistanceMeters: 42, ETASeconds: 90, RespondedAt: 1200,
	}}
	repo.breadcrumbs["a1"] = []*entity.AlertBreadcrumb{{
		AlertID: "a1", Latitude: -6.21, Longitude: 106.85, RecordedAt: 1300,
	}}
	seedActiveAlert(repo, "a2", "sender", 2000)
	repo.recipients["a2"] = []string{"user-b"}
	repo.responses["a2"] = []*entity.AlertResponse{{
		ID: "resp-b", AlertID: "a2", ResponderID: "user-b",
		DistanceMeters: 15, ETASeconds: 40, RespondedAt: 2200,
	}}

	active, err := uc.ListActive(context.Background(), "sender")
	if err != nil {
		t.Fatalf("ListActive error = %v", err)
	}
	for _, item := range active {
		single, err := uc.GetAlert(context.Background(), "sender", item.AlertID)
		if err != nil {
			t.Fatalf("GetAlert(sender, %s) error = %v", item.AlertID, err)
		}
		if got, want := marshalDetail(t, item), marshalDetail(t, single); got != want {
			t.Fatalf("list/single JSON differ for %s:\nlist=%s\nsingle=%s", item.AlertID, got, want)
		}
	}

	inbox, err := uc.ListInbox(context.Background(), "user-b")
	if err != nil {
		t.Fatalf("ListInbox error = %v", err)
	}
	for _, item := range inbox {
		single, err := uc.GetAlert(context.Background(), "user-b", item.AlertID)
		if err != nil {
			t.Fatalf("GetAlert(user-b, %s) error = %v", item.AlertID, err)
		}
		if got, want := marshalDetail(t, item), marshalDetail(t, single); got != want {
			t.Fatalf("inbox/single JSON differ for %s:\nlist=%s\nsingle=%s", item.AlertID, got, want)
		}
	}
}

func marshalDetail(t *testing.T, detail interface{}) string {
	t.Helper()
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	return string(raw)
}

func TestGetAlertResolvesResponderOnce(t *testing.T) {
	uc, repo, users := detailBatchSetup(t)
	seedActiveAlert(repo, "a1", "sender", 1000)
	repo.recipients["a1"] = []string{"user-b"}
	repo.responses["a1"] = []*entity.AlertResponse{{
		ID: "resp-c", AlertID: "a1", ResponderID: "user-c", RespondedAt: 1100,
	}}

	repo.resetDetailCounters()
	users.listByIDsCalls = 0
	if _, err := uc.GetAlert(context.Background(), "sender", "a1"); err != nil {
		t.Fatalf("GetAlert(sender) error = %v", err)
	}
	if repo.isResponderCalls != 0 || repo.listRecipientsCalls != 0 {
		t.Fatalf("sender lookup ran responder checks: %d/%d, want 0/0",
			repo.isResponderCalls, repo.listRecipientsCalls)
	}
	if users.listByIDsCalls != 1 {
		t.Fatalf("sender name lookups = %d, want 1", users.listByIDsCalls)
	}

	repo.resetDetailCounters()
	users.listByIDsCalls = 0
	if _, err := uc.GetAlert(context.Background(), "user-b", "a1"); err != nil {
		t.Fatalf("GetAlert(recipient) error = %v", err)
	}
	if repo.isResponderCalls != 1 || repo.listRecipientsCalls != 1 {
		t.Fatalf("recipient checks = responder:%d recipients:%d, want 1/1",
			repo.isResponderCalls, repo.listRecipientsCalls)
	}
	if users.listByIDsCalls != 1 {
		t.Fatalf("recipient sender name lookups = %d, want 1", users.listByIDsCalls)
	}

	repo.responses["a1"] = append(repo.responses["a1"], &entity.AlertResponse{
		ID: "resp-b", AlertID: "a1", ResponderID: "user-b", RespondedAt: 1200,
	})
	repo.resetDetailCounters()
	users.listByIDsCalls = 0
	if _, err := uc.GetAlert(context.Background(), "user-b", "a1"); err != nil {
		t.Fatalf("GetAlert(responder) error = %v", err)
	}
	if repo.isResponderCalls != 1 || repo.listRecipientsCalls != 0 {
		t.Fatalf("responder checks = responder:%d recipients:%d, want 1/0 (no repeat)",
			repo.isResponderCalls, repo.listRecipientsCalls)
	}
	if users.listByIDsCalls != 1 {
		t.Fatalf("responder sender name lookups = %d, want 1", users.listByIDsCalls)
	}
}

func TestListActiveEmptyPageSkipsDetailReads(t *testing.T) {
	uc, repo, users := detailBatchSetup(t)
	repo.resetDetailCounters()
	users.listByIDsCalls = 0

	items, err := uc.ListActive(context.Background(), "sender")
	if err != nil {
		t.Fatalf("ListActive error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("ListActive returned %d items, want 0", len(items))
	}
	if repo.countRespondersBatchCalls != 0 || repo.latestBreadcrumbsBatchCalls != 0 ||
		repo.responderSummariesBatchCalls != 0 || repo.listRespondedBatchCalls != 0 {
		t.Fatalf("empty page ran batch reads: %+v", repo)
	}
	assertNoPerRowDetailReads(t, repo)
	if users.listByIDsCalls != 0 {
		t.Fatalf("empty page ran %d user lookups, want 0", users.listByIDsCalls)
	}

	repo.resetDetailCounters()
	inbox, err := uc.ListInbox(context.Background(), "user-b")
	if err != nil {
		t.Fatalf("ListInbox error = %v", err)
	}
	if len(inbox) != 0 {
		t.Fatalf("ListInbox returned %d items, want 0", len(inbox))
	}
	if repo.countRespondersBatchCalls != 0 || repo.latestBreadcrumbsBatchCalls != 0 ||
		repo.responderSummariesBatchCalls != 0 || repo.listRespondedBatchCalls != 0 {
		t.Fatalf("empty inbox ran batch reads: %+v", repo)
	}
	assertNoPerRowDetailReads(t, repo)
}

func TestListInboxFailsWhenRosterReadFails(t *testing.T) {
	uc, repo, _ := detailBatchSetup(t)
	seedActiveAlert(repo, "a1", "sender", 1000)
	repo.recipients["a1"] = []string{"user-b"}
	repo.responses["a1"] = []*entity.AlertResponse{{
		ID: "resp-b", AlertID: "a1", ResponderID: "user-b", RespondedAt: 1100,
	}}
	rosterErr := errors.New("roster unavailable")
	repo.responderSummariesErr = rosterErr

	if _, err := uc.ListInbox(context.Background(), "user-b"); !errors.Is(err, rosterErr) {
		t.Fatalf("ListInbox error = %v, want %v", err, rosterErr)
	}
}

// The resolved-viewer path is a single-alert optimization; a partial map
// must fail loudly instead of silently masking a responder's own incident.
func TestBuildAlertDetailsRejectsMissingResolvedViewer(t *testing.T) {
	uc, repo, _ := detailBatchSetup(t)
	alert := seedActiveAlert(repo, "a1", "sender", 1000)

	_, err := uc.buildAlertDetails(context.Background(), []*entity.Alert{alert}, detailBatchOptions{
		requesterID:     "user-b",
		withResponders:  true,
		viewersResolved: true,
	})
	if err == nil {
		t.Fatal("missing resolved viewer must fail instead of falling back to masking")
	}
}
