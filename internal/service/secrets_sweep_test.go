package service_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/service"
)

// dirComIdade cria o diretório name dentro de base com um arquivo de segredo e a mtime no passado, idade atrás de now.
func dirComIdade(t *testing.T, base, name string, idade time.Duration, now time.Time) string {
	t.Helper()
	p := filepath.Join(base, name)
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "kubeconfig"), []byte("conteudo"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := now.Add(-idade)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatal(err)
	}
	return p
}

func existe(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// A varredura só apaga pastas hanko-secret-* mais velhas que o limite; o resto fica.
func TestVarreduraApagaSoPastasDeSegredoAntigas(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	maxAge := 2 * time.Hour
	velha := dirComIdade(t, base, "hanko-secret-velha", 3*time.Hour, now)
	nova := dirComIdade(t, base, "hanko-secret-nova", time.Hour, now)
	outra := dirComIdade(t, base, "outra-pasta-velha", 3*time.Hour, now)
	arquivo := filepath.Join(base, "hanko-secret-arquivo-solto")
	if err := os.WriteFile(arquivo, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := now.Add(-3 * time.Hour)
	if err := os.Chtimes(arquivo, when, when); err != nil {
		t.Fatal(err)
	}

	n, err := service.SweepStaleSecretDirs(base, maxAge, now)
	if err != nil || n != 1 {
		t.Fatalf("varredura: n=%d err=%v", n, err)
	}
	if existe(velha) {
		t.Error("a pasta antiga de segredo continuou no lugar")
	}
	for _, p := range []string{nova, outra, arquivo} {
		if !existe(p) {
			t.Errorf("a varredura apagou o que não devia: %s", p)
		}
	}
}

// Base vazia usa o diretório temporário do sistema (TMPDIR no Linux).
func TestVarreduraBaseVaziaUsaOTemporario(t *testing.T) {
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	now := time.Now()
	velha := dirComIdade(t, base, "hanko-secret-velha", 3*time.Hour, now)

	n, err := service.SweepStaleSecretDirs("", 2*time.Hour, now)
	if err != nil || n != 1 || existe(velha) {
		t.Fatalf("varredura no temporário: n=%d err=%v velha ainda existe=%v", n, err, existe(velha))
	}
}

// Idade máxima zero ou negativa é erro: nunca se apaga tudo por engano.
func TestVarreduraRecusaIdadeNaoPositiva(t *testing.T) {
	base := t.TempDir()
	now := time.Now()
	velha := dirComIdade(t, base, "hanko-secret-velha", 3*time.Hour, now)
	for _, idade := range []time.Duration{0, -time.Hour} {
		n, err := service.SweepStaleSecretDirs(base, idade, now)
		if err == nil || n != 0 {
			t.Fatalf("idade %v: n=%d err=%v; era para recusar", idade, n, err)
		}
	}
	if !existe(velha) {
		t.Fatal("a recusa não pode apagar nada")
	}
}

// Base que não existe: a varredura falha com erro, sem apagar nada.
func TestVarreduraBaseInexistenteFalha(t *testing.T) {
	inexistente := filepath.Join(t.TempDir(), "nao-existe")
	n, err := service.SweepStaleSecretDirs(inexistente, time.Hour, time.Now())
	if err == nil || n != 0 {
		t.Fatalf("base inexistente: n=%d err=%v", n, err)
	}
}

// Uma pasta que não se apaga não para a varredura: as outras saem e o erro volta junto.
func TestVarreduraContinuaAposFalhaAoApagar(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root apaga pasta sem permissão de escrita: o caso de falha não se reproduz")
	}
	base := t.TempDir()
	now := time.Now()
	travada := dirComIdade(t, base, "hanko-secret-travada", 3*time.Hour, now)
	sub := filepath.Join(travada, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "preso"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })
	livre := dirComIdade(t, base, "hanko-secret-livre", 3*time.Hour, now)

	n, err := service.SweepStaleSecretDirs(base, 2*time.Hour, now)
	if err == nil {
		t.Fatal("a pasta travada devia voltar no erro")
	}
	if n != 1 || existe(livre) {
		t.Fatalf("a pasta livre devia sair mesmo com a travada: n=%d livre existe=%v", n, existe(livre))
	}
	if !existe(travada) {
		t.Fatal("a pasta travada não devia sumir")
	}
}
