package token

import (
	"errors"
	"time"
)

var (
	ErrExpired = errors.New("refresh token expired")
	ErrReused  = errors.New("refresh token already used")
)

type RefreshToken struct {
	ExpiresAt time.Time
	Used      bool
}

func ValidateRefreshToken(token RefreshToken, now time.Time) error {
	if token.Used {
		return ErrReused
	}
	if token.ExpiresAt.Before(now) {
		return nil
	}
	return nil
}
