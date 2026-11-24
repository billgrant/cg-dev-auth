// Package chainctl wraps the chainctl commands cg-dev-auth needs.
package chainctl

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LibrariesAudience is the token audience Chainguard Libraries accepts.
const LibrariesAudience = "libraries.cgr.dev"

// Runner runs chainctl with args and returns stdout. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, args ...string) (stdout []byte, err error)
}

// ExecRunner runs the real chainctl binary.
type ExecRunner struct {
	// Path to chainctl; "chainctl" (looked up on PATH) when empty.
	Path string
}

// Run implements Runner. Stdin is not connected and stderr is captured, so
// chainctl never starts an interactive browser or device login: when the
// cached refresh token cannot be renewed, the command fails instead.
func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	path := r.Path
	if path == "" {
		path = "chainctl"
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("chainctl not found on PATH: install it from https://edu.chainguard.dev/chainguard/chainctl-usage/how-to-install-chainctl/")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("chainctl %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// Client is a typed wrapper around a Runner.
type Client struct {
	Runner Runner
	// Timeout bounds each chainctl invocation; 60s when zero.
	Timeout time.Duration
}

func (c Client) run(ctx context.Context, args ...string) ([]byte, error) {
	t := c.Timeout
	if t == 0 {
		t = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	return c.Runner.Run(ctx, args...)
}

// ErrNotLoggedIn means chainctl has no session it can renew without a login.
var ErrNotLoggedIn = errors.New("chainctl session expired or missing: run `cg-dev-auth login` (or `chainctl auth login`)")

// LibrariesToken returns a short-lived token for libraries.cgr.dev. chainctl
// renews it from the cached refresh token when needed, without a browser.
func (c Client) LibrariesToken(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "auth", "token", "--audience="+LibrariesAudience)
	if err != nil {
		return "", fmt.Errorf("%w\n  (%v)", ErrNotLoggedIn, err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" || strings.ContainsAny(tok, " \t\r\n") {
		return "", errors.New("chainctl auth token returned an empty or malformed token")
	}
	return tok, nil
}

// Status reports whether chainctl has a valid session it can renew.
func (c Client) Status(ctx context.Context) error {
	if _, err := c.run(ctx, "auth", "status"); err != nil {
		return fmt.Errorf("%w\n  (%v)", ErrNotLoggedIn, err)
	}
	return nil
}

// TokenExpiry reads the exp claim of a token without verifying it. It looks
// at every dot-separated segment for a base64url JSON object with exp, which
// covers standard three-part JWTs and other dotted formats. It returns the
// zero time when no segment carries exp.
func TokenExpiry(tok string) time.Time {
	for _, seg := range strings.Split(tok, ".") {
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(seg, "="))
		if err != nil {
			continue
		}
		var claims struct {
			Exp float64 `json:"exp"`
		}
		if json.Unmarshal(payload, &claims) == nil && claims.Exp != 0 {
			return time.Unix(int64(claims.Exp), 0)
		}
	}
	return time.Time{}
}
