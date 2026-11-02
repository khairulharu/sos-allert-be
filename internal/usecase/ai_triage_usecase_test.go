package usecase

import (
	"context"
	"errors"
	"testing"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
)

// triggerTriageDeps builds the standard fakes plus a scripted triage
// engine and the use case wired to them.
func triggerTriageDeps(triage EmergencyTriage) (*fakeLocationRepository, *fakeAlertRepository, *fakeNotifier, *TriggerSOSUseCase) {
	sender := &entity.LocationCache{UserId: "sender", Longitude: 108.8272, Latitude: -6.1754}
	loc := &fakeLocationRepository{sender: sender, nearby: []string{"user-b"}, lastSeenMs: map[string]int64{"user-b": freshLastSeen()}}
	alerts := &fakeAlertRepository{}
	notifier := &fakeNotifier{}
	uc := newUseCaseWithDeps(TriggerSOSDeps{
		Location: loc, Alerts: alerts, Notifier: notifier, Triage: triage,
	})
	return loc, alerts, notifier, uc
}

func TestRadiusStartIndex(t *testing.T) {
	uc := NewTriggerSOSUseCase(nil, noopLogger(), TriggerSOSDeps{})
	// defaults from NewTriggerSOSUseCase: {1000, 2000, 5000}
	cases := []struct {
		severity float64
		want     int
	}{
		{severity: 0, want: 0},
		{severity: -1, want: 0},
		{severity: 0.3, want: 0},
		{severity: 0.34, want: 1},
		{severity: 0.5, want: 1},
		{severity: 0.67, want: 2},
		{severity: 0.9, want: 2},
		{severity: 1, want: 2},
	}
	for _, tc := range cases {
		if got := uc.radiusStartIndex(tc.severity); got != tc.want {
			t.Errorf("radiusStartIndex(%v) = %d, want %d", tc.severity, got, tc.want)
		}
	}
}

func TestTriggerSOSAITriageClassification(t *testing.T) {
	triage := &fakeTriageEngine{res: TriageResult{
		EmergencyType: entity.EmergencyTypeMedical,
		Severity:      0.8,
		Summary:       "Kemungkinan darurat medis",
	}}
	loc, alerts, notifier, uc := triggerTriageDeps(triage)

	result, err := uc.ExecuteWithRequest(context.Background(), "sender",
		&model.TriggerSOSRequest{Description: "ada orang pingsan di pantai"}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}

	if len(triage.call) != 1 || triage.call[0].Description != "ada orang pingsan di pantai" {
		t.Errorf("triage calls = %+v, want one call with the description", triage.call)
	}
	if alerts.lastAlert.EmergencyType != entity.EmergencyTypeMedical {
		t.Errorf("alert.EmergencyType = %q, want MEDICAL", alerts.lastAlert.EmergencyType)
	}
	// severity 0.8 -> radius start index 2 -> first probe is 5 km.
	if len(loc.radii) == 0 || loc.radii[0] != 5000 {
		t.Errorf("first GeoSearch radius = %v, want 5000", loc.radii)
	}
	if result.Triage == nil || result.Triage.Severity != 0.8 || result.Triage.EmergencyType != entity.EmergencyTypeMedical {
		t.Errorf("result.Triage = %+v, want MEDICAL/0.8", result.Triage)
	}
	if !notifier.notifyCalled {
		t.Error("Notifier.Notify() was not called")
	}
	if notifier.lastAlert.Severity != 0.8 || notifier.lastAlert.Summary != "Kemungkinan darurat medis" {
		t.Errorf("notified alert triage = %+v, want severity 0.8 and the AI summary", notifier.lastAlert)
	}
	if alerts.lastAlert.Severity == nil || *alerts.lastAlert.Severity != 0.8 ||
		alerts.lastAlert.Summary == nil || *alerts.lastAlert.Summary != "Kemungkinan darurat medis" {
		t.Errorf("persisted triage = %+v, want severity 0.8 and the AI summary", alerts.lastAlert)
	}
	detail, err := uc.GetAlert(context.Background(), "sender", result.Alert.ID)
	if err != nil {
		t.Fatalf("GetAlert() error = %v", err)
	}
	if detail.Severity != 0.8 || detail.Summary != "Kemungkinan darurat medis" {
		t.Errorf("detail triage = severity %v summary %q, want 0.8 / Kemungkinan darurat medis", detail.Severity, detail.Summary)
	}
}

