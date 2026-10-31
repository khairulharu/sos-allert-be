package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

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

// MediaStorage saves uploaded media bytes and returns a storage path.
// Satisfied by *storage.LocalStorage.
type MediaStorage interface {
	Save(ctx context.Context, filename string, r io.Reader) (string, error)
}

const maxMediaBytes = 50 << 20 // 50 MB

// GetAlert returns one incident detail, masking the exact location for
// viewers who are neither the sender nor a responder.
func (u *TriggerSOSUseCase) GetAlert(ctx context.Context, requesterID, alertID string) (*model.AlertDetailResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	return u.buildDetail(ctx, alert, requesterID, false)
}

// ListActive returns the sender's active incidents for cold-start sync.
func (u *TriggerSOSUseCase) ListActive(ctx context.Context, senderID string) ([]*model.AlertDetailResponse, error) {
	alerts, err := u.Deps.Alerts.ListActiveBySender(ctx, u.DB, senderID, 20)
	if err != nil {
		return nil, err
	}
	out := make([]*model.AlertDetailResponse, 0, len(alerts))
	for _, alert := range alerts {
		detail, err := u.buildDetail(ctx, alert, senderID, false)
		if err != nil {
			return nil, err
		}
		out = append(out, detail)
	}
	return out, nil
}

