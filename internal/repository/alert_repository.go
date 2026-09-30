package repository

import (
	"context"
	"golang-clean-architecture/internal/model"
)

type AlertRepository struct{}

func NewAllertRepository() *AlertRepository {
	return &AlertRepository{}
}

func (a *AlertRepository) Create(ctx context.Context, alert model.Alert) error {
	return nil
}
