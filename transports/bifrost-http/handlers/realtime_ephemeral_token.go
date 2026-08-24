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
	// Bound replica clock-skew tolerance on both expiry checks. Issuers clamp
	// below MaxTTL for slower verifiers, while verifiers allow the same reserve
	// at the lower bound when their clock is modestly ahead.
	realtimeEphemeralTokenClockSkew = time.Minute
)

var errInvalidRealtimeEphemeralToken = errors.New("invalid realtime ephemeral token")

type realtimeEphemeralTokenPayload struct {
	Version       int    `json:"v"`
	UpstreamToken string `json:"t"`
	KeyID         string `json:"k"`
	VirtualKey    string `json:"vk,omitempty"`
	ExpiresAt     int64  `json:"e"`
}

type realtimeEphemeralTokenCodec struct {
	aead cipher.AEAD
	now  func() time.Time
}

func newRealtimeEphemeralTokenCodec(masterKey []byte) (*realtimeEphemeralTokenCodec, error) {
	if len(masterKey) == 0 {
		return nil, nil
	}
	aead, err := newRealtimeEphemeralTokenAEAD(masterKey)
	if err != nil {
		return nil, err
	}
	return &realtimeEphemeralTokenCodec{aead: aead, now: time.Now}, nil
}

func (codec *realtimeEphemeralTokenCodec) seal(
	upstreamToken string,
	keyID string,
	virtualKey string,
	expiresAt int64,
) (string, error) {
	token, _, err := codec.sealWithExpiry(upstreamToken, keyID, virtualKey, expiresAt)
	return token, err
}

func (codec *realtimeEphemeralTokenCodec) sealWithExpiry(
	upstreamToken string,
	keyID string,
	virtualKey string,
	expiresAt int64,
) (string, int64, error) {
	now := codec.currentTime()
	if codec == nil || codec.aead == nil || strings.TrimSpace(upstreamToken) == "" || strings.TrimSpace(keyID) == "" || expiresAt <= now.Add(-realtimeEphemeralTokenClockSkew).Unix() {
		return "", 0, errInvalidRealtimeEphemeralToken
	}
	maxExpiresAt := now.Add(realtimeEphemeralKeyMappingMaxTTL - realtimeEphemeralTokenClockSkew).Unix()
	if expiresAt > maxExpiresAt {
		expiresAt = maxExpiresAt
	}

	payload, err := json.Marshal(realtimeEphemeralTokenPayload{
		Version:       realtimeEphemeralTokenVersion,
		UpstreamToken: strings.TrimSpace(upstreamToken),
		KeyID:         strings.TrimSpace(keyID),
		VirtualKey:    strings.TrimSpace(virtualKey),
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		return "", 0, err
	}

	nonce := make([]byte, codec.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", 0, err
	}
	sealed := codec.aead.Seal(nonce, nonce, payload, []byte(realtimeEphemeralTokenPrefix))
	return realtimeEphemeralTokenPrefix + base64.RawURLEncoding.EncodeToString(sealed), expiresAt, nil
}

func (codec *realtimeEphemeralTokenCodec) open(token string) (realtimeEphemeralKeyMapping, bool) {
	token = strings.TrimSpace(token)
	if codec == nil || codec.aead == nil || !strings.HasPrefix(token, realtimeEphemeralTokenPrefix) || len(token) > realtimeEphemeralTokenMaxLength {
		return realtimeEphemeralKeyMapping{}, false
	}

	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, realtimeEphemeralTokenPrefix))
	if err != nil {
		return realtimeEphemeralKeyMapping{}, false
	}
	if len(sealed) < codec.aead.NonceSize() {
		return realtimeEphemeralKeyMapping{}, false
	}
	nonce, ciphertext := sealed[:codec.aead.NonceSize()], sealed[codec.aead.NonceSize():]
	payloadJSON, err := codec.aead.Open(nil, nonce, ciphertext, []byte(realtimeEphemeralTokenPrefix))
	if err != nil {
		return realtimeEphemeralKeyMapping{}, false
	}

	var payload realtimeEphemeralTokenPayload
	now := codec.currentTime()
	if err := json.Unmarshal(payloadJSON, &payload); err != nil ||
		payload.Version != realtimeEphemeralTokenVersion ||
		strings.TrimSpace(payload.UpstreamToken) == "" ||
		strings.TrimSpace(payload.KeyID) == "" ||
		payload.ExpiresAt <= now.Add(-realtimeEphemeralTokenClockSkew).Unix() ||
		payload.ExpiresAt > now.Add(realtimeEphemeralKeyMappingMaxTTL).Unix() {
		return realtimeEphemeralKeyMapping{}, false
	}

	return realtimeEphemeralKeyMapping{
		Version:       realtimeEphemeralKeyMappingVersion,
		KeyID:         strings.TrimSpace(payload.KeyID),
		VirtualKey:    strings.TrimSpace(payload.VirtualKey),
		UpstreamToken: strings.TrimSpace(payload.UpstreamToken),
	}, true
}

func (codec *realtimeEphemeralTokenCodec) currentTime() time.Time {
	if codec != nil && codec.now != nil {
		return codec.now()
	}
	return time.Now()
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
