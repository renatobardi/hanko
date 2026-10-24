package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/secretref"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/storage"
)

// Fatia 3 do cofre (ADR-0068 §4 e §5, issue #58): hanko_secret_get com política ask e gate, TOTP que não devolve a
// semente, campo arquivo cifrado e ${secretfile:} como arquivo 0600 apagado ao fim da run.

const (
	// semente TOTP da RFC 6238 em base32: se ela aparecer onde não deve, o teste falha
	sentinelSemente = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	sentinelArquivo = "SENTINELA-arquivo-kubeconfig-9f8e7d"
	sentinelSenha   = "SENTINELA-senha-hanko-get-1a2b3c"
)

// createSecretWithValue cria um item com o campo value (o padrão da referência).
func createSecretWithValue(t *testing.T, e *toolsEnv, name, value string) {
	t.Helper()
	if _, err := e.svc.CreateSecret(context.Background(), domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: name, Value: value}); err != nil {
		t.Fatalf("criar %s: %v", name, err)
	}
}

func addField(t *testing.T, e *toolsEnv, secret string, in service.CreateSecretFieldInput) {
	t.Helper()
	if _, err := e.svc.CreateSecretField(context.Background(), domain.HumanActor, e.co.ID, domain.SecretScopeCompany, secret, in); err != nil {
		t.Fatalf("campo %s de %s: %v", in.Name, secret, err)
	}
}

func getSecret(e *toolsEnv, c service.ToolCaller, item, field string) (service.SecretGetResult, error) {
	return e.svc.SecretGet(context.Background(), e.co.ID, service.SecretGetInput{RunID: *c.RunID, Item: item, Field: field})
}

