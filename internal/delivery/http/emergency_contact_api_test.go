package http_test

// HTTP tests for POST /api/emergency-contacts validation: the phone becomes
// an SMS target, so invalid numbers, duplicates and an oversized list must
// be rejected with 422 (not 200 and not 500).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	deliveryhttp "golang-clean-architecture/internal/delivery/http"
	"golang-clean-architecture/internal/entity"
	"golang-clean-architecture/internal/model"
	"golang-clean-architecture/internal/usecase"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type memEmergencyContacts struct {
	rows []*entity.EmergencyContact
}

func (m *memEmergencyContacts) Create(ctx context.Context, db *gorm.DB, c *entity.EmergencyContact) error {
	m.rows = append(m.rows, c)
	return nil
}

func (m *memEmergencyContacts) ListByUserID(ctx context.Context, db *gorm.DB, userID string) ([]*entity.EmergencyContact, error) {
	var out []*entity.EmergencyContact
	for _, row := range m.rows {
		if row.UserID == userID {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestEmergencyContactAPIValidation(t *testing.T) {
	log := logrus.New()
	log.SetOutput(io.Discard)
	store := &memEmergencyContacts{}
	uc := usecase.NewTriggerSOSUseCase(nil, log, usecase.TriggerSOSDeps{
		Validate:          validator.New(),
		EmergencyContacts: store,
	})

	app := fiber.New(fiber.Config{
		ErrorHandler: func(ctx *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			return ctx.Status(code).JSON(fiber.Map{"errors": err.Error()})
		},
	})
	app.Use(func(ctx *fiber.Ctx) error {
		ctx.Locals("auth", &model.Auth{ID: strings.Clone(ctx.Get("X-Test-User"))})
		return ctx.Next()
	})
	ctl := deliveryhttp.NewEmergencyContactController(uc, log)
	app.Get("/api/emergency-contacts", ctl.List)
	app.Post("/api/emergency-contacts", ctl.Create)

	post := func(phone string) (int, []byte) {
		t.Helper()
		body := `{"contact_name":"Ibu","relationship":"Mother","phone_number":"` + phone + `"}`
		req := httptest.NewRequest("POST", "/api/emergency-contacts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Test-User", "user-a")
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatalf("POST contact: %v", err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	for _, bad := range []string{"abc", "1234", "081234567890", "+6281234567890123"} {
		if status, body := post(bad); status != http.StatusUnprocessableEntity {
			t.Fatalf("contact %q status = %d body = %s, want 422", bad, status, body)
		}
	}
	if len(store.rows) != 0 {
		t.Fatalf("rejected numbers were persisted: %+v", store.rows)
	}

	if status, body := post("+62 812-3456-7890"); status != http.StatusOK {
		t.Fatalf("valid contact status = %d body = %s, want 200", status, body)
	}
	if status, body := post("+6281234567890"); status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate contact status = %d body = %s, want 422", status, body)
	}

	// Fill the list to the cap, then the eleventh is refused.
	for i := 1; i < 10; i++ {
		if status, body := post(fmt.Sprintf("+62821234%04d", i)); status != http.StatusOK {
			t.Fatalf("contact %d status = %d body = %s, want 200", i, status, body)
		}
	}
	if status, body := post("+628999999999"); status != http.StatusUnprocessableEntity {
		t.Fatalf("eleventh contact status = %d body = %s, want 422", status, body)
	}

	// The list still reads back every stored contact.
	req := httptest.NewRequest("GET", "/api/emergency-contacts", nil)
	req.Header.Set("X-Test-User", "user-a")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("GET contacts: %v", err)
	}
	defer resp.Body.Close()
	var list model.WebResponse[[]model.EmergencyContactResponse]
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("contacts decode: %v", err)
	}
	if len(list.Data) != 10 {
		t.Fatalf("contacts = %d, want 10", len(list.Data))
	}
}
