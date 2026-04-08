package agent

import "os"

// resolveShell returns a POSIX shell path for child process environments.
// Claude CLI requires the SHELL environment variable to be set.
func resolveShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	// Fallback to /bin/sh which is guaranteed to exist on POSIX systems.
	return "/bin/sh"
}
