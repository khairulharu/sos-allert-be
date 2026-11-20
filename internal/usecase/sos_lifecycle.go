package usecase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// EmergencyContactStore persists Tier-1 contacts.
// Satisfied by *repository.EmergencyContactRepository.
type EmergencyContactStore interface {
	Create(ctx context.Context, db *gorm.DB, contact *entity.EmergencyContact) error
	ListByUserID(ctx context.Context, db *gorm.DB, userID string) ([]*entity.EmergencyContact, error)
}

// MediaStorage saves uploaded media bytes and opens them back for
// authenticated download. Satisfied by *storage.LocalStorage.
type MediaStorage interface {
	Save(ctx context.Context, filename string, r io.Reader) (string, error)
	Open(ctx context.Context, path string) (io.ReadCloser, error)
	// Delete removes a stored file, used to undo an upload whose
	// metadata insert failed.
	Delete(ctx context.Context, path string) error
}

const (
	maxMediaBytes         = 50 << 20   // 50 MB
	maxTranscriptionBytes = 25_000_000 // Groq free tier: 25 MB (decimal)

	// alert_media.filename is VARCHAR(255); keep a margin and preserve
	// the extension so a very long client filename cannot fail the
	// insert (which used to surface as a 500).
	maxMediaFilenameRunes    = 200
	maxMediaFilenameExtRunes = 16
)

// sanitizeFilename turns an untrusted multipart filename into a value
// that is safe to store (VARCHAR(255)) and on disk: path separators and
// control characters are dropped, ".." is collapsed, and the name is
// truncated to maxMediaFilenameRunes while keeping its extension.
func sanitizeFilename(name string) (string, error) {
	name = strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	for strings.Contains(name, "..") {
		name = strings.ReplaceAll(name, "..", ".")
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "", fiber.ErrBadRequest
	}
	runes := []rune(name)
	if len(runes) <= maxMediaFilenameRunes {
		return name, nil
	}
	ext := filepath.Ext(name)
	extRunes := []rune(ext)
	if len(extRunes) > 1 && len(extRunes) <= maxMediaFilenameExtRunes {
		keep := maxMediaFilenameRunes - len(extRunes)
		return string(runes[:keep]) + ext, nil
	}
	return string(runes[:maxMediaFilenameRunes]), nil
}

// detailViewer records what the caller already is to one incident, so a
// single-alert read does not repeat IsResponder after authorization.
type detailViewer struct {
	isSender    bool
	isResponder bool
}

func (v detailViewer) participant() bool {
	return v.isSender || v.isResponder
}

// viewerFor reports what a user is to an incident and whether they may act
// on it. Recipients are recorded at trigger time, before the grace-period
// dispatch, so a notified user qualifies while the countdown is still
// running.
func (u *TriggerSOSUseCase) viewerFor(ctx context.Context, alert *entity.Alert, userID string) (detailViewer, bool, error) {
	if alert.SenderID == userID {
		return detailViewer{isSender: true}, true, nil
	}
	isResponder, err := u.Deps.Alerts.IsResponder(ctx, u.DB, alert.ID, userID)
	if err != nil {
		return detailViewer{}, false, err
	}
	if isResponder {
		return detailViewer{isResponder: true}, true, nil
	}
	recipients, err := u.Deps.Alerts.ListRecipients(ctx, u.DB, alert.ID)
	if err != nil {
		return detailViewer{}, false, err
	}
	for _, id := range recipients {
		if id == userID {
			return detailViewer{}, true, nil
		}
	}
	return detailViewer{}, false, nil
}

// detailBatchOptions tunes buildAlertDetails for one page of alerts.
type detailBatchOptions struct {
	requesterID string
	// role "sent" means every row was triggered by the requester, so the
	// responded-alert lookup can be skipped.
	role string
	// withResponders attaches the responder roster (identities, distance,
	// ETA) to the rows the viewer participates in.
	withResponders bool
	// viewersResolved marks opt.viewers as the complete, authoritative
	// answer for every alert, resolving the viewer once for a single alert.
	// It is explicit so a partial map can never silently fall back to
	// masking a responder's own incident.
	viewersResolved bool
	// viewers short-circuits per-alert viewer resolution for callers that
	// already authorized the read (GetAlert, Acknowledge, Resolve).
	viewers map[string]detailViewer
}

