package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/chainctl"
	"github.com/billgrant/cg-dev-auth/internal/config"
	"github.com/billgrant/cg-dev-auth/internal/state"
)

// fakeRunner answers chainctl calls from a table.
type fakeRunner struct {
	token string
	err   error
	calls []string
	onRun func(args []string)
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.onRun != nil {
		f.onRun(args)
	}
	if f.err != nil {
		return nil, f.err
	}
	if len(args) >= 2 && args[0] == "auth" && args[1] == "token" {
		return []byte(f.token + "\n"), nil
	}
	return nil, nil
}

func jwt(exp time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix()))) + ".sig"
}

func newTestEnv(t *testing.T, f *fakeRunner) (*Env, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "cg-dev-auth")
	store, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default("example.com")
	cfg.Ecosystems = []string{config.EcoNPM}
	out := &bytes.Buffer{}
	return &Env{
		Home: home, ConfigDir: dir, Config: cfg, Store: store,
		Chainctl: chainctl.Client{Runner: f}, Out: out, Now: time.Now,
		YarnClassic: func() bool { return false },
		OS:          "linux",
	}, out
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInstallRefreshStatus(t *testing.T) {
	tok1 := jwt(time.Now().Add(time.Hour))
	f := &fakeRunner{token: tok1}
	e, out := newTestEnv(t, f)
	ctx := context.Background()

	orig := "save-exact=true\n"
	if err := os.WriteFile(filepath.Join(e.Home, ".npmrc"), []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	npmrcData := read(t, filepath.Join(e.Home, ".npmrc"))
	if !strings.HasPrefix(npmrcData, orig) || !strings.Contains(npmrcData, ":_authToken="+tok1) {
		t.Fatalf(".npmrc:\n%s", npmrcData)
	}
	if !strings.Contains(read(t, filepath.Join(e.Home, ".yarnrc.yml")), tok1) {
		t.Fatal("token missing from .yarnrc.yml")
	}
	if fi, _ := os.Stat(filepath.Join(e.Home, ".npmrc")); fi.Mode().Perm() != 0o600 {
		t.Fatalf(".npmrc mode %v", fi.Mode().Perm())
	}

	// The original .npmrc was backed up; .yarnrc.yml is recorded as new.
	st := e.Store.State()
	rec := st.Files[filepath.Join(e.Home, ".npmrc")]
	if rec == nil || !rec.Existed || read(t, rec.Backup) != orig {
		t.Fatalf("npmrc backup record: %+v", rec)
	}
	if r := st.Files[filepath.Join(e.Home, ".yarnrc.yml")]; r == nil || r.Existed {
		t.Fatalf("yarnrc record: %+v", r)
	}
	if v, ok := e.Store.Prev(prevYarnServer); !ok || v != nil {
		t.Fatalf("prev yarn server = %v, %v", v, ok)
	}

	// Refresh swaps in the new token and nothing else.
	tok2 := jwt(time.Now().Add(2 * time.Hour))
	f.token = tok2
	if err := Refresh(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(e.Home, ".npmrc")); got != strings.Replace(npmrcData, tok1, tok2, 1) {
		t.Fatalf("refresh changed more than the token:\n%s", got)
	}

	out.Reset()
	if err := Status(ctx, e, false); err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "token valid for") {
		t.Fatalf("status output:\n%s", out)
	}

	// State survives a reload.
	s2, err := state.Open(e.ConfigDir)
	if err != nil || len(s2.State().Files) != 2 {
		t.Fatalf("reloaded state: %+v %v", s2.State(), err)
	}
}

func TestRefreshDoesNotCreateConfig(t *testing.T) {
	e, out := newTestEnv(t, &fakeRunner{token: jwt(time.Now().Add(time.Hour))})
	if err := Refresh(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".npmrc", ".yarnrc.yml"} {
		if _, err := os.Stat(filepath.Join(e.Home, f)); !os.IsNotExist(err) {
			t.Errorf("%s created by refresh", f)
		}
	}
	if !strings.Contains(out.String(), "not installed") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestRefreshFailureLeavesFilesAlone(t *testing.T) {
	tok := jwt(time.Now().Add(time.Hour))
	f := &fakeRunner{token: tok}
	e, _ := newTestEnv(t, f)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	before := read(t, filepath.Join(e.Home, ".npmrc"))

	f.err = errors.New("refresh token expired")
	err := Refresh(ctx, e)
	if !errors.Is(err, chainctl.ErrNotLoggedIn) {
		t.Fatalf("err = %v", err)
	}
	if read(t, filepath.Join(e.Home, ".npmrc")) != before {
		t.Fatal(".npmrc changed after failed refresh")
	}
}

func TestStatusReportsExpiredToken(t *testing.T) {
	f := &fakeRunner{token: jwt(time.Now().Add(-10 * time.Minute))}
	e, out := newTestEnv(t, f)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if err := Status(ctx, e, false); err == nil {
		t.Fatalf("expected failure:\n%s", out)
	}
	if !strings.Contains(out.String(), "token expired") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestNamedRepoURL(t *testing.T) {
	c := config.Default("example.com")
	c.LibrariesRepo = "prod"
	if got := c.JavaScriptRegistry(); got != "https://libraries.cgr.dev/javascript/~/example.com/prod/" {
		t.Fatal(got)
	}
	if got := c.PythonIndexURL(); got != "https://libraries.cgr.dev/python/~/example.com/prod/simple/" {
		t.Fatal(got)
	}
}

func TestRefreshKeepsAlwaysAuthChosenAtInstall(t *testing.T) {
	f := &fakeRunner{token: jwt(time.Now().Add(time.Hour))}
	e, _ := newTestEnv(t, f)
	e.YarnClassic = func() bool { return true }
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	// The background agent may not find yarn; refresh must not drop the line.
	e.YarnClassic = func() bool { return false }
	f.token = jwt(time.Now().Add(2 * time.Hour))
	if err := Refresh(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(e.Home, ".npmrc")), ":always-auth=true") {
		t.Fatal("refresh dropped always-auth")
	}
}

func TestTokenExpiryTwoSegmentFormat(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	enc := base64.RawURLEncoding.EncodeToString
	tok := "opaque-prefix!." + enc([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	if got := chainctl.TokenExpiry(tok); !got.Equal(exp) {
		t.Fatalf("got %v want %v", got, exp)
	}
}

func TestRefreshAddsAlwaysAuthWhenYarnClassicAppears(t *testing.T) {
	f := &fakeRunner{token: jwt(time.Now().Add(time.Hour))}
	e, out := newTestEnv(t, f)
	ctx := context.Background()
	if err := Install(ctx, e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.Home, ".npmrc")
	if strings.Contains(read(t, path), "always-auth") {
		t.Fatal("always-auth written without Yarn 1.x")
	}
	// The user installs Yarn 1.x afterwards: status warns, refresh fixes it.
	e.YarnClassic = func() bool { return true }
	out.Reset()
	_ = Status(ctx, e, false)
	if !strings.Contains(out.String(), "lacks always-auth") {
		t.Fatalf("status did not warn:\n%s", out)
	}
	if err := Refresh(ctx, e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, path), ":always-auth=true") {
		t.Fatal("refresh did not add always-auth")
	}
}
