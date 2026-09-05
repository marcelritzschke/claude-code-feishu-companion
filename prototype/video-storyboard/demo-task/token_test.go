package token

import (
	"errors"
	"testing"
	"time"
)

func TestValidateRefreshToken(t *testing.T) {
	now := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		token RefreshToken
		want  error
	}{
		{
			name:  "valid token",
			token: RefreshToken{ExpiresAt: now.Add(time.Hour)},
		},
		{
			name:  "expired token",
			token: RefreshToken{ExpiresAt: now.Add(-time.Minute)},
			want:  ErrExpired,
		},
		{
			name:  "reused token",
			token: RefreshToken{ExpiresAt: now.Add(time.Hour), Used: true},
			want:  ErrReused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateRefreshToken(tt.token, now); !errors.Is(got, tt.want) {
				t.Fatalf("ValidateRefreshToken() = %v, want %v", got, tt.want)
			}
		})
	}
}
