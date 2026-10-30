package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/storage"
)

// Troca de arquivo entre drivers (#117, ADR-0051): o texto cifrado antigo não fica em nenhum driver depois da troca.
// Pulam sem HANKO_TEST_DATABASE_URL.

// Com fs ativo, o campo fica no fs; a troca sobrescreve o arquivo lá e não cria linha no postgres.
func TestTrocaArquivoComFSAtivoSobrescreveNoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(e.svc.Pool))
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	key := storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}

	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	raw, err := fs.Get(ctx, key)
	if err != nil || strings.Contains(string(raw), conteudoNovoArquivo) || strings.Contains(string(raw), conteudoAntigoArquivo) {
		t.Fatalf("fs depois da troca: %v", err)
	}
	if linhasDoCampo(t, e, fieldID) != 0 {
		t.Fatal("a troca criou linha no postgres com fs ativo")
	}
	if got := lerKubeconfig(t, e); got != conteudoNovoArquivo {
		t.Fatalf("leitura = %q, quer o conteúdo novo", got)
	}
}

// Com postgres ativo e o campo criado no fs, a troca cria a linha no postgres com o conteúdo novo e apaga a cópia
// antiga do fs (depois do commit).
func TestTrocaArquivoComPostgresAtivoApagaACopiaAntigaDoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(e.svc.Pool))
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	key := storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}

	e.svc.Blobs = storage.NewRegistry(storage.NewPostgres(e.svc.Pool), fs)
	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	if linhasDoCampo(t, e, fieldID) != 1 {
		t.Fatal("a troca não criou a linha no postgres")
	}
	if _, err := fs.Get(ctx, key); err == nil {
		t.Fatal("a cópia antiga do fs continua depois da troca")
	}
	if got := lerKubeconfig(t, e); got != conteudoNovoArquivo {
		t.Fatalf("leitura = %q, quer o conteúdo novo", got)
	}
}
