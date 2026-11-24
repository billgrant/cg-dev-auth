package npmrc

import (
	"strings"
	"testing"
)

const reg = "https://libraries.cgr.dev/javascript/"

func TestApplyPreservesUserContentAndIsIdempotent(t *testing.T) {
	orig := "; my settings\nsave-exact=true\n@acme:registry=https://npm.acme.example/\n"
	out, err := Apply(orig, Settings{Registry: reg, Token: "tok1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, orig) {
		t.Fatalf("user content not preserved verbatim:\n%s", out)
	}
	for _, want := range []string{
		"registry=" + reg + "\n",
		"//libraries.cgr.dev/javascript/:_authToken=tok1\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	again, err := Apply(out, Settings{Registry: reg, Token: "tok1"})
	if err != nil || again != out {
		t.Fatalf("not idempotent (err=%v):\n%s\nvs\n%s", err, out, again)
	}
	back, err := Remove(out)
	if err != nil || back != orig {
		t.Fatalf("Remove did not restore original (err=%v):\n%q", err, back)
	}
}

func TestApplyMovesBlockToEndAndReplacesToken(t *testing.T) {
	start, _ := Apply("", Settings{Registry: reg, Token: "old"})
	withLater := start + "fund=false\n"
	out, err := Apply(withLater, Settings{Registry: reg, Token: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "old") {
		t.Fatalf("old token remains:\n%s", out)
	}
	if !strings.HasPrefix(out, "fund=false\n"+BeginMarker) || !strings.HasSuffix(out, EndMarker+"\n") {
		t.Fatalf("block not moved to end:\n%s", out)
	}
	if n := strings.Count(out, BeginMarker); n != 1 {
		t.Fatalf("%d blocks", n)
	}
}

func TestApplyNoTrailingNewline(t *testing.T) {
	out, err := Apply("save-exact=true", Settings{Registry: reg, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "save-exact=true\n"+BeginMarker) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestRead(t *testing.T) {
	out, _ := Apply("x=1\n", Settings{Registry: reg, Token: "abc.def.ghi"})
	s, ok, err := Read(out)
	if err != nil || !ok || s.Registry != reg || s.Token != "abc.def.ghi" {
		t.Fatalf("Read = %+v %v %v", s, ok, err)
	}
	if _, ok, _ := Read("x=1\n"); ok {
		t.Fatal("found block in unmanaged file")
	}
}

func TestBrokenMarkersAreErrors(t *testing.T) {
	for _, c := range []string{BeginMarker + "\nregistry=x\n", "registry=x\n" + EndMarker + "\n"} {
		if _, err := Apply(c, Settings{Registry: reg}); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestConflicts(t *testing.T) {
	content := "registry=https://registry.npmjs.org/\n# registry=commented\n//libraries.cgr.dev/javascript/:_auth=xyz\n"
	out, _ := Apply(content, Settings{Registry: reg, Token: "t"})
	got := Conflicts(out, reg)
	if len(got) != 2 || got[0] != "registry" || got[1] != "//libraries.cgr.dev/javascript/:_auth" {
		t.Fatalf("Conflicts = %v", got)
	}
}

func TestAlwaysAuthOnlyWhenAsked(t *testing.T) {
	without, _ := Apply("", Settings{Registry: reg, Token: "t"})
	if strings.Contains(without, "always-auth") {
		t.Fatalf("always-auth written by default:\n%s", without)
	}
	with, _ := Apply("", Settings{Registry: reg, Token: "t", AlwaysAuth: true})
	if !strings.Contains(with, "//libraries.cgr.dev/javascript/:always-auth=true\n") {
		t.Fatalf("always-auth missing:\n%s", with)
	}
	if s, _, _ := Read(with); !s.AlwaysAuth {
		t.Fatal("Read lost AlwaysAuth")
	}
}
