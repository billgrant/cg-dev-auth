package dockercfg

import (
	"encoding/json"
	"testing"
)

func TestSetCredHelpersKeepsOtherKeys(t *testing.T) {
	orig := []byte(`{"auths":{"ghcr.io":{}},"credsStore":"osxkeychain","credHelpers":{"gcr.io":"gcloud"}}`)
	cgr := "cgr"
	out, err := SetCredHelpers(orig, map[string]*string{"cgr.dev": &cgr})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["credsStore"] != "osxkeychain" || m["auths"] == nil {
		t.Fatalf("lost keys: %s", out)
	}
	h, _ := CredHelpers(out)
	if h["cgr.dev"] != "cgr" || h["gcr.io"] != "gcloud" {
		t.Fatalf("helpers = %v", h)
	}
	out, _ = SetCredHelpers(out, map[string]*string{"cgr.dev": nil, "gcr.io": nil})
	m = map[string]any{}
	if err := json.Unmarshal(out, &m); err != nil || m["credHelpers"] != nil {
		t.Fatalf("credHelpers not removed: %s", out)
	}
}
