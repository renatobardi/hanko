package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FS guarda o conteúdo em arquivos sob um diretório local (ADR-0065): <raiz>/<empresa>/<artefato>, modo 0600. É o
// driver de quem quer arquivos grandes fora do banco sem um serviço S3: o disco da própria máquina, com o backup
// local copiando o diretório. As chaves são UUIDs, então o caminho nunca sai da raiz.
type FS struct {
	root string
}

// NewFS valida a raiz (caminho absoluto) e a cria se faltar.
func NewFS(root string) (*FS, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, errors.New("fs: HANKO_BLOB_DIR precisa ser um caminho absoluto")
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("fs: criar %s: %w", root, err)
	}
	probe, err := os.CreateTemp(root, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("fs: %s sem permissão de escrita: %w", root, err)
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return &FS{root: root}, nil
}

func (f *FS) path(k Key) string {
	return filepath.Join(f.root, k.CompanyID.String(), k.ArtifactID.String())
}

// Name implementa BlobStore.
func (f *FS) Name() string { return DriverFS }

// Transactional implementa BlobStore: o arquivo é gravado antes da transação do artefato, como no s3.
func (f *FS) Transactional() bool { return false }

// Put implementa BlobStore. Grava num temporário do mesmo diretório e renomeia: um leitor nunca vê arquivo pela
// metade, e o fsync do arquivo e do diretório garante que o conteúdo sobrevive a uma queda depois do commit.
func (f *FS) Put(_ context.Context, k Key, data []byte, _ string) error {
	dir := filepath.Dir(f.path(k))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("storage fs: criar diretório: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("storage fs: gravar: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp.Name())
		}
	}()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return fmt.Errorf("storage fs: gravar: %w", werr)
	}
	if err := os.Rename(tmp.Name(), f.path(k)); err != nil {
		return fmt.Errorf("storage fs: gravar: %w", err)
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Get implementa BlobStore.
func (f *FS) Get(_ context.Context, k Key) ([]byte, error) {
	b, err := os.ReadFile(f.path(k))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage fs: ler: %w", err)
	}
	return b, nil
}

// Delete implementa BlobStore.
func (f *FS) Delete(_ context.Context, k Key) error {
	if err := os.Remove(f.path(k)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage fs: apagar: %w", err)
	}
	return nil
}

// Stat implementa BlobStore.
func (f *FS) Stat(_ context.Context, k Key) (int64, error) {
	st, err := os.Stat(f.path(k))
	if errors.Is(err, os.ErrNotExist) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("storage fs: stat: %w", err)
	}
	return st.Size(), nil
}