// buildAlertDetails maps a page of alerts to detail responses with a fixed
// number of queries (#59). Responder counts, latest breadcrumbs, the
// caller's own responses, sender names and the responder roster are each
// batched once; the independent batches run concurrently, so a page costs
// roughly two round trips instead of 4-6 per row. History uses the same
// builder with the roster turned off.
func (u *TriggerSOSUseCase) buildAlertDetails(ctx context.Context, alerts []*entity.Alert, opt detailBatchOptions) ([]*model.AlertDetailResponse, error) {
	items := make([]*model.AlertDetailResponse, 0, len(alerts))
	if len(alerts) == 0 {
		return items, nil
	}

	ids := make([]string, 0, len(alerts))
	senderIDs := make([]string, 0, len(alerts))
	seenSender := make(map[string]struct{}, len(alerts))
	for _, alert := range alerts {
		ids = append(ids, alert.ID)
		if _, ok := seenSender[alert.SenderID]; !ok {
			seenSender[alert.SenderID] = struct{}{}
			senderIDs = append(senderIDs, alert.SenderID)
		}
	}

	var (
		responderCounts map[string]int
		latestCrumbs    map[string]*entity.AlertBreadcrumb
		responded       = map[string]bool{}
		names           = map[string]string{}
		summaries       = map[string][]model.ResponderSummary{}
	)

	// The roster is only fetched for rows the caller may actually see.
	// role=sent rows all belong to the caller; a resolved-viewer call
	// already knows each viewer. Inbox rows need the responded lookup
	// first, so their roster is fetched after the first wave.
	rosterIDs := make([]string, 0, len(alerts))
	if opt.withResponders {
		switch {
		case opt.viewersResolved:
			for _, alert := range alerts {
				if opt.viewers[alert.ID].participant() {
					rosterIDs = append(rosterIDs, alert.ID)
				}
			}
		case opt.role == "sent":
			rosterIDs = ids
		}
	}

	tasks := []func(context.Context) error{
		func(ctx context.Context) (err error) {
			responderCounts, err = u.Deps.Alerts.CountRespondersByAlertIDs(ctx, u.DB, ids)
			return err
		},
		func(ctx context.Context) (err error) {
			latestCrumbs, err = u.Deps.Alerts.LatestBreadcrumbsByAlertIDs(ctx, u.DB, ids)
			return err
		},
	}
	if opt.role != "sent" && !opt.viewersResolved {
		tasks = append(tasks, func(ctx context.Context) (err error) {
			responded, err = u.Deps.Alerts.ListRespondedAlertIDs(ctx, u.DB, opt.requesterID, ids)
			return err
		})
	}
	if u.Deps.Users != nil && len(senderIDs) > 0 {
		tasks = append(tasks, func(ctx context.Context) error {
			users, err := u.Deps.Users.ListByIDs(ctx, senderIDs)
			if err != nil {
				// A missing display name must not fail the page.
				u.Log.WithError(err).Warn("could not load alert sender names")
				return nil
			}
			for id, user := range users {
				names[id] = user.Name
			}
			return nil
		})
	}
	if len(rosterIDs) > 0 {
		tasks = append(tasks, func(ctx context.Context) (err error) {
			summaries, err = u.Deps.Alerts.ListResponderSummariesByAlertIDs(ctx, u.DB, rosterIDs)
			return err
		})
	}
	if err := runConcurrently(ctx, tasks...); err != nil {
		return nil, err
	}

	viewers := make(map[string]detailViewer, len(alerts))
	for _, alert := range alerts {
		if viewer, ok := opt.viewers[alert.ID]; ok {
			viewers[alert.ID] = viewer
			continue
		}
		if opt.viewersResolved {
			return nil, fmt.Errorf("alert %s has no resolved viewer", alert.ID)
		}
		viewers[alert.ID] = detailViewer{
			isSender:    alert.SenderID == opt.requesterID,
			isResponder: responded[alert.ID],
		}
	}

	if opt.withResponders && len(rosterIDs) == 0 {
		participantIDs := make([]string, 0, len(alerts))
		for _, alert := range alerts {
			if viewers[alert.ID].participant() {
				participantIDs = append(participantIDs, alert.ID)
			}
		}
		if len(participantIDs) > 0 {
			var err error
			summaries, err = u.Deps.Alerts.ListResponderSummariesByAlertIDs(ctx, u.DB, participantIDs)
			if err != nil {
				return nil, err
			}
		}
	}

	for _, alert := range alerts {
		detail := alertDetailBase(alert)
		detail.Sender.Name = names[alert.SenderID]
		detail.RespondersEnRoute = responderCounts[alert.ID]

		initial := model.LocationPoint{Latitude: alert.Latitude, Longitude: alert.Longitude}
		latest := initial
		if crumb := latestCrumbs[alert.ID]; crumb != nil {
			ts := crumb.RecordedAt
			latest = model.LocationPoint{Latitude: crumb.Latitude, Longitude: crumb.Longitude, UpdatedAt: &ts}
		}
		viewer := viewers[alert.ID]
		// Names and distances of the responders are participant-only: other
		// viewers keep the bare count so the endpoint does not leak who is
		// answering a stranger's incident.
		if viewer.participant() {
			detail.Responders = summaries[alert.ID]
		}
		setDetailLocations(detail, initial, latest, viewer.participant())
		items = append(items, detail)
	}
	return items, nil
}

// detailForAlert builds one detail response from a viewer that was already
// resolved, so a single-incident read does not repeat IsResponder.
func (u *TriggerSOSUseCase) detailForAlert(ctx context.Context, alert *entity.Alert, requesterID string, viewer detailViewer) (*model.AlertDetailResponse, error) {
	items, err := u.buildAlertDetails(ctx, []*entity.Alert{alert}, detailBatchOptions{
		requesterID:     requesterID,
		withResponders:  true,
		viewersResolved: true,
		viewers:         map[string]detailViewer{alert.ID: viewer},
	})
	if err != nil {
		return nil, err
	}
	return items[0], nil
}

// GetAlert returns one incident detail to a participant; everybody else
// gets ErrAlertNotFound. The exact location stays masked until the viewer
// is the sender or a responder.
func (u *TriggerSOSUseCase) GetAlert(ctx context.Context, requesterID, alertID string) (*model.AlertDetailResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	// Non-participants get 404 instead of 403 so the endpoint also stops
	// confirming which alert ids exist. The payload itself stays masked
	// for a recipient until they actually respond.
	viewer, participant, err := u.viewerFor(ctx, alert, requesterID)
	if err != nil {
		return nil, err
	}
	if !participant {
		return nil, model.ErrAlertNotFound
	}
	return u.detailForAlert(ctx, alert, requesterID, viewer)
}

