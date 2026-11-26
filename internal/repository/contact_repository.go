package repository

import (
	"strings"

	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type ContactRepository struct {
	Repository[entity.Contact]
	Log *logrus.Logger
}

func NewContactRepository(log *logrus.Logger) *ContactRepository {
	return &ContactRepository{
		Log: log,
	}
}

func (r *ContactRepository) FindByIdAndUserId(db *gorm.DB, contact *entity.Contact, id string, userId string) error {
	return db.Where("id = ? AND user_id = ?", id, userId).Take(contact).Error
}

func (r *ContactRepository) Search(db *gorm.DB, request *model.SearchContactRequest) ([]entity.Contact, int64, error) {
	var contacts []entity.Contact
	if err := db.Scopes(r.FilterContact(request)).Offset((request.Page - 1) * request.Size).Limit(request.Size).Find(&contacts).Error; err != nil {
		return nil, 0, err
	}

	var total int64 = 0
	if err := db.Model(&entity.Contact{}).Scopes(r.FilterContact(request)).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	return contacts, total, nil
}

func (r *ContactRepository) FilterContact(request *model.SearchContactRequest) func(tx *gorm.DB) *gorm.DB {
	return func(tx *gorm.DB) *gorm.DB {
		tx = tx.Where("user_id = ?", request.UserId)

		// LOWER() on both sides keeps the search case-insensitive the way
		// MySQL's default collation made it; PostgreSQL's LIKE is
		// case-sensitive, so without this "budi" would stop matching "Budi".
		if name := request.Name; name != "" {
			name = "%" + strings.ToLower(name) + "%"
			tx = tx.Where("LOWER(first_name) LIKE ? OR LOWER(last_name) LIKE ?", name, name)
		}

		if phone := request.Phone; phone != "" {
			phone = "%" + strings.ToLower(phone) + "%"
			tx = tx.Where("LOWER(phone) LIKE ?", phone)
		}

		if email := request.Email; email != "" {
			email = "%" + strings.ToLower(email) + "%"
			tx = tx.Where("LOWER(email) LIKE ?", email)
		}

		return tx
	}
}
