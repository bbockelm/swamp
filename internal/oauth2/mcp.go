package oauth2

import (
	"encoding/json"
	"net/http"
)

// MCPResourceMetadata returns the MCP resource metadata document.
// This tells MCP clients (like VS Code) how to authenticate.
// See: https://spec.modelcontextprotocol.io/specification/2025-03-26/basic/authorization/
func (h *Handlers) MCPResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.issuerURL

	doc := map[string]any{
		"resource":                 base,
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"mcp", "openid", "profile", "offline_access"},
		"bearer_methods_supported": []string{"header"},
		"resource_documentation":   base + "/api/v1/docs",
		"mcp_endpoint":             base + "/mcp",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(doc)
}

// MCPAuthMetadata returns the OAuth2 Authorization Server Metadata for MCP.
// This is the entry point MCP clients use to discover OAuth2 endpoints.
// See: RFC 8414 and MCP spec authorization section.
func (h *Handlers) MCPAuthMetadata(w http.ResponseWriter, r *http.Request) {
	base := h.issuerURL

	doc := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"revocation_endpoint":                   base + "/oauth/revoke",
		"introspection_endpoint":                base + "/oauth/introspect",
		"registration_endpoint":                 base + "/oauth/register",
		"jwks_uri":                              base + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"scopes_supported":                      []string{"openid", "profile", "mcp", "offline_access"},
		"code_challenge_methods_supported":      []string{"S256"},
		"service_documentation":                 base + "/api/v1/docs",
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(doc)
}
