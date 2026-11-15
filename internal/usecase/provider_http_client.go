package usecase

import (
	"net/http"
	"time"
)

// ProviderHTTPTimeout bounds one outbound push/SMS call. Without it the
// notifiers fell back to http.DefaultClient (Timeout 0), so a provider that
// accepted the connection and then stalled held the request — or the Kafka
// consumer partition — forever (#42).
const ProviderHTTPTimeout = 3 * time.Second

// defaultProviderHTTPClient is shared by every notifier that was built
// without an injected client. It is read-only after init, so sharing it is
// safe; callers that need per-provider tuning inject their own.
var defaultProviderHTTPClient = &http.Client{Timeout: ProviderHTTPTimeout}

// providerClient returns the injected client or the bounded default. A nil
// *http.Client must never reach http.Client.Do, where it would mean "no
// timeout".
func providerClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return defaultProviderHTTPClient
}