// ListActive returns the sender's active incidents for cold-start sync.
func (u *TriggerSOSUseCase) ListActive(ctx context.Context, senderID string) ([]*model.AlertDetailResponse, error) {
	alerts, err := u.Deps.Alerts.ListActiveBySender(ctx, u.DB, senderID, 20)
	if err != nil {
		return nil, err
	}
	return u.buildAlertDetails(ctx, alerts, detailBatchOptions{
		requesterID:    senderID,
		role:           "sent",
		withResponders: true,
	})
}

// ListInbox returns the active incidents a user was notified for
// (push recipients recorded at trigger time), newest first.
func (u *TriggerSOSUseCase) ListInbox(ctx context.Context, userID string) ([]*model.AlertDetailResponse, error) {
	alerts, err := u.Deps.Alerts.ListInbox(ctx, u.DB, userID, 20)
	if err != nil {
		return nil, err
	}
	return u.buildAlertDetails(ctx, alerts, detailBatchOptions{
		requesterID:    userID,
		role:           "received",
		withResponders: true,
	})
}

// alertDetailBase maps an entity to the detail fields shared by every
// response (sender id, status, triage, timestamps). Locations are set
// afterwards because they depend on the viewer.
func alertDetailBase(alert *entity.Alert) *model.AlertDetailResponse {
	detail := &model.AlertDetailResponse{
		AlertID:       alert.ID,
		Sender:        model.SenderSummary{ID: alert.SenderID},
		Status:        alert.Status,
		EmergencyType: alert.EmergencyType,
		Severity:      alert.Severity,
		TriggeredAt:   alert.TriggeredAt,
		ResolvedAt:    alert.ResolvedAt,
		CancelledAt:   alert.CancelledAt,
	}
	if alert.Summary != nil {
		detail.Summary = *alert.Summary
	}
	return detail
}

// setDetailLocations applies the single masking rule used by every
// detail response: exact coordinates for the sender and recorded
// responders, ~100 m (~3 decimals) for everyone else.
func setDetailLocations(detail *model.AlertDetailResponse, initial, latest model.LocationPoint, isParticipant bool) {
	if isParticipant {
		detail.InitialLocation = initial
		detail.LatestLocation = latest
		return
	}
	lat, lng := model.MaskCoordinates(initial.Latitude, initial.Longitude)
	detail.InitialLocation = model.LocationPoint{Latitude: lat, Longitude: lng}
	lat, lng = model.MaskCoordinates(latest.Latitude, latest.Longitude)
	detail.LatestLocation = model.LocationPoint{Latitude: lat, Longitude: lng, UpdatedAt: latest.UpdatedAt}
	detail.Masked = true
}

func breadcrumbToResponse(c *entity.AlertBreadcrumb) model.BreadcrumbResponse {
	return model.BreadcrumbResponse{
		Latitude:          c.Latitude,
		Longitude:         c.Longitude,
		SpeedMps:          c.SpeedMps,
		HeadingDeg:        c.HeadingDeg,
		AltitudeMeters:    c.AltitudeMeters,
		AccuracyMeters:    c.AccuracyMeters,
		BatteryPercentage: c.BatteryPercentage,
		RecordedAt:        c.RecordedAt,
	}
}

// Cancel aborts an active incident after verifying the sender's safe
// PIN. Within the grace period this also prevents the scheduled fan-out.
func (u *TriggerSOSUseCase) Cancel(ctx context.Context, senderID, alertID string, req *model.CancelSOSRequest) (*model.CancelSOSResponse, error) {
	if req == nil {
		req = &model.CancelSOSRequest{}
	}
	if err := u.validate(req); err != nil {
		return nil, err
	}
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if alert.SenderID != senderID {
		return nil, model.ErrNotAlertOwner
	}
	if err := u.verifyPIN(ctx, senderID, req.PIN); err != nil {
		return nil, err
	}
	if !alert.CanTransitionTo(entity.AlertStatusCancelled) {
		return nil, model.ErrInvalidTransition
	}
	reason := req.Reason
	if reason == "" {
		reason = "FALSE_ALARM"
	}
	now := time.Now().UnixMilli()
	changed, err := u.Deps.Alerts.Transition(ctx, u.DB, alertID,
		[]string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged},
		map[string]any{"status": entity.AlertStatusCancelled, "cancellation_reason": reason, "cancelled_at": now})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, model.ErrInvalidTransition
	}
	u.publishStatus(alertID, entity.AlertStatusCancelled, now)
	return &model.CancelSOSResponse{AlertID: alertID, Status: entity.AlertStatusCancelled, CancelledAt: now}, nil
}

// verifyPIN compares the PIN against the bcrypt hash in users.safe_pin_hash.
func (u *TriggerSOSUseCase) verifyPIN(ctx context.Context, userID, pin string) error {
	if u.Deps.Users == nil {
		return model.ErrPINNotConfigured
	}
	user, err := u.Deps.Users.GetByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || user.SafePINHash == nil || *user.SafePINHash == "" {
		return model.ErrPINNotConfigured
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.SafePINHash), []byte(pin)); err != nil {
		return model.ErrInvalidPIN
	}
	return nil
}

