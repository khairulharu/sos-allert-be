package usecase

import (
	"context"
	"golang-clean-architecture/internal/model"
)

type LocationStore struct {
}

func NewLocationStoreUseCase() *LocationStore {
	return &LocationStore{}
}

func (ls *LocationStore) Save(ctx context.Context, request *model.UpdateLocationRequest) error {
	return nil
}
