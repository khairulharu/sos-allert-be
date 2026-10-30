package usecase

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APNsNotifier sends Apple Push Notification service alerts, including
// Critical Alerts that bypass Silent/DND (interruption-level: critical).
// It is config-gated: without key id, team id, bundle id and a .p8
// private key it reports ErrPushNotConfigured.
type APNsNotifier struct {
	Enabled       bool
	KeyID         string
	TeamID        string
	BundleID      string
	PrivateKeyPEM []byte
	Production    bool
	BaseURL       string
	HTTPClient    *http.Client
	Now           func() time.Time
}

func (n *APNsNotifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n *APNsNotifier) baseURL() string {
	if n.BaseURL != "" {
		return strings.TrimSuffix(n.BaseURL, "/")
	}
	if n.Production {
		return "https://api.push.apple.com"
	}
	return "https://api.sandbox.push.apple.com"
}

func (n *APNsNotifier) client() *http.Client {
	if n.HTTPClient != nil {
		return n.HTTPClient
	}
	return http.DefaultClient
}

func (n *APNsNotifier) parseKey() (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(n.PrivateKeyPEM)
	if block == nil {
		return nil, errors.New("apns private key is not PEM encoded")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("apns parse private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("apns private key is not an ECDSA key")
	}
	return key, nil
}

// BuildJWT creates the ES256 provider token. Exported for tests.
func (n *APNsNotifier) BuildJWT() (string, error) {
	key, err := n.parseKey()
	if err != nil {
		return "", err
	}
	now := n.now()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": n.KeyID, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{"iss": n.TeamID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	hash := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)
	return signingInput + "." + enc.EncodeToString(sig), nil
}

type apnsPayload struct {
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
		Badge             int    `json:"badge"`
		InterruptionLevel string `json:"interruption-level"`
		Category          string `json:"category"`
	} `json:"aps"`
	AlertID string `json:"alert_id"`
}

// Send delivers a critical alert to one device token.
func (n *APNsNotifier) Send(ctx context.Context, deviceToken, title, body, alertID string) error {
	if !n.Enabled || n.KeyID == "" || n.TeamID == "" || n.BundleID == "" || len(n.PrivateKeyPEM) == 0 {
		return ErrPushNotConfigured
	}
	if deviceToken == "" {
		return ErrPushNotConfigured
	}
	token, err := n.BuildJWT()
	if err != nil {
		return fmt.Errorf("apns jwt: %w", err)
	}
	payload := apnsPayload{AlertID: alertID}
	payload.APS.Alert.Title = title
	payload.APS.Alert.Body = body
	payload.APS.Sound.Critical = 1
	payload.APS.Sound.Name = "siren.wav"
	payload.APS.Sound.Volume = 1.0
	payload.APS.Badge = 1
	payload.APS.InterruptionLevel = "critical"
	payload.APS.Category = "SOS_ALERT"
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/3/device/%s", n.baseURL(), deviceToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("apns-topic", n.BundleID)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", "0")
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client().Do(req)
	if err != nil {
		return fmt.Errorf("apns request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("apns request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
