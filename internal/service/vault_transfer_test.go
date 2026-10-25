package service_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/vaultfile"
)

// Import de .env e export cifrado do cofre (ADR-0068 §8, issue #62).

const (
	sentEnvPass  = "SENTINELA-env-senha-5c1d8e"
	sentEnvHost  = "SENTINELA-env-host-72ab"
	sentEnvMulti = "SENTINELA-linha1\nSENTINELA-linha2"
	sentTotp     = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP"
	sentFile     = "SENTINELA-arquivo-kubeconfig-aa11"
	sentText     = "SENTINELA-campo-token-33ff"
	exportPass   = "senha-do-arquivo-1234"
)

func envContent() string {
	return "# banco de produção\n\nexport DB_HOST=" + sentEnvHost + "\nDB_PASS=\"" + sentEnvPass + " #nao-e-comentario\"  # fim\n" +
		"API_KEY='lit\\n$x'\nVAZIA=\n1RUIM=x\nCERT=\"" + strings.ReplaceAll(sentEnvMulti, "\n", "\\n") + "\"\n"
}

// resolved lê o valor de ${secret:ref} como o despacho lê.
func resolved(t *testing.T, e *secEnv, ref string) string {
	t.Helper()
	got, _, err := e.svc.ResolveSecretText(context.Background(), e.co.ID, "${secret:"+ref+"}")
	if err != nil {
		t.Fatalf("resolver %s: %v", ref, err)
	}
	return got
}

