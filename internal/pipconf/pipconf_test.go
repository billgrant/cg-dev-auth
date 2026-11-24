package pipconf

import "testing"

func TestSetInsertsIntoExistingGlobalAndRestores(t *testing.T) {
	orig := "# my pip\n[global]\ntimeout = 60\nindex_url = https://pypi.org/simple\nextra-index-url =\n    https://a.example/simple\n    https://b.example/simple\n\n[install]\nuser = true\n"
	out, prev := Set(orig, "index-url", "https://_token@libraries.cgr.dev/python/simple/")
	if prev == nil || *prev != "https://pypi.org/simple" {
		t.Fatalf("prev = %v", prev)
	}
	out, prevKr := Set(out, "keyring-provider", "subprocess")
	if prevKr != nil {
		t.Fatalf("prevKr = %q", *prevKr)
	}
	want := "# my pip\n[global]\ntimeout = 60\nindex-url = https://_token@libraries.cgr.dev/python/simple/\nextra-index-url =\n    https://a.example/simple\n    https://b.example/simple\nkeyring-provider = subprocess\n\n[install]\nuser = true\n"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	if v, _ := Get(out, "extra_index_url"); v != "https://a.example/simple\nhttps://b.example/simple" {
		t.Fatalf("multi-line Get = %q", v)
	}
	back := Restore(Restore(out, "keyring-provider", prevKr), "index-url", prev)
	// The key is renamed to its canonical spelling; everything else is restored.
	wantBack := "# my pip\n[global]\ntimeout = 60\nindex-url = https://pypi.org/simple\nextra-index-url =\n    https://a.example/simple\n    https://b.example/simple\n\n[install]\nuser = true\n"
	if back != wantBack {
		t.Fatalf("restore got:\n%s\nwant:\n%s", back, wantBack)
	}
}

func TestSetCreatesSectionAndRestoreRemovesIt(t *testing.T) {
	for _, orig := range []string{"", "[install]\nuser = true\n"} {
		out, p1 := Set(orig, "index-url", "X")
		out, p2 := Set(out, "keyring-provider", "subprocess")
		if v, ok := Get(out, "index-url"); !ok || v != "X" {
			t.Fatalf("Get after Set on %q: %q %v\n%s", orig, v, ok, out)
		}
		back := Restore(Restore(out, "keyring-provider", p2), "index-url", p1)
		if back != orig {
			t.Fatalf("restore of %q gave %q", orig, back)
		}
	}
}

func TestGetIgnoresOtherSectionsAndComments(t *testing.T) {
	c := "[install]\nindex-url = nope\n[global]\n# index-url = commented\n"
	if _, ok := Get(c, "index-url"); ok {
		t.Fatal("found key outside [global] or in a comment")
	}
}
