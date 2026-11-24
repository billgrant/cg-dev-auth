package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billgrant/cg-dev-auth/internal/config"
	"github.com/billgrant/cg-dev-auth/internal/dockercfg"
	"github.com/billgrant/cg-dev-auth/internal/pipconf"
	"github.com/billgrant/cg-dev-auth/internal/uvtoml"
)

func TestPythonInstall(t *testing.T) {
	tok := jwt(time.Now().Add(time.Hour))
	e, _ := newTestEnv(t, &fakeRunner{token: tok})
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(e.Home, ".config"))
	e.Config.Ecosystems = []string{config.EcoPython}

	installed := false
	var uvCmd Cmd
	e.LookPath = func(name string) (string, error) {
		if name == "uv" {
			return "/usr/bin/uv", nil
		}
		return "", errors.New("not found")
	}
	e.Exec = func(_ context.Context, c Cmd) ([]byte, error) {
		switch {
		case c.Name == "keyring" && c.Args[0] == "--list-backends":
			if installed {
				return []byte("keyrings.chainctl_auth.ChainctlAuth (priority: 9)\n"), nil
			}
			return nil, errors.New("keyring: not found")
		case c.Name == "uv":
			uvCmd = c
			installed = true
			return nil, nil
		}
		return nil, errors.New("unexpected " + c.String())
	}

	if err := Install(context.Background(), e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uvCmd.String(), tok) {
		t.Fatal("token passed on the command line")
	}
	if len(uvCmd.Env) != 1 || !strings.Contains(uvCmd.Env[0], "UV_DEFAULT_INDEX=https://_token:"+tok+"@libraries.cgr.dev/python/simple/") {
		t.Fatalf("uv env = %v", uvCmd.Env)
	}

	want := "https://_token@libraries.cgr.dev/python/simple/"
	pip := read(t, filepath.Join(e.Home, ".config", "pip", "pip.conf"))
	if v, _ := pipconf.Get(pip, "index-url"); v != want {
		t.Fatalf("pip.conf:\n%s", pip)
	}
	if strings.Contains(pip, tok) {
		t.Fatal("token written to pip.conf")
	}
	info, err := uvtoml.Parse(read(t, filepath.Join(e.Home, ".config", "uv", "uv.toml")))
	if ix, ok := info.Managed(); err != nil || !ok || ix.URL != want || info.KeyringProvider != "subprocess" {
		t.Fatalf("uv.toml: %+v %v", info, err)
	}
	// Refresh does nothing for Python.
	if err := Refresh(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

func TestDockerInstall(t *testing.T) {
	f := &fakeRunner{}
	e, out := newTestEnv(t, f)
	dockerDir := filepath.Join(e.Home, ".docker")
	t.Setenv("DOCKER_CONFIG", dockerDir)
	e.Config.Ecosystems = []string{config.EcoDocker}
	path := filepath.Join(dockerDir, "config.json")
	os.MkdirAll(dockerDir, 0o755)
	os.WriteFile(path, []byte(`{"credsStore":"osxkeychain","credHelpers":{"uploads.cgr.dev":"other"}}`), 0o644)

	f.onRun = func(args []string) {
		if strings.Join(args, " ") != "auth configure-docker" {
			return
		}
		data, _ := os.ReadFile(path)
		cgr := "cgr"
		data, _ = dockercfg.SetCredHelpers(data, map[string]*string{"cgr.dev": &cgr, "uploads.cgr.dev": &cgr})
		os.WriteFile(path, data, 0o644)
	}
	if err := Install(context.Background(), e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if v, ok := e.Store.Prev(prevDockerPrefix + "cgr.dev"); !ok || v != nil {
		t.Fatalf("prev cgr.dev = %v %v", v, ok)
	}
	if v, ok := e.Store.Prev(prevDockerPrefix + "uploads.cgr.dev"); !ok || v == nil || *v != "other" {
		t.Fatalf("prev uploads = %v %v", v, ok)
	}

	// Second install is a no-op (does not call chainctl again).
	n := len(f.calls)
	if err := Install(context.Background(), e, InstallOptions{NoAgent: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != n {
		t.Fatalf("chainctl called again: %v", f.calls[n:])
	}

	e.LookPath = func(string) (string, error) { return "/usr/bin/docker-credential-cgr", nil }
	out.Reset()
	if err := Status(context.Background(), e, false); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "credential helper for cgr.dev, uploads.cgr.dev") {
		t.Fatalf("status:\n%s", out)
	}
}
