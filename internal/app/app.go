// Package app implements cg-dev-auth's commands on top of the per-tool
// editors, independent of the CLI framework.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/chainctl"
	"github.com/billgrant/cg-dev-auth/internal/config"
	"github.com/billgrant/cg-dev-auth/internal/state"
)

// Env is everything a command needs. Tests build one around a temp dir.
type Env struct {
	Home      string
	ConfigDir string
	Config    *config.Config
	Store     *state.Store
	Chainctl  chainctl.Client
	HTTP      *http.Client
	Out       io.Writer
	Now       func() time.Time
	// YarnClassic reports whether Yarn 1.x is installed; nil means detect by
	// running `yarn --version`.
	YarnClassic func() bool
	// Exec runs an external command (uv, keyring, docker-credential-cgr...);
	// nil means run it for real. Tests substitute a fake.
	Exec func(ctx context.Context, c Cmd) ([]byte, error)
	// OS is the operating system (runtime.GOOS values); empty means the real
	// one. Tests pin it so results do not depend on the CI runner.
	OS string
	// LookPath finds a binary on PATH; nil means exec.LookPath.
	LookPath func(name string) (string, error)
}

// Cmd describes an external command.
type Cmd struct {
	Name  string
	Args  []string
	Env   []string // extra KEY=VALUE pairs on top of the current environment
	Stdin string
}

func (c Cmd) String() string { return strings.Join(append([]string{c.Name}, c.Args...), " ") }

func (e *Env) exec(ctx context.Context, c Cmd) ([]byte, error) {
	if e.Exec != nil {
		return e.Exec(ctx, c)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 2000 {
			msg = "…" + msg[len(msg)-2000:]
		}
		return out, fmt.Errorf("%s: %w\n%s", c, err, msg)
	}
	return out, nil
}

func (e *Env) lookPath(name string) (string, error) {
	if e.LookPath != nil {
		return e.LookPath(name)
	}
	return exec.LookPath(name)
}

func (e *Env) yarnClassic() bool {
	if e.YarnClassic != nil {
		return e.YarnClassic()
	}
	return detectYarnClassic()
}

func detectYarnClassic() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "yarn", "--version").Output()
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "1.")
}

// ConfigPath is the path of the config file.
func (e *Env) ConfigPath() string { return filepath.Join(e.ConfigDir, "config.yaml") }

// NewEnv builds an Env for the current user, without loading the config.
func NewEnv(out io.Writer) (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := config.Dir(home)
	store, err := state.Open(dir)
	if err != nil {
		return nil, err
	}
	return &Env{
		Home:      home,
		ConfigDir: dir,
		Store:     store,
		Chainctl:  chainctl.Client{Runner: chainctl.ExecRunner{}},
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		Out:       out,
		Now:       time.Now,
	}, nil
}

// LoadConfig loads the config into e.
func (e *Env) LoadConfig() error {
	c, err := config.Load(e.ConfigPath())
	if err != nil {
		return err
	}
	e.Config = c
	return nil
}

func (e *Env) printf(format string, args ...any) { fmt.Fprintf(e.Out, format, args...) }

// Level grades a status check.
type Level int

const (
	OK Level = iota
	Warn
	Fail
	Info
)

func (l Level) symbol() string {
	switch l {
	case OK:
		return "✓"
	case Warn:
		return "!"
	case Fail:
		return "✗"
	default:
		return "·"
	}
}

// Check is one line of `status` output.
type Check struct {
	Level  Level
	Name   string
	Detail string
}

// Ecosystem is one group of tools cg-dev-auth configures.
type Ecosystem interface {
	// Name matches the config's ecosystem name.
	Name() string
	// UsesToken reports whether Apply needs a Libraries token. Ecosystems
	// whose tools fetch credentials themselves return false.
	UsesToken() bool
	// Apply writes the tool configuration. With create false it only updates
	// configuration cg-dev-auth already manages (the refresh path) and
	// reports whether anything was managed.
	Apply(ctx context.Context, e *Env, token string, create bool) (managed bool, err error)
	// Status reports how the ecosystem is configured.
	Status(ctx context.Context, e *Env, online bool) []Check
	// Remove undoes Apply: it restores the settings cg-dev-auth replaced and
	// deletes files it created that are now empty.
	Remove(ctx context.Context, e *Env) error
}

// Ecosystems returns the implemented ecosystems in display order.
func Ecosystems() []Ecosystem {
	return []Ecosystem{&NPM{}, &Python{}, &Docker{}}
}

func enabled(e *Env) []Ecosystem {
	var out []Ecosystem
	for _, eco := range Ecosystems() {
		if e.Config.Enabled(eco.Name()) && !e.isDisabled(eco.Name()) {
			out = append(out, eco)
		}
	}
	return out
}

func (e *Env) isDisabled(name string) bool {
	for _, d := range e.Store.State().Disabled {
		if d == name {
			return true
		}
	}
	return false
}

func (e *Env) needsToken(ecos []Ecosystem) bool {
	for _, eco := range ecos {
		if eco.UsesToken() {
			return true
		}
	}
	return false
}

