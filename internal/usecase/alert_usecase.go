package usecase

import (
	"context"
	"fmt"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/repository"
	"time"
)

type TriggerSOSUseCase struct {
	Locations    *LocationStore
	Finder       *NearbyFinder
	Alerts       *repository.AlertRepository
	Notifier     *AlertNotifier
	RadiusMeters float64
	Now          func() time.Time
	NewID        func() string
}

func NewTriggerSOSUseCase(locations *LocationStore, finder *NearbyFinder, alerts *repository.AlertRepository, notifier *AlertNotifier, radiusMeters float64, now func() time.Time, newID func() string) *TriggerSOSUseCase {
	return &TriggerSOSUseCase{
		Locations:    locations,
		Finder:       finder,
		Alerts:       alerts,
		Notifier:     notifier,
		RadiusMeters: radiusMeters,
		Now:          now,
		NewID:        newID,
	}
}

func (uc *TriggerSOSUseCase) Execute(ctx context.Context, senderID string, loc entity.UserLocation) (*model.TriggerSOSResult, error) {
	if senderID == "" {
		return nil, model.ErrInvalidSender
	}
	if loc.Latitude < -90 || loc.Latitude > 90 || loc.Longitude < -180 || loc.Longitude > 180 {
		return nil, model.ErrInvalidLocation
	}

	request := model.UpdateLocationRequest{
		UserId:    loc.UserId,
		Latitude:  loc.Latitude,
		Longitude: loc.Longitude,
	}
	// 1. Update lokasi terbaru si pengirim.
	if err := uc.Locations.Save(ctx, &request); err != nil {
		return nil, fmt.Errorf("save sender location: %w", err)
	}

	// 2. Cari user dalam radius (hasil bisa ikut memuat si pengirim).
	nearby, err := uc.Finder.FindNearby(ctx, loc, uc.RadiusMeters)
	if err != nil {
		return nil, fmt.Errorf("find nearby users: %w", err)
	}

	// 3. Buang pengirim, sisanya jadi penerima.
	recipients := make([]string, 0, len(nearby))
	for _, id := range nearby {
		if id != senderID {
			recipients = append(recipients, id)
		}
	}

	// 4. Simpan alert (riwayat/audit) sebelum kirim notifikasi.
	alert := model.Alert{
		ID:          uc.NewID(),
		SenderId:    senderID,
		Location:    model.LocationResponse(request),
		TriggeredAt: uc.Now(),
	}
	if err := uc.Alerts.Create(ctx, alert); err != nil {
		return nil, fmt.Errorf("create alert: %w", err)
	}

	result := &model.TriggerSOSResult{Alert: alert, Recipients: recipients}

	// 5. Kirim ke penerima. Kalau tidak ada yang di radius, selesai tanpa error.
	if len(recipients) == 0 {
		return result, nil
	}
	if err := uc.Notifier.Notify(ctx, recipients, alert); err != nil {
		return result, fmt.Errorf("notify recipients: %w", err)
	}

	return result, nil
}
