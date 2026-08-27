package anthropic

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anthropicTestRawPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9ZQmcAAAAASUVORK5CYII="

func TestConvertToAnthropicImageBlockRejectsMalformedInputs(t *testing.T) {
	tests := []struct {
		name  string
		image *schemas.ChatInputImage
	}{
		{name: "missing image"},
		{name: "empty URL", image: &schemas.ChatInputImage{}},
		{name: "protocol relative URL", image: &schemas.ChatInputImage{URL: "//example.com/image.png"}},
		{name: "unknown raw base64", image: &schemas.ChatInputImage{URL: "QUJD"}},
		{name: "non-base64 data URL", image: &schemas.ChatInputImage{URL: "data:image/png,not-base64"}},
		{name: "unsupported inline image type", image: &schemas.ChatInputImage{URL: "data:image/svg+xml;base64,PHN2Zy8+"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			block := schemas.ChatContentBlock{Type: schemas.ChatContentBlockTypeImage, ImageURLStruct: tt.image}
			assert.Nil(t, convertToAnthropicImageBlock(block))
			assert.Equal(t, AnthropicContentBlock{}, ConvertToAnthropicImageBlock(block))
		})
	}
}

func TestConvertToAnthropicImageBlockNormalizesRawBase64(t *testing.T) {
	converted := convertToAnthropicImageBlock(schemas.ChatContentBlock{
		Type:           schemas.ChatContentBlockTypeImage,
		ImageURLStruct: &schemas.ChatInputImage{URL: anthropicTestRawPNGBase64},
	})
	require.NotNil(t, converted)
	require.NotNil(t, converted.Source)
	require.NotNil(t, converted.Source.SourceObj)

	source := converted.Source.SourceObj
	assert.Equal(t, "base64", source.Type)
	require.NotNil(t, source.MediaType)
	assert.Equal(t, "image/png", *source.MediaType)
	require.NotNil(t, source.Data)
	assert.Equal(t, anthropicTestRawPNGBase64, *source.Data)
}

func TestConvertToAnthropicImageBlockAcceptsCaseInsensitiveHTTPS(t *testing.T) {
	converted := convertToAnthropicImageBlock(schemas.ChatContentBlock{
		Type:           schemas.ChatContentBlockTypeImage,
		ImageURLStruct: &schemas.ChatInputImage{URL: "HTTPS://example.com/image.png"},
	})
	require.NotNil(t, converted)
	require.NotNil(t, converted.Source)
	require.NotNil(t, converted.Source.SourceObj)

	source := converted.Source.SourceObj
	assert.Equal(t, "url", source.Type)
	require.NotNil(t, source.URL)
	assert.Equal(t, "https://example.com/image.png", *source.URL)
}
