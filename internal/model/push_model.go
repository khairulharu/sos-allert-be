package model

// PushSubscribeRequest is the POST /api/push/subscriptions body: the
// PushSubscription.toJSON() shape from the browser Push API.
type PushSubscribeRequest struct {
	UserID    string `json:"-" validate:"required"`
	Endpoint  string `json:"endpoint" validate:"required,url,max=512"`
	P256DH    string `json:"p256dh" validate:"required,max=255"`
	Auth      string `json:"auth" validate:"required,max=255"`
	UserAgent string `json:"user_agent" validate:"max=255"`
}

// PushUnsubscribeRequest is the DELETE /api/push/subscriptions body.
type PushUnsubscribeRequest struct {
	UserID   string `json:"-" validate:"required"`
	Endpoint string `json:"endpoint" validate:"required,url,max=512"`
}

// PushSubscriptionResponse is a stored subscription.
type PushSubscriptionResponse struct {
	ID        string `json:"id"`
	Endpoint  string `json:"endpoint"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// PushVAPIDKeyResponse is the GET /api/push/vapid-public-key body.
type PushVAPIDKeyResponse struct {
	PublicKey string `json:"public_key"`
}
