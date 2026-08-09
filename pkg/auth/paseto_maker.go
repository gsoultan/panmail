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

func (maker *PasetoMaker) CreateToken(req TokenRequest) (string, error) {
	if req.Purpose == "" {
		return "", errors.New("token purpose is mandatory")
	}

	payload := &TokenPayload{
		UserID:    req.UserID,
		TenantID:  req.TenantID,
		Role:      req.Role,
		Purpose:   req.Purpose,
		IssuedAt:  time.Now(),
		ExpiredAt: time.Now().Add(req.Duration),
	}

	return maker.paseto.Encrypt(maker.symmetricKey, payload, nil)
}

func (maker *PasetoMaker) VerifyToken(token string, purpose TokenPurpose) (*TokenPayload, error) {
	payload := &TokenPayload{}

	if err := maker.paseto.Decrypt(token, maker.symmetricKey, payload, nil); err != nil {
		return nil, ErrInvalidToken
	}

	if err := payload.Valid(purpose); err != nil {
		return nil, err
	}

	return payload, nil
}
