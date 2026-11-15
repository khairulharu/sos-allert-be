package usecase

import (
	"net/url"
	"strings"
)

// DefaultWebPushHosts lists the push services browsers hand out. Any other
// endpoint host is refused, so a stored subscription can never make the
// server POST to an attacker-chosen host (stored SSRF).
var DefaultWebPushHosts = []string{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
	"web.push.apple.com",
}

// WebPushHostsOr returns the configured allowlist, or the default one when
// nothing is configured.
func WebPushHostsOr(allowed []string) []string {
	if len(allowed) == 0 {
		return DefaultWebPushHosts
	}
	return allowed
}

// IsAllowedWebPushEndpoint reports whether rawURL is an https URL on an
// allowlisted host. Entries starting with "." match a suffix, for push
// services that hand out per-device subdomains. The scheme is enforced
// too: a push service only speaks https, so "http://fcm.googleapis.com/…"
// is refused instead of being downgraded to a plaintext POST.
func IsAllowedWebPushEndpoint(rawURL string, allowed []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return false
	}
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if strings.HasPrefix(entry, ".") {
			if strings.HasSuffix(host, entry) {
				return true
			}
			continue
		}
		if host == entry {
			return true
		}
	}
	return false
}
