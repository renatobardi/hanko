package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

func TestFSRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "blobs")
	f, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name() != DriverFS || f.Transactional() {
		t.Fatalf("name/transactional = %q/%v", f.Name(), f.Transactional())
	}
	k := Key{CompanyID: domain.NewID(), ArtifactID: domain.NewID()}
	if _, err := f.Get(ctx, k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get antes do put = %v, want ErrNotFound", err)
	}
	if _, err := f.Stat(ctx, k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat antes do put = %v, want ErrNotFound", err)
	}
	if err := f.Put(ctx, k, []byte("conteúdo"), "text/plain"); err != nil {
		t.Fatal(err)
	}
	b, err := f.Get(ctx, k)
	if err != nil || string(b) != "conteúdo" {
		t.Fatalf("get = %q, %v", b, err)
	}
	if n, err := f.Stat(ctx, k); err != nil || n != int64(len("conteúdo")) {
		t.Fatalf("stat = %d, %v", n, err)
	}
	p := filepath.Join(root, k.CompanyID.String(), k.ArtifactID.String())
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("arquivo %s: %v, modo %v", p, err, st.Mode().Perm())
	}
	// regravar substitui sem deixar temporário para trás
	if err := f.Put(ctx, k, []byte("outro"), ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.Get(ctx, k); string(b) != "outro" {
		t.Fatalf("depois de regravar = %q", b)
	}
	if es, _ := os.ReadDir(filepath.Dir(p)); len(es) != 1 {
		t.Fatalf("arquivos no diretório da empresa = %d, want 1", len(es))
	}
	if err := f.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, k); err != nil { // idempotente
		t.Fatal(err)
	}
	if _, err := f.Get(ctx, k); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get depois do delete = %v", err)
	}
}

func TestNewFSRejectsRelativeRoot(t *testing.T) {
	if _, err := NewFS("blobs"); err == nil {
		t.Fatal("raiz relativa aceita")
	}
	if _, err := NewFS(""); err == nil {
		t.Fatal("raiz vazia aceita")
	}
}
