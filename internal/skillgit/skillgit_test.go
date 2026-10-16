package skillgit

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/netguard"
)

// gitRepo cria um repositório local com os arquivos dados (caminho → conteúdo; prefixo "x:" = executável,
// "l:" = link simbólico para o conteúdo) e devolve a URL file://.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git não está no PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for p, c := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasPrefix(c, "l:"):
			if err := os.Symlink(c[2:], full); err != nil {
				t.Fatal(err)
			}
		case strings.HasPrefix(c, "x:"):
			if err := os.WriteFile(full, []byte(c[2:]), 0o755); err != nil { //nolint:gosec // script de teste
				t.Fatal(err)
			}
		default:
			if err := os.WriteFile(full, []byte(c), 0o644); err != nil { //nolint:gosec // arquivo de teste
				t.Fatal(err)
			}
		}
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return "file://" + dir
}

func TestFetchLocalRepo(t *testing.T) {
	url := gitRepo(t, map[string]string{
		"README.md":                 "# repo",
		"skills/revisar/SKILL.md":   "---\nname: revisar-pr\ndescription: Use ao revisar PR\n---\n\n# Passos\n",
		"skills/deploy/skill.md":    "---\nname: deploy\ndescription: Deploy\n---\ncorpo",
		"skills/deploy/run.sh":      "x:#!/bin/sh\necho oi\n",
		"skills/quebrada/SKILL.md":  "---\nname: [\n",
		"skills/link/SKILL.md":      "l:../revisar/SKILL.md",
		"outros/SKILL.md":           "fora do prefixo",
		"skills/excluida/SKILL.md":  "---\nname: excluida\n---\nx",
		"skills/deploy/sub/tool.py": "print(1)",
	})
	snap, err := Fetch(context.Background(), url, "HEAD", Scan{Prefix: "skills", Excluded: []string{"skills/excluida"}}, Options{AllowFile: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Commit) != 40 || snap.Tree == "" {
		t.Fatalf("commit/árvore: %q %q", snap.Commit, snap.Tree)
	}
	got := map[string]Found{}
	for _, f := range snap.Skills {
		got[f.Path] = f
	}
	if len(got) != 4 {
		t.Fatalf("skills: %+v", snap.Skills)
	}
	if f := got["skills/revisar"]; f.Name != "revisar-pr" || f.Description != "Use ao revisar PR" || f.Err != "" || f.HasScripts {
		t.Fatalf("revisar: %+v", f)
	}
	if f := got["skills/deploy"]; f.Name != "deploy" || !f.HasScripts || !strings.Contains(f.Raw, "corpo") {
		t.Fatalf("deploy: %+v", f)
	}
	if f := got["skills/quebrada"]; f.Err == "" {
		t.Fatalf("quebrada sem erro: %+v", f)
	}
	if f := got["skills/link"]; f.Err == "" || f.Raw != "" {
		t.Fatalf("link simbólico foi lido: %+v", f)
	}
	if _, ok := got["outros"]; ok {
		t.Fatal("prefixo não aplicado")
	}

	// branch inexistente
	if _, err := Fetch(context.Background(), url, "nao-existe", Scan{}, Options{AllowFile: true}); err == nil {
		t.Fatal("branch inexistente sem erro")
	}
	// teto de quantidade
	snap, err = Fetch(context.Background(), url, "main", Scan{}, Options{AllowFile: true, MaxSkills: 2})
	if err != nil || len(snap.Skills) != 2 || !snap.Truncated {
		t.Fatalf("teto: %+v %v", snap, err)
	}
}

func TestFetchTooLarge(t *testing.T) {
	big := strings.Repeat("abcdefghij", 300_000) // 3 MB, mas comprime: use bytes variados
	var sb strings.Builder
	for i := 0; i < 200_000; i++ {
		sb.WriteString(netip.AddrFrom4([4]byte{byte(i), byte(i >> 8), byte(i >> 16), 7}).String())
	}
	url := gitRepo(t, map[string]string{"a/SKILL.md": "x", "blob.txt": big + sb.String()})
	_, err := Fetch(context.Background(), url, "HEAD", Scan{}, Options{AllowFile: true, MaxBytes: 64 << 10})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return f[host], nil
}

func TestValidateURLAndHost(t *testing.T) {
	o := Options{}
	for _, bad := range []string{"http://example.com/r.git", "https://u:p@example.com/r.git", "https://127.0.0.1/r",
		"https://example.com/r?x=1", "https://example.com:8443/r", "file:///tmp/r", "ssh://example.com/r", "https://localhost/r"} {
		if _, err := ValidateURL(bad, o); err == nil {
			t.Errorf("%s aceita", bad)
		}
	}
	if u, err := ValidateURL("https://example.com/org/repo.git", o); err != nil || u != "https://example.com/org/repo.git" {
		t.Fatalf("válida: %q %v", u, err)
	}
	res := fakeResolver{"interno.example.com": {netip.MustParseAddr("10.0.0.5")}, "ok.example.com": {netip.MustParseAddr("93.184.216.34")}}
	if err := checkHost(context.Background(), "https://interno.example.com/r", Options{Resolver: res}); !errors.Is(err, netguard.ErrBlocked) {
		t.Fatalf("host interno: %v", err)
	}
	if err := checkHost(context.Background(), "https://ok.example.com/r", Options{Resolver: res}); err != nil {
		t.Fatalf("host público: %v", err)
	}
	if err := checkHost(context.Background(), "https://interno.example.com/r", Options{Resolver: res, AllowPrivate: true}); err != nil {
		t.Fatalf("operador liberou: %v", err)
	}
	for _, r := range []string{"-x", "a..b", "a b", "x.lock", "a//b", "a@{1}"} {
		if ValidateRef(r) == nil {
			t.Errorf("ref %q aceita", r)
		}
	}
	for _, r := range []string{"HEAD", "main", "release/1.2"} {
		if err := ValidateRef(r); err != nil {
			t.Errorf("ref %q recusada: %v", r, err)
		}
	}
	for _, p := range []string{"/abs", "../fora", "a/../../b"} {
		if _, err := CleanPath(p); err == nil {
			t.Errorf("caminho %q aceito", p)
		}
	}
}
