package service_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/storage"
)

// Campo arquivo nos drivers de blob (ADR-0068 §5.1, ADR-0072): migração entre drivers, rekey por campo e a purga da
// saída do humano. O driver postgres guarda o conteúdo em secret_field_blobs; fs, no disco.

// linhasDoCampo conta as linhas do campo na tabela do driver postgres (0 ou 1).
func linhasDoCampo(t *testing.T, e *toolsEnv, fieldID domain.ID) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM secret_field_blobs WHERE field_id=$1`, fieldID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// conteudoDoCampo lê o texto cifrado do campo na tabela do driver postgres; a linha tem de existir.
func conteudoDoCampo(t *testing.T, e *toolsEnv, fieldID domain.ID) []byte {
	t.Helper()
	var b []byte
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT content FROM secret_field_blobs WHERE field_id=$1`, fieldID).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}

// registroFSEPostgres monta o service com o postgres e o fs; o driver ativo é o postgres (as escritas novas vão para lá).
func registroFSEPostgres(t *testing.T, e *toolsEnv) (*storage.FS, *storage.Registry) {
	t.Helper()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := storage.NewRegistry(storage.NewPostgres(e.svc.Pool), fs)
	e.svc.Blobs = reg
	return fs, reg
}

// Dry-run não muda nada; a migração copia, confere e apaga a origem; o campo continua legível (a leitura acha o conteúdo
// no driver que o tem); reexecutar não falha.
func TestMigraCampoArquivoDoPostgresParaOFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, reg := registroFSEPostgres(t, e)
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	key := storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}

	rep, err := storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs", DryRun: true})
	if err != nil || rep.Moved != 1 || linhasDoCampo(t, e, fieldID) != 1 {
		t.Fatalf("dry-run: %+v %v", rep, err)
	}
	if _, err := fs.Get(ctx, key); err == nil {
		t.Fatal("o dry-run gravou no destino")
	}

	rep, err = storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs"})
	if err != nil || rep.Moved != 1 || rep.Failed != 0 || rep.Leftover != 0 {
		t.Fatalf("migração: %+v %v", rep, err)
	}
	raw, err := fs.Get(ctx, key)
	if err != nil || bytes.Contains(raw, []byte(sentinelArquivo)) || !bytes.HasPrefix(raw, []byte("v1:")) {
		t.Fatalf("o fs não tem o campo cifrado: %v", err)
	}
	if linhasDoCampo(t, e, fieldID) != 0 {
		t.Fatal("a linha do postgres não saiu depois da migração")
	}
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	if d, err := getSecret(e, c, "prd-cluster", "kubeconfig"); err != nil || d.Value != sentinelArquivo {
		t.Fatalf("leitura depois da migração: %+v %v", d, err)
	}

	// reexecutar: a origem já está vazia e o destino tem o conteúdo, então não há falha nem cópia
	rep, err = storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs"})
	if err != nil || rep.Failed != 0 || rep.Moved != 0 {
		t.Fatalf("reexecução: %+v %v", rep, err)
	}
}

// Com KeepSource a origem fica: a cópia vai para o destino e a linha do postgres segue lá.
func TestMigraCampoArquivoComKeepSourceMantemAOrigem(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, reg := registroFSEPostgres(t, e)
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")

	rep, err := storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs", KeepSource: true})
	if err != nil || rep.Moved != 1 || rep.Failed != 0 {
		t.Fatalf("keep-source: %+v %v", rep, err)
	}
	if linhasDoCampo(t, e, fieldID) != 1 {
		t.Fatal("com --keep-source a origem foi apagada")
	}
	if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}); err != nil {
		t.Fatalf("a cópia não chegou ao fs: %v", err)
	}
}

// Conflito: o postgres já guarda outro conteúdo para o campo; a cópia não sobrescreve e a falha é contada.
func TestMigraCampoArquivoConflitoNaoSobrescreve(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, reg := registroFSEPostgres(t, e)
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	outro := []byte("v1:outra:conteudo")
	if err := fs.Put(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}, outro, "application/octet-stream"); err != nil {
		t.Fatal(err)
	}

	rep, err := storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "fs", To: "postgres"})
	if err != nil || rep.Failed != 1 || rep.Moved != 0 || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "outro conteúdo") {
		t.Fatalf("conflito: %+v %v", rep, err)
	}
	if bytes.Equal(conteudoDoCampo(t, e, fieldID), outro) {
		t.Fatal("o conflito sobrescreveu a linha do postgres")
	}
}

