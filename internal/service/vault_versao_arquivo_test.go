package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/storage"
)

// Versão anterior de campo arquivo (ADR-0068 §3.1 e §3.3, #117): a troca arquiva o arquivo que sai, sobe a versão e a
// restauração copia a versão como versão nova. O arquivo arquivado tem chave própria em cada driver: a linha de
// secret_field_version_blobs no postgres, ou o BlobStore (fs). Pulam sem HANKO_TEST_DATABASE_URL.

// versaoDoCampo devolve o id da versão `n` do campo.
func versaoDoCampo(t *testing.T, e *toolsEnv, fieldID domain.ID, n int) domain.ID {
	t.Helper()
	var id domain.ID
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT id FROM secret_field_versions WHERE field_id=$1 AND version=$2`, fieldID, n).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// blobDaVersaoNoPostgres lê o texto cifrado do arquivo arquivado na tabela do postgres; a linha tem de existir.
func blobDaVersaoNoPostgres(t *testing.T, e *toolsEnv, versionID domain.ID) []byte {
	t.Helper()
	var b []byte
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT content FROM secret_field_version_blobs WHERE version_id=$1`, versionID).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

// Troca com postgres ativo: a versão 1 sai com o arquivo antigo arquivado (cifrado, na tabela de versões), o campo vai
// para a versão 2 com o conteúdo novo, e restaurar a versão 1 põe o antigo de volta como versão 3, sem apagar nada.
func TestTrocaArquivoArquivaAVersaoERestaura(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")

	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	hist, err := e.svc.ListSecretFieldHistory(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig")
	if err != nil || len(hist) != 1 || hist[0].Version != 1 {
		t.Fatalf("histórico depois da troca = %+v (%v), quer a versão 1", hist, err)
	}
	archived := blobDaVersaoNoPostgres(t, e, versaoDoCampo(t, e, fieldID, 1))
	if bytes.Contains(archived, []byte(conteudoAntigoArquivo)) {
		t.Fatal("o arquivo arquivado está em claro")
	}

	if _, err := e.svc.RestoreSecretFieldVersion(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig", 1); err != nil {
		t.Fatalf("restaurar a versão 1: %v", err)
	}
	if got := lerKubeconfig(t, e); got != conteudoAntigoArquivo {
		t.Fatalf("leitura depois de restaurar = %q, quer o arquivo antigo", got)
	}
	if n := e.count(t, `SELECT version FROM secret_fields WHERE id=$1`, fieldID); n != 3 {
		t.Fatalf("versão do campo depois de restaurar = %d, quer 3 (restaurar é versão nova)", n)
	}
	hist, err = e.svc.ListSecretFieldHistory(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig")
	if err != nil || len(hist) != 2 {
		t.Fatalf("histórico depois de restaurar = %d versões (%v), quer 2", len(hist), err)
	}
}

// Com fs ativo, o arquivo arquivado vai para o fs sob a chave da versão, e não cria linha na tabela de versões do postgres.
func TestTrocaArquivoComFSAtivoArquivaNoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(e.svc.Pool))
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")

	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	vid := versaoDoCampo(t, e, fieldID, 1)
	raw, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: vid})
	if err != nil || bytes.Contains(raw, []byte(conteudoAntigoArquivo)) {
		t.Fatalf("o fs não tem o arquivo arquivado cifrado: %v", err)
	}
	if e.count(t, `SELECT count(*) FROM secret_field_version_blobs WHERE version_id=$1`, vid) != 0 {
		t.Fatal("com fs ativo, o arquivo arquivado criou linha no postgres")
	}
	if _, err := e.svc.RestoreSecretFieldVersion(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig", 1); err != nil {
		t.Fatalf("restaurar: %v", err)
	}
	if got := lerKubeconfig(t, e); got != conteudoAntigoArquivo {
		t.Fatalf("leitura depois de restaurar com fs = %q, quer o arquivo antigo", got)
	}
}

// Apagar o campo leva o arquivo dele e o das versões arquivadas: no fs, nenhuma das chaves continua depois do commit.
func TestApagarCampoArquivoApagaAsVersoesNoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(e.svc.Pool))
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	vkey := storage.Key{CompanyID: e.co.ID, ArtifactID: versaoDoCampo(t, e, fieldID, 1)}
	fkey := storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}

	if err := e.svc.DeleteSecretField(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig"); err != nil {
		t.Fatalf("apagar o campo: %v", err)
	}
	for name, k := range map[string]storage.Key{"campo": fkey, "versão": vkey} {
		if _, err := fs.Get(ctx, k); !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("o fs ainda tem o arquivo da %s depois de apagar o campo: %v", name, err)
		}
	}
}

