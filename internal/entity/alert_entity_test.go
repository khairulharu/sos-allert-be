package entity

import "testing"

func TestAlertIsActive(t *testing.T) {
	active := []string{AlertStatusTriggered, AlertStatusAcknowledged}
	for _, status := range active {
		alert := Alert{Status: status}
		if !alert.IsActive() {
			t.Errorf("status %q should be active", status)
		}
	}

	inactive := []string{AlertStatusResolved, AlertStatusCancelled, AlertStatusExpired}
	for _, status := range inactive {
		alert := Alert{Status: status}
		if alert.IsActive() {
			t.Errorf("status %q should be inactive", status)
		}
	}
}

func TestAlertCanTransitionTo(t *testing.T) {
	tests := []struct {
		from string
		to   string
		want bool
	}{
		{AlertStatusTriggered, AlertStatusAcknowledged, true},
		{AlertStatusTriggered, AlertStatusResolved, true},
		{AlertStatusTriggered, AlertStatusCancelled, true},
		{AlertStatusTriggered, AlertStatusExpired, true},
		{AlertStatusAcknowledged, AlertStatusResolved, true},
		{AlertStatusAcknowledged, AlertStatusCancelled, true},
		{AlertStatusAcknowledged, AlertStatusTriggered, false},
		{AlertStatusResolved, AlertStatusCancelled, false},
		{AlertStatusCancelled, AlertStatusResolved, false},
		{AlertStatusExpired, AlertStatusAcknowledged, false},
	}

	for _, tc := range tests {
		alert := Alert{Status: tc.from}
		if got := alert.CanTransitionTo(tc.to); got != tc.want {
			t.Errorf("%s -> %s = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
