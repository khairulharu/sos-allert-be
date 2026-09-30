package usecase

import (
	"context"
	"golang-clean-architecture/internal/model"
)

type NearbyFinder struct {
}

func NewNearbyFinderUseCase() *NearbyFinder {
	return &NearbyFinder{}
}

func (n *NearbyFinder) FindNearby(ctx context.Context, loc model.Location, radiusMeter float64) ([]string, error) {
	return make([]string, 0), nil
}