// Acknowledge moves a TRIGGERED incident to ACKNOWLEDGED. The response is
// built from the caller's perspective, so non-participants only see masked
// coordinates (the previous implementation always used the sender view).
func (u *TriggerSOSUseCase) Acknowledge(ctx context.Context, requesterID, alertID string) (*model.AlertDetailResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	viewer, participant, err := u.viewerFor(ctx, alert, requesterID)
	if err != nil {
		return nil, err
	}
	if !participant {
		return nil, model.ErrNotAlertParticipant
	}
	if alert.Status == entity.AlertStatusAcknowledged {
		return u.detailForAlert(ctx, alert, requesterID, viewer)
	}
	changed, err := u.Deps.Alerts.Transition(ctx, u.DB, alertID,
		[]string{entity.AlertStatusTriggered},
		map[string]any{"status": entity.AlertStatusAcknowledged})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, model.ErrInvalidTransition
	}
	updated, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	u.publishStatus(alertID, updated.Status, time.Now().UnixMilli())
	return u.detailForAlert(ctx, updated, requesterID, viewer)
}

// Resolve closes an active incident. Sender only.
func (u *TriggerSOSUseCase) Resolve(ctx context.Context, senderID, alertID string) (*model.AlertDetailResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if alert.SenderID != senderID {
		return nil, model.ErrNotAlertOwner
	}
	now := time.Now().UnixMilli()
	changed, err := u.Deps.Alerts.Transition(ctx, u.DB, alertID,
		[]string{entity.AlertStatusTriggered, entity.AlertStatusAcknowledged},
		map[string]any{"status": entity.AlertStatusResolved, "resolved_at": now})
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, model.ErrInvalidTransition
	}
	updated, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	u.publishStatus(alertID, entity.AlertStatusResolved, now)
	return u.detailForAlert(ctx, updated, senderID, detailViewer{isSender: true})
}

// Respond records a responder answer with ETA and distance, and moves
// the incident to ACKNOWLEDGED when it was still TRIGGERED.
func (u *TriggerSOSUseCase) Respond(ctx context.Context, responderID, alertID string, req *model.RespondSOSRequest) (*model.RespondSOSResponse, error) {
	if req == nil {
		req = &model.RespondSOSRequest{}
	}
	if err := u.validate(req); err != nil {
		return nil, err
	}
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	viewer, participant, err := u.viewerFor(ctx, alert, responderID)
	if err != nil {
		return nil, err
	}
	if !participant {
		return nil, model.ErrNotAlertParticipant
	}
	if alert.SenderID == responderID {
		return nil, fiber.ErrForbidden
	}
	if !alert.IsActive() {
		return nil, model.ErrInvalidTransition
	}
	// One answer per responder (#42). viewerFor already resolved whether
	// this user answered; the unique index remains the concurrency guard.
	if viewer.isResponder {
		return nil, model.ErrDuplicateResponse
	}
	victimLat, victimLng := alert.Latitude, alert.Longitude
	if crumb, err := u.Deps.Alerts.LatestBreadcrumb(ctx, u.DB, alertID); err != nil {
		return nil, err
	} else if crumb != nil {
		victimLat, victimLng = crumb.Latitude, crumb.Longitude
	}
	distance := CalculateDistanceMeters(victimLat, victimLng, *req.ResponderLatitude, *req.ResponderLongitude)
	response := &entity.AlertResponse{
		ID:                 uuid.NewString(),
		AlertID:            alertID,
		ResponderID:        responderID,
		ResponderLatitude:  *req.ResponderLatitude,
		ResponderLongitude: *req.ResponderLongitude,
		DistanceMeters:     distance,
		ETASeconds:         EstimateETASeconds(distance),
		RespondedAt:        time.Now().UnixMilli(),
	}
	if err := u.Deps.Alerts.CreateResponse(ctx, u.DB, response); err != nil {
		// A responder answering twice is a client conflict, not a server
		// error: the repository collapses the duplicate insert and reports
		// model.ErrDuplicateResponse (#42).
		return nil, err
	}
	if alert.Status == entity.AlertStatusTriggered {
		changed, err := u.Deps.Alerts.Transition(ctx, u.DB, alertID,
			[]string{entity.AlertStatusTriggered},
			map[string]any{"status": entity.AlertStatusAcknowledged})
		if err != nil {
			return nil, err
		}
		if changed {
			u.publishStatus(alertID, entity.AlertStatusAcknowledged, response.RespondedAt)
		}
	}
	u.publishResponderAdded(ctx, alertID, response)
	return &model.RespondSOSResponse{
		ResponseID:           response.ID,
		ETASeconds:           response.ETASeconds,
		VictimDistanceMeters: int(distance + 0.5),
	}, nil
}

// Breadcrumb timestamps are client-supplied and ordering-sensitive:
// LatestBreadcrumb sorts recorded_at DESC, so a far-future timestamp
// would pin latest_location to that point forever.
const (
	// breadcrumbFutureSkewMs is the clock skew tolerated before a
	// client timestamp is clamped to now.
	breadcrumbFutureSkewMs = 60 * 1000
	// breadcrumbMaxAgeMs is how old a client timestamp may be.
	breadcrumbMaxAgeMs = 24 * 60 * 60 * 1000
)

