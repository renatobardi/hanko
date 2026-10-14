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
	if err := os.MkdirAll(filepath.Join(root, "ok"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	s := &Service{WorkspacesRoot: root}
	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"vazio", "", true},
		{"dentro, existente", filepath.Join(root, "ok"), true},
		{"dentro, inexistente", filepath.Join(root, "novo", "sub"), true},
		{"relativo", "ok/x", false},
		{"com ..", root + "/ok/../ok", false},
		{"barra final", root + "/ok/", false},
		{"barra dupla", root + "//ok", false},
		{"fora da raiz", outside, false},
		{"a própria raiz", root, false},
		{"prefixo parecido", root + "-irmao", false},
		{"symlink escapando", filepath.Join(root, "escape"), false},
		{"dentro de symlink escapando", filepath.Join(root, "escape", "x"), false},
		{"/etc", "/etc", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.checkWorkspacePath(tc.path)
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
	if _, err := free.checkWorkspacePath("/srv/repo"); err != nil {
		t.Errorf("sem raiz: %v", err)
	}
	if _, err := free.checkWorkspacePath("rel"); err == nil {
		t.Error("relativo deveria falhar")
	}
}
