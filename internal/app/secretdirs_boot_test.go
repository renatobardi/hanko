package app

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/config"
)

// No boot, a varredura apaga as pastas de segredo mais velhas que run_timeout + 1 h e registra o que apagou.
func TestBootVarreAsPastasDeSegredoAntigas(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	cfg := config.Config{RunTimeout: 30 * time.Minute} // limite: 1 h 30 min
	velha := filepath.Join(base, "hanko-secret-velha")
	if err := os.Mkdir(velha, 0o700); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(velha, when, when); err != nil {
		t.Fatal(err)
	}
	nova := filepath.Join(base, "hanko-secret-nova")
	if err := os.Mkdir(nova, 0o700); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	sweepStaleSecretDirsAtBoot(cfg, slog.New(slog.NewTextHandler(&logs, nil)))

	if _, err := os.Stat(velha); err == nil {
		t.Error("a pasta antiga de segredo continuou no lugar")
	}
	if _, err := os.Stat(nova); err != nil {
		t.Errorf("a pasta recente saiu na varredura: %v", err)
	}
	if !strings.Contains(logs.String(), "pastas de arquivos de segredo antigas apagadas") {
		t.Errorf("faltou o aviso de quantas pastas saíram: %s", logs.String())
	}
}

// Sem pasta antiga para apagar, o boot não registra aviso de apagadas nem de falha.
func TestBootSemPastasAntigasNaoAvisa(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	var logs bytes.Buffer
	sweepStaleSecretDirsAtBoot(config.Config{RunTimeout: 30 * time.Minute}, slog.New(slog.NewTextHandler(&logs, nil)))
	if logs.Len() != 0 {
		t.Errorf("sem pasta antiga, o boot não devia registrar nada: %s", logs.String())
	}
}

// Se a varredura falha (temporário inexistente), o boot segue: só um aviso, sem pânico.
func TestBootVarreduraFalhaVirAvisoSemDerrubarOBoot(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "nao-existe"))
	var logs bytes.Buffer
	sweepStaleSecretDirsAtBoot(config.Config{RunTimeout: time.Minute}, slog.New(slog.NewTextHandler(&logs, nil)))
	if !strings.Contains(logs.String(), "varredura de pastas de arquivos de segredo antigas falhou") {
		t.Errorf("faltou o aviso da falha: %s", logs.String())
	}
}
