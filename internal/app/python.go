package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/billgrant/cg-dev-auth/internal/fsutil"
	"github.com/billgrant/cg-dev-auth/internal/pipconf"
	"github.com/billgrant/cg-dev-auth/internal/uvtoml"
)

// Python configures pip and uv to use Chainguard Libraries for Python via
// Chainguard's keyring backend (keyrings-chainguard-libraries), which asks
// chainctl for a short-lived token on demand. No token is written to disk.
type Python struct{}

const (
	// keyringUser is the username the backend answers with and the one the
	// keyring subprocess provider needs in the index URL.
	keyringUser       = "_token"
	keyringPackage    = "keyrings-chainguard-libraries"
	keyringBackendTag = "chainctl_auth"

	prevPipIndex   = "python.pip.index-url"
	prevPipKeyring = "python.pip.keyring-provider"
	// prevKeyringInstaller records which tool (uv or pipx) cg-dev-auth used
	// to install keyring, so uninstall removes only what it added.
	prevKeyringInstaller = "python.keyring.installed-by"
)

func (*Python) Name() string    { return "python" }
func (*Python) UsesToken() bool { return false }

// pipConfPath follows pip's choice of user config file: on macOS
// ~/Library/Application Support/pip/pip.conf when that directory exists,
// otherwise $XDG_CONFIG_HOME/pip/pip.conf (~/.config/pip/pip.conf).
func pipConfPath(e *Env) string {
	if runtime.GOOS == "darwin" {
		dir := filepath.Join(e.Home, "Library", "Application Support", "pip")
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, "pip.conf")
		}
	}
	return filepath.Join(xdgConfigHome(e), "pip", "pip.conf")
}

// uvTomlPath is uv's user config file, $XDG_CONFIG_HOME/uv/uv.toml on macOS
// and Linux alike.
func uvTomlPath(e *Env) string { return filepath.Join(xdgConfigHome(e), "uv", "uv.toml") }

func xdgConfigHome(e *Env) string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(e.Home, ".config")
}

// indexURLWithUser inserts the keyring username into the index URL.
func indexURLWithUser(url string) string {
	return strings.Replace(url, "https://", "https://"+keyringUser+"@", 1)
}

func (p *Python) Apply(ctx context.Context, e *Env, _ string, create bool) (bool, error) {
	if !create {
		return false, nil // nothing to refresh: the keyring backend fetches tokens itself
	}
	if err := p.ensureKeyring(ctx, e); err != nil {
		return false, err
	}
	url := indexURLWithUser(e.Config.PythonIndexURL())
	if err := p.applyPip(e, url); err != nil {
		return false, err
	}
	if err := p.applyUV(e, url); err != nil {
		return false, err
	}
	return true, nil
}

func (*Python) keyringReady(ctx context.Context, e *Env) bool {
	out, err := e.exec(ctx, Cmd{Name: "keyring", Args: []string{"--list-backends"}})
	return err == nil && strings.Contains(string(out), keyringBackendTag)
}