func gatesOfKind(t *testing.T, e *toolsEnv, kind domain.GateKind) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gates WHERE company_id=$1 AND kind=$2`,
		e.co.ID, string(kind)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Critério 1: a tool em ask abre gate; "sempre permitir" grava a regra; a chamada seguinte passa direto.
func TestHankoSecretGetAskGateSempreGravaRegra(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "gh-login", sentinelSenha)

	// sem política: pergunta antes; nenhum valor sai e o gate fica pendente
	d, err := getSecret(e, c, "gh-login", "")
	if err != nil || d.Decision != service.NativeAsk || d.Value != "" {
		t.Fatalf("ask: %+v %v", d, err)
	}
	if !strings.Contains(d.Reason, "aguardando aprovação") {
		t.Fatalf("o motivo diz que espera aprovação: %q", d.Reason)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 1 {
		t.Fatalf("gates de tool abertos = %d, quero 1", n)
	}
	// a mesma chamada reaproveita o gate; não abre outro
	if _, err := getSecret(e, c, "gh-login", ""); err != nil {
		t.Fatal(err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 1 {
		t.Fatalf("a repetição abriu outro gate: %d", n)
	}

	// o admin aprova "sempre permitir": a chamada aprovada entrega o valor
	var gate domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT id FROM gates WHERE company_id=$1 AND kind=$2`, e.co.ID, string(domain.GateTool)).Scan(&gate); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(ctx, domain.HumanActor, e.co.ID, gate, service.DecideGateInput{Decision: domain.GateApproved, Always: true}); err != nil {
		t.Fatal(err)
	}
	d, err = getSecret(e, c, "gh-login", "")
	if err != nil || d.Decision != service.NativeAllow || d.Value != sentinelSenha {
		t.Fatalf("aprovada: %+v %v", d, err)
	}

	// "sempre permitir" gravou a política desta persona para a tool; a seguinte não pergunta
	var pol int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM tool_policies WHERE company_id=$1 AND target='native' AND tool_name=$2 AND mode='allow' AND persona_id=$3`,
		e.co.ID, service.HankoSecretGetTool, ana.ID).Scan(&pol); err != nil {
		t.Fatal(err)
	}
	if pol != 1 {
		t.Fatalf("regra de sempre permitir = %d, quero 1", pol)
	}
	d, err = getSecret(e, c, "gh-login", "")
	if err != nil || d.Decision != service.NativeAllow || d.Value != sentinelSenha {
		t.Fatalf("com a regra gravada: %+v %v", d, err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 1 {
		t.Fatalf("com a regra não se abre gate novo: %d", n)
	}
}

// Critério 1: uma política off nega a tool antes de qualquer gate.
func TestHankoSecretGetPoliticaOffNega(t *testing.T) {
	e := newToolsEnv(t)
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "gh-login", sentinelSenha)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyOff))

	d, err := getSecret(e, c, "gh-login", "")
	if err != nil || d.Decision != service.NativeDeny || d.Value != "" {
		t.Fatalf("off: %+v %v", d, err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 0 {
		t.Fatalf("política off não abre gate: %d", n)
	}
}

// Critério 1: item fora das coleções da persona é recusado antes da política, sem gate e sem valor.
func TestHankoSecretGetItemForaDaColecaoRecusado(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "prd-db", sentinelSenha)
	if err := e.svc.RemoveSecretFromCollection(ctx, domain.HumanActor, e.co.ID, domain.DefaultCollectionName, "prd-db"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateCollection(ctx, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "restrita"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AddSecretToCollection(ctx, domain.HumanActor, e.co.ID, "restrita", "prd-db"); err != nil {
		t.Fatal(err)
	}

	d, err := getSecret(e, c, "prd-db", "")
	if err == nil || d.Value != "" || d.Decision != "" {
		t.Fatalf("item fora da coleção: %+v %v (quero recusa sem valor)", d, err)
	}
	if !strings.Contains(err.Error(), "prd-db") || strings.Contains(err.Error(), sentinelSenha) {
		t.Fatalf("o erro nomeia o item e não vaza o valor: %v", err)
	}
	if n := gatesOfKind(t, e, domain.GateTool); n != 0 {
		t.Fatalf("item sem alcance não abre gate: %d", n)
	}
}

// Critério 2: o campo TOTP devolve só o código do momento; a semente não sai pela tool nem pelo uso do campo.
func TestHankoSecretGetTOTPNuncaDevolveSemente(t *testing.T) {
	e := newToolsEnv(t)
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "github", sentinelSenha)
	addField(t, e, "github", service.CreateSecretFieldInput{Name: "totp", Kind: domain.SecretFieldTOTP, Value: sentinelSemente})
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))

	d, err := getSecret(e, c, "github", "totp")
	if err != nil || d.Decision != service.NativeAllow || d.Kind != domain.SecretFieldTOTP {
		t.Fatalf("totp: %+v %v", d, err)
	}
	if len(d.Code) != 6 || d.ExpiresIn < 1 || d.ExpiresIn > 30 || d.Value != "" {
		t.Fatalf("código do momento: %+v", d)
	}
	if strings.Contains(d.Code, sentinelSemente) || strings.Contains(d.Value, sentinelSemente) {
		t.Fatal("a semente saiu pela tool")
	}
	// o registro de uso guarda o campo, nunca a semente nem o código
	var dump string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(t::text, ' '), '') FROM secret_access_log t`).Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, sentinelSemente) || !strings.Contains(dump, "totp") {
		t.Fatalf("registro de uso: %s", dump)
	}
	// ${secret:} com o campo TOTP não resolve: o código venceria antes do uso
	ref := secretref.Ref{Name: "github", Field: "totp"}.String()
	if _, _, err := e.svc.ResolveSecretText(context.Background(), e.co.ID, ref); err == nil || strings.Contains(err.Error(), sentinelSemente) {
		t.Fatalf("${secret:github#totp} precisa falhar sem vazar a semente: %v", err)
	}
}

