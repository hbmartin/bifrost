//go:build tinygo || wasm

package schemas

// WASM plugins cannot open MCP transports, but the shared request and plugin
// contracts still mention the MCP configuration types. Keep lightweight wire
// stubs here so those contracts remain buildable without pulling the native
// MCP client/server implementation into a WASM binary.

type MCPHeadersProvider interface{}

type MCPAuthType string

const (
	MCPAuthTypeNone           MCPAuthType = "none"
	MCPAuthTypeHeaders        MCPAuthType = "headers"
	MCPAuthTypeOauth          MCPAuthType = "oauth"
	MCPAuthTypePerUserOauth   MCPAuthType = "per_user_oauth"
	MCPAuthTypePerUserHeaders MCPAuthType = "per_user_headers"
	MCPAuthTypeTokenExchange  MCPAuthType = "token_exchange"
)

type MCPConnectionType string

const (
	MCPConnectionTypeHTTP      MCPConnectionType = "http"
	MCPConnectionTypeSTDIO     MCPConnectionType = "stdio"
	MCPConnectionTypeSSE       MCPConnectionType = "sse"
	MCPConnectionTypeInProcess MCPConnectionType = "inprocess"
)

type MCPClientConfig struct {
	ID             string            `json:"client_id"`
	Name           string            `json:"name"`
	ConnectionType MCPConnectionType `json:"connection_type"`
	AuthType       MCPAuthType       `json:"auth_type"`
}

type MCPClientConnectionInfo struct {
	Type               MCPConnectionType `json:"type"`
	ConnectionURL      *string           `json:"connection_url,omitempty"`
	StdioCommandString *string           `json:"stdio_command_string,omitempty"`
}

type MCPAuthRequiredError struct {
	Message string `json:"message"`
}

func (e *MCPAuthRequiredError) Error() string { return e.Message }

// MCPConfig is a stub for WASM builds.
// MCP functionality is not available in WASM plugins.
type MCPConfig struct{}
