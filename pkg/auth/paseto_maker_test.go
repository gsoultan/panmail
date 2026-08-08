package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

func newTestMaker(t *testing.T) *PasetoMaker {
	t.Helper()

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	maker, err := NewPasetoMaker(hex.EncodeToString(key))
	if err != nil {
		t.Fatalf("failed to create paseto maker: %v", err)
	}
	return maker
}

func TestPasetoMaker(t *testing.T) {
	maker := newTestMaker(t)

	req := TokenRequest{
		UserID:   "test-user-id",
		TenantID: "test-tenant-id",
		Role:     "USER_ROLE_ADMIN",
		Purpose:  PurposeSession,
		Duration: time.Minute,
	}

	token, err := maker.CreateToken(req)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}

	payload, err := maker.VerifyToken(token, PurposeSession)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}

	if payload.UserID != req.UserID {
		t.Errorf("expected userID %s, got %s", req.UserID, payload.UserID)
	}
	if payload.TenantID != req.TenantID {
		t.Errorf("expected tenantID %s, got %s", req.TenantID, payload.TenantID)
	}
	if payload.Role != req.Role {
		t.Errorf("expected role %s, got %s", req.Role, payload.Role)
	}
	if payload.Purpose != PurposeSession {
		t.Errorf("expected purpose %s, got %s", PurposeSession, payload.Purpose)
	}
	if time.Now().After(payload.ExpiredAt) {
		t.Error("token is expired")
	}
}

func TestExpiredToken(t *testing.T) {
	maker := newTestMaker(t)

	token, err := maker.CreateToken(TokenRequest{
		UserID:   "user",
		TenantID: "tenant",
		Role:     "USER_ROLE_ADMIN",
		Purpose:  PurposeSession,
		Duration: -time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	if _, err := maker.VerifyToken(token, PurposeSession); !errors.Is(err, ErrExpiredToken) {
		t.Errorf("expected ErrExpiredToken, got %v", err)
	}
}

// A challenge token only permits a second-factor attempt. Accepting it as a
// session token would hand out API access for a password alone.
func TestTokenPurposeIsEnforced(t *testing.T) {
	maker := newTestMaker(t)

	tests := []struct {
		name     string
		issued   TokenPurpose
		verified TokenPurpose
		wantErr  error
	}{
		{"session used as session", PurposeSession, PurposeSession, nil},
		{"challenge used as challenge", PurposeTwoFactorChallenge, PurposeTwoFactorChallenge, nil},
		{"challenge replayed as session", PurposeTwoFactorChallenge, PurposeSession, ErrWrongTokenPurpose},
		{"session used as challenge", PurposeSession, PurposeTwoFactorChallenge, ErrWrongTokenPurpose},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, err := maker.CreateToken(TokenRequest{
				UserID:   "user",
				TenantID: "tenant",
				Role:     "USER_ROLE_SUPER_ADMIN",
				Purpose:  tc.issued,
				Duration: time.Minute,
			})
			if err != nil {
				t.Fatalf("failed to create token: %v", err)
			}

			_, err = maker.VerifyToken(token, tc.verified)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("VerifyToken(%s as %s) = %v; want %v", tc.issued, tc.verified, err, tc.wantErr)
			}
		})
	}
}

func TestCreateTokenRequiresPurpose(t *testing.T) {
	maker := newTestMaker(t)

	if _, err := maker.CreateToken(TokenRequest{UserID: "user", Duration: time.Minute}); err == nil {
		t.Error("expected an error when minting a token with no purpose")
	}
}