// Critério 4 e campo arquivo: o conteúdo fica cifrado no driver, a tool entrega texto e recusa binário, e o campo some
// com o arquivo quando é apagado.
func TestCampoArquivoCifradoNoDriver(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo + "\napiVersion: v1\n"), FileName: "kubeconfig", ContentType: "text/yaml"})
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))

	// o driver guarda o texto cifrado, nunca o conteúdo em claro (lê a linha direto, sem a API)
	var stored []byte
	if err := e.svc.Pool.QueryRow(ctx, `SELECT b.content FROM secret_field_blobs b JOIN secret_fields f ON f.id=b.field_id WHERE f.name='kubeconfig'`).Scan(&stored); err != nil {
		t.Fatalf("blob do campo: %v", err)
	}
	if bytes.Contains(stored, []byte(sentinelArquivo)) || !bytes.HasPrefix(stored, []byte("v1:")) {
		t.Fatalf("o blob não está cifrado: %.40q", stored)
	}

	// a metadata vai na linha; a tool entrega o texto do arquivo
	fields, err := e.svc.ListSecretFields(ctx, e.co.ID, domain.SecretScopeCompany, "prd-cluster")
	if err != nil {
		t.Fatal(err)
	}
	var kc service.SecretFieldView
	for _, f := range fields {
		if f.Name == "kubeconfig" {
			kc = f
		}
	}
	if kc.Kind != domain.SecretFieldFile || kc.FileName != "kubeconfig" || kc.SizeBytes == 0 || kc.RevealedValue != "" {
		t.Fatalf("metadata do arquivo: %+v", kc)
	}
	d, err := getSecret(e, c, "prd-cluster", "kubeconfig")
	if err != nil || d.Decision != service.NativeAllow || !strings.HasPrefix(d.Value, sentinelArquivo) {
		t.Fatalf("texto do arquivo: %+v %v", d, err)
	}

	// arquivo binário: a tool recusa e manda usar ${secretfile:}
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "chave", Kind: domain.SecretFieldFile,
		File: []byte{0xff, 0xfe, 0x00, 0x01}, FileName: "chave.bin"})
	if _, err := getSecret(e, c, "prd-cluster", "chave"); err == nil || !strings.Contains(err.Error(), "secretfile") {
		t.Fatalf("binário precisa ser recusado com a dica de ${secretfile:}: %v", err)
	}

	// revelar campo arquivo não existe; apagar o campo apaga a linha do blob (cascata)
	if _, err := e.svc.RevealSecretField(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig"); err == nil {
		t.Fatal("campo arquivo não se revela")
	}
	if err := e.svc.DeleteSecretField(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM secret_field_blobs b WHERE NOT EXISTS (SELECT 1 FROM secret_fields f WHERE f.id=b.field_id)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("blob órfão depois de apagar o campo: %d", n)
	}
}

