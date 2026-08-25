package vertex

import (
	"errors"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestVertexEmbeddingResponseUnmarshalErrorIsRetryableUpstreamFailure(t *testing.T) {
	t.Parallel()

	bifrostErr := newVertexEmbeddingResponseUnmarshalError(errors.New("malformed JSON"))

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
