package skillgit

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// ScanLocal lê as skills de um repositório que já está no disco (o espelho de um workspace, ADR-0054), sem clonar.
func TestScanLocal(t *testing.T) {
	url := gitRepo(t, map[string]string{
		"README.md":                "# repo",
		"skills/revisar/SKILL.md":  "---\nname: revisar-pr\ndescription: Use ao revisar PR\n---\n\n# Passos\n",
		"skills/deploy/SKILL.md":   "---\nname: deploy\ndescription: Deploy\n---\ncorpo",
		"skills/deploy/run.sh":     "x:#!/bin/sh\necho oi\n",
		"outros/SKILL.md":          "fora do prefixo",
		"skills/excluida/SKILL.md": "---\nname: excluida\n---\nx",
	})
	dir := strings.TrimPrefix(url, "file://")
	ctx := context.Background()

	snap, err := ScanLocal(ctx, dir, "HEAD", Scan{Prefix: "skills", Excluded: []string{"skills/excluida"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Commit) != 40 || snap.Tree == "" || len(snap.Skills) != 2 {
		t.Fatalf("snapshot: %+v", snap)
	}
	got := map[string]Found{}
	for _, f := range snap.Skills {
		got[f.Path] = f
	}
	if f := got["skills/revisar"]; f.Name != "revisar-pr" || f.Err != "" {
		t.Fatalf("revisar: %+v", f)
	}
	if f := got["skills/deploy"]; !f.HasScripts || !strings.Contains(f.Raw, "corpo") {
		t.Fatalf("deploy: %+v", f)
	}

	// o resultado é o mesmo de um clone
	cloned, err := Fetch(ctx, url, "HEAD", Scan{Prefix: "skills", Excluded: []string{"skills/excluida"}}, Options{AllowFile: true})
	if err != nil || cloned.Commit != snap.Commit || cloned.Tree != snap.Tree {
		t.Fatalf("clone x local: %+v %v", cloned, err)
	}

	// revisões inválidas ou inexistentes
	for _, rev := range []string{"", "-x", "a..b", "a b", "HEAD:skills", "HEAD^", "refs/nao/existe"} {
		if _, err := ScanLocal(ctx, dir, rev, Scan{}, Options{}); err == nil {
			t.Errorf("rev %q deveria falhar", rev)
		}
	}
	if _, err := ScanLocal(ctx, dir, "HEAD", Scan{Prefix: "../fora"}, Options{}); err == nil {
		t.Error("prefixo fora do repositório deveria falhar")
	}
	if _, err := ScanLocal(ctx, t.TempDir(), "HEAD", Scan{}, Options{}); err == nil {
		t.Error("pasta que não é repositório deveria falhar")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está no PATH")
	}
}
