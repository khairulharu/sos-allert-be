package http

import "testing"

// The stored Content-Type comes from the uploader, so only the allowlist may
// reach the browser as-is; everything else is served as an opaque binary.
func TestMediaDownloadContentType(t *testing.T) {
	cases := map[string]string{
		"audio/mp4":              "audio/mp4",
		"audio/webm;codecs=opus": "audio/webm",
		"video/mp4":              "video/mp4",
		"text/html":              "application/octet-stream",
		"image/svg+xml":          "application/octet-stream",
		"garbage":                "application/octet-stream",
	}
	for stored, want := range cases {
		if got := mediaDownloadContentType(stored); got != want {
			t.Errorf("mediaDownloadContentType(%q) = %q, want %q", stored, got, want)
		}
	}
}
