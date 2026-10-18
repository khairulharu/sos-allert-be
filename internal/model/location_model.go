package model

type LocationResponse struct {
	UserId    string  `json:"user_id"`
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

type UpdateLocationRequest struct {
	UserId    string   `json:"-" validate:"required"`
	Longitude *float64 `json:"longitude" validate:"required,gte=-180,lte=180"`
	Latitude  *float64 `json:"latitude"  validate:"required,gte=-85.05,lte=85.05"`
}
