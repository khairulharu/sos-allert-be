package usecase

import (
	"context"
	"time"
)

// minHousekeepingInterval floors the housekeeping tick so a misconfigured
// sos.housekeeping_interval_seconds cannot expire/purge in a tight loop.
const minHousekeepingInterval = time.Second

// RunHousekeeping expires stale alerts and purges old breadcrumb trails
// (#42). ExpireOldAlerts and PurgeOldBreadcrumbs had no caller, so TRIGGERED
// rows never left the active set (inflating active_sos_count and the
// active-SOS queries) and alert_breadcrumbs grew forever.
//
// It runs in the web process next to RunActiveSOSGauge/RunDispatchLoop and
// blocks until ctx is cancelled. One pass runs immediately, then every
// interval.
func (u *TriggerSOSUseCase) RunHousekeeping(ctx context.Context, interval time.Duration) {
	if interval < minHousekeepingInterval {
		interval = minHousekeepingInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		u.runHousekeepingOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runHousekeepingOnce runs one expiry + purge pass. Both steps are
// best-effort: a failing statement is logged and the other one still runs,
// and a failed pass never stops the loop.
func (u *TriggerSOSUseCase) runHousekeepingOnce(ctx context.Context) {
	if u.Deps.Alerts == nil {
		return
	}
	expired, err := u.ExpireOldAlerts(ctx)
	if err != nil {
		u.Log.WithError(err).Warn("housekeeping: expiring stale alerts failed")
	} else if expired > 0 {
		u.Log.Infof("housekeeping: expired %d stale alert(s)", expired)
		// Expiry changes the active set, so refresh the gauge now instead
		// of waiting for the next gauge tick (it is one indexed count).
		u.refreshActiveSOS(ctx)
	} else {
		u.Log.Debug("housekeeping: no stale alerts to expire")
	}

	purged, err := u.PurgeOldBreadcrumbs(ctx)
	if err != nil {
		u.Log.WithError(err).Warn("housekeeping: purging old breadcrumbs failed")
	} else if purged > 0 {
		u.Log.Infof("housekeeping: purged %d breadcrumb(s) older than 30 days", purged)
	} else {
		u.Log.Debug("housekeeping: no breadcrumbs to purge")
	}
}

// refreshActiveSOS recomputes active_sos_count from the alerts table.
func (u *TriggerSOSUseCase) refreshActiveSOS(ctx context.Context) {
	if u.Deps.Alerts == nil {
		return
	}
	n, err := u.Deps.Alerts.CountActive(ctx, u.DB)
	if err != nil {
		u.Log.WithError(err).Warn("active_sos_count refresh failed")
		return
	}
	if u.Deps.Metrics != nil {
		u.Deps.Metrics.SetActiveSOS(n)
	}
}
