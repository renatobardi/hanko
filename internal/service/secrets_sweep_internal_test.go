package service

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Uma pasta que a remoção não consegue apagar não para a varredura: as outras saem e o erro volta junto. A falha é
// injetada na remoção, não por permissão de diretório, que o root e alguns sistemas de arquivos ignoram.
func TestSweepSecretDirsContinuaAposFalhaAoApagar(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	for _, nome := range []string{"hanko-secret-travada", "hanko-secret-livre"} {
		p := filepath.Join(base, nome)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		when := now.Add(-3 * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	travada := filepath.Join(base, "hanko-secret-travada")
	falha := errors.New("remoção recusada")
	remove := func(p string) error {
		if p == travada {
			return falha
		}
		return os.RemoveAll(p)
	}

	n, err := sweepSecretDirs(base, 2*time.Hour, now, remove)
	if !errors.Is(err, falha) {
		t.Fatalf("a falha da remoção devia voltar no erro: %v", err)
	}
	if n != 1 {
		t.Fatalf("a pasta livre devia sair mesmo com a travada: n=%d", n)
	}
	if _, err := os.Stat(filepath.Join(base, "hanko-secret-livre")); err == nil {
		t.Fatal("a pasta livre continuou no lugar")
	}
	if _, err := os.Stat(travada); err != nil {
		t.Fatal("a pasta travada não devia sumir")
	}
}
