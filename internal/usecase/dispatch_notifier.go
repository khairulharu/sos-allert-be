package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
)

// SOSMetrics is the subset of metrics the SOS use cases record.
// Satisfied by *metrics.Metrics; nil means metrics are disabled.
type SOSMetrics interface {
	ObserveDispatchDuration(ms float64)
	IncPushFailures()
	SetActiveSOS(n int64)
}

// TokenReader loads users by id: push device tokens for the dispatcher,
// display names for the alert detail view.
type TokenReader interface {
	GetByID(ctx context.Context, id string) (*entity.User, error)
	ListByIDs(ctx context.Context, ids []string) (map[string]*entity.User, error)
}

// ContactResolver loads SMS recipients for a sender.
type ContactResolver interface {
	DispatchContacts(ctx context.Context, userID string) ([]model.EmergencyContactResponse, error)
}

// AlertDispatcher implements Notifier with the multi-channel fan-out
// from the README: push first (FCM token/topic + APNs critical alert),
// then SMS/voice fallback to Tier-1 emergency contacts. When a channel
// is not configured it degrades to the previous log-only behaviour
// instead of failing the SOS request.
type AlertDispatcher struct {
	Log      *logrus.Logger
	FCM      *FCMNotifier
	APNs     *APNsNotifier
	Twilio   *TwilioNotifier
	Web      *WebPushNotifier
	Tokens   TokenReader
	Contacts ContactResolver
	Metrics  SOSMetrics
}

// NewAlertDispatcher builds a dispatcher; any channel may be nil.
func NewAlertDispatcher(log *logrus.Logger, fcm *FCMNotifier, apns *APNsNotifier, twilio *TwilioNotifier, web *WebPushNotifier, tokens TokenReader, contacts ContactResolver, metrics SOSMetrics) *AlertDispatcher {
	return &AlertDispatcher{Log: log, FCM: fcm, APNs: apns, Twilio: twilio, Web: web, Tokens: tokens, Contacts: contacts, Metrics: metrics}
}

func (d *AlertDispatcher) Notify(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	title, body, data := d.notificationContent(alert)
	pushAttempted, pushFailed := d.pushToRecipients(ctx, recipientIDs, alert.ID, title, body, data)
	if len(recipientIDs) == 0 {
		d.Log.Infof("[SOS] alert %s: no recipients in range", alert.ID)
	}

	// Tier-1 fallback: SMS to the sender's emergency contacts with a
	// Google Maps link, per README section 1.
	smsErr := d.notifyEmergencyContacts(ctx, alert, body)
	if pushFailed && d.Metrics != nil {
		d.Metrics.IncPushFailures()
	}
	if smsErr != nil && d.Metrics != nil {
		d.Metrics.IncPushFailures()
	}
	if pushAttempted && pushFailed && smsErr != nil {
		return fmt.Errorf("push delivery failed and SMS fallback failed: %w", smsErr)
	}
	return nil
}

// NotifyRecipients pushes an alert to the given recipients without the
// emergency-contact SMS fallback. Used when a post-trigger escalation
// widens the fan-out: the extra responders must be reached, but the
// sender's emergency contacts must not be texted twice.
func (d *AlertDispatcher) NotifyRecipients(ctx context.Context, recipientIDs []string, alert model.Alert) error {
	if len(recipientIDs) == 0 {
		return nil
	}
	title, body, data := d.notificationContent(alert)
	pushAttempted, pushFailed := d.pushToRecipients(ctx, recipientIDs, alert.ID, title, body, data)
	if pushAttempted && pushFailed && d.Metrics != nil {
		d.Metrics.IncPushFailures()
	}
	if pushAttempted && pushFailed {
		return fmt.Errorf("push delivery failed for %d recipient(s)", len(recipientIDs))
	}
	return nil
}

// notificationContent builds the push title/body/data shared by the
// initial fan-out and a post-trigger escalation.
func (d *AlertDispatcher) notificationContent(alert model.Alert) (string, string, map[string]string) {
	title := "SOS Alert"
	body := fmt.Sprintf("Emergency %s nearby. Open the app for the live location.", alert.EmergencyType)
	if alert.EmergencyType == "" {
		body = "Emergency alert nearby. Open the app for the live location."
	}
	if summary := strings.TrimSpace(alert.Summary); summary != "" {
		body = summary + " — " + body
	}
	// The sender's own claim is labelled as such, so a responder never
	// reads it as the system's assessment.
	if claim := strings.TrimSpace(alert.ClientSummary); claim != "" {
		body = "Pelapor menyebut: " + claim + " — " + body
	}
	data := map[string]string{
		"alert_id":       alert.ID,
		"sender_id":      alert.SenderId,
		"emergency_type": alert.EmergencyType,
		"latitude":       fmt.Sprintf("%f", alert.Location.Latitude),
		"longitude":      fmt.Sprintf("%f", alert.Location.Longitude),
	}
	if alert.Severity > 0 {
		data["severity"] = fmt.Sprintf("%.2f", alert.Severity)
	}
	if summary := strings.TrimSpace(alert.Summary); summary != "" {
		data["summary"] = summary
	}
	if alert.ClientSeverity != nil {
		data["client_severity"] = fmt.Sprintf("%.2f", *alert.ClientSeverity)
	}
	if claim := strings.TrimSpace(alert.ClientSummary); claim != "" {
		data["client_summary"] = claim
	}
	return title, body, data
}