// AddBreadcrumb stores one live-tracking point, refreshes Redis GEO,
// and broadcasts it to WebSocket subscribers of the incident.
func (u *TriggerSOSUseCase) AddBreadcrumb(ctx context.Context, senderID, alertID string, req *model.BreadcrumbRequest) (*model.BreadcrumbResponse, error) {
	if req == nil {
		req = &model.BreadcrumbRequest{}
	}
	if err := u.validate(req); err != nil {
		return nil, err
	}
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if alert.SenderID != senderID {
		return nil, model.ErrNotAlertOwner
	}
	if !alert.IsActive() {
		return nil, model.ErrInvalidTransition
	}
	now := time.Now().UnixMilli()
	recordedAt := now
	if req.Timestamp != nil {
		ts := *req.Timestamp
		switch {
		case ts <= 0 || ts < now-breadcrumbMaxAgeMs:
			// Absurdly old (or negative) timestamps cannot belong to
			// this incident.
			return nil, fiber.ErrBadRequest
		case ts > now+breadcrumbFutureSkewMs:
			// A future point would sort above every later breadcrumb
			// and freeze latest_location; clamp it to now.
			recordedAt = now
		default:
			recordedAt = ts
		}
	}
	crumb := &entity.AlertBreadcrumb{
		AlertID:           alertID,
		Latitude:          *req.Latitude,
		Longitude:         *req.Longitude,
		SpeedMps:          req.SpeedMps,
		HeadingDeg:        req.HeadingDeg,
		AltitudeMeters:    req.AltitudeMeters,
		AccuracyMeters:    req.AccuracyMeters,
		BatteryPercentage: req.BatteryPercentage,
		RecordedAt:        recordedAt,
	}
	if err := u.Deps.Alerts.CreateBreadcrumb(ctx, u.DB, crumb); err != nil {
		return nil, err
	}
	if u.Deps.Location != nil {
		_ = u.Deps.Location.Save(ctx, &entity.LocationCache{
			UserId: senderID, Longitude: *req.Longitude, Latitude: *req.Latitude,
		})
	}
	if u.Deps.Hub != nil {
		msg := model.LocationUpdateMessage{
			Type: "LOCATION_UPDATE", AlertID: alertID,
			Latitude: *req.Latitude, Longitude: *req.Longitude,
			SpeedMps: req.SpeedMps, HeadingDeg: req.HeadingDeg,
		}
		if data, err := json.Marshal(msg); err == nil {
			u.Deps.Hub.Publish(alertID, data)
		}
	}
	return &model.BreadcrumbResponse{
		Latitude: *req.Latitude, Longitude: *req.Longitude,
		SpeedMps: req.SpeedMps, HeadingDeg: req.HeadingDeg,
		AltitudeMeters: req.AltitudeMeters, AccuracyMeters: req.AccuracyMeters,
		BatteryPercentage: req.BatteryPercentage, RecordedAt: recordedAt,
	}, nil
}

// ListBreadcrumbs returns the stored trail for a participant.
func (u *TriggerSOSUseCase) ListBreadcrumbs(ctx context.Context, requesterID, alertID string) ([]model.BreadcrumbResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if requesterID != alert.SenderID {
		isResponder, err := u.Deps.Alerts.IsResponder(ctx, u.DB, alertID, requesterID)
		if err != nil {
			return nil, err
		}
		if !isResponder {
			return nil, model.ErrAlertNotFound
		}
	}
	crumbs, err := u.Deps.Alerts.ListBreadcrumbs(ctx, u.DB, alertID, 1000)
	if err != nil {
		return nil, err
	}
	out := make([]model.BreadcrumbResponse, 0, len(crumbs))
	for _, c := range crumbs {
		out = append(out, breadcrumbToResponse(c))
	}
	return out, nil
}

// maxEmergencyContactsPerUser caps the Tier-1 list. Every contact is an
// SMS/voice target on each alert, so an unbounded list is a toll-fraud
// amplifier.
const maxEmergencyContactsPerUser = 10

// e164Phone matches E.164: a "+" followed by 8-15 digits whose first digit
// is not zero. Short premium codes ("1234", "abc", "+123") are rejected
// because they either are not dialable international numbers or route to a
// billable premium service.
var e164Phone = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// normalizePhoneNumber strips the separators people type in a phone field
// so "+62 812-3456-7890" is accepted and stored in canonical E.164 form.
func normalizePhoneNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case ' ', '-', '(', ')', '.':
			continue
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CreateEmergencyContact adds a Tier-1 contact for the user. The number
// must be E.164, the list is capped, and the same number may only be
// stored once per user.
func (u *TriggerSOSUseCase) CreateEmergencyContact(ctx context.Context, req *model.CreateEmergencyContactRequest) (*model.EmergencyContactResponse, error) {
	if err := u.validate(req); err != nil {
		return nil, err
	}
	if u.Deps.EmergencyContacts == nil {
		return nil, fmt.Errorf("emergency contact store not configured")
	}
	phone := normalizePhoneNumber(req.PhoneNumber)
	if !e164Phone.MatchString(phone) {
		return nil, model.ErrInvalidPhoneNumber
	}
	existing, err := u.Deps.EmergencyContacts.ListByUserID(ctx, u.DB, req.UserID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= maxEmergencyContactsPerUser {
		return nil, model.ErrTooManyEmergencyContacts
	}
	for _, c := range existing {
		// Legacy rows are compared in normalized form too, so a stored
		// "+62 812..." still blocks a duplicate of the same number.
		if normalizePhoneNumber(c.PhoneNumber) == phone {
			return nil, model.ErrDuplicateEmergencyContact
		}
	}
	contact := &entity.EmergencyContact{
		ID:           uuid.NewString(),
		UserID:       req.UserID,
		ContactName:  req.ContactName,
		PhoneNumber:  phone,
		Relationship: req.Relationship,
		IsVerified:   false,
		CreatedAt:    time.Now().UnixMilli(),
	}
	if err := u.Deps.EmergencyContacts.Create(ctx, u.DB, contact); err != nil {
		return nil, err
	}
	return &model.EmergencyContactResponse{
		ID: contact.ID, ContactName: contact.ContactName, PhoneNumber: contact.PhoneNumber,
		Relationship: contact.Relationship, IsVerified: contact.IsVerified, CreatedAt: contact.CreatedAt,
	}, nil
}

// ListEmergencyContacts returns the user's Tier-1 contacts. Rows stored
// before the E.164 rule stay readable: the list never filters or rewrites
// them.
func (u *TriggerSOSUseCase) ListEmergencyContacts(ctx context.Context, userID string) ([]model.EmergencyContactResponse, error) {
	if u.Deps.EmergencyContacts == nil {
		return nil, fmt.Errorf("emergency contact store not configured")
	}
	contacts, err := u.Deps.EmergencyContacts.ListByUserID(ctx, u.DB, userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.EmergencyContactResponse, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, model.EmergencyContactResponse{
			ID: c.ID, ContactName: c.ContactName, PhoneNumber: c.PhoneNumber,
			Relationship: c.Relationship, IsVerified: c.IsVerified, CreatedAt: c.CreatedAt,
		})
	}
	return out, nil
}

