package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Varredura das pastas de arquivos de segredo deixadas para trás (ADR-0073). O ${secretfile:} cria uma pasta
// hanko-secret-* por run e a limpeza da run a apaga; se o processo morrer no meio (kill -9, falta de memória), a pasta
// fica. No boot seguinte ela é apagada quando mais velha que o teto de vida de uma run.

// SweepStaleSecretDirs apaga, no diretório base (vazio = os.TempDir()), as pastas hanko-secret-* cuja idade passa
// maxAge. Só pastas do ${secretfile:} entram: arquivo solto com o mesmo prefixo fica. Devolve quantas apagou; falhas
// de apagar uma pasta não param a varredura e voltam juntas no erro.
func SweepStaleSecretDirs(base string, maxAge time.Duration, now time.Time) (int, error) {
	if maxAge <= 0 {
		return 0, errors.New("varredura de pastas de segredo: idade máxima tem de ser positiva")
	}
	if base == "" {
		base = os.TempDir()
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0, fmt.Errorf("varredura de pastas de segredo: ler %s: %w", base, err)
	}
	cutoff := now.Add(-maxAge)
	var n int
	var errs error
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "hanko-secret-") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("pasta %s: %w", e.Name(), err))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
			errs = errors.Join(errs, fmt.Errorf("pasta %s: %w", e.Name(), err))
			continue
		}
		n++
	}
	return n, errs
}