// ensureKeyring installs the keyring CLI with Chainguard's backend as an
// isolated tool. The packages come from Chainguard Libraries (credentials
// passed in the environment, never on the command line); PyPI is used only if
// that fails.
func (p *Python) ensureKeyring(ctx context.Context, e *Env) error {
	if p.keyringReady(ctx, e) {
		return nil
	}
	cgIndex := e.Config.PythonIndexURL()
	var attempts []error
	token, err := e.Chainctl.LibrariesToken(ctx)
	if err != nil {
		return err
	}
	withToken := strings.Replace(cgIndex, "https://", "https://"+keyringUser+":"+token+"@", 1)

	var installer string
	var install func(index string) error
	switch {
	case e.has("uv"):
		installer = "uv"
		install = func(index string) error {
			_, err := e.exec(ctx, Cmd{
				Name: "uv",
				Args: []string{"tool", "install", "--force", "--no-config", "--keyring-provider", "disabled", "keyring", "--with", keyringPackage},
				Env:  []string{"UV_DEFAULT_INDEX=" + index},
			})
			return err
		}
	case e.has("pipx"):
		installer = "pipx"
		install = func(index string) error {
			env := []string{"PIP_INDEX_URL=" + index, "PIP_KEYRING_PROVIDER=disabled"}
			if _, err := e.exec(ctx, Cmd{Name: "pipx", Args: []string{"install", "--force", "keyring"}, Env: env}); err != nil {
				return err
			}
			_, err := e.exec(ctx, Cmd{Name: "pipx", Args: []string{"inject", "keyring", keyringPackage}, Env: env})
			return err
		}
	default:
		return errors.New("need uv or pipx to install the keyring backend (brew install uv)")
	}

	e.printf("· installing keyring + %s with %s\n", keyringPackage, installer)
	if err := install(withToken); err != nil {
		attempts = append(attempts, fmt.Errorf("from Chainguard: %w", redact(err, token)))
		e.printf("! could not install from Chainguard Libraries, trying PyPI\n")
		if err := install("https://pypi.org/simple/"); err != nil {
			attempts = append(attempts, fmt.Errorf("from PyPI: %w", err))
		}
	}
	if len(attempts) < 2 {
		e.Store.RememberPrev(prevKeyringInstaller, &installer)
	}

	if p.keyringReady(ctx, e) {
		e.printf("✓ keyring backend installed\n")
		return nil
	}
	if len(attempts) > 0 {
		return fmt.Errorf("install keyring backend: %w", errors.Join(attempts...))
	}
	return errors.New("keyring installed but not on PATH: run `uv tool update-shell` (or `pipx ensurepath`) and open a new shell")
}

func redact(err error, secret string) error {
	if secret == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), secret, "<token>"))
}

func (e *Env) has(bin string) bool {
	_, err := e.lookPath(bin)
	return err == nil
}

func (*Python) applyPip(e *Env, url string) error {
	path := pipConfPath(e)
	data, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return err
	}
	out, prevIdx := pipconf.Set(string(data), "index-url", url)
	out, prevKr := pipconf.Set(out, "keyring-provider", "subprocess")
	if out == string(data) {
		return nil
	}
	if err := e.Store.EnsureBackup(path); err != nil {
		return err
	}
	e.Store.RememberPrev(prevPipIndex, prevIdx)
	e.Store.RememberPrev(prevPipKeyring, prevKr)
	return fsutil.WriteFileAtomic(path, []byte(out), 0o644)
}

func (*Python) applyUV(e *Env, url string) error {
	path := uvTomlPath(e)
	data, _, err := fsutil.ReadFileIfExists(path)
	if err != nil {
		return err
	}
	out, err := uvtoml.Apply(string(data), uvtoml.Settings{IndexURL: url})
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if out == string(data) {
		return nil
	}
	if err := e.Store.EnsureBackup(path); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, []byte(out), 0o644)
}

