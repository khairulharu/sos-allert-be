package usecase

import (
	"context"
	"golang-clean-architecture/internal/model"
)

type AlertNotifier struct {
}

func NewAllertNotifierUseCase() *AlertNotifier {
	return &AlertNotifier{}
}

func (a *AlertNotifier) Notify(ctx context.Context, recipientsIDs []string, alert model.Alert) error {
	return nil
}
