package usecase

import (
	"context"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
)

// AlertNotifier sends notifications to SOS recipients.
// This implementation logs only; replace with FCM when ready.
type AlertNotifier struct {
	Log *logrus.Logger
}

func NewAlertNotifier(log *logrus.Logger) *AlertNotifier {
	return &AlertNotifier{Log: log}
}

func (a *AlertNotifier) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	if len(recipientIDs) == 0 {
		a.Log.Infof("[SOS] alert %s: no recipients in range", alert.ID)
		return nil
	}
	a.Log.Infof("[SOS] alert %s from sender %s → notifying %d recipient(s): %v",
		alert.ID, alert.SenderId, len(recipientIDs), recipientIDs)
	return nil
}