func secretCount(t *testing.T, e *secEnv) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM secrets WHERE company_id=$1`, e.co.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func activityCount(t *testing.T, e *secEnv) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM activity_events WHERE company_id=$1`, e.co.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// .env com comentários, aspas, export e linhas vazias vira um segredo por variável com o valor certo; a prévia não grava.
func TestImportEnvUmSegredoPorVariavel(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	in := service.EnvImportInput{Content: envContent(), Mode: service.EnvImportItems}

	before := activityCount(t, e)
	plan, err := e.svc.PreviewEnvImport(bg, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(plan.Items); n != 4 { // DB_HOST, DB_PASS, API_KEY, CERT; VAZIA e 1RUIM ficam de fora
		t.Fatalf("itens da prévia = %+v", plan.Items)
	}
	if len(plan.Skipped) != 2 || len(plan.Conflicts) != 0 {
		t.Fatalf("prévia: ignoradas %+v, conflitos %v", plan.Skipped, plan.Conflicts)
	}
	if secretCount(t, e) != 0 || activityCount(t, e) != before {
		t.Fatal("a prévia gravou algo")
	}

	res, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Secrets != 4 || res.Fields != 4 || res.Existing != 0 {
		t.Fatalf("resultado = %+v", res)
	}
	for ref, want := range map[string]string{
		"DB_HOST": sentEnvHost,
		"DB_PASS": sentEnvPass + " #nao-e-comentario",
		"API_KEY": `lit\n$x`,
		"CERT":    sentEnvMulti,
	} {
		if got := resolved(t, e, ref); got != want {
			t.Errorf("%s = %q, quer %q", ref, got, want)
		}
	}
	// o valor não vai para atividade nem para o log
	var data string
	if err := e.svc.Pool.QueryRow(bg, `SELECT coalesce(string_agg(data::text, ' '), '') FROM activity_events WHERE company_id=$1`, e.co.ID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{sentEnvHost, sentEnvPass, "SENTINELA-linha1"} {
		if strings.Contains(data, s) || strings.Contains(e.logs.String(), s) {
			t.Fatalf("o valor %q vazou para atividade ou log", s)
		}
	}
}

// single_item: um segredo com um campo por variável; a primeira vira o campo value.
func TestImportEnvUmItemComCampos(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	in := service.EnvImportInput{Content: "DB_HOST=h1\nDB_PASS=p1\nvalue=colide\nDB_USER=u1\n", Mode: service.EnvImportSingleItem, ItemName: "banco"}
	plan, err := e.svc.PreviewEnvImport(bg, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || strings.Join(plan.Items[0].Fields, ",") != "value ← DB_HOST,DB_PASS,DB_USER" {
		t.Fatalf("prévia = %+v", plan.Items)
	}
	if len(plan.Skipped) != 1 || plan.Skipped[0].Line != 3 {
		t.Fatalf("ignoradas = %+v", plan.Skipped)
	}
	res, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.Secrets != 1 || res.Fields != 3 {
		t.Fatalf("resultado = %+v", res)
	}
	for ref, want := range map[string]string{"banco": "h1", "banco#value": "h1", "banco#DB_PASS": "p1", "banco#DB_USER": "u1"} {
		if got := resolved(t, e, ref); got != want {
			t.Errorf("%s = %q, quer %q", ref, got, want)
		}
	}
	if _, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, service.EnvImportInput{Content: "A=1", Mode: service.EnvImportSingleItem, ItemName: "nome inválido!"}); err == nil {
		t.Fatal("nome de item inválido devia falhar")
	}
}

// Nome que já existe: a prévia aponta o conflito, o import pula; com Overwrite rotaciona e guarda o valor anterior.
func TestImportEnvConflitoPulaEOverwriteRotaciona(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "DB_HOST", "antigo")
	in := service.EnvImportInput{Content: "DB_HOST=novo\nOUTRA=2\n", Mode: service.EnvImportItems}

	plan, err := e.svc.PreviewEnvImport(bg, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != "DB_HOST" || !plan.Items[0].Exists || plan.Items[1].Exists {
		t.Fatalf("prévia = %+v", plan)
	}
	res, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in)
	if err != nil || res.Secrets != 1 || res.Existing != 1 || res.Updated != 0 {
		t.Fatalf("sem overwrite: %+v %v", res, err)
	}
	if got := resolved(t, e, "DB_HOST"); got != "antigo" {
		t.Fatalf("DB_HOST = %q: o import pulado não pode trocar o valor", got)
	}

	in.Overwrite = true
	res, err = e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in)
	if err != nil || res.Updated != 2 || res.Secrets != 0 {
		t.Fatalf("com overwrite: %+v %v", res, err)
	}
	if got := resolved(t, e, "DB_HOST"); got != "novo" {
		t.Fatalf("DB_HOST = %q, quer novo", got)
	}
	var versions int
	if err := e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secret_field_versions`).Scan(&versions); err != nil || versions != 2 {
		t.Fatalf("versões no histórico = %d (%v), quer 2", versions, err)
	}
}

// Coleção escolhida: o item entra nela e não na padrão (que é concedida a todas as personas).
func TestImportEnvColecaoEscolhida(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	if _, err := e.svc.CreateCollection(bg, domain.HumanActor, e.co.ID, service.CollectionInput{Name: "infra"}); err != nil {
		t.Fatal(err)
	}
	in := service.EnvImportInput{Content: "A_KEY=1\n", Mode: service.EnvImportItems, Collection: "infra"}
	if _, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in); err != nil {
		t.Fatal(err)
	}
	var cols string
	if err := e.svc.Pool.QueryRow(bg, `SELECT string_agg(c.name, ',' ORDER BY c.name) FROM secret_collection_items i
		JOIN secret_collections c ON c.id = i.collection_id JOIN secrets s ON s.id = i.secret_id WHERE s.name = 'A_KEY'`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if cols != "infra" {
		t.Fatalf("coleções do item = %q, quer só infra", cols)
	}
	in.Collection = "não existe"
	if _, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("coleção inexistente: %v", err)
	}
	for name, bad := range map[string]service.EnvImportInput{
		"modo":  {Content: "A=1", Mode: "x"},
		"vazio": {Content: "# só comentário\n", Mode: service.EnvImportItems},
	} {
		if _, err := e.svc.PreviewEnvImport(bg, e.co.ID, bad); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v, quer ErrInvalid", name, err)
		}
	}
}

// Gravar o import e exportar exigem pessoa, admin e reautenticação recente; sem a prova nada é gravado.
func TestImportEExportExigemReautenticacao(t *testing.T) {
	f := newReauthFixture(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	f.e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	bg := context.Background()
	_, noProof := f.session(t, f.owner)
	in := service.EnvImportInput{Content: "A_KEY=1\n", Mode: service.EnvImportItems}

	ctx := auth.WithIdentity(bg, noProof)
	var re *domain.ReauthError
	if _, err := f.e.svc.ImportEnv(ctx, f.owner.Actor(), f.co.ID, in); !errors.As(err, &re) {
		t.Fatalf("ImportEnv sem prova: %v, quer ReauthError", err)
	}
	if _, err := f.e.svc.ExportVault(ctx, f.owner.Actor(), f.co.ID, exportPass); !errors.As(err, &re) {
		t.Fatalf("ExportVault sem prova: %v, quer ReauthError", err)
	}
	if _, err := f.e.svc.ImportVault(ctx, f.owner.Actor(), f.co.ID, []byte("x"), exportPass); !errors.As(err, &re) {
		t.Fatalf("ImportVault sem prova: %v, quer ReauthError", err)
	}
	var n int
	if err := f.e.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secrets`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("segredos gravados sem prova: %d %v", n, err)
	}

	_, proved := f.provedSession(t)
	ctx = auth.WithIdentity(bg, proved)
	if _, err := f.e.svc.ImportEnv(ctx, f.owner.Actor(), f.co.ID, in); err != nil {
		t.Fatalf("ImportEnv com prova: %v", err)
	}
	if _, err := f.e.svc.ExportVault(ctx, f.owner.Actor(), f.co.ID, exportPass); err != nil {
		t.Fatalf("ExportVault com prova: %v", err)
	}
}

