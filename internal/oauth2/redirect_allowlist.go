package oauth2

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// validateDCRRedirectURIs checks that all redirect URIs in a dynamic client
// registration request are safe. Per the MCP specification, redirect URIs
// for native/desktop clients MUST use either:
//   - Loopback addresses (http://localhost, http://127.0.0.1, http://[::1])
//     with any port and path
//   - HTTPS with an allowed domain
//   - Allowed custom URI schemes (e.g. vscode://)
//
// This prevents open-redirector attacks where a malicious DCR could point
// the authorization code to an attacker-controlled server.
func validateDCRRedirectURIs(uris []string) error {
	for _, rawURI := range uris {
		if err := validateOneRedirectURI(rawURI); err != nil {
			return fmt.Errorf("invalid redirect_uri %q: %w", rawURI, err)
		}
	}
	return nil
}

func validateOneRedirectURI(rawURI string) error {
	u, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("malformed URL")
	}

	scheme := strings.ToLower(u.Scheme)

	// Allow well-known custom URI schemes used by MCP clients.
	if isAllowedCustomScheme(scheme) {
		return nil
	}

	// Only http and https are allowed beyond custom schemes.
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q not allowed; use http://localhost, https://, or an allowed app scheme", scheme)
	}

	hostname := strings.ToLower(u.Hostname())

	// HTTPS is allowed for any host on the allowed HTTPS domains list.
	if scheme == "https" {
		if isAllowedHTTPSDomain(hostname) {
			return nil
		}
		// HTTPS to localhost is always fine.
		if isLoopback(hostname) {
			return nil
		}
		return fmt.Errorf("HTTPS redirect to %q not allowed; use localhost or a well-known MCP client domain", hostname)
	}

	// HTTP is only allowed to loopback addresses (any port is fine).
	if scheme == "http" {
		if isLoopback(hostname) {
			return nil
		}
		return fmt.Errorf("HTTP redirects are only allowed to localhost/127.0.0.1/[::1]")
	}

	return fmt.Errorf("redirect URI not allowed")
}

// isLoopback returns true if the hostname resolves to a loopback address.
func isLoopback(hostname string) bool {
	switch hostname {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	// Also check if it's a numeric loopback (e.g. 127.0.0.2).
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// allowedCustomSchemes lists URI schemes used by well-known MCP client
// applications for OAuth2 redirect callbacks.
var allowedCustomSchemes = map[string]bool{
	"vscode":              true, // VS Code
	"vscode-insiders":     true, // VS Code Insiders
	"cursor":              true, // Cursor IDE
	"windsurf":            true, // Windsurf (Codeium)
	"zed":                 true, // Zed editor
	"jetbrains":           true, // JetBrains IDEs
}

func isAllowedCustomScheme(scheme string) bool {
	return allowedCustomSchemes[scheme]
}

// allowedHTTPSDomains lists domains (and their subdomains) that are permitted
// as HTTPS redirect targets for dynamically registered clients. These are
// domains belonging to well-known MCP client vendors.
var allowedHTTPSDomains = []string{
	"vscode.dev",
	"insiders.vscode.dev",
	"github.dev",
	"github.com",
	"cursorapi.com",
	"cursor.sh",
	"codeium.com",
	"zed.dev",
	"jetbrains.com",
}

// extraAllowedHTTPSDomains holds additional domains added via configuration.
var extraAllowedHTTPSDomains []string

// SetExtraAllowedDomains adds additional HTTPS domains to the redirect URI
// allowlist. This is called at startup from config.
func SetExtraAllowedDomains(domains []string) {
	extraAllowedHTTPSDomains = domains
}

func isAllowedHTTPSDomain(hostname string) bool {
	for _, domain := range allowedHTTPSDomains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	for _, domain := range extraAllowedHTTPSDomains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return true
		}
	}
	return false
}