func (u *TriggerSOSUseCase) buildDetail(ctx context.Context, alert *entity.Alert, requesterID string, includeBreadcrumbs bool) (*model.AlertDetailResponse, error) {
	detail := &model.AlertDetailResponse{
		AlertID:       alert.ID,
		Sender:        model.SenderSummary{ID: alert.SenderID},
		Status:        alert.Status,
		EmergencyType: alert.EmergencyType,
		TriggeredAt:   alert.TriggeredAt,
		ResolvedAt:    alert.ResolvedAt,
		CancelledAt:   alert.CancelledAt,
	}
	if u.Deps.Users != nil {
		if sender, err := u.Deps.Users.GetByID(ctx, alert.SenderID); err == nil && sender != nil {
			detail.Sender.Name = sender.Name
		}
	}
	responders, err := u.Deps.Alerts.CountResponders(ctx, u.DB, alert.ID)
	if err != nil {
		return nil, err
	}
	detail.RespondersEnRoute = responders

	initial := model.LocationPoint{Latitude: alert.Latitude, Longitude: alert.Longitude}
	latest := initial
	if crumb, err := u.Deps.Alerts.LatestBreadcrumb(ctx, u.DB, alert.ID); err != nil {
		return nil, err
	} else if crumb != nil {
		ts := crumb.RecordedAt
		latest = model.LocationPoint{Latitude: crumb.Latitude, Longitude: crumb.Longitude, UpdatedAt: &ts}
	}

	isSender := requesterID == alert.SenderID
	isResponder := false
	if !isSender {
		isResponder, err = u.Deps.Alerts.IsResponder(ctx, u.DB, alert.ID, requesterID)
		if err != nil {
			return nil, err
		}
	}
	if !isSender && !isResponder {
		lat, lng := model.MaskCoordinates(initial.Latitude, initial.Longitude)
		initial = model.LocationPoint{Latitude: lat, Longitude: lng}
		lat, lng = model.MaskCoordinates(latest.Latitude, latest.Longitude)
		latest = model.LocationPoint{Latitude: lat, Longitude: lng, UpdatedAt: latest.UpdatedAt}
		detail.Masked = true
	}
	detail.InitialLocation = initial
	detail.LatestLocation = latest

	if includeBreadcrumbs {
		crumbs, err := u.Deps.Alerts.ListBreadcrumbs(ctx, u.DB, alert.ID, 1000)
		if err != nil {
			return nil, err
		}
		for _, c := range crumbs {
			detail.Breadcrumbs = append(detail.Breadcrumbs, breadcrumbToResponse(c))
		}
	}
	return detail, nil
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

// Acknowledge moves a TRIGGERED incident to ACKNOWLEDGED.
func (u *TriggerSOSUseCase) Acknowledge(ctx context.Context, alertID string) (*model.AlertDetailResponse, error) {
	alert, err := u.Deps.Alerts.GetByID(ctx, u.DB, alertID)
	if err != nil {
		return nil, err
	}
	if alert.Status == entity.AlertStatusAcknowledged {
		return u.buildDetail(ctx, alert, alert.SenderID, false)
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
	return u.buildDetail(ctx, updated, updated.SenderID, false)
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
	return u.buildDetail(ctx, updated, senderID, false)
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
	if alert.SenderID == responderID {
		return nil, fiber.ErrForbidden
	}
	if !alert.IsActive() {
		return nil, model.ErrInvalidTransition
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
		return nil, err
	}
	if alert.Status == entity.AlertStatusTriggered {
		_, err := u.Deps.Alerts.Transition(ctx, u.DB, alertID,
			[]string{entity.AlertStatusTriggered},
			map[string]any{"status": entity.AlertStatusAcknowledged})
		if err != nil {
			return nil, err
		}
	}
	return &model.RespondSOSResponse{
		ResponseID:           response.ID,
		ETASeconds:           response.ETASeconds,
		VictimDistanceMeters: int(distance + 0.5),
	}, nil
}

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
	recordedAt := time.Now().UnixMilli()
	if req.Timestamp != nil {
		recordedAt = *req.Timestamp
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
			return nil, model.ErrNotAlertParticipant
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

// CreateEmergencyContact adds a Tier-1 contact for the user.
func (u *TriggerSOSUseCase) CreateEmergencyContact(ctx context.Context, req *model.CreateEmergencyContactRequest) (*model.EmergencyContactResponse, error) {
	if err := u.validate(req); err != nil {
		return nil, err
	}
	if u.Deps.EmergencyContacts == nil {
		return nil, fmt.Errorf("emergency contact store not configured")
	}
	contact := &entity.EmergencyContact{
		ID:           uuid.NewString(),
		UserID:       req.UserID,
		ContactName:  req.ContactName,
		PhoneNumber:  req.PhoneNumber,
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

// ListEmergencyContacts returns the user's Tier-1 contacts.
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
	if u.Deps.MediaStore == nil {
		return nil, model.ErrStorageNotConfigured
	}
	storedName := alertID + "_" + uuid.NewString() + "_" + filepath.Base(filename)
	path, err := u.Deps.MediaStore.Save(ctx, storedName, r)
	if err != nil {
		return nil, err
	}
	media := &entity.AlertMedia{
		ID:          uuid.NewString(),
		AlertID:     alertID,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   size,
		StoragePath: path,
		UploadedBy:  senderID,
		UploadedAt:  time.Now().UnixMilli(),
	}
	if err := u.Deps.Alerts.CreateMedia(ctx, u.DB, media); err != nil {
		return nil, err
	}
	return &model.AlertMediaResponse{
		ID: media.ID, AlertID: media.AlertID, Filename: media.Filename,
		ContentType: media.ContentType, SizeBytes: media.SizeBytes, UploadedAt: media.UploadedAt,
	}, nil
}

// ExpireOldAlerts moves TRIGGERED alerts older than ExpireAfter to EXPIRED.
func (u *TriggerSOSUseCase) ExpireOldAlerts(ctx context.Context) (int64, error) {
	before := time.Now().Add(-u.Deps.ExpireAfter).UnixMilli()
	return u.Deps.Alerts.ExpireOlderThan(ctx, u.DB, before, time.Now().UnixMilli())
}

// PurgeOldBreadcrumbs deletes trails older than 30 days for terminal alerts.
func (u *TriggerSOSUseCase) PurgeOldBreadcrumbs(ctx context.Context) (int64, error) {
	before := time.Now().AddDate(0, 0, -30).UnixMilli()
	return u.Deps.Alerts.PurgeBreadcrumbs(ctx, u.DB, before)
}

// AuthorizeTracking reports whether a user may subscribe to an
// incident's live stream (sender or responder only).
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
		return model.ErrNotAlertParticipant
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
