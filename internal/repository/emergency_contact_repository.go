package repository

import (
	"context"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// EmergencyContactRepository manages the Tier-1 emergency_contacts table.
type EmergencyContactRepository struct {
	Repository[entity.EmergencyContact]
	Log *logrus.Logger
}

func NewEmergencyContactRepository(log *logrus.Logger) *EmergencyContactRepository {
	return &EmergencyContactRepository{Log: log}
}

func (r *EmergencyContactRepository) Create(ctx context.Context, db *gorm.DB, contact *entity.EmergencyContact) error {
	return db.WithContext(ctx).Create(contact).Error
}

func (r *EmergencyContactRepository) ListByUserID(ctx context.Context, db *gorm.DB, userID string) ([]*entity.EmergencyContact, error) {
	var out []*entity.EmergencyContact
	if err := db.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// DispatchContacts returns SMS recipients for a sender: verified
// emergency contacts first, falling back to the generic contacts table
// (which predates emergency_contacts) when none are configured.
func (r *EmergencyContactRepository) DispatchContacts(ctx context.Context, db *gorm.DB, userID string) ([]model.EmergencyContactResponse, error) {
	contacts, err := r.ListByUserID(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	out := make([]model.EmergencyContactResponse, 0, len(contacts))
	for _, c := range contacts {
		if c.PhoneNumber == "" {
			continue
		}
		out = append(out, model.EmergencyContactResponse{
			ID: c.ID, ContactName: c.ContactName, PhoneNumber: c.PhoneNumber,
			Relationship: c.Relationship, IsVerified: c.IsVerified, CreatedAt: c.CreatedAt,
		})
	}
	if len(out) > 0 {
		return out, nil
	}
	var generic []entity.Contact
	if err := db.WithContext(ctx).Where("user_id = ? AND phone <> ''", userID).Find(&generic).Error; err != nil {
		return nil, err
	}
	for _, c := range generic {
		name := c.FirstName
		if c.LastName != "" {
			name += " " + c.LastName
		}
		out = append(out, model.EmergencyContactResponse{
			ID: c.ID, ContactName: name, PhoneNumber: c.Phone,
		})
	}
	return out, nil
}

// DispatchContactRepository adapts EmergencyContactRepository to the
// usecase ContactResolver interface (no transaction needed).
type DispatchContactRepository struct {
	DB   *gorm.DB
	Repo *EmergencyContactRepository
}

// DispatchContacts implements usecase.ContactResolver.
func (r *DispatchContactRepository) DispatchContacts(ctx context.Context, userID string) ([]model.EmergencyContactResponse, error) {
	return r.Repo.DispatchContacts(ctx, r.DB, userID)
}

// UserReaderRepository reads users by id without a transaction, for
// SOS detail views and push-token lookup.
type UserReaderRepository struct {
	DB  *gorm.DB
	Log *logrus.Logger
}

func NewUserReaderRepository(db *gorm.DB, log *logrus.Logger) *UserReaderRepository {
	return &UserReaderRepository{DB: db, Log: log}
}

func (r *UserReaderRepository) GetByID(ctx context.Context, id string) (*entity.User, error) {
	user := new(entity.User)
	if err := r.DB.WithContext(ctx).Where("id = ?", id).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}
