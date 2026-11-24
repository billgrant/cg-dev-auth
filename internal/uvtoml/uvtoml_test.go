package uvtoml

import (
	"strings"
	"testing"
)

const url = "https://_token@libraries.cgr.dev/python/simple/"

func TestApplyEmpty(t *testing.T) {
	out, err := Apply("", Settings{IndexURL: url})
	if err != nil {
		t.Fatal(err)
	}
	info, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	ix, ok := info.Managed()
	if info.KeyringProvider != "subprocess" || !ok || ix.URL != url || !ix.Default || ix.Authenticate != "always" {
		t.Fatalf("info = %+v\n%s", info, out)
	}
	back, _ := Remove(out)
	if back != "" {
		t.Fatalf("Remove = %q", back)
	}
}

func TestApplyKeepsUserTopLevelKeysTopLevel(t *testing.T) {
	orig := "# user settings\nnative-tls = true\nkeyring-provider = \"disabled\"\n\n[pip]\ncompile-bytecode = true\n"
	out, err := Apply(orig, Settings{IndexURL: url})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, disabledTag+`keyring-provider = "disabled"`) {
		t.Fatalf("competing key not disabled:\n%s", out)
	}
	info, err := Parse(out)
	if err != nil || info.KeyringProvider != "subprocess" {
		t.Fatalf("parse: %+v %v", info, err)
	}
	// Idempotent.
	again, err := Apply(out, Settings{IndexURL: url})
	if err != nil || again != out {
		t.Fatalf("not idempotent:\n%s\nvs\n%s", out, again)
	}
	back, err := Remove(out)
	if err != nil || back != orig {
		t.Fatalf("Remove:\n%q\nwant\n%q", back, orig)
	}
}

func TestCompetingDefaults(t *testing.T) {
	orig := "[[index]]\nname = \"corp\"\nurl = \"https://corp.example/simple\"\ndefault = true\n"
	out, err := Apply(orig, Settings{IndexURL: url})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := Parse(out)
	if d := info.CompetingDefaults(); len(d) != 1 || d[0] != "https://corp.example/simple" {
		t.Fatalf("CompetingDefaults = %v", d)
	}
}

func TestInvalidInputIsRejected(t *testing.T) {
	if _, err := Apply("this is = = not toml\n", Settings{IndexURL: url}); err == nil {
		t.Fatal("expected error")
	}
}
