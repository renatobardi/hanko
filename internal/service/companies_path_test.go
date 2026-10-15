package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestCheckWorkspacePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	home := filepath.Join(root, "acme") // a pasta da empresa dentro da raiz compartilhada
	if err := os.MkdirAll(filepath.Join(home, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "outra", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outra"), filepath.Join(home, "para-outra")); err != nil {
		t.Fatal(err)
	}
	s := &Service{WorkspacesRoot: root}
	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"vazio", "", true},
		{"dentro, existente", filepath.Join(home, "ok"), true},
		{"dentro, inexistente", filepath.Join(home, "novo", "sub"), true},
		{"relativo", "ok/x", false},
		{"com ..", home + "/ok/../ok", false},
		{"barra final", home + "/ok/", false},
		{"barra dupla", home + "//ok", false},
		{"fora da raiz", outside, false},
		{"a própria raiz", root, false},
		{"a própria pasta da empresa", home, false},
		{"direto na raiz, sem subpasta da empresa", filepath.Join(root, "solto"), false},
		{"pasta de outra empresa", filepath.Join(root, "outra", "x"), false},
		{"prefixo parecido com a empresa", home + "-irmao/x", false},
		{"symlink escapando", filepath.Join(home, "escape"), false},
		{"dentro de symlink escapando", filepath.Join(home, "escape", "x"), false},
		{"symlink para a pasta de outra empresa", filepath.Join(home, "para-outra", "x"), false},
		{"/etc", "/etc", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.checkWorkspacePath("acme", tc.path)
			if tc.ok {
				if err != nil || got != tc.path {
					t.Fatalf("got %q, %v", got, err)
				}
				return
			}
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("quer ErrInvalid, veio %v", err)
			}
		})
	}
	// sem raiz: só absoluto e limpo
	free := &Service{}
	if _, err := free.checkWorkspacePath("acme", "/srv/repo"); err != nil {
		t.Errorf("sem raiz: %v", err)
	}
	if _, err := free.checkWorkspacePath("acme", "rel"); err == nil {
		t.Error("relativo deveria falhar")
	}
}

// Um caminho salvo antes da regra da subpasta segue valendo; a UI só recebe o aviso (OutsideRoot).
func TestPathOutsideCompanyRoot(t *testing.T) {
	s := &Service{WorkspacesRoot: "/srv/ws"}
	for path, want := range map[string]bool{
		"/srv/ws/acme/site":  false,
		"/srv/ws/acme":       true,
		"/srv/ws/site":       true,
		"/srv/ws/outra/site": true,
		"/opt/x":             true,
		"":                   false,
	} {
		if got := s.pathOutsideCompanyRoot("acme", path); got != want {
			t.Errorf("pathOutsideCompanyRoot(%q) = %v, want %v", path, got, want)
		}
	}
	if (&Service{}).pathOutsideCompanyRoot("acme", "/qualquer") {
		t.Error("sem raiz nada está fora")
	}
	if got := s.CompanyWorkspacesRoot("acme"); got != "/srv/ws/acme" {
		t.Errorf("CompanyWorkspacesRoot = %q", got)
	}
}
