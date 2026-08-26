package schemas

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolvedInputAssetIsTransientAndDeepCopied(t *testing.T) {
	cacheTTL := "1h"
	cacheScope := "user"
	cachePointTTL := "5m"
	citationsEnabled := true
	breakpointMode := "explicit"
	for _, block := range []ChatContentBlock{
		{
			Type: ChatContentBlockTypeImage,
			ImageURLStruct: &ChatInputImage{
				URL:           "https://example.com/image.png",
				ResolvedAsset: &ResolvedInputAsset{Data: "aW1hZ2U=", MediaType: "image/png"},
			},
		},
		{
			Type:         ChatContentBlockTypeFile,
			CacheControl: &CacheControl{Type: CacheControlTypeEphemeral, TTL: &cacheTTL, Scope: &cacheScope},
			CachePoint:   &CachePoint{Type: "default", TTL: &cachePointTTL},
			Citations:    &Citations{Enabled: &citationsEnabled},
			PromptCacheBreakpoint: &PromptCacheBreakpoint{
				Mode: &breakpointMode,
			},
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
			require.NotNil(t, copied.CacheControl)
			require.NotNil(t, copied.CachePoint)
			require.NotNil(t, copied.Citations)
			require.NotNil(t, copied.PromptCacheBreakpoint)
			assert.NotSame(t, block.CacheControl, copied.CacheControl)
			assert.NotSame(t, block.CacheControl.TTL, copied.CacheControl.TTL)
			assert.NotSame(t, block.CacheControl.Scope, copied.CacheControl.Scope)
			assert.NotSame(t, block.CachePoint, copied.CachePoint)
			assert.NotSame(t, block.CachePoint.TTL, copied.CachePoint.TTL)
			assert.NotSame(t, block.Citations, copied.Citations)
			assert.NotSame(t, block.Citations.Enabled, copied.Citations.Enabled)
			assert.NotSame(t, block.PromptCacheBreakpoint, copied.PromptCacheBreakpoint)
			assert.NotSame(t, block.PromptCacheBreakpoint.Mode, copied.PromptCacheBreakpoint.Mode)
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