// Histórico de arquivo não se revela em texto: a versão não tem valor a mostrar, e a recusa vem antes de marcar a versão.
func TestRevelarVersaoDeArquivoEhRecusado(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}

	_, _, err := e.svc.RevealSecretFieldVersion(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig", 1)
	if err == nil || !strings.Contains(err.Error(), "não se revela em texto") {
		t.Fatalf("revelar versão de arquivo: %v", err)
	}
	if e.count(t, `SELECT count(*) FROM secret_field_versions WHERE version=1 AND revealed_at IS NOT NULL`) != 0 {
		t.Fatal("a recusa marcou a versão como revelada")
	}
}

// A migração postgres -> fs move também o arquivo arquivado da versão. Depois dela, a restauração (com o postgres ainda
// ativo) acha o arquivo no fs e devolve o conteúdo antigo.
func TestMigraVersaoDeArquivoDoPostgresParaOFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, reg := registroFSEPostgres(t, e)
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	vid := versaoDoCampo(t, e, fieldID, 1)

	rep, err := storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs"})
	if err != nil || rep.Failed != 0 || rep.Moved < 2 {
		t.Fatalf("migração (campo e versão): %+v %v", rep, err)
	}
	if e.count(t, `SELECT count(*) FROM secret_field_version_blobs WHERE version_id=$1`, vid) != 0 {
		t.Fatal("a linha da versão no postgres não saiu depois da migração")
	}
	if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: vid}); err != nil {
		t.Fatalf("o fs não tem o arquivo arquivado depois da migração: %v", err)
	}
	if _, err := e.svc.RestoreSecretFieldVersion(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig", 1); err != nil {
		t.Fatalf("restaurar depois da migração: %v", err)
	}
	if got := lerKubeconfig(t, e); got != conteudoAntigoArquivo {
		t.Fatalf("leitura depois de restaurar = %q, quer o arquivo antigo", got)
	}
}

// Rotação de HANKO_SECRET_KEY: o arquivo arquivado é recifrado com a chave nova (o texto muda) e a restauração continua
// devolvendo o conteúdo antigo.
func TestRekeyVersaoDeArquivoRecifra(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
		t.Fatalf("trocar: %v", err)
	}
	vid := versaoDoCampo(t, e, fieldID, 1)
	antes := blobDaVersaoNoPostgres(t, e, vid)

	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	if _, err := e.svc.RekeyIntegrationSecrets(ctx); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if depois := blobDaVersaoNoPostgres(t, e, vid); bytes.Equal(antes, depois) {
		t.Fatal("o rekey não recifrou o arquivo arquivado")
	}
	if _, err := e.svc.RestoreSecretFieldVersion(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig", 1); err != nil {
		t.Fatalf("restaurar depois do rekey: %v", err)
	}
	if got := lerKubeconfig(t, e); got != conteudoAntigoArquivo {
		t.Fatalf("leitura depois do rekey e da restauração = %q, quer o arquivo antigo", got)
	}
}

// A poda do histórico (ADR-0068 §3.2) leva o arquivo arquivado junto com a versão: com fs ativo, as duas versões mais
// antigas saem do fs quando passam do limite de N; as que ficam continuam lá.
func TestPodaDoHistoricoApagaOArquivoArquivadoNoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(e.svc.Pool))
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	const trocas = 7 // com o limite de N = domain.DefaultHistoryKeepVersions (5), duas versões saem
	for i := 0; i < trocas; i++ {
		if _, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
			service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "novo.yaml"}); err != nil {
			t.Fatalf("troca %d: %v", i+1, err)
		}
	}

	// os ids se leem antes da poda: a linha da versão que sai não se acha depois
	velhas := []domain.ID{versaoDoCampo(t, e, fieldID, 1), versaoDoCampo(t, e, fieldID, 2)}
	fica := versaoDoCampo(t, e, fieldID, 3)

	removed, err := e.svc.PruneSecretFieldHistory(ctx)
	if err != nil || removed != trocas-domain.DefaultHistoryKeepVersions {
		t.Fatalf("poda: removidas %d (quer %d): %v", removed, trocas-domain.DefaultHistoryKeepVersions, err)
	}
	for _, id := range velhas {
		if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: id}); err == nil {
			t.Fatalf("o fs ainda guarda o arquivo da versão %s, que a poda removeu", id)
		}
	}
	if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fica}); err != nil {
		t.Fatalf("a poda apagou o arquivo de uma versão que fica: %v", err)
	}
}
