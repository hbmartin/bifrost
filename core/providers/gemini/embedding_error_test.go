package gemini

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestNewGeminiEmbeddingResponseErrorClassifiesInvalidUpstreamResponse(t *testing.T) {
	bifrostErr := newGeminiEmbeddingResponseError()

	if bifrostErr == nil || bifrostErr.IsBifrostError {
		t.Fatalf("error = %#v, want provider-caused error", bifrostErr)
	}
	if bifrostErr.StatusCode == nil || *bifrostErr.StatusCode != 502 {
		t.Fatalf("status = %v, want 502", bifrostErr.StatusCode)
	}
	if bifrostErr.Error == nil || bifrostErr.Error.Message != schemas.ErrProviderResponseUnmarshal {
		t.Fatalf("error field = %#v, want %q", bifrostErr.Error, schemas.ErrProviderResponseUnmarshal)
	}
	if bifrostErr.Error.Type == nil || *bifrostErr.Error.Type != schemas.ProviderResponseInvalid {
		t.Fatalf("type = %v, want %q", bifrostErr.Error.Type, schemas.ProviderResponseInvalid)
	}
}
