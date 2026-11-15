package repository

import (
	"testing"

	"golang-clean-architecture/internal/model"
)

func TestHistoryOffset(t *testing.T) {
	cases := []struct {
		name    string
		page    int
		size    int
		want    int
		wantErr bool
	}{
		{name: "first page", page: 1, size: 20, want: 0},
		{name: "second page", page: 2, size: 20, want: 20},
		{name: "largest accepted page", page: 100000, size: 100, want: 9999900},
		{name: "zero page", page: 0, size: 20, wantErr: true},
		{name: "zero size", page: 1, size: 0, wantErr: true},
		{name: "overflowing page", page: 9223372036854775807, size: 20, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := historyOffset(&model.SearchAlertHistoryRequest{Page: tc.page, Size: tc.size})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("historyOffset(%d, %d) = %d, want an error", tc.page, tc.size, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("historyOffset(%d, %d) error = %v", tc.page, tc.size, err)
			}
			if got != tc.want {
				t.Fatalf("historyOffset(%d, %d) = %d, want %d", tc.page, tc.size, got, tc.want)
			}
		})
	}
}
