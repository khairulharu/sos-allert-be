package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang-clean-architecture/internal/entity"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/sirupsen/logrus"
)

// PushSubscriptionStore reads and cleans up browser push subscriptions.
// Satisfied by *repository.PushSubscriptionRepository.
type PushSubscriptionStore interface {
	Upsert(ctx context.Context, sub *entity.PushSubscription) error
	ListByUserID(ctx context.Context, userID string) ([]*entity.PushSubscription, error)
	DeleteByUserAndEndpoint(ctx context.Context, userID, endpoint string) error
	DeleteByEndpoint(ctx context.Context, endpoint string) error
}

// WebPushNotifier delivers an SOS notification to every browser
// subscription a recipient has registered (VAPID, RFC 8291). It is
// config-gated: without a VAPID key pair or without a subscription
// store it reports ErrPushNotConfigured and the dispatcher logs instead.
type WebPushNotifier struct {
	Enabled         bool
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	Subscriber      string
	TTLSeconds      int
	Subs            PushSubscriptionStore
	Log             *logrus.Logger
	HTTPClient      *http.Client
	// AllowedHosts re-checks stored endpoints at send time, so rows written
	// before the subscribe-time check cannot be used as an outbound target.
	// Empty falls back to DefaultWebPushHosts.
	AllowedHosts []string
}

// webPushPayload is what service worker `push` handlers receive.
type webPushPayload struct {
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data"`
}

func (n *WebPushNotifier) client() *http.Client {
	if n.HTTPClient != nil {
		return n.HTTPClient
	}
	return http.DefaultClient
}

// buildPayload renders the JSON body sent to the push service. `data`
// carries the same keys as the FCM payload plus `url` (the SPA root;
// the app surfaces the incident through its existing inbox poll).
func (n *WebPushNotifier) buildPayload(title, body string, data map[string]string) ([]byte, error) {
	out := map[string]string{"url": "/"}
	for k, v := range data {
		out[k] = v
	}
	return json.Marshal(webPushPayload{Title: title, Body: body, Data: out})
}

// SendToUser pushes to all of a user's subscriptions. A subscription
// the push service reports as gone (404/410) is deleted; other failures
// are returned as the first error so the dispatcher can count them.
// ErrPushNotConfigured is returned when the channel is not usable.
func (n *WebPushNotifier) SendToUser(ctx context.Context, userID, title, body string, data map[string]string) error {
	if !n.Enabled || n.VAPIDPublicKey == "" || n.VAPIDPrivateKey == "" || n.Subs == nil {
		return ErrPushNotConfigured
	}
	subs, err := n.Subs.ListByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("list push subscriptions for %s: %w", userID, err)
	}
	if len(subs) == 0 {
		n.Log.Infof("[SOS] user %s has no browser push subscription", userID)
		return nil
	}
	payload, err := n.buildPayload(title, body, data)
	if err != nil {
		return err
	}
	options := &webpush.Options{
		Subscriber:      n.Subscriber,
		VAPIDPublicKey:  n.VAPIDPublicKey,
		VAPIDPrivateKey: n.VAPIDPrivateKey,
		TTL:             n.TTLSeconds,
		Urgency:         webpush.UrgencyHigh,
		HTTPClient:      n.client(),
	}
	var firstErr error
	for _, sub := range subs {
		if !IsAllowedWebPushEndpoint(sub.Endpoint, WebPushHostsOr(n.AllowedHosts)) {
			if n.Log != nil {
				n.Log.Warnf("dropping push subscription with a non-allowlisted endpoint for user %s", userID)
			}
			if err := n.Subs.DeleteByEndpoint(ctx, sub.Endpoint); err != nil && n.Log != nil {
				n.Log.WithError(err).Warn("could not delete non-allowlisted push subscription")
			}
			continue
		}
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{Auth: sub.Auth, P256dh: sub.P256DH},
		}, options)
		if err != nil {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
			if firstErr == nil {
				firstErr = err
			}
			n.Log.WithError(err).Warnf("web push send failed for user %s", userID)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
			n.Log.Infof("web push endpoint gone for user %s, deleting subscription", userID)
			if err := n.Subs.DeleteByEndpoint(ctx, sub.Endpoint); err != nil && n.Log != nil {
				n.Log.WithError(err).Warn("could not delete stale push subscription")
			}
		case resp.StatusCode >= 400:
			if firstErr == nil {
				firstErr = fmt.Errorf("push service returned %d", resp.StatusCode)
			}
		}
	}
	return firstErr
}
