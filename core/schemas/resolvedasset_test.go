package schemas

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvedInputAssetIsTransientAndDeepCopied(t *testing.T) {
	for _, block := range []ChatContentBlock{
		{
			Type: ChatContentBlockTypeImage,
			ImageURLStruct: &ChatInputImage{
				URL:           "https://example.com/image.png",
				ResolvedAsset: &ResolvedInputAsset{Data: "aW1hZ2U=", MediaType: "image/png"},
			},
		},
		{
			Type: ChatContentBlockTypeFile,
			File: &ChatInputFile{
				FileURL:       Ptr("https://example.com/file.pdf"),
				FileType:      Ptr("application/pdf"),
				ResolvedAsset: &ResolvedInputAsset{Data: "ZmlsZQ==", MediaType: "application/pdf"},
			},
		},
	} {
		copied := deepCopyChatContentBlock(block)
		if block.ImageURLStruct != nil {
			require.NotNil(t, copied.ImageURLStruct, "image clone should preserve its image fields", copied)
		} else {
			require.NotNil(t, copied.File)
			require.NotNil(t, copied.File.FileURL)
			require.NotNil(t, copied.File.FileType)
			assert.Equal(t, *block.File.FileURL, *copied.File.FileURL)
			assert.Equal(t, *block.File.FileType, *copied.File.FileType)
		}

		var originalAsset, copiedAsset *ResolvedInputAsset
		if block.ImageURLStruct != nil {
			originalAsset = block.ImageURLStruct.ResolvedAsset
			copiedAsset = copied.ImageURLStruct.ResolvedAsset
		} else {
			originalAsset = block.File.ResolvedAsset
			copiedAsset = copied.File.ResolvedAsset
		}
		require.NotNil(t, copiedAsset)
		assert.NotSame(t, originalAsset, copiedAsset)
		assert.Equal(t, *originalAsset, *copiedAsset)

		raw, err := MarshalSorted(block)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "aW1hZ2U=")
		assert.NotContains(t, string(raw), "ZmlsZQ==")
		assert.NotContains(t, string(raw), "ResolvedAsset")
	}
}
