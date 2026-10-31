package model

type UserResponse struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Token     string `json:"token,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

type VerifyUserRequest struct {
	Token string `validate:"required,max=100"`
}

type RegisterUserRequest struct {
	ID       string `json:"id" validate:"required,max=100"`
	Password string `json:"password" validate:"required,max=100"`
	Name     string `json:"name" validate:"required,max=100"`
}

type UpdateUserRequest struct {
	ID        string `json:"-" validate:"required,max=100"`
	Password  string `json:"password,omitempty" validate:"max=100"`
	Name      string `json:"name,omitempty" validate:"max=100"`
	SafePIN   string `json:"safe_pin,omitempty" validate:"omitempty,len=4,numeric"`
	FCMToken  string `json:"fcm_token,omitempty" validate:"max=255"`
	APNsToken string `json:"apns_token,omitempty" validate:"max=255"`
}

type LoginUserRequest struct {
	ID       string `json:"id" validate:"required,max=100"`
	Password string `json:"password" validate:"required,max=100"`
}

type LogoutUserRequest struct {
	ID string `json:"id" validate:"required,max=100"`
}

type GetUserRequest struct {
	ID string `json:"id" validate:"required,max=100"`
}
