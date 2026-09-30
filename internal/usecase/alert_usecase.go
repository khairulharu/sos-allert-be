package usecase

import (
	"context"
	"fmt"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/repository"
	"time"
)

type TriggerSOSUseCase struct {
	locations    *LocationStore
	finder       *NearbyFinder
	alerts       *repository.AlertRepository
	notifier     *AlertNotifier
	radiusMeters float64
	now          func() time.Time
	newID        func() string
}

func NewTriggerSOSUseCase(locations *LocationStore, finder *NearbyFinder, alerts *repository.AlertRepository, notifier *AlertNotifier, radiusMeters float64, now func() time.Time, newID func() string) *TriggerSOSUseCase {
	return &TriggerSOSUseCase{
		locations:    locations,
		finder:       finder,
		alerts:       alerts,
		notifier:     notifier,
		radiusMeters: radiusMeters,
		now:          now,
		newID:        newID,
	}
}

func (uc *TriggerSOSUseCase) Execute(ctx context.Context, senderID string, loc model.Location) (*model.TriggerSOSResult, error) {
	if senderID == "" {
		return nil, model.ErrInvalidSender
	}
	if loc.Lat < -90 || loc.Lat > 90 || loc.Lng < -180 || loc.Lng > 180 {
		return nil, model.ErrInvalidLocation
	}

	// 1. Update lokasi terbaru si pengirim.
	if err := uc.locations.Save(ctx, senderID, loc); err != nil {
		return nil, fmt.Errorf("save sender location: %w", err)
	}

	// 2. Cari user dalam radius (hasil bisa ikut memuat si pengirim).
	nearby, err := uc.finder.FindNearby(ctx, loc, uc.radiusMeters)
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
		ID:          uc.newID(),
		SenderID:    senderID,
		Location:    loc,
		TriggeredAt: uc.now(),
	}
	if err := uc.alerts.Create(ctx, alert); err != nil {
		return nil, fmt.Errorf("create alert: %w", err)
	}

	result := &model.TriggerSOSResult{Alert: alert, Recipients: recipients}

	// 5. Kirim ke penerima. Kalau tidak ada yang di radius, selesai tanpa error.
	if len(recipients) == 0 {
		return result, nil
	}
	if err := uc.notifier.Notify(ctx, recipients, alert); err != nil {
		return result, fmt.Errorf("notify recipients: %w", err)
	}

	return result, nil
}
