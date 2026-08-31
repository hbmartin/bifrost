//go:build tinygo || wasm

package schemas

// LLMPluginShortCircuit represents a plugin's decision to short-circuit the normal flow.
// It can contain either a response (success short-circuit), a stream (streaming short-circuit), or an error (error short-circuit).
// Streams are not supported in WASM plugins.
type LLMPluginShortCircuit struct {
	Response *BifrostResponse // If set, short-circuit with this response (skips provider call)
	Error    *BifrostError    // If set, short-circuit with this error (can set AllowFallbacks field)
}

// MCPPluginShortCircuit keeps the shared plugin interface buildable for WASM.
// MCP execution itself is unavailable in WASM plugins.
type MCPPluginShortCircuit struct {
	Response *BifrostMCPResponse
	Error    *BifrostError
}

// MCPConnectionShortCircuit is the connection-hook counterpart retained in
// the WASM-facing interface contract.
type MCPConnectionShortCircuit struct {
	Response *BifrostMCPConnectResponse
	Error    *BifrostError
}

// PluginShortCircuit is the legacy name for LLMPluginShortCircuit (v1.3.x compatibility).
// Deprecated: Use LLMPluginShortCircuit instead.
type PluginShortCircuit = LLMPluginShortCircuit
