package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbockelm/swamp/internal/models"
)

// cloneTimeout is the maximum time allowed for a git clone operation.
const cloneTimeout = 5 * time.Minute

// SecureGitClone clones a private repository using the provided credential.
//
// The token is passed through an inherited file descriptor (pipe) to a
// credential helper script, so it never appears in:
//   - command-line arguments (visible via /proc/pid/cmdline to all users)
//   - environment variables (visible via /proc/pid/environ to same user)
//   - files on disk
//
// The flow:
//  1. A pipe is created; the token is written to the write end.
//  2. A tiny credential helper script is created that reads from fd 3.
//     The script contains no secrets — only "cat <&3".
//  3. git clone is started with the pipe's read end as fd 3 (via ExtraFiles)
//     and credential.helper pointing to the script.
//  4. When git needs authentication, it invokes the credential helper.
//     The helper inherits fd 3 from git and reads the token from the pipe.
//  5. The pipe is consumed (single-read), the helper script is deleted.
func SecureGitClone(ctx context.Context, cred *models.GitCloneCredential, workDir string) (string, error) {
	// Apply a timeout so a hanging clone doesn't block the worker forever.
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	repoDir := filepath.Join(workDir, "repo")

	// The git credential-helper protocol uses newlines as field delimiters
	// and has no escaping mechanism. Reject tokens that contain characters
	// which would break the protocol.
	if strings.ContainsAny(cred.Token, "\n\r\x00") {
		return "", fmt.Errorf("credential token contains invalid characters (newline or null)")
	}

	// Create a credential helper script. It contains no secrets; it reads
	// the credential response from fd 3 (an inherited pipe).
	helperPath := filepath.Join(workDir, ".git-credential-helper")
	if err := os.WriteFile(helperPath, []byte("#!/bin/sh\ncat <&3\n"), 0700); err != nil {
		return "", fmt.Errorf("write credential helper: %w", err)
	}
	defer func() { _ = os.Remove(helperPath) }()

	// Create a pipe. The write end carries the credential; the read end
	// will be inherited as fd 3 by git (and its credential-helper child).
	pr, pw, err := os.Pipe()
	if err != nil {
		return "", fmt.Errorf("create credential pipe: %w", err)
	}

	// Write the git credential-helper protocol response to the pipe.
	// Done in a goroutine because pipe writes can block if the buffer is full.
	go func() {
		defer func() { _ = pw.Close() }()
		_, _ = fmt.Fprintf(pw, "username=x-access-token\npassword=%s\n", cred.Token)
	}()

	// Build git clone command. The -c flag sets the credential helper to
	// our script. Only the *path* to the script appears on the command line,
	// never the token.
	args := []string{
		"-c", "credential.helper=" + helperPath,
		"clone", "--depth", "1",
	}
	if cred.Branch != "" {
		args = append(args, "--branch", cred.Branch)
	}
	args = append(args, cred.CloneURL, repoDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	// Pass the pipe read end as fd 3. Go's ExtraFiles[i] maps to fd 3+i
	// with close-on-exec cleared, so git inherits it. When git fork+execs
	// the credential helper, fd 3 is inherited again (git does not set
	// close-on-exec on unknown descriptors).
	cmd.ExtraFiles = []*os.File{pr}

	output, err := cmd.CombinedOutput()
	_ = pr.Close()
	if err != nil {
		return "", fmt.Errorf("git clone failed: %w: %s", err, string(output))
	}

	return repoDir, nil
}
