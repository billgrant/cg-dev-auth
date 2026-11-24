package yarnrc

import (
	"strings"
	"testing"
)

const reg = "https://libraries.cgr.dev/javascript/"

func TestApplyEmpty(t *testing.T) {
	out, prev, err := Apply(nil, Settings{Registry: reg, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if prev != nil {
		t.Fatalf("prev = %q", *prev)
	}
	want := `npmRegistryServer: "https://libraries.cgr.dev/javascript"
npmRegistries:
  "//libraries.cgr.dev/javascript":
    npmAlwaysAuth: true
    npmAuthToken: "tok"
`
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestApplyPreservesCommentsAndOtherKeys(t *testing.T) {
	orig := `# global yarn settings
nodeLinker: node-modules # keep this
npmRegistryServer: "https://registry.yarnpkg.com"
npmRegistries:
  "//npm.acme.example":
    npmAuthToken: "acme"
`
	out, prev, err := Apply([]byte(orig), Settings{Registry: reg, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if prev == nil || *prev != "https://registry.yarnpkg.com" {
		t.Fatalf("prev = %v", prev)
	}
	s := string(out)
	for _, want := range []string{"# global yarn settings", "nodeLinker: node-modules # keep this", `"//npm.acme.example":`, `npmAuthToken: "acme"`, `npmAuthToken: "tok"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	server, token, err := Read(out, reg)
	if err != nil || server != "https://libraries.cgr.dev/javascript" || token != "tok" {
		t.Fatalf("Read = %q %q %v", server, token, err)
	}

	// Refresh replaces only the token.
	out2, _, err := Apply(out, Settings{Registry: reg, Token: "tok2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Replace(string(out), `"tok"`, `"tok2"`, 1) != string(out2) {
		t.Fatalf("refresh changed more than the token:\n%s", out2)
	}

	back, err := Remove(out2, reg, prev)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != orig {
		t.Fatalf("Remove did not restore:\n%s\nwant:\n%s", back, orig)
	}
}

func TestRemoveFromEmptyOrigin(t *testing.T) {
	out, prev, _ := Apply(nil, Settings{Registry: reg, Token: "tok"})
	back, err := Remove(out, reg, prev)
	if err != nil || len(back) != 0 {
		t.Fatalf("got %q, %v", back, err)
	}
}

func TestRegistryKey(t *testing.T) {
	if got := RegistryKey("https://libraries.cgr.dev/javascript/~/acme.com/prod/"); got != "//libraries.cgr.dev/javascript/~/acme.com/prod" {
		t.Fatal(got)
	}
}
