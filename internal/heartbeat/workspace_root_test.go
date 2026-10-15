package heartbeat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

func runWithWorkspace(t *testing.T, root, path string) (dir string, be *fakeBackend) {
	t.Helper()
	be = &fakeBackend{rc: sampleContext()}
	be.rc.Workspace = &domain.Workspace{Slug: "api", Path: path}
	ad := &scriptedAdapter{run: func(_ context.Context, spec adapter.Spec, _ adapter.Sink) (adapter.Result, error) {
		dir = spec.Dir
		return adapter.Result{SessionRef: "s"}, nil
	}}
	h, _ := newTestHB(t, be, ad, &memStub{}, Options{WorkspacesRoot: root})
	_ = h.executeRun(context.Background(), ExecuteRunArgs{CompanyID: be.rc.Company.ID, RunID: be.rc.Run.ID})
	return dir, be
}

// O diretório é revalidado no uso: um symlink trocado depois de salvar o workspace, apontando para fora da raiz,
// faz a run usar o diretório temporário e deixar um aviso no log.
func TestExecuteRunWorkspaceSymlinkOutsideRootFallsBackToTemp(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "acme", "api")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlink indisponível: ", err)
	}
	dir, be := runWithWorkspace(t, root, link)
	if dir == "" || dir == link || dir == outside || !strings.Contains(filepath.Base(dir), "hanko-run-") {
		t.Fatalf("dir = %q; esperava um diretório temporário", dir)
	}
	warned := false
	be.mu.Lock()
	for _, e := range be.events {
		if e.Kind == domain.EventSystem && strings.Contains(e.Text, "fora da raiz") {
			warned = true
		}
	}
	be.mu.Unlock()
	if !warned {
		t.Errorf("faltou o aviso de workspace fora da raiz: %+v", be.events)
	}
}

func TestExecuteRunWorkspaceInsideRootIsUsed(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "acme", "api")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if dir, _ := runWithWorkspace(t, root, ws); dir != real {
		t.Fatalf("dir = %q; esperava %q", dir, real)
	}
	// a própria raiz não é um workspace
	if dir, _ := runWithWorkspace(t, root, root); dir == root || dir == "" {
		t.Fatalf("a raiz inteira não pode ser o diretório de trabalho: %q", dir)
	}
}
