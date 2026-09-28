package test

import (
	"golang-clean-architecture/internal/usecase"
	"testing"
)

func TestCalculateDistance(t *testing.T) {
	tests := []struct {
		nama                   string
		lat1, lon1, lat2, lon2 float64
		harapan                bool
	}{
		{"titik yang sama", -8.6705, 115.2126, -8.6705, 115.2126, true},
		{"sekitar 550 m", 0, 0, 0.005, 0, true},
		{"batas: sekitar 990 m", 0, 0, 0.0089, 0, true},
		{"batas: sekitar 1012 m", 0, 0, 0.0091, 0, false},
		{"sekitar 5 km", 0, 0, 0.045, 0, false},
		{"Denpasar ke Surabaya", -8.6705, 115.2126, -7.2575, 112.7521, false},
	}

	for _, tc := range tests {
		t.Run(tc.nama, func(t *testing.T) {
			hasil := usecase.CalculateDistance(tc.lat1, tc.lon1, tc.lat2, tc.lon2)
			if hasil != tc.harapan {
				t.Errorf("CalculateDistance(%v, %v, %v, %v) = %v, harapan %v",
					tc.lat1, tc.lon1, tc.lat2, tc.lon2, hasil, tc.harapan)
			}
		})
	}
}

func TestCalculateDistanceSimetris(t *testing.T) {
	// A ke B harus sama hasilnya dengan B ke A
	a := usecase.CalculateDistance(-8.6705, 115.2126, -7.2575, 112.7521)
	b := usecase.CalculateDistance(-7.2575, 112.7521, -8.6705, 115.2126)

	if a != b {
		t.Errorf("hasil tidak simetris: A->B = %v, B->A = %v", a, b)
	}
}
