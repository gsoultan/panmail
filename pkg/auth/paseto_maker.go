package auth

import (
	"encoding/hex"
	"errors"
	"time"

	"github.com/o1egl/paseto/v2"
)

type PasetoMaker struct {
	paseto       *paseto.V2
	symmetricKey []byte
}

func NewPasetoMaker(symmetricKeyHex string) (*PasetoMaker, error) {
	key, err := hex.DecodeString(symmetricKeyHex)
	if err != nil {
		return nil, errors.New("invalid symmetric key: must be hex encoded")
	}
	if len(key) != 32 {
		return nil, errors.New("invalid symmetric key: must be 32 bytes")
	}

	return &PasetoMaker{
		paseto:       paseto.NewV2(),
		symmetricKey: key,
	}, nil
}

func (maker *PasetoMaker) CreateToken(userID string, tenantID string, role string, duration time.Duration) (string, error) {
	payload := &TokenPayload{
		UserID:    userID,
		TenantID:  tenantID,
		Role:      role,
		IssuedAt:  time.Now(),
		ExpiredAt: time.Now().Add(duration),
	}

	return maker.paseto.Encrypt(maker.symmetricKey, payload, nil)
}

func (maker *PasetoMaker) VerifyToken(token string) (*TokenPayload, error) {
	payload := &TokenPayload{}

	err := maker.paseto.Decrypt(token, maker.symmetricKey, payload, nil)
	if err != nil {
		return nil, ErrInvalidToken
	}

	err = payload.Valid()
	if err != nil {
		return nil, err
	}

	return payload, nil
}