// Critério 4 com o driver fs: o conteúdo cifrado vai para o BlobStore, e ler o driver direto não acha o texto em claro.
func TestCampoArquivoNoBlobStoreFSCifrado(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	fs, err := storage.NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs)
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo), FileName: "kubeconfig"})

	var fieldID domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT id FROM secret_fields WHERE name='kubeconfig'`).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	raw, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID})
	if err != nil {
		t.Fatalf("driver fs sem o arquivo do campo: %v", err)
	}
	if bytes.Contains(raw, []byte(sentinelArquivo)) || !bytes.HasPrefix(raw, []byte("v1:")) {
		t.Fatalf("o arquivo no driver fs não está cifrado: %.40q", raw)
	}
	// o caminho de leitura decifra com a mesma chave
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	d, err := getSecret(e, c, "prd-cluster", "kubeconfig")
	if err != nil || d.Value != sentinelArquivo {
		t.Fatalf("leitura pelo driver fs: %+v %v", d, err)
	}
	// apagar o campo apaga o objeto do driver depois do commit
	if err := e.svc.DeleteSecretField(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}); err == nil {
		t.Fatal("o objeto do campo apagado continua no driver")
	}
}

// Critério 3: ${secretfile:} vira um arquivo 0600 dentro de um diretório 0700, com o conteúdo do campo; a limpeza apaga
// tudo. Quem chama roda a limpeza ao fim da run, mesmo quando ela falha.
func TestSecretfileArquivo0600ApagadoAoFim(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo), FileName: "kubeconfig"})

	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd-cluster#kubeconfig}"},"model":"m1"}`)
	out, values, cleanup, err := e.svc.MaterializeSecretFiles(ctx, e.co.ID, ana.ID, *c.RunID, cfg)
	if err != nil {
		t.Fatalf("materializar: %v", err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	path := doc.Env["KUBECONFIG"]
	if path == "" || strings.Contains(string(out), "${secretfile:") || strings.Contains(string(out), sentinelArquivo) {
		t.Fatalf("a configuração troca a referência pelo caminho, sem o conteúdo: %s", out)
	}
	if len(values) != 1 || values[0] != sentinelArquivo {
		t.Fatalf("valores para a redação = %q", values)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissão do arquivo = %o, quero 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("permissão do diretório = %o, quero 700", dirInfo.Mode().Perm())
	}
	if got, _ := os.ReadFile(path); string(got) != sentinelArquivo {
		t.Fatalf("conteúdo do arquivo: %q", got)
	}
	dir := filepath.Dir(path)
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("o arquivo sobreviveu à limpeza: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("o diretório sobreviveu à limpeza: %v", err)
	}
}

// Critério 3: uma referência que a persona não alcança falha antes de criar qualquer arquivo.
func TestSecretfileRecusaSemAlcanceSemArquivo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo), FileName: "kubeconfig"})
	if err := e.svc.RemoveSecretFromCollection(ctx, domain.HumanActor, e.co.ID, domain.DefaultCollectionName, "prd-cluster"); err != nil {
		t.Fatal(err)
	}
	cfg := json.RawMessage(`{"env":{"KUBECONFIG":"${secretfile:prd-cluster#kubeconfig}"}}`)
	_, _, cleanup, err := e.svc.MaterializeSecretFiles(ctx, e.co.ID, ana.ID, *c.RunID, cfg)
	if err == nil || strings.Contains(err.Error(), sentinelArquivo) {
		t.Fatalf("sem alcance precisa recusar sem vazar: %v", err)
	}
	cleanup() // a limpeza vazia nunca falha
}

// Rotação da chave do cofre recifra o arquivo do campo: sem isso, ele deixaria de abrir.
func TestRekeyRecifraArquivoDeCampo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo), FileName: "kubeconfig"})

	// a chave nova passa a ser a atual; a antiga fica como anterior, para ler o que ainda não foi recifrado
	novo, err := secretbox.New(keyTwo, keyOne)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Integrations = service.IntegrationsConfig{Secrets: novo, AllowPrivate: true}
	n, err := e.svc.RekeyIntegrationSecrets(ctx)
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	if n == 0 {
		t.Fatal("o rekey não recifrou nada")
	}
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	d, err := getSecret(e, c, "prd-cluster", "kubeconfig")
	if err != nil || d.Value != sentinelArquivo {
		t.Fatalf("depois do rekey: %+v %v", d, err)
	}
}

// Apagar o segredo inteiro leva junto o arquivo dos campos arquivo: no driver fs, o objeto sai depois do commit.
func TestApagarSegredoComArquivoApagaOBlobNoFS(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	fs, err := storage.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Blobs = storage.NewRegistry(fs)
	createSecretWithValue(t, e, "prd-cluster", "principal")
	addField(t, e, "prd-cluster", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile,
		File: []byte(sentinelArquivo), FileName: "kubeconfig"})
	var fieldID domain.ID
	if err := e.svc.Pool.QueryRow(ctx, `SELECT id FROM secret_fields WHERE name='kubeconfig'`).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteSecret(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", true); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Get(ctx, storage.Key{CompanyID: e.co.ID, ArtifactID: fieldID}); err == nil {
		t.Fatal("o arquivo de um campo de segredo apagado continua no driver fs")
	}
}
