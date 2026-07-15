package auth

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"
)

func TestPasetoMaker(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	keyHex := hex.EncodeToString(key)

	maker, err := NewPasetoMaker(keyHex)
	if err != nil {
		t.Fatalf("failed to create paseto maker: %v", err)
	}

	userID := "test-user-id"
	tenantID := "test-tenant-id"
	role := "USER_ROLE_ADMIN"
	duration := time.Minute

	token, err := maker.CreateToken(userID, tenantID, role, duration)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}

	payload, err := maker.VerifyToken(token)
	if err != nil {
		t.Fatalf("failed to verify token: %v", err)
	}

	if payload.UserID != userID {
		t.Errorf("expected userID %s, got %s", userID, payload.UserID)
	}

	if payload.TenantID != tenantID {
		t.Errorf("expected tenantID %s, got %s", tenantID, payload.TenantID)
	}

	if payload.Role != role {
		t.Errorf("expected role %s, got %s", role, payload.Role)
	}

	if time.Now().After(payload.ExpiredAt) {
		t.Error("token is expired")
	}
}

func TestExpiredToken(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	keyHex := hex.EncodeToString(key)

	maker, err := NewPasetoMaker(keyHex)
	if err != nil {
		t.Fatalf("failed to create paseto maker: %v", err)
	}

	token, err := maker.CreateToken("user", "tenant", "USER_ROLE_ADMIN", -time.Minute)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	_, err = maker.VerifyToken(token)
	if err != ErrExpiredToken {
		t.Errorf("expected ErrExpiredToken, got %v", err)
	}
}
