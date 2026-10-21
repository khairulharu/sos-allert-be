package usecase

import (
	"context"
	"golang-clean-architecture/internal/entity"
)

type NearbyFinder struct {
}

func NewNearbyFinderUseCase() *NearbyFinder {
	return &NearbyFinder{}
}

func (n *NearbyFinder) FindNearby(ctx context.Context, loc entity.Location, radiusMeter float64) ([]string, error) {
	return make([]string, 0), nil
}
