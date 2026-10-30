package usecase

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// TwilioNotifier sends SMS via the Twilio Messages API. It is
// config-gated: without account SID, auth token and a from-number it
// reports ErrPushNotConfigured and the dispatcher falls back to logging.
type TwilioNotifier struct {
	Enabled    bool
	AccountSID string
	AuthToken  string
	FromNumber string
	BaseURL    string
	HTTPClient *http.Client
}

func (n *TwilioNotifier) baseURL() string {
	if n.BaseURL != "" {
		return strings.TrimSuffix(n.BaseURL, "/")
	}
	return "https://api.twilio.com"
}

func (n *TwilioNotifier) client() *http.Client {
	if n.HTTPClient != nil {
		return n.HTTPClient
	}
	return http.DefaultClient
}

// SendSMS sends one SMS. `to` must be in E.164 format.
func (n *TwilioNotifier) SendSMS(ctx context.Context, to, body string) error {
	if !n.Enabled || n.AccountSID == "" || n.AuthToken == "" || n.FromNumber == "" {
		return ErrPushNotConfigured
	}
	form := url.Values{}
	form.Set("To", to)
	form.Set("From", n.FromNumber)
	form.Set("Body", body)
	endpoint := fmt.Sprintf("%s/2010-04-01/Accounts/%s/Messages.json", n.baseURL(), n.AccountSID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(n.AccountSID, n.AuthToken)
	resp, err := n.client().Do(req)
	if err != nil {
		return fmt.Errorf("twilio request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("twilio request failed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}
