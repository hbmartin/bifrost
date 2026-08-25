package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	frameworkEncrypt "github.com/maximhq/bifrost/framework/encrypt"
	"gorm.io/gorm"
)

type fakeWSTicketNonceStore struct {
	mu     sync.Mutex
	tokens map[string]tables.TempToken
}

func newFakeWSTicketNonceStore() *fakeWSTicketNonceStore {
	return &fakeWSTicketNonceStore{tokens: make(map[string]tables.TempToken)}
}

func (s *fakeWSTicketNonceStore) CreateTempToken(_ context.Context, token *tables.TempToken, _ ...*gorm.DB) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token.Scope+":"+token.ResourceID] = *token
	return nil
}

func (s *fakeWSTicketNonceStore) DeleteTempTokensByResourceID(_ context.Context, scope, resourceID string, _ ...*gorm.DB) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scope + ":" + resourceID
	if _, ok := s.tokens[key]; !ok {
		return 0, nil
	}
	delete(s.tokens, key)
	return 1, nil
}

func (s *fakeWSTicketNonceStore) addNonce(nonce string, expiresAt time.Time) {
	_ = s.CreateTempToken(context.Background(), &tables.TempToken{
		ID:         "test-token",
		Token:      nonce,
		Scope:      wsTicketNonceScope,
		ResourceID: frameworkEncrypt.HashSHA256(nonce),
		ExpiresAt:  expiresAt,
	})
}

func TestSignedWSTicketValidatesAcrossStores(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	nonceStore := newFakeWSTicketNonceStore()
	issuer := NewSignedWSTicketStore(key, nonceStore)
	consumer := NewSignedWSTicketStore(key, nonceStore)

	ticket, err := issuer.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if got := consumer.Consume(ticket); got != "session-token" {
		t.Fatalf("Consume() = %q, want %q", got, "session-token")
	}
	if got := consumer.Consume(ticket); got != "" {
		t.Fatalf("second Consume() = %q, want empty string", got)
	}
}

func TestSignedWSTicketConcurrentConsumeSucceedsOnceAcrossStores(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	nonceStore := newFakeWSTicketNonceStore()
	issuer := NewSignedWSTicketStore(key, nonceStore)
	consumerA := NewSignedWSTicketStore(key, nonceStore)
	consumerB := NewSignedWSTicketStore(key, nonceStore)

	ticket, err := issuer.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	results := make(chan string, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for _, consumer := range []*WSTicketStore{consumerA, consumerB} {
		go func(store *WSTicketStore) {
			defer wg.Done()
			results <- store.Consume(ticket)
		}(consumer)
	}
	wg.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result == "session-token" {
			successes++
		} else if result != "" {
			t.Fatalf("Consume() = %q, want session token or empty string", result)
		}
	}
	if successes != 1 {
		t.Fatalf("successful consumes = %d, want 1", successes)
	}
}

func TestSignedWSTicketRejectsWrongKey(t *testing.T) {
	nonceStore := newFakeWSTicketNonceStore()
	issuer := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), nonceStore)
	consumer := NewSignedWSTicketStore([]byte("abcdef0123456789abcdef0123456789"), nonceStore)

	ticket, err := issuer.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if got := consumer.Consume(ticket); got != "" {
		t.Fatalf("Consume() = %q, want empty string", got)
	}
}

func TestSignedWSTicketRejectsExpiredTicket(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	store := NewSignedWSTicketStore(key, newFakeWSTicketNonceStore())
	ticket := buildSignedWSTicketForTest(t, store.signingKey, signedWSTicketPayload{
		Version:      wsTicketVersion,
		SessionToken: "session-token",
		ExpiresAt:    time.Now().Add(-5 * time.Second).Unix(),
		Nonce:        "nonce",
	})

	if got := store.Consume(ticket); got != "" {
		t.Fatalf("Consume() = %q, want empty string", got)
	}
}