// InstallOptions tunes Install.
type InstallOptions struct {
	// NoAgent skips the background refresh agent.
	NoAgent bool
}

// Install configures every enabled ecosystem and, when one of them stores a
// token, the background agent that keeps it fresh.
func Install(ctx context.Context, e *Env, opts InstallOptions) error {
	ecos := enabled(e)
	var token string
	if e.needsToken(ecos) {
		t, err := e.Chainctl.LibrariesToken(ctx)
		if err != nil {
			return err
		}
		token = t
	}
	var errs []error
	for _, eco := range ecos {
		if _, err := eco.Apply(ctx, e, token, true); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", eco.Name(), err))
			continue
		}
		e.printf("✓ %s configured\n", eco.Name())
	}
	if err := e.Store.Save(); err != nil {
		errs = append(errs, err)
	}
	if e.needsToken(ecos) && !opts.NoAgent {
		if err := InstallAgent(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Refresh writes a fresh token into every managed configuration that stores
// one. It never creates configuration and never opens a browser.
func Refresh(ctx context.Context, e *Env) error {
	var ecos []Ecosystem
	for _, eco := range enabled(e) {
		if eco.UsesToken() {
			ecos = append(ecos, eco)
		}
	}
	if len(ecos) == 0 {
		return nil
	}
	token, err := e.Chainctl.LibrariesToken(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, eco := range ecos {
		managed, err := eco.Apply(ctx, e, token, false)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", eco.Name(), err))
			continue
		}
		if managed {
			exp := chainctl.TokenExpiry(token)
			if exp.IsZero() {
				e.printf("✓ %s token refreshed\n", eco.Name())
			} else {
				e.printf("✓ %s token refreshed (valid until %s)\n", eco.Name(), exp.Local().Format("15:04"))
			}
		} else {
			e.printf("· %s not installed, skipping (run: cg-dev-auth install)\n", eco.Name())
		}
	}
	if err := e.Store.Save(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Status prints the state of chainctl and every enabled ecosystem. It returns
// an error when any check failed.
func Status(ctx context.Context, e *Env, online bool) error {
	e.printf("org: %s   config: %s\n\n", e.Config.Org, e.ConfigPath())
	checks := map[string][]Check{}
	var order []string

	cc := []Check{}
	if err := e.Chainctl.Status(ctx); err != nil {
		cc = append(cc, Check{Fail, "session", err.Error()})
	} else {
		cc = append(cc, Check{OK, "session", "logged in (renews without a browser)"})
	}
	order = append(order, "chainctl")
	checks["chainctl"] = cc

	ecos, _ := selectEcosystems(e, nil)
	for _, eco := range ecos {
		name := eco.Name()
		order = append(order, name)
		if e.isDisabled(name) {
			checks[name] = []Check{{Info, "", "switched off (previous registry settings in use; `cg-dev-auth on " + name + "` to switch back)"}}
			continue
		}
		checks[name] = eco.Status(ctx, e, online)
	}
	if e.needsToken(enabled(e)) {
		order = append(order, "background")
		checks["background"] = AgentStatus(ctx, e)
	}

	failed := false
	for _, name := range order {
		e.printf("%s\n", name)
		for _, c := range checks[name] {
			if c.Level == Fail {
				failed = true
			}
			if c.Name != "" {
				e.printf("  %s %-12s %s\n", c.Level.symbol(), c.Name, c.Detail)
			} else {
				e.printf("  %s %s\n", c.Level.symbol(), c.Detail)
			}
		}
	}
	if failed {
		return errors.New("some checks failed")
	}
	return nil
}

// tokenCheck describes a stored token's remaining lifetime.
func tokenCheck(e *Env, name, token string) Check {
	if token == "" {
		return Check{Fail, name, "no token stored (run: cg-dev-auth refresh)"}
	}
	exp := chainctl.TokenExpiry(token)
	if exp.IsZero() {
		return Check{Info, name, "token present (expiry unknown)"}
	}
	left := exp.Sub(e.Now())
	if left <= 0 {
		return Check{Fail, name, fmt.Sprintf("token expired %s ago (run: cg-dev-auth refresh)", (-left).Round(time.Minute))}
	}
	if left < 5*time.Minute {
		return Check{Warn, name, fmt.Sprintf("token expires in %s", left.Round(time.Second))}
	}
	return Check{OK, name, fmt.Sprintf("token valid for %s", left.Round(time.Minute))}
}

// probe sends a GET with the given Authorization header value.
func probe(ctx context.Context, e *Env, name, url, authorization string) Check {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Check{Fail, name, err.Error()}
	}
	req.Header.Set("Authorization", authorization)
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return Check{Fail, name, err.Error()}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
		return Check{OK, name, fmt.Sprintf("GET %s → 200", url)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return Check{Fail, name, fmt.Sprintf("GET %s → %d (token rejected or org not entitled)", url, resp.StatusCode)}
	default:
		return Check{Warn, name, fmt.Sprintf("GET %s → %d", url, resp.StatusCode)}
	}
}

func (e *Env) goos() string {
	if e.OS != "" {
		return e.OS
	}
	return runtime.GOOS
}
