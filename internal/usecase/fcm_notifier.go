package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrPushNotConfigured means a push/SMS channel has no credentials, so
// the dispatcher falls back to logging instead of failing the SOS.
var ErrPushNotConfigured = errors.New("push channel not configured")

// FCMNotifier sends Firebase Cloud Messaging v1 messages. It is
// config-gated: without a project id and a valid OAuth2 access token it
// logs and reports ErrPushNotConfigured. The access token is supplied
// via config (fcm.access_token); refreshing it is the operator's job
// (gcloud / service-account exchange) until a token-refresh flow lands.
type FCMNotifier struct {
	Enabled     bool
	ProjectID   string
	AccessToken string
	BaseURL     string
	HTTPClient  *http.Client
}

func (n *FCMNotifier) baseURL() string {
	if n.BaseURL != "" {
		return strings.TrimSuffix(n.BaseURL, "/")
	}
	return "https://fcm.googleapis.com"
}

func (n *FCMNotifier) client() *http.Client {
	if n.HTTPClient != nil {
		return n.HTTPClient
	}
	return http.DefaultClient
}

type fcmMessage struct {
	Message fcmPayload `json:"message"`
}

type fcmPayload struct {
	Topic        string            `json:"topic,omitempty"`
	Token        string            `json:"token,omitempty"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data,omitempty"`
	Android      fcmAndroid        `json:"android"`
	APNS         fcmAPNS           `json:"apns"`
}

type fcmNotification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type fcmAndroid struct {
	Priority     string `json:"priority"`
	Notification struct {
		Sound        string `json:"sound,omitempty"`
		ChannelID    string `json:"channel_id,omitempty"`
		DefaultSound bool   `json:"default_sound,omitempty"`
	} `json:"notification"`
}

type fcmAPNS struct {
	Payload struct {
		APS struct {
			Alert struct {
				Title string `json:"title"`
				Body  string `json:"body"`
			} `json:"alert"`
			Sound struct {
				Critical int     `json:"critical"`
				Name     string  `json:"name"`
				Volume   float64 `json:"volume"`
			} `json:"sound"`
			InterruptionLevel string `json:"interruption-level"`
		} `json:"aps"`
	} `json:"payload"`
}

func (n *FCMNotifier) buildPayload(topic, token, title, body string, data map[string]string) fcmMessage {
	p := fcmPayload{
		Topic:        topic,
		Token:        token,
		Notification: fcmNotification{Title: title, Body: body},
		Data:         data,
		Android:      fcmAndroid{Priority: "HIGH"},
	}
	p.Android.Notification.Sound = "siren"
	p.Android.Notification.ChannelID = "sos_alerts"
	p.Android.Notification.DefaultSound = true
	p.APNS.Payload.APS.Alert.Title = title
	p.APNS.Payload.APS.Alert.Body = body
	p.APNS.Payload.APS.Sound.Critical = 1
	p.APNS.Payload.APS.Sound.Name = "siren.wav"
	p.APNS.Payload.APS.Sound.Volume = 1.0
	p.APNS.Payload.APS.InterruptionLevel = "critical"
	return fcmMessage{Message: p}
}

func (n *FCMNotifier) send(ctx context.Context, msg fcmMessage) error {
	if !n.Enabled || n.ProjectID == "" || n.AccessToken == "" {
		return ErrPushNotConfigured
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/v1/projects/%s/messages:send", n.baseURL(), n.ProjectID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+n.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client().Do(req)
	if err != nil {
		return fmt.Errorf("fcm request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("fcm request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// SendToTopic delivers to an FCM topic (per-user topic: "user_<id>").
// Topics need no device-token storage; clients subscribe on login.
func (n *FCMNotifier) SendToTopic(ctx context.Context, topic, title, body string, data map[string]string) error {
	return n.send(ctx, n.buildPayload(topic, "", title, body, data))
}

// SendToToken delivers to one FCM registration token.
func (n *FCMNotifier) SendToToken(ctx context.Context, token, title, body string, data map[string]string) error {
	return n.send(ctx, n.buildPayload("", token, title, body, data))
}
