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

// IsAllowedWebPushEndpoint reports whether rawURL's host is allowlisted.
// Entries starting with "." match a suffix, for push services that hand out
// per-device subdomains. The scheme is not enforced here: a non-https URL
// on an allowlisted host is refused by that push service, and the host check
// is what stops the request from leaving for somewhere else.
func IsAllowedWebPushEndpoint(rawURL string, allowed []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
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
