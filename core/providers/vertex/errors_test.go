package vertex

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestVertexEmbeddingMalformedSuccessResponsePreservesUpstreamFailureShape(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"predictions":`))
	}))
	defer server.Close()

	provider := &VertexProvider{
		client: &fasthttp.Client{
			Dial: func(string) (net.Conn, error) {
				return net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
			},
			TLSConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test-only TLS endpoint
		},
	}
	ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
	key := schemas.Key{
		Value: *schemas.NewSecretVar("test-api-key"),
		VertexKeyConfig: &schemas.VertexKeyConfig{
			ProjectID: *schemas.NewSecretVar("test-project"),
			Region:    *schemas.NewSecretVar("us-central1"),
		},
	}

	resp, bifrostErr := provider.Embedding(ctx, key, &schemas.BifrostEmbeddingRequest{
		Model: "text-embedding-004",
		Input: &schemas.EmbeddingInput{
			Texts: []string{"hello"},
		},
	})

	assert.Nil(t, resp)
	require.NotNil(t, bifrostErr)
	assert.False(t, bifrostErr.IsBifrostError)
	require.NotNil(t, bifrostErr.StatusCode)
	assert.Equal(t, fasthttp.StatusBadGateway, *bifrostErr.StatusCode)
	require.NotNil(t, bifrostErr.Error)
	assert.Equal(t, schemas.ErrProviderResponseUnmarshal, bifrostErr.Error.Message)
	require.NotNil(t, bifrostErr.Error.Type)
	assert.Equal(t, schemas.ProviderResponseInvalid, *bifrostErr.Error.Type)
}

// TestParseVertexError_PopulatesStatusType verifies the Vertex status
// (e.g. RESOURCE_EXHAUSTED) is surfaced on error.type rather than being dropped,
// so passthrough/OpenAI-shaped consumers see the exception type.
func TestParseVertexError_PopulatesStatusType(t *testing.T) {
	var resp fasthttp.Response
	resp.SetStatusCode(fasthttp.StatusTooManyRequests)
	resp.SetBodyString(`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`)

	bifrostErr := parseVertexError(&resp)

	require.NotNil(t, bifrostErr)
	require.NotNil(t, bifrostErr.Error)
	require.NotNil(t, bifrostErr.Error.Type, "nested error.type must be populated from status")
	assert.Equal(t, "RESOURCE_EXHAUSTED", *bifrostErr.Error.Type)
	assert.Equal(t, "Quota exceeded", bifrostErr.Error.Message)
}

// TestParseVertexError_NoStatusNoType verifies that when the body carries no
// Vertex status we don't fabricate an error.type.
func TestParseVertexError_NoStatusNoType(t *testing.T) {
	var resp fasthttp.Response
	resp.SetStatusCode(fasthttp.StatusBadRequest)
	resp.SetBodyString(`{"error":{"code":400,"message":"bad request"}}`)

	bifrostErr := parseVertexError(&resp)

	require.NotNil(t, bifrostErr)
	require.NotNil(t, bifrostErr.Error)
	assert.Nil(t, bifrostErr.Error.Type, "no status present, so none should be fabricated")
}

func TestParseVertexError_MalformedBodyPreservesUpstreamStatus(t *testing.T) {
	var resp fasthttp.Response
	resp.SetStatusCode(fasthttp.StatusUnauthorized)
	resp.SetBodyString(`not-json`)

	bifrostErr := parseVertexError(&resp)

	require.NotNil(t, bifrostErr)
	require.NotNil(t, bifrostErr.StatusCode)
	assert.Equal(t, fasthttp.StatusUnauthorized, *bifrostErr.StatusCode)
	assert.False(t, bifrostErr.IsBifrostError)
	require.NotNil(t, bifrostErr.Error)
	assert.Equal(t, schemas.ErrProviderResponseUnmarshal, bifrostErr.Error.Message)
	require.NotNil(t, bifrostErr.Error.Type)
	assert.Equal(t, schemas.ProviderResponseInvalid, *bifrostErr.Error.Type)
	assert.Equal(t, "not-json", bifrostErr.ExtraFields.RawResponse)
}