func (p *Python) Status(ctx context.Context, e *Env, online bool) []Check {
	var checks []Check
	url := indexURLWithUser(e.Config.PythonIndexURL())

	if p.keyringReady(ctx, e) {
		checks = append(checks, Check{OK, "keyring", "Chainguard backend installed"})
	} else {
		checks = append(checks, Check{Fail, "keyring", "keyring CLI with " + keyringPackage + " not found on PATH (run: cg-dev-auth install)"})
	}

	// pip
	path := pipConfPath(e)
	data, _, err := fsutil.ReadFileIfExists(path)
	switch {
	case err != nil:
		checks = append(checks, Check{Fail, "pip.conf", err.Error()})
	default:
		idx, _ := pipconf.Get(string(data), "index-url")
		kr, _ := pipconf.Get(string(data), "keyring-provider")
		switch {
		case idx == url && kr == "subprocess":
			checks = append(checks, Check{OK, "pip.conf", "index " + e.Config.PythonIndexURL()})
		case idx == "":
			checks = append(checks, Check{Fail, "pip.conf", "not configured (run: cg-dev-auth install)"})
		default:
			checks = append(checks, Check{Warn, "pip.conf", fmt.Sprintf("index-url=%s keyring-provider=%s (run: cg-dev-auth install)", idx, kr)})
		}
		if extra, ok := pipconf.Get(string(data), "extra-index-url"); ok {
			checks = append(checks, Check{Warn, "", "pip.conf sets extra-index-url (" + oneLine(extra) + "): pip may pick packages from it over Chainguard"})
		}
	}
	legacy := filepath.Join(e.Home, ".pip", "pip.conf")
	if d, ok, _ := fsutil.ReadFileIfExists(legacy); ok {
		if v, ok := pipconf.Get(string(d), "index-url"); ok {
			checks = append(checks, Check{Info, "", fmt.Sprintf("%s also sets index-url=%s (overridden by %s)", legacy, v, path)})
		}
	}
	for _, k := range []string{"PIP_INDEX_URL", "PIP_EXTRA_INDEX_URL", "PIP_KEYRING_PROVIDER"} {
		if v := os.Getenv(k); v != "" {
			checks = append(checks, Check{Warn, "", fmt.Sprintf("$%s is set and overrides pip.conf", k)})
		}
	}

	// uv
	upath := uvTomlPath(e)
	udata, _, err := fsutil.ReadFileIfExists(upath)
	if err != nil {
		checks = append(checks, Check{Fail, "uv.toml", err.Error()})
	} else if info, err := uvtoml.Parse(string(udata)); err != nil {
		checks = append(checks, Check{Fail, "uv.toml", err.Error()})
	} else {
		ix, ok := info.Managed()
		switch {
		case !ok:
			checks = append(checks, Check{Fail, "uv.toml", "not configured (run: cg-dev-auth install)"})
		case ix.URL != url || info.KeyringProvider != "subprocess":
			checks = append(checks, Check{Warn, "uv.toml", fmt.Sprintf("index %s keyring-provider=%s (run: cg-dev-auth install)", ix.URL, info.KeyringProvider)})
		default:
			checks = append(checks, Check{OK, "uv.toml", "index " + e.Config.PythonIndexURL()})
		}
		for _, d := range info.CompetingDefaults() {
			checks = append(checks, Check{Warn, "", "uv.toml marks another index default: " + d})
		}
		if info.IndexURL != "" || len(info.ExtraIndexURL) > 0 {
			checks = append(checks, Check{Warn, "", "uv.toml sets index-url/extra-index-url, which compete with the Chainguard index"})
		}
	}
	for _, k := range []string{"UV_INDEX_URL", "UV_DEFAULT_INDEX", "UV_INDEX", "UV_EXTRA_INDEX_URL", "UV_KEYRING_PROVIDER"} {
		if v := os.Getenv(k); v != "" {
			checks = append(checks, Check{Warn, "", fmt.Sprintf("$%s is set and overrides uv.toml", k)})
		}
	}
	checks = append(checks, Check{Info, "", "projects with [[tool.uv.index]] in pyproject.toml or a requirements file with --index-url override these settings"})

	if online {
		out, err := e.exec(ctx, Cmd{Name: "keyring", Args: []string{"get", e.Config.PythonIndexURL(), keyringUser}})
		tok := strings.TrimSpace(string(out))
		if err != nil || tok == "" {
			checks = append(checks, Check{Fail, "registry", "keyring returned no token (is chainctl logged in?)"})
		} else {
			basic := base64.StdEncoding.EncodeToString([]byte(keyringUser + ":" + tok))
			checks = append(checks, probe(ctx, e, "registry", e.Config.PythonIndexURL()+"requests/", "Basic "+basic))
		}
	}
	return checks
}

func oneLine(s string) string { return strings.ReplaceAll(s, "\n", ", ") }
