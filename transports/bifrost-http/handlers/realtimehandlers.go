package handlers

import (
	"fmt"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/framework/encrypt"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	bfws "github.com/maximhq/bifrost/transports/bifrost-http/websocket"
)

// NewRealtimeHandlers constructs the realtime transport handlers with one
// shared immutable token codec. Codec initialization failures are returned to
// server startup instead of crashing the process from a handler constructor.
func NewRealtimeHandlers(
	client *bifrost.Bifrost,
	config *lib.Config,
	pool *bfws.Pool,
) (*WSRealtimeHandler, *WebRTCRealtimeHandler, *RealtimeClientSecretsHandler, error) {
	tokenCodec, err := newRealtimeEphemeralTokenCodec(encrypt.Key())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("initialize realtime ephemeral token codec: %w", err)
	}

	return newWSRealtimeHandler(client, config, pool, tokenCodec),
		newWebRTCRealtimeHandler(client, config, tokenCodec),
		newRealtimeClientSecretsHandler(client, config, tokenCodec),
		nil
}