func TestImportEExportRecusamPersona(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	ana := e.persona(t, "Ana", nil)
	actor := domain.Actor{Type: domain.ActorPersona, ID: &ana.ID}
	if _, err := e.svc.ImportEnv(bg, actor, e.co.ID, service.EnvImportInput{Content: "A_KEY=1", Mode: service.EnvImportItems}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ImportEnv por persona: %v", err)
	}
	if _, err := e.svc.ExportVault(bg, actor, e.co.ID, exportPass); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("ExportVault por persona: %v", err)
	}
	if _, err := e.svc.ExportVault(bg, domain.HumanActor, e.co.ID, "curta"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("senha curta: %v", err)
	}
}

// otherInstance é uma segunda instância (outro banco, outra HANKO_SECRET_KEY) com a persona `ana` e um cofre externo.
func otherInstance(t *testing.T) *secEnv {
	t.Helper()
	b := newSecEnv(t)
	box, err := secretbox.New(keyTwo, "")
	if err != nil {
		t.Fatal(err)
	}
	b.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	b.box = box
	b.persona(t, "Ana", nil)
	return b
}

// seedVault monta, na instância a, itens com texto, TOTP, arquivo e referência de cofre externo, duas coleções e concessões.
func seedVault(t *testing.T, a *secEnv) {
	t.Helper()
	bg := context.Background()
	a.persona(t, "Ana", nil)
	a.persona(t, "Beto", nil)
	if _, err := a.svc.CreateOwner(bg, "dona@exemplo.com", "Dona", goodPass); err != nil {
		t.Fatal(err)
	}
	a.create(t, "github", sentEnvPass)
	field := func(secret string, in service.CreateSecretFieldInput) {
		t.Helper()
		if _, err := a.svc.CreateSecretField(bg, domain.HumanActor, a.co.ID, domain.SecretScopeCompany, secret, in); err != nil {
			t.Fatalf("campo %s: %v", in.Name, err)
		}
	}
	visible := false
	field("github", service.CreateSecretFieldInput{Name: "usuario", Sensitive: &visible, SecretFieldValueInput: service.SecretFieldValueInput{Value: "robo"}})
	field("github", service.CreateSecretFieldInput{Name: "token", SecretFieldValueInput: service.SecretFieldValueInput{Value: sentText}})
	field("github", service.CreateSecretFieldInput{Name: "otp", Kind: domain.SecretFieldTOTP, SecretFieldValueInput: service.SecretFieldValueInput{Value: sentTotp}})
	field("github", service.CreateSecretFieldInput{Name: "kubeconfig", Kind: domain.SecretFieldFile, File: []byte(sentFile), FileName: "kubeconfig", ContentType: "text/plain"})
	if _, err := a.svc.CreateSecret(bg, domain.HumanActor, a.co.ID, service.CreateSecretInput{Name: "externo",
		Provider: domain.SecretProviderVault, ExternalRef: "apps/hanko#token"}); err != nil {
		t.Fatal(err)
	}
	a.create(t, "so-na-padrao", "valor-simples")
	if _, err := a.svc.CreateCollection(bg, domain.HumanActor, a.co.ID, service.CollectionInput{Name: "infra", Description: "chaves de infra"}); err != nil {
		t.Fatal(err)
	}
	for _, g := range []service.GrantInput{
		{SubjectType: domain.CollectionSubjectAllPersonas, Permission: domain.CollectionUse},
		{SubjectType: domain.CollectionSubjectPersona, Subject: "ana", Permission: domain.CollectionUse},
		{SubjectType: domain.CollectionSubjectPersona, Subject: "beto", Permission: domain.CollectionUse},
		{SubjectType: domain.CollectionSubjectUser, Subject: "dona@exemplo.com", Permission: domain.CollectionView},
	} {
		if _, err := a.svc.CreateCollectionGrant(bg, domain.HumanActor, a.co.ID, "infra", g); err != nil {
			t.Fatalf("concessão %+v: %v", g, err)
		}
	}
	for _, name := range []string{"github", "externo"} {
		if err := a.svc.AddSecretToCollection(bg, domain.HumanActor, a.co.ID, "infra", name); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.svc.RemoveSecretFromCollection(bg, domain.HumanActor, a.co.ID, domain.DefaultCollectionName, "github"); err != nil {
		t.Fatal(err)
	}
}

// plainOf decifra um campo direto do banco, com a chave da instância e o AAD do campo.
func plainOf(t *testing.T, e *secEnv, secret, field string) string {
	t.Helper()
	var sid, fid, kind, enc string
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT s.id::text, f.id::text, f.kind, f.value_enc FROM secret_fields f
		JOIN secrets s ON s.id = f.secret_id WHERE s.company_id=$1 AND s.name=$2 AND f.name=$3`, e.co.ID, secret, field).Scan(&sid, &fid, &kind, &enc); err != nil {
		t.Fatalf("campo %s#%s: %v", secret, field, err)
	}
	if kind == "file" {
		var blob []byte
		if err := e.svc.Pool.QueryRow(context.Background(), `SELECT content FROM secret_field_blobs WHERE field_id=$1::uuid`, fid).Scan(&blob); err != nil {
			t.Fatal(err)
		}
		enc = string(blob)
	}
	aad := "secret/" + sid + "/field/" + fid
	out, err := e.box.Open(enc, aad)
	if err != nil {
		t.Fatalf("decifrar %s#%s: %v", secret, field, err)
	}
	return string(out)
}

// O export cifrado de uma instância importa noutra com valores iguais, em outra chave de cifragem (#62).
func TestExportCifradoImportaNoutraInstancia(t *testing.T) {
	a, b := newSecEnv(t), otherInstance(t)
	bg := context.Background()
	seedVault(t, a)

	exp, err := a.svc.ExportVault(bg, domain.HumanActor, a.co.ID, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Secrets != 3 {
		t.Fatalf("segredos exportados = %d, quer 3", exp.Secrets)
	}
	res, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, exp.File, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	// 3 segredos; campos: github 5 (value, usuario, token, otp, kubeconfig) + externo 1 + so-na-padrao 1 = 7
	if res.Secrets != 3 || res.Fields != 7 || res.Existing != 0 {
		t.Fatalf("resultado = %+v", res)
	}
	// valores iguais, campo a campo (cada instância decifra com a própria chave)
	for _, c := range [][2]string{{"github", "value"}, {"github", "usuario"}, {"github", "token"}, {"github", "otp"},
		{"github", "kubeconfig"}, {"so-na-padrao", "value"}} {
		if got, want := plainOf(t, b, c[0], c[1]), plainOf(t, a, c[0], c[1]); got != want || got == "" {
			t.Errorf("%s#%s = %q, quer %q", c[0], c[1], got, want)
		}
	}
	if got := plainOf(t, b, "github", "kubeconfig"); got != sentFile {
		t.Errorf("arquivo = %q", got)
	}
	if got := resolved(t, b, "github#token"); got != sentText {
		t.Errorf("resolução do token = %q", got)
	}
	// o campo visível segue visível; o de cofre externo leva a referência, não o valor
	var sensitive bool
	if err := b.svc.Pool.QueryRow(bg, `SELECT f.sensitive FROM secret_fields f JOIN secrets s ON s.id=f.secret_id WHERE s.name='github' AND f.name='usuario'`).Scan(&sensitive); err != nil || sensitive {
		t.Fatalf("usuario sensível = %v (%v), quer visível", sensitive, err)
	}
	var ref string
	if err := b.svc.Pool.QueryRow(bg, `SELECT f.external_ref FROM secret_fields f JOIN secrets s ON s.id=f.secret_id WHERE s.name='externo' AND f.name='value'`).Scan(&ref); err != nil || ref != "apps/hanko#token" {
		t.Fatalf("referência = %q (%v)", ref, err)
	}

	// coleções: infra criada com as duas concessões que fazem sentido no destino; beto e dona não existem lá
	if res.Collections != 1 || res.Grants != 2 || res.Skipped != 2 {
		t.Fatalf("coleções/concessões = %+v, quer 1 coleção, 2 concessões (all_personas e ana), 2 ignoradas (beto e dona)", res)
	}
	var members string
	if err := b.svc.Pool.QueryRow(bg, `SELECT string_agg(c.name || ':' || s.name, ',' ORDER BY c.name, s.name) FROM secret_collection_items i
		JOIN secret_collections c ON c.id=i.collection_id JOIN secrets s ON s.id=i.secret_id`).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if want := "infra:externo,infra:github,padrão:externo,padrão:so-na-padrao"; members != want {
		t.Fatalf("coleções dos itens = %q, quer %q", members, want)
	}
	var persona string
	if err := b.svc.Pool.QueryRow(bg, `SELECT p.slug FROM secret_collection_grants g JOIN personas p ON p.id=g.subject_id WHERE g.subject_type='persona'`).Scan(&persona); err != nil || persona != "ana" {
		t.Fatalf("concessão de persona = %q (%v)", persona, err)
	}

	// importar de novo não duplica nem troca: tudo já existe
	again, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, exp.File, exportPass)
	if err != nil || again.Secrets != 0 || again.Existing != 3 || again.Collections != 0 {
		t.Fatalf("segunda importação = %+v %v", again, err)
	}
}

// O arquivo exportado não contém valor em claro: nem os valores, nem em base64, nem os nomes (#62).
func TestExportCifradoSemValorEmClaro(t *testing.T) {
	a := newSecEnv(t)
	bg := context.Background()
	seedVault(t, a)
	exp, err := a.svc.ExportVault(bg, domain.HumanActor, a.co.ID, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(exp.File, []byte(vaultfile.Magic)) {
		t.Fatal("o arquivo não começa pelo cabeçalho do .hkv")
	}
	for _, s := range []string{sentEnvPass, sentText, sentTotp, sentFile, "robo", "valor-simples", sentinelVT,
		"github", "kubeconfig", "so-na-padrao", "infra", "dona@exemplo.com", exportPass} {
		for name, enc := range map[string][]byte{
			"claro":     []byte(s),
			"base64":    []byte(base64.StdEncoding.EncodeToString([]byte(s))),
			"base64url": []byte(base64.RawURLEncoding.EncodeToString([]byte(s))),
		} {
			if bytes.Contains(exp.File, enc) {
				t.Errorf("o arquivo exportado contém %q em %s", s, name)
			}
		}
	}
	// e o fato do export não leva valor
	var data string
	if err := a.svc.Pool.QueryRow(bg, `SELECT coalesce(string_agg(data::text, ' '), '') FROM activity_events WHERE verb='vault.exported'`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(data, sentEnvPass) || !strings.Contains(data, `"secrets"`) {
		t.Fatalf("fato do export = %s", data)
	}
	if strings.Contains(a.logs.String(), sentText) {
		t.Fatal("o valor vazou para o log")
	}
}

// Senha errada, arquivo adulterado e arquivo que não é .hkv falham sem gravar nada (#62).
func TestImportCofreSenhaErradaNaoGrava(t *testing.T) {
	a, b := newSecEnv(t), otherInstance(t)
	bg := context.Background()
	seedVault(t, a)
	exp, err := a.svc.ExportVault(bg, domain.HumanActor, a.co.ID, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	secrets, events := secretCount(t, b), activityCount(t, b)
	var colls0 int
	if err := b.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secret_collections`).Scan(&colls0); err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Clone(exp.File)
	tampered[len(tampered)-1] ^= 1
	for name, c := range map[string]struct {
		file []byte
		pass string
	}{
		"senha errada":   {exp.File, "outra-senha-qualquer-9"},
		"adulterado":     {tampered, exportPass},
		"não é um .hkv":  {[]byte(`{"secrets":[]}`), exportPass},
		"arquivo vazio":  {nil, exportPass},
		"senha em falta": {exp.File, ""},
	} {
		if _, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, c.file, c.pass); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v, quer ErrInvalid", name, err)
		}
	}
	var colls1 int
	if err := b.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secret_collections`).Scan(&colls1); err != nil {
		t.Fatal(err)
	}
	if secretCount(t, b) != secrets || activityCount(t, b) != events || colls1 != colls0 {
		t.Fatalf("a importação recusada gravou: segredos %d→%d, fatos %d→%d, coleções %d→%d",
			secrets, secretCount(t, b), events, activityCount(t, b), colls0, colls1)
	}
}

// Falha no meio da gravação desfaz tudo: o segundo item pede cofre externo, que o destino não tem (#62).
func TestImportCofreFalhaNoMeioDesfazTudo(t *testing.T) {
	b := otherInstance(t)
	b.svc.Vault = nil
	bg := context.Background()
	doc := map[string]any{"version": 1, "secrets": []map[string]any{
		{"name": "primeiro", "fields": []map[string]any{{"name": "value", "kind": "text", "sensitive": true, "provider": "local", "value": "v1"}}},
		{"name": "segundo", "fields": []map[string]any{{"name": "value", "kind": "text", "sensitive": true, "provider": "vault", "external_ref": "apps/hanko#token"}}},
	}, "collections": []map[string]any{{"name": "nova", "items": []string{"primeiro"}, "grants": []any{}}}}
	plain, _ := json.Marshal(doc)
	file, err := vaultfile.Seal(plain, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, file, exportPass); err == nil {
		t.Fatal("o import devia falhar no cofre externo ausente")
	}
	var colls int
	if err := b.svc.Pool.QueryRow(bg, `SELECT count(*) FROM secret_collections WHERE name='nova'`).Scan(&colls); err != nil {
		t.Fatal(err)
	}
	if n := secretCount(t, b); n != 0 || colls != 0 {
		t.Fatalf("restou %d segredo(s) e %d coleção(ões) de um import que falhou", n, colls)
	}
}

// Conteúdo que o destino recusaria é recusado antes de gravar: nome inválido, sem value e campo de tipo errado.
func TestImportCofreConteudoInvalido(t *testing.T) {
	b := otherInstance(t)
	bg := context.Background()
	field := func(name string) map[string]any {
		return map[string]any{"name": name, "kind": "text", "sensitive": true, "provider": "local", "value": "v"}
	}
	for name, secrets := range map[string][]map[string]any{
		"nome inválido": {{"name": "com espaço", "fields": []any{field("value")}}},
		"sem value":     {{"name": "ok", "fields": []any{field("outro")}}},
		"repetido":      {{"name": "ok", "fields": []any{field("value")}}, {"name": "ok", "fields": []any{field("value")}}},
		"campo ruim":    {{"name": "ok", "fields": []any{field("value"), field("1ruim")}}},
	} {
		plain, _ := json.Marshal(map[string]any{"version": 1, "secrets": secrets})
		file, err := vaultfile.Seal(plain, exportPass)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, file, exportPass); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v, quer ErrInvalid", name, err)
		}
	}
	plain, _ := json.Marshal(map[string]any{"version": 9, "secrets": []any{}})
	file, _ := vaultfile.Seal(plain, exportPass)
	if _, err := b.svc.ImportVault(bg, domain.HumanActor, b.co.ID, file, exportPass); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("versão futura: %v", err)
	}
	if secretCount(t, b) != 0 {
		t.Fatal("conteúdo inválido gravou segredo")
	}
}

// .env acima de 256 KiB é recusado na prévia e na gravação, antes de qualquer linha ser lida (ADR-0068 §8.1, #62).
func TestImportEnvAcimaDe256KiBRecusado(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	in := service.EnvImportInput{Content: "A=" + strings.Repeat("x", 256<<10), Mode: service.EnvImportItems}
	if _, err := e.svc.PreviewEnvImport(bg, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("prévia de .env acima do limite: %v, quer ErrInvalid", err)
	}
	if _, err := e.svc.ImportEnv(bg, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("import de .env acima do limite: %v, quer ErrInvalid", err)
	}
	if n := secretCount(t, e); n != 0 {
		t.Fatalf("segredos gravados com o .env acima do limite: %d", n)
	}
}

// Concessão a persona apagada não vai no export: o sujeito já não existe e a concessão não aponta para ninguém (#62).
func TestExportPulaConcessaoDePersonaApagada(t *testing.T) {
	a := newSecEnv(t)
	bg := context.Background()
	seedVault(t, a)
	if _, err := a.svc.Pool.Exec(bg, `DELETE FROM personas WHERE company_id=$1 AND slug=$2`, a.co.ID, "beto"); err != nil {
		t.Fatal(err)
	}
	exp, err := a.svc.ExportVault(bg, domain.HumanActor, a.co.ID, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := vaultfile.Open(exp.File, exportPass)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), `"subject":"beto"`) {
		t.Fatal("o export levou a concessão da persona apagada")
	}
	if !strings.Contains(string(plain), `"subject":"ana"`) { // sem esta, o teste não prova que a concessão existia
		t.Fatal("o export não levou a concessão da persona que continua")
	}
}