func TestSignedWSTicketAllowsBoundedVerifierClockSkew(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	nonceStore := newFakeWSTicketNonceStore()
	store := NewSignedWSTicketStore(key, nonceStore)
	payload := signedWSTicketPayload{
		Version:      wsTicketVersion,
		SessionToken: "session-token",
		ExpiresAt:    time.Now().Unix(),
		Nonce:        "nonce",
	}
	nonceStore.addNonce(payload.Nonce, time.Unix(payload.ExpiresAt, 0).Add(wsTicketClockSkew))
	ticket := buildSignedWSTicketForTest(t, store.signingKey, payload)

	if got := store.Consume(ticket); got != "session-token" {
		t.Fatalf("Consume() = %q, want bounded clock skew to preserve the signed ticket", got)
	}
}

func TestSignedWSTicketRejectsMalformedTicket(t *testing.T) {
	store := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), newFakeWSTicketNonceStore())

	for _, ticket := range []string{"", "missing-dot", "payload.", ".signature", "payload.not-base64"} {
		if got := store.Consume(ticket); got != "" {
			t.Fatalf("Consume(%q) = %q, want empty string", ticket, got)
		}
	}
}

func TestSignedWSTicketRejectsTamperedTicket(t *testing.T) {
	store := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), newFakeWSTicketNonceStore())

	ticket, err := store.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	parts := strings.Split(ticket, ".")
	if len(parts) != 2 {
		t.Fatalf("ticket parts = %d, want 2", len(parts))
	}
	tampered := parts[0] + "a." + parts[1]

	if got := store.Consume(tampered); got != "" {
		t.Fatalf("Consume() = %q, want empty string", got)
	}
}

func TestLegacyWSTicketRemainsSingleUse(t *testing.T) {
	store := NewWSTicketStore()
	defer store.Stop()

	ticket, err := store.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := store.Consume(ticket); got != "session-token" {
		t.Fatalf("Consume() = %q, want %q", got, "session-token")
	}
	if got := store.Consume(ticket); got != "" {
		t.Fatalf("second Consume() = %q, want empty string", got)
	}
}

func TestSignedWSTicketDoesNotExposeSessionToken(t *testing.T) {
	store := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), newFakeWSTicketNonceStore())

	ticket, err := store.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	if strings.Contains(ticket, "session-token") || strings.Contains(ticket, base64.RawURLEncoding.EncodeToString([]byte("session-token"))) {
		t.Fatalf("ticket exposes session token: %q", ticket)
	}
}

func buildSignedWSTicketForTest(t *testing.T, key []byte, payload signedWSTicketPayload) string {
	t.Helper()

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	encryptedPayload, err := encryptWSTicketPayload(key, payloadBytes)
	if err != nil {
		t.Fatalf("encryptWSTicketPayload() error = %v", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(encryptedPayload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(encodedPayload))
	return encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestSignedWSTicketPayloadEncryptionRejectsShortPayload(t *testing.T) {
	store := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), newFakeWSTicketNonceStore())
	encodedPayload := base64.RawURLEncoding.EncodeToString([]byte("short"))
	mac := hmac.New(sha256.New, store.signingKey)
	mac.Write([]byte(encodedPayload))
	ticket := encodedPayload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if got := store.Consume(ticket); got != "" {
		t.Fatalf("Consume() = %q, want empty string", got)
	}
}

func TestSignedWSTicketNonceIsHex(t *testing.T) {
	store := NewSignedWSTicketStore([]byte("0123456789abcdef0123456789abcdef"), newFakeWSTicketNonceStore())

	ticket, err := store.Issue("session-token")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	parts := strings.Split(ticket, ".")
	encryptedPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	payloadBytes, err := decryptWSTicketPayload(store.signingKey, encryptedPayload)
	if err != nil {
		t.Fatalf("decryptWSTicketPayload() error = %v", err)
	}
	var payload signedWSTicketPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, err := hex.DecodeString(payload.Nonce); err != nil {
		t.Fatalf("nonce is not hex: %v", err)
	}
}