func TestTriggerSOSAITriageLowSeverityKeepsOneKm(t *testing.T) {
	triage := &fakeTriageEngine{res: TriageResult{Severity: 0.3}}
	loc, alerts, _, uc := triggerTriageDeps(triage)

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender",
		&model.TriggerSOSRequest{Description: "ada kejadian kecil"}, ""); err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}

	if len(loc.radii) == 0 || loc.radii[0] != 1000 {
		t.Errorf("first GeoSearch radius = %v, want 1000", loc.radii)
	}
	// no AI type signal: alert keeps the DANGER default.
	if alerts.lastAlert.EmergencyType != entity.EmergencyTypeDanger {
		t.Errorf("alert.EmergencyType = %q, want DANGER", alerts.lastAlert.EmergencyType)
	}
}

func TestTriggerSOSExplicitRequestTypeWinsOverTriage(t *testing.T) {
	triage := &fakeTriageEngine{res: TriageResult{
		EmergencyType: entity.EmergencyTypeMedical,
		Severity:      0.8,
	}}
	_, alerts, _, uc := triggerTriageDeps(triage)

	if _, err := uc.ExecuteWithRequest(context.Background(), "sender",
		&model.TriggerSOSRequest{EmergencyType: entity.EmergencyTypeAccident, Description: "diancam"}, ""); err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}

	if alerts.lastAlert.EmergencyType != entity.EmergencyTypeAccident {
		t.Errorf("alert.EmergencyType = %q, want explicit ACCIDENT", alerts.lastAlert.EmergencyType)
	}
}

func TestTriggerSOSTriageErrorFailsOpen(t *testing.T) {
	triage := &fakeTriageEngine{err: errors.New("llm down")}
	loc, alerts, notifier, uc := triggerTriageDeps(triage)

	result, err := uc.ExecuteWithRequest(context.Background(), "sender",
		&model.TriggerSOSRequest{Description: "ada orang pingsan"}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() must not fail with triage down, got %v", err)
	}

	if alerts.lastAlert.EmergencyType != entity.EmergencyTypeDanger {
		t.Errorf("alert.EmergencyType = %q, want DANGER default", alerts.lastAlert.EmergencyType)
	}
	if len(loc.radii) == 0 || loc.radii[0] != 1000 {
		t.Errorf("first GeoSearch radius = %v, want 1000 (default)", loc.radii)
	}
	if result.Triage != nil {
		t.Errorf("result.Triage = %+v, want nil on triage failure", result.Triage)
	}
	if !notifier.notifyCalled {
		t.Error("Notifier.Notify() was not called")
	}
}

func TestTriggerSOSTriageZeroSignalKeepsDefaults(t *testing.T) {
	triage := &fakeTriageEngine{res: TriageResult{}}
	loc, alerts, _, uc := triggerTriageDeps(triage)

	result, err := uc.ExecuteWithRequest(context.Background(), "sender",
		&model.TriggerSOSRequest{Description: ""}, "")
	if err != nil {
		t.Fatalf("ExecuteWithRequest() error = %v", err)
	}

	if alerts.lastAlert.EmergencyType != entity.EmergencyTypeDanger {
		t.Errorf("alert.EmergencyType = %q, want DANGER", alerts.lastAlert.EmergencyType)
	}
	if len(loc.radii) == 0 || loc.radii[0] != 1000 {
		t.Errorf("first GeoSearch radius = %v, want 1000", loc.radii)
	}
	if result.Triage != nil {
		t.Errorf("result.Triage = %+v, want nil for zero signal", result.Triage)
	}
}
