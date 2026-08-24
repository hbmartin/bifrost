package handlers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	realtimeEphemeralTokenPrefix    = "ek_bf_"
	realtimeEphemeralTokenVersion   = 1
	realtimeEphemeralTokenMaxLength = 16 * 1024
)

var errInvalidRealtimeEphemeralToken = errors.New("invalid realtime ephemeral token")

type realtimeEphemeralTokenPayload struct {
	Version       int    `json:"v"`
	UpstreamToken string `json:"t"`
	KeyID         string `json:"k"`
	VirtualKey    string `json:"vk,omitempty"`
	ExpiresAt     int64  `json:"e"`
}

// sealRealtimeEphemeralToken binds an upstream secret to the provider key and
// governance identity that minted it. AES-GCM keeps the upstream token and
// virtual key confidential while allowing any replica with the same stable
// Bifrost encryption key to authenticate and recover the mapping.
func sealRealtimeEphemeralToken(
	masterKey []byte,
	upstreamToken string,
	keyID string,
	virtualKey string,
	expiresAt int64,
) (string, error) {
	if len(masterKey) == 0 || strings.TrimSpace(upstreamToken) == "" || strings.TrimSpace(keyID) == "" || expiresAt <= time.Now().Unix() {
		return "", errInvalidRealtimeEphemeralToken
	}

	payload, err := json.Marshal(realtimeEphemeralTokenPayload{
		Version:       realtimeEphemeralTokenVersion,
		UpstreamToken: strings.TrimSpace(upstreamToken),
		KeyID:         strings.TrimSpace(keyID),
		VirtualKey:    strings.TrimSpace(virtualKey),
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		return "", err
	}

	aead, err := newRealtimeEphemeralTokenAEAD(masterKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, payload, []byte(realtimeEphemeralTokenPrefix))
	return realtimeEphemeralTokenPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func openRealtimeEphemeralToken(masterKey []byte, token string) (realtimeEphemeralKeyMapping, bool) {
	token = strings.TrimSpace(token)
	if len(masterKey) == 0 || !strings.HasPrefix(token, realtimeEphemeralTokenPrefix) || len(token) > realtimeEphemeralTokenMaxLength {
		return realtimeEphemeralKeyMapping{}, false
	}

	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, realtimeEphemeralTokenPrefix))
	if err != nil {
		return realtimeEphemeralKeyMapping{}, false
	}
	aead, err := newRealtimeEphemeralTokenAEAD(masterKey)
	if err != nil || len(sealed) < aead.NonceSize() {
		return realtimeEphemeralKeyMapping{}, false
	}
	nonce, ciphertext := sealed[:aead.NonceSize()], sealed[aead.NonceSize():]
	payloadJSON, err := aead.Open(nil, nonce, ciphertext, []byte(realtimeEphemeralTokenPrefix))
	if err != nil {
		return realtimeEphemeralKeyMapping{}, false
	}

	var payload realtimeEphemeralTokenPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil ||
		payload.Version != realtimeEphemeralTokenVersion ||
		strings.TrimSpace(payload.UpstreamToken) == "" ||
		strings.TrimSpace(payload.KeyID) == "" ||
		payload.ExpiresAt <= time.Now().Unix() {
		return realtimeEphemeralKeyMapping{}, false
	}

	return realtimeEphemeralKeyMapping{
		Version:       realtimeEphemeralKeyMappingVersion,
		KeyID:         strings.TrimSpace(payload.KeyID),
		VirtualKey:    strings.TrimSpace(payload.VirtualKey),
		UpstreamToken: strings.TrimSpace(payload.UpstreamToken),
	}, true
}

func newRealtimeEphemeralTokenAEAD(masterKey []byte) (cipher.AEAD, error) {
	hash := sha256.New()
	_, _ = hash.Write([]byte("bifrost-realtime-ephemeral-token-v1:"))
	_, _ = hash.Write(masterKey)
	block, err := aes.NewCipher(hash.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