// Campo sem conteúdo em nenhum dos dois lugares: a migração conta a falha, nomeia o campo e segue.
func TestMigraCampoArquivoSemConteudoContaFalha(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	_, reg := registroFSEPostgres(t, e)
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM secret_field_blobs WHERE field_id=$1`, fieldID); err != nil {
		t.Fatal(err)
	}

	rep, err := storage.Migrate(ctx, e.svc.Pool, reg, storage.MigrateOptions{From: "postgres", To: "fs"})
	if err != nil || rep.Failed != 1 || !strings.Contains(strings.Join(rep.Errors, "|"), "conteúdo ausente na origem") {
		t.Fatalf("sem conteúdo: %+v %v", rep, err)
	}
}

// Rekey por campo no driver fs: o arquivo é recifrado no próprio driver e continua legível com a chave nova.
func TestRekeyCampoArquivoNoFSRecifra(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs)
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	key := storage.Key{CompanyID: e.co.ID, ArtifactID: fieldIDOf(t, e, "kubeconfig")}
	antes, err := fs.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	n, err := e.svc.RekeyIntegrationSecrets(ctx)
	if err != nil || n < 1 {
		t.Fatalf("rekey no fs: n=%d err=%v", n, err)
	}
	depois, err := fs.Get(ctx, key)
	if err != nil || bytes.Equal(antes, depois) {
		t.Fatalf("o rekey não regravou o arquivo no fs: %v", err)
	}
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	if d, err := getSecret(e, c, "prd-cluster", "kubeconfig"); err != nil || d.Value != sentinelArquivo {
		t.Fatalf("leitura depois do rekey no fs: %+v %v", d, err)
	}
}

// Rekey em páginas: 101 campos arquivo (duas páginas de 100 e uma), todos recifrados. O rekey não carrega o conteúdo de
// todos de uma vez: cada campo entra e sai um por vez, em páginas por id.
func TestRekeyCampoArquivoEmPaginas(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	const total = 101
	var ids []domain.ID
	for k := 0; k < total; k++ {
		seg := fmt.Sprintf("seg-%d", k/25) // 25 campos arquivo por segredo, mais o campo value: abaixo do máximo de 32
		if k%25 == 0 {
			createSecretWithValue(t, e, seg, "principal")
		}
		name := fmt.Sprintf("arq-%03d", k)
		addField(t, e, seg, service.CreateSecretFieldInput{Name: name, Kind: domain.SecretFieldFile, File: []byte(sentinelArquivo), FileName: name})
		ids = append(ids, fieldIDOf(t, e, name))
	}
	antes := make([][]byte, len(ids))
	for i, id := range ids {
		antes[i] = conteudoDoCampo(t, e, id)
	}

	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	n, err := e.svc.RekeyIntegrationSecrets(ctx)
	if err != nil || n < total {
		t.Fatalf("rekey: n=%d err=%v; era para recifrar pelo menos os %d campos", n, err, total)
	}
	for i, id := range ids {
		if bytes.Equal(antes[i], conteudoDoCampo(t, e, id)) {
			t.Fatalf("o campo %d não foi recifrado", i)
		}
	}
}

// Arquivo que não abre com nenhuma chave conhecida faz o rekey falhar, nomeando o campo.
func TestRekeyCampoArquivoIlegivelFalhaNomeandoOCampo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	// id de chave que não existe: NeedsReseal é verdadeiro e Open falha
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE secret_field_blobs SET content=$1 WHERE field_id=$2`, []byte("v1:zzzzzz:AAAA"), fieldID); err != nil {
		t.Fatal(err)
	}
	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	_, err = e.svc.RekeyIntegrationSecrets(ctx)
	if err == nil || !strings.Contains(err.Error(), "arquivo do campo") || !strings.Contains(err.Error(), fieldID.String()) {
		t.Fatalf("era para falhar nomeando o campo: %v", err)
	}
}

// Campo arquivo sem linha de blob (gravação incompleta) não derruba o rekey: o campo é pulado.
func TestRekeyCampoArquivoSemBlobPulaOCampo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(sentinelArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")
	if _, err := e.svc.Pool.Exec(ctx, `DELETE FROM secret_field_blobs WHERE field_id=$1`, fieldID); err != nil {
		t.Fatal(err)
	}
	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	if _, err := e.svc.RekeyIntegrationSecrets(ctx); err != nil {
		t.Fatalf("o campo sem blob não devia falhar o rekey: %v", err)
	}
}

// Saída do humano com o arquivo do campo pessoal no driver fs: o objeto sai depois do commit, junto com o segredo.
func TestSaidaDoHumanoApagaOArquivoNoFS(t *testing.T) {
	d := newDelegEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.svc.Blobs = storage.NewRegistry(fs, storage.NewPostgres(d.svc.Pool))
	d.personal(d.ac, t, d.alice, "meu-cofre", "principal")
	if _, err := d.svc.CreateSecretField(d.ac, d.alice.Actor(), d.co.ID, domain.SecretScopeUser, "meu-cofre",
		service.CreateSecretFieldInput{Name: "arquivo", Kind: domain.SecretFieldFile, File: []byte(sentinelArquivo), FileName: "a.txt"}); err != nil {
		t.Fatal(err)
	}
	var fieldID domain.ID
	if err := d.svc.Pool.QueryRow(ctx, `SELECT id FROM secret_fields WHERE name='arquivo'`).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	key := storage.Key{CompanyID: d.co.ID, ArtifactID: fieldID}
	if _, err := fs.Get(ctx, key); err != nil {
		t.Fatalf("o arquivo do campo pessoal não foi para o fs: %v", err)
	}

	if _, err := d.svc.RemoveMemberReport(d.ac, d.alice.Actor(), d.co.ID, d.alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Get(ctx, key); err == nil {
		t.Fatal("o arquivo do campo pessoal continuou no fs depois da saída")
	}
}
