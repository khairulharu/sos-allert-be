package repository

import (
	"context"
	"golang-clean-architecture/internal/entity"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type AlertRepository struct {
	Repository[entity.Alert]
	Log *logrus.Logger
}

func NewAllertRepository(log *logrus.Logger) *AlertRepository {
	return &AlertRepository{Log: log}
}

func (a *AlertRepository) Create(ctx context.Context, db *gorm.DB, alert *entity.Alert) error {
	if err := db.WithContext(ctx).Create(alert).Error; err != nil {
		a.Log.WithError(err).Error("failed to create alert")
		return err
	}
	return nil
}