// pushToRecipients fans a notification out over FCM/APNs/Web Push and
// reports whether any push channel was attempted and whether one failed.
func (d *AlertDispatcher) pushToRecipients(ctx context.Context, recipientIDs []string, alertID, title, body string, data map[string]string) (attempted bool, failed bool) {
	for _, recipientID := range recipientIDs {
		var fcmToken, apnsToken string
		if d.Tokens != nil {
			if user, err := d.Tokens.GetByID(ctx, recipientID); err == nil && user != nil {
				if user.FCMToken != nil {
					fcmToken = *user.FCMToken
				}
				if user.APNsToken != nil {
					apnsToken = *user.APNsToken
				}
			} else if err != nil {
				d.Log.WithError(err).Warnf("could not load push tokens for %s", recipientID)
			}
		}
		if d.FCM != nil {
			attempted = true
			var err error
			if fcmToken != "" {
				err = d.FCM.SendToToken(ctx, fcmToken, title, body, data)
			} else {
				err = d.FCM.SendToTopic(ctx, "user_"+recipientID, title, body, data)
			}
			if err != nil && !errors.Is(err, ErrPushNotConfigured) {
				failed = true
				d.Log.WithError(err).Errorf("FCM delivery failed for %s", recipientID)
			} else if errors.Is(err, ErrPushNotConfigured) {
				d.Log.Infof("[SOS] alert %s: FCM not configured, log-only for %s", alertID, recipientID)
			}
		}
		if d.APNs != nil && apnsToken != "" {
			attempted = true
			if err := d.APNs.Send(ctx, apnsToken, title, body, alertID); err != nil && !errors.Is(err, ErrPushNotConfigured) {
				failed = true
				d.Log.WithError(err).Errorf("APNs delivery failed for %s", recipientID)
			}
		}
		if d.Web != nil {
			attempted = true
			if err := d.Web.SendToUser(ctx, recipientID, title, body, data); err != nil {
				if errors.Is(err, ErrPushNotConfigured) {
					d.Log.Infof("[SOS] alert %s: web push not configured, log-only for %s", alertID, recipientID)
				} else {
					failed = true
					d.Log.WithError(err).Errorf("web push delivery failed for %s", recipientID)
				}
			}
		}
	}
	return attempted, failed
}

func (d *AlertDispatcher) notifyEmergencyContacts(ctx context.Context, alert model.Alert, body string) error {
	if d.Contacts == nil {
		d.Log.Infof("[SOS] alert %s: no emergency-contact resolver, skipping SMS fallback", alert.ID)
		return nil
	}
	contacts, err := d.Contacts.DispatchContacts(ctx, alert.SenderId)
	if err != nil {
		d.Log.WithError(err).Warn("could not load emergency contacts")
		return err
	}
	if len(contacts) == 0 {
		d.Log.Infof("[SOS] alert %s: no emergency contacts configured", alert.ID)
		return nil
	}
	mapsLink := fmt.Sprintf("https://www.google.com/maps/search/?api=1&query=%f,%f",
		alert.Location.Latitude, alert.Location.Longitude)
	text := fmt.Sprintf("SOS ALERT (%s): %s Location: %s", alert.EmergencyType, body, mapsLink)
	if d.Twilio == nil {
		for _, c := range contacts {
			d.Log.Infof("[SOS] alert %s: SMS not configured, would text %s (%s): %s", alert.ID, c.ContactName, c.PhoneNumber, text)
		}
		return nil
	}
	var firstErr error
	for _, c := range contacts {
		if err := d.Twilio.SendSMS(ctx, c.PhoneNumber, text); err != nil {
			if errors.Is(err, ErrPushNotConfigured) {
				d.Log.Infof("[SOS] alert %s: Twilio not configured, log-only SMS to %s", alert.ID, c.PhoneNumber)
				continue
			}
			d.Log.WithError(err).Errorf("Twilio SMS failed to %s", c.PhoneNumber)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