// UploadMedia stores an incident audio/video capture. Sender only.
// Audio up to the transcription limit is sent to the configured STT
// provider, then the transcript is fed to the existing triage engine.
// STT/triage failures are fail-open: the evidence is still stored.
func (u *TriggerSOSUseCase) UploadMedia(ctx context.Context, senderID, alertID, filename, contentType string, size int64, r io.Reader) (*model.AlertMediaResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if alert.SenderID != senderID {
		return nil, model.ErrNotAlertOwner
	}
	if !alert.IsActive() {
		return nil, model.ErrInvalidTransition
	}
	if !strings.HasPrefix(contentType, "audio/") && !strings.HasPrefix(contentType, "video/") {
		return nil, fiber.ErrBadRequest
	}
	if size > maxMediaBytes {
		return nil, fiber.ErrBadRequest
	}
	filename, err = sanitizeFilename(filename)
	if err != nil {
		return nil, err
	}
	if u.Deps.MediaStore == nil {
		return nil, model.ErrStorageNotConfigured
	}

	// The severity before this clip: used to detect a post-trigger
	// escalation that must widen the responder fan-out.
	previousSeverity := 0.0
	if alert.Severity != nil {
		previousSeverity = *alert.Severity
	}

	isAudio := strings.HasPrefix(contentType, "audio/")
	transcribe := isAudio && u.Deps.Transcriber != nil && size <= maxTranscriptionBytes

	media := &entity.AlertMedia{
		ID:                  uuid.NewString(),
		AlertID:             alertID,
		Filename:            filename,
		ContentType:         contentType,
		SizeBytes:           size,
		UploadedBy:          senderID,
		UploadedAt:          time.Now().UnixMilli(),
		TranscriptionStatus: "disabled",
	}

	storedName := alertID + "_" + uuid.NewString() + "_" + filepath.Base(filename)
	var audio []byte
	if transcribe {
		audio, err = io.ReadAll(io.LimitReader(r, maxTranscriptionBytes+1))
		if err != nil {
			return nil, fiber.ErrInternalServerError
		}
		if int64(len(audio)) > maxTranscriptionBytes {
			return nil, fiber.ErrBadRequest
		}
		media.SizeBytes = int64(len(audio))
		path, saveErr := u.Deps.MediaStore.Save(ctx, storedName, bytes.NewReader(audio))
		if saveErr != nil {
			return nil, saveErr
		}
		media.StoragePath = path
	} else {
		if isAudio && size > maxTranscriptionBytes {
			media.TranscriptionStatus = "too_large"
		}
		path, saveErr := u.Deps.MediaStore.Save(ctx, storedName, r)
		if saveErr != nil {
			return nil, saveErr
		}
		media.StoragePath = path
	}

	var triageInfo *model.TriageInfo
	if transcribe {
		transcript, transcribeErr := u.Deps.Transcriber.Transcribe(ctx, filename, contentType, bytes.NewReader(audio))
		switch {
		case errors.Is(transcribeErr, ErrNoSpeechDetected):
			// Silence/room noise: the provider answered but heard no
			// speech, so there is nothing to store and nothing to
			// classify (#54).
			media.TranscriptionStatus = "empty"
			now := time.Now().UnixMilli()
			media.TranscribedAt = &now
		case transcribeErr != nil:
			media.TranscriptionStatus = "failed"
			u.Log.WithError(transcribeErr).Warn("audio transcription failed, keeping media without transcript")
		case strings.TrimSpace(transcript) == "":
			media.TranscriptionStatus = "empty"
			now := time.Now().UnixMilli()
			media.TranscribedAt = &now
		default:
			now := time.Now().UnixMilli()
			media.TranscribedAt = &now
			media.Transcript = transcript
			media.TranscriptionStatus = "completed"
			triageInfo = u.triageTranscript(ctx, transcript)
		}
	}

	db := u.DB
	var tx *gorm.DB
	if db != nil && triageInfo != nil {
		tx = db.WithContext(ctx).Begin()
		defer tx.Rollback()
		db = tx
	}
	// The bytes are already on disk; if the metadata write fails (or the
	// transaction rolls back) the row never exists, so remove the file
	// instead of leaving an orphan behind.
	discardStored := func() {
		if media.StoragePath == "" {
			return
		}
		if err := u.Deps.MediaStore.Delete(ctx, media.StoragePath); err != nil {
			u.Log.WithError(err).Warn("could not remove stored media after a failed insert")
		}
	}
	if err := u.Deps.Alerts.CreateMedia(ctx, db, media); err != nil {
		discardStored()
		return nil, err
	}
	if triageInfo != nil {
		if err := u.Deps.Alerts.UpdateTriage(ctx, db, alertID, triageInfo.EmergencyType, &triageInfo.Severity, triageInfo.Summary); err != nil {
			discardStored()
			return nil, err
		}
	}
	if tx != nil {
		if err := tx.Commit().Error; err != nil {
			discardStored()
			return nil, err
		}
	}

	if triageInfo != nil {
		u.publishTriage(alertID, *triageInfo)
	}

	// Audio that raises the severity widens the fan-out: responders
	// beyond the radius the description selected are notified here,
	// still respecting the trigger's grace period.
	recipientsAdded := 0
	if triageInfo != nil {
		added, err := u.escalateFanout(ctx, alert, *triageInfo, previousSeverity)
		if err != nil {
			u.Log.WithError(err).Warn("post-trigger escalation fan-out failed")
		}
		recipientsAdded = added
	}

	response := mediaToResponse(media)
	response.Triage = triageInfo
	if recipientsAdded > 0 {
		response.Escalated = true
		response.RecipientsAdded = recipientsAdded
	}
	return response, nil
}

