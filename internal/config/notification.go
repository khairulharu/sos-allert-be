package config

import (
	"os"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// NotificationConfig carries FCM / APNs / Twilio settings. All channels
// are optional: missing credentials leave that channel in log-only mode
// (see usecase.ErrPushNotConfigured). Nothing here is fatal.
type NotificationConfig struct {
	FCMEnabled     bool
	FCMProjectID   string
	FCMAccessToken string
	APNsEnabled    bool
	APNsKeyID      string
	APNsTeamID     string
	APNsBundleID   string
	APNsPrivateKey []byte
	APNsProduction bool
	TwilioEnabled  bool
	TwilioSID      string
	TwilioToken    string
	TwilioFrom     string
}

// NewNotificationConfig reads the `fcm`, `apns` and `twilio` sections.
// The APNs .p8 key is read from disk when apns.private_key_path is set;
// a missing/unreadable key only disables APNs, with a warning.
func NewNotificationConfig(v *viper.Viper, log *logrus.Logger) *NotificationConfig {
	cfg := &NotificationConfig{
		FCMEnabled:     v.GetBool("fcm.enabled"),
		FCMProjectID:   v.GetString("fcm.project_id"),
		FCMAccessToken: v.GetString("fcm.access_token"),
		APNsEnabled:    v.GetBool("apns.enabled"),
		APNsKeyID:      v.GetString("apns.key_id"),
		APNsTeamID:     v.GetString("apns.team_id"),
		APNsBundleID:   v.GetString("apns.bundle_id"),
		APNsProduction: v.GetBool("apns.production"),
		TwilioEnabled:  v.GetBool("twilio.enabled"),
		TwilioSID:      v.GetString("twilio.account_sid"),
		TwilioToken:    v.GetString("twilio.auth_token"),
		TwilioFrom:     v.GetString("twilio.from_number"),
	}
	if path := v.GetString("apns.private_key_path"); path != "" {
		key, err := os.ReadFile(path)
		if err != nil {
			log.WithError(err).Warn("could not read APNs private key, APNs disabled")
		} else {
			cfg.APNsPrivateKey = key
		}
	}
	return cfg
}

// SOSConfig carries SOS tuning knobs with README defaults.
type SOSConfig struct {
	GracePeriod      time.Duration
	RateLimitPerHour int
	RateLimitWindow  time.Duration
	MaxRecipients    int
	AdaptiveRadiiM   []float64
	MinRecipients    int
	ExpireAfter      time.Duration
}

// NewSOSConfig reads the `sos` section, falling back to README defaults.
func NewSOSConfig(v *viper.Viper) *SOSConfig {
	grace := v.GetInt("sos.grace_period_seconds")
	if grace <= 0 {
		grace = 10
	}
	limit := v.GetInt("sos.rate_limit_per_hour")
	if limit <= 0 {
		limit = 3
	}
	maxRecipients := v.GetInt("sos.max_recipients")
	if maxRecipients <= 0 {
		maxRecipients = 50
	}
	return &SOSConfig{
		GracePeriod:      time.Duration(grace) * time.Second,
		RateLimitPerHour: limit,
		RateLimitWindow:  time.Hour,
		MaxRecipients:    maxRecipients,
		AdaptiveRadiiM:   []float64{1000, 2000, 5000},
		MinRecipients:    3,
		ExpireAfter:      24 * time.Hour,
	}
}

// StorageConfig selects the incident-media storage backend.
type StorageConfig struct {
	Backend  string
	LocalDir string
}

// NewStorageConfig reads the `storage` section. Default: local disk.
func NewStorageConfig(v *viper.Viper) *StorageConfig {
	backend := v.GetString("storage.backend")
	if backend == "" {
		backend = "local"
	}
	dir := v.GetString("storage.local_dir")
	if dir == "" {
		dir = "./uploads"
	}
	return &StorageConfig{Backend: backend, LocalDir: dir}
}