// triageTranscript classifies the speech-to-text result. A zero result
// (no emergency type) leaves the alert's existing classification intact,
// matching the AITriage contract where EmergencyType == "" means "not an
// answer" rather than a classification with an empty label (#55).
func (u *TriggerSOSUseCase) triageTranscript(ctx context.Context, transcript string) *model.TriageInfo {
	if u.Deps.Triage == nil {
		return nil
	}
	result, err := u.Deps.Triage.Triage(ctx, TriageInput{Description: transcript})
	if err != nil {
		u.Log.WithError(err).Warn("triage from audio transcript failed")
		return nil
	}
	if result.EmergencyType == "" {
		return nil
	}
	return &model.TriageInfo{
		EmergencyType: result.EmergencyType,
		Severity:      clampSeverity(result.Severity),
		Summary:       result.Summary,
	}
}

// ClassifyAudio transcribes a short clip and classifies it without
// persisting anything (POST /api/sos/classify). It lets a client obtain
// the audio-derived classification before POST /api/sos, so the first
// fan-out already uses it; audio that arrives afterwards widens the
// fan-out through UploadMedia instead.
func (u *TriggerSOSUseCase) ClassifyAudio(ctx context.Context, senderID, filename, contentType string, size int64, r io.Reader) (*model.ClassifyResponse, error) {
	if !strings.HasPrefix(contentType, "audio/") {
		return nil, fiber.ErrBadRequest
	}
	if size > maxTranscriptionBytes {
		return nil, fiber.ErrBadRequest
	}
	if u.Deps.Transcriber == nil {
		return nil, model.ErrTranscriptionNotConfigured
	}
	audio, err := io.ReadAll(io.LimitReader(r, maxTranscriptionBytes+1))
	if err != nil {
		return nil, fiber.ErrInternalServerError
	}
	if int64(len(audio)) > maxTranscriptionBytes {
		return nil, fiber.ErrBadRequest
	}

	response := &model.ClassifyResponse{TranscriptionStatus: "completed"}
	transcript, err := u.Deps.Transcriber.Transcribe(ctx, filename, contentType, bytes.NewReader(audio))
	if errors.Is(err, ErrNoSpeechDetected) {
		// A silent clip is not a provider failure (#54): answer with the
		// documented "empty" status so the client keeps the description
		// classification instead of showing an audio-service error.
		response.TranscriptionStatus = "empty"
		return response, nil
	}
	if err != nil {
		u.Log.WithError(err).Warn("audio classification transcription failed")
		return nil, model.ErrTranscriptionFailed
	}
	if strings.TrimSpace(transcript) == "" {
		response.TranscriptionStatus = "empty"
		return response, nil
	}
	response.Transcript = transcript
	if info := u.triageTranscript(ctx, transcript); info != nil {
		info.Severity = clampSeverity(info.Severity)
		response.EmergencyType = info.EmergencyType
		response.Severity = info.Severity
		response.Summary = info.Summary
		// Hand the server-computed triage to the next trigger of this
		// user, so POST /api/sos need not trust a client-supplied score.
		if u.Deps.Classify != nil {
			if err := u.Deps.Classify.Save(ctx, senderID, *info); err != nil {
				u.Log.WithError(err).Warn("could not store the pending audio triage")
			}
		}
	}
	return response, nil
}

// publishTriage notifies tracking subscribers that post-trigger audio
// changed the incident classification.
func (u *TriggerSOSUseCase) publishTriage(alertID string, info model.TriageInfo) {
	if u.Deps.Hub == nil {
		return
	}
	msg := model.AlertTriageMessage{
		Type:          "ALERT_TRIAGE",
		AlertID:       alertID,
		EmergencyType: info.EmergencyType,
		Severity:      info.Severity,
		Summary:       info.Summary,
		UpdatedAt:     time.Now().UnixMilli(),
	}
	if data, err := json.Marshal(msg); err == nil {
		u.Deps.Hub.Publish(alertID, data)
	}
}

// ListAlertMedia returns an incident's uploads. Sender and responders
// only, because the recordings are incident evidence.
func (u *TriggerSOSUseCase) ListAlertMedia(ctx context.Context, requesterID, alertID string) ([]*model.AlertMediaResponse, error) {
	if err := u.AuthorizeTracking(ctx, requesterID, alertID); err != nil {
		return nil, err
	}
	media, err := u.Deps.Alerts.ListMedia(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	out := make([]*model.AlertMediaResponse, 0, len(media))
	for _, m := range media {
		out = append(out, mediaToResponse(m))
	}
	return out, nil
}

// OpenAlertMedia returns media metadata plus a reader for playback.
// Sender and responders only; the caller closes the reader.
func (u *TriggerSOSUseCase) OpenAlertMedia(ctx context.Context, requesterID, alertID, mediaID string) (*entity.AlertMedia, io.ReadCloser, error) {
	if err := u.AuthorizeTracking(ctx, requesterID, alertID); err != nil {
		return nil, nil, err
	}
	media, err := u.Deps.Alerts.GetMedia(ctx, u.DB, alertID, mediaID)
	if err != nil {
		return nil, nil, err
	}
	if u.Deps.MediaStore == nil {
		return nil, nil, model.ErrStorageNotConfigured
	}
	reader, err := u.Deps.MediaStore.Open(ctx, media.StoragePath)
	if err != nil {
		return nil, nil, err
	}
	return media, reader, nil
}

func mediaToResponse(m *entity.AlertMedia) *model.AlertMediaResponse {
	status := m.TranscriptionStatus
	if status == "" {
		status = "disabled"
	}
	return &model.AlertMediaResponse{
		ID: m.ID, AlertID: m.AlertID, Filename: m.Filename,
		ContentType: m.ContentType, SizeBytes: m.SizeBytes, UploadedAt: m.UploadedAt,
		URL:                 "/api/sos/" + url.PathEscape(m.AlertID) + "/media/" + url.PathEscape(m.ID),
		Transcript:          m.Transcript,
		TranscriptionStatus: status,
		TranscribedAt:       m.TranscribedAt,
	}
}

// publishResponderAdded tells tracking subscribers that someone is on
// the way, so an open alert view can append them without re-polling.
func (u *TriggerSOSUseCase) publishResponderAdded(ctx context.Context, alertID string, response *entity.AlertResponse) {
	if u.Deps.Hub == nil {
		return
	}
	name := ""
	if u.Deps.Users != nil {
		if user, err := u.Deps.Users.GetByID(ctx, response.ResponderID); err == nil && user != nil {
			name = user.Name
		}
	}
	msg := model.ResponderAddedMessage{
		Type:           "RESPONDER_ADDED",
		AlertID:        alertID,
		ResponderID:    response.ResponderID,
		Name:           name,
		DistanceMeters: int(response.DistanceMeters + 0.5),
		ETASeconds:     response.ETASeconds,
		RespondedAt:    response.RespondedAt,
	}
	if data, err := json.Marshal(msg); err == nil {
		u.Deps.Hub.Publish(alertID, data)
	}
}

// publishStatus broadcasts an ALERT_STATUS event to tracking subscribers
// so responders learn about cancel/acknowledge/resolve without polling.
func (u *TriggerSOSUseCase) publishStatus(alertID, status string, updatedAt int64) {
	if u.Deps.Hub == nil {
		return
	}
	msg := model.AlertStatusMessage{Type: "ALERT_STATUS", AlertID: alertID, Status: status, UpdatedAt: updatedAt}
	if data, err := json.Marshal(msg); err == nil {
		u.Deps.Hub.Publish(alertID, data)
	}
}

// RunActiveSOSGauge refreshes the active_sos_count gauge from the alerts
// table so it cannot drift when a status transition is missed. Blocks
// until ctx is cancelled.
func (u *TriggerSOSUseCase) RunActiveSOSGauge(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		u.refreshActiveSOS(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ExpireOldAlerts moves TRIGGERED alerts older than ExpireAfter to EXPIRED.
// Called by RunHousekeeping, which is the only caller.
func (u *TriggerSOSUseCase) ExpireOldAlerts(ctx context.Context) (int64, error) {
	before := time.Now().Add(-u.Deps.ExpireAfter).UnixMilli()
	return u.Deps.Alerts.ExpireOlderThan(ctx, u.DB, before, time.Now().UnixMilli())
}

// PurgeOldBreadcrumbs deletes trails older than 30 days for terminal alerts.
// Called by RunHousekeeping.
func (u *TriggerSOSUseCase) PurgeOldBreadcrumbs(ctx context.Context) (int64, error) {
	before := time.Now().AddDate(0, 0, -30).UnixMilli()
	return u.Deps.Alerts.PurgeBreadcrumbs(ctx, u.DB, before)
}

// AuthorizeTracking reports whether a user may subscribe to an
// incident's live stream (sender or responder only). Everyone else gets
// ErrAlertNotFound, exactly like an unknown id: the tracking and media
// read paths must not confirm which alert ids exist.
func (u *TriggerSOSUseCase) AuthorizeTracking(ctx context.Context, userID, alertID string) error {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return err
	}
	if alert.SenderID == userID {
		return nil
	}
	isResponder, err := u.Deps.Alerts.IsResponder(ctx, u.DB, alertID, userID)
	if err != nil {
		return err
	}
	if !isResponder {
		return model.ErrAlertNotFound
	}
	return nil
}

func (u *TriggerSOSUseCase) validate(req any) error {
	if u.Deps.Validate == nil {
		return nil
	}
	if err := u.Deps.Validate.Struct(req); err != nil {
		u.Log.Warnf("Invalid request: %+v", err)
		return fiber.ErrBadRequest
	}
	return nil
}
