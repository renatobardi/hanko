package ui

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Cofre cifrado (.hkv) na tela (ADR-0068 §8.2 e §8.3, #62): exportar baixa o arquivo; importar envia o arquivo e a
// senha do arquivo. Senha da conta, quando o servidor a pede, é provada pelo backend (ADR-0070). Nenhuma senha volta
// ao HTML, e o arquivo exportado só aparece na resposta do download.

const (
	cofreArquivoPage = "/c/hanko/configuracoes/segredos/cofre"
	cofreExportPath  = cofreArquivoPage + "/exportar"
	cofreImportPath  = cofreArquivoPage + "/importar"
	hkvExportado     = "SENTINELA-hkv-cifrado-7e21"
	senhaArquivo     = "SENTINELA-senha-arquivo-41ce"
	senhaConta       = "SENTINELA-senha-conta-9a0f"
)

// comProva diz se a requisição chegou com a prova de reautenticação no contexto.
func comProva(ctx context.Context) bool {
	id, ok := auth.IdentityFrom(ctx)
	return ok && id.Session.ReauthAt != nil
}

// comSessao põe uma pessoa autenticada por sessão em todas as requisições: a senha da conta tem onde ser provada.
func comSessao(e *env) {
	inner := e.h
	e.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), auth.Identity{User: ana, Via: auth.ViaSession})))
	})
}

// postMultipart manda o formulário de importar como o navegador manda: campos e, se houver, o arquivo.
func postMultipart(t *testing.T, e *env, path string, fields map[string]string, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("arquivo", "cofre.hkv")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	rq := httptest.NewRequest(http.MethodPost, path, &body)
	rq.Header.Set("Content-Type", mw.FormDataContentType())
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	return rec
}

func (c *cofreFake) ExportVault(ctx context.Context, _ domain.Actor, _ domain.ID, password string) (service.VaultExportResult, error) {
	c.exportPass = append(c.exportPass, password)
	if c.needProof && !comProva(ctx) {
		return service.VaultExportResult{}, &domain.ReauthError{WindowMinutes: 5}
	}
	if c.vaultErr != nil {
		return service.VaultExportResult{}, c.vaultErr
	}
	if c.needProof {
		c.proved++
	}
	return service.VaultExportResult{File: []byte(hkvExportado), Secrets: 2}, nil
}

func (c *cofreFake) ImportVault(ctx context.Context, _ domain.Actor, _ domain.ID, file []byte, password string) (service.VaultImportResult, error) {
	c.importFile = append(c.importFile, file)
	c.importPass = append(c.importPass, password)
	if c.needProof && !comProva(ctx) {
		return service.VaultImportResult{}, &domain.ReauthError{WindowMinutes: 5}
	}
	if c.vaultErr != nil {
		return service.VaultImportResult{}, c.vaultErr
	}
	if c.needProof {
		c.proved++
	}
	return service.VaultImportResult{Secrets: 2}, nil
}

func TestVaultFilePageIsAdminOnly(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberMember)
	wantStatus(t, e.get(t, cofreArquivoPage), http.StatusForbidden)

	e, _ = newCofreEnv(t, domain.MemberAdmin)
	rec := e.get(t, cofreArquivoPage)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `action="`+cofreExportPath+`"`, `action="`+cofreImportPath+`"`, `enctype="multipart/form-data"`,
		`name="senha_arquivo"`, `name="arquivo"`, `minlength="12"`)
	wantNotContains(t, rec, `name="senha"`) // a senha da conta só aparece quando o servidor a pede
}

func TestVaultExportDevolveOArquivo(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}}, false)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Body.String(); got != hkvExportado {
		t.Fatalf("corpo = %q, quer só o arquivo cifrado", got)
	}
	if h := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(h, "attachment;") {
		t.Fatalf("Content-Disposition = %q, quer download", h)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, quer no-store", rec.Header().Get("Cache-Control"))
	}
	if len(cf.exportPass) != 1 || cf.exportPass[0] != senhaArquivo {
		t.Fatalf("senhas do arquivo no backend = %v", cf.exportPass)
	}
}

func TestVaultExportSenhaCurtaNaoChegaAoBackend(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {"curta"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "pelo menos 12 caracteres")
	wantNotContains(t, rec, hkvExportado)
	if len(cf.exportPass) != 0 {
		t.Fatalf("senha curta chegou ao backend: %v", cf.exportPass)
	}
}

// Exportar pede a reautenticação recente: sem a senha da conta, o servidor recusa e a tela pede a senha; com ela, o
// backend recebe a prova e o arquivo sai. A senha do arquivo nunca volta ao HTML.
func TestVaultExportPedeReautenticacao(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof = true

	rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}}, false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "prova recente de identidade", `name="senha"`)
	wantNotContains(t, rec, hkvExportado, senhaArquivo)

	rec = e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}, "senha": {senhaConta}}, false)
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Body.String(); got != hkvExportado {
		t.Fatalf("corpo = %q, quer o arquivo", got)
	}
	if len(cf.reauthPasswords) != 1 || cf.reauthPasswords[0] != senhaConta {
		t.Fatalf("senhas da conta no backend = %v", cf.reauthPasswords)
	}
	if cf.proved != 1 {
		t.Fatalf("exportações com prova = %d, quer 1", cf.proved)
	}
}

// Senha da conta errada não exporta: a tela volta com o campo e sem o arquivo, e nenhuma senha aparece na resposta.
func TestVaultExportSenhaDaContaErradaNaoExporta(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof, cf.reauthWrong = true, true

	rec := e.post(t, cofreExportPath, url.Values{"senha_arquivo": {senhaArquivo}, "senha": {"errada-SENTINELA-conta"}}, false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "Senha da conta incorreta.", `name="senha"`)
	wantNotContains(t, rec, hkvExportado, senhaArquivo, "errada-SENTINELA-conta")
	if len(cf.exportPass) != 0 {
		t.Fatalf("exportou com a senha da conta errada: %v", cf.exportPass)
	}
}

func TestVaultImportEnviaArquivoESenha(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != "/c/hanko/configuracoes/segredos?ok=cofre" {
		t.Fatalf("redirect = %q", loc)
	}
	if len(cf.importFile) != 1 || string(cf.importFile[0]) != hkvExportado {
		t.Fatalf("arquivos no backend = %q", cf.importFile)
	}
	if len(cf.importPass) != 1 || cf.importPass[0] != senhaArquivo {
		t.Fatalf("senhas do arquivo no backend = %v", cf.importPass)
	}
}

// Senha do arquivo errada (ou arquivo alterado): o backend recusa, a tela mostra a mensagem e não redireciona.
func TestVaultImportSenhaErradaNaoRedireciona(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.vaultErr = domain.Invalid("password", "senha errada ou arquivo alterado")
	rec := postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Senha errada ou arquivo alterado: nada foi gravado.")
	wantNotContains(t, rec, senhaArquivo, hkvExportado)
}

func TestVaultImportSemArquivo(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, nil)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "Escolha um arquivo .hkv.")
	if len(cf.importFile) != 0 {
		t.Fatalf("import sem arquivo chegou ao backend")
	}
}

// Importar também pede a reautenticação: a primeira tentativa volta com o campo da senha da conta, a segunda grava.
func TestVaultImportPedeReautenticacao(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof = true

	rec := postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "prova recente de identidade", `name="senha"`)
	wantNotContains(t, rec, senhaArquivo)

	rec = postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo, "senha": senhaConta}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusSeeOther)
	if cf.proved != 1 || len(cf.importFile) != 2 {
		t.Fatalf("gravações com prova = %d, arquivos recebidos = %d", cf.proved, len(cf.importFile))
	}
}

// Senha da conta errada no import: nada é gravado e a tela pede a senha de novo.
func TestVaultImportSenhaDaContaErradaNaoGrava(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof, cf.reauthWrong = true, true
	rec := postMultipart(t, e, cofreImportPath, map[string]string{"senha_arquivo": senhaArquivo, "senha": "errada-SENTINELA-conta"}, []byte(hkvExportado))
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "Senha da conta incorreta.", `name="senha"`)
	wantNotContains(t, rec, senhaArquivo, "errada-SENTINELA-conta")
	if len(cf.importFile) != 0 {
		t.Fatalf("importou com a senha da conta errada")
	}
}

// (b) Senha da conta na tela do .env (ReauthWithPassword com sucesso): a prova chega ao backend e a gravação segue.
func TestEnvImportSenhaDaContaProvaReautenticacao(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof = true
	form := envForm("gravar")
	form.Set("senha", senhaConta)
	rec := e.post(t, cofreEnvPage, form, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.reauthPasswords) != 1 || cf.reauthPasswords[0] != senhaConta {
		t.Fatalf("senhas da conta no backend = %v", cf.reauthPasswords)
	}
	if cf.proved != 1 {
		t.Fatalf("gravações com prova = %d, quer 1", cf.proved)
	}
}

// (b) Senha da conta errada na tela do .env: a prova é recusada, nada é gravado e a tela pede a senha de novo.
func TestEnvImportSenhaDaContaErradaNaoGrava(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	comSessao(e)
	cf.needProof, cf.reauthWrong = true, true
	form := envForm("gravar")
	form.Set("senha", "errada-SENTINELA-conta")
	rec := e.post(t, cofreEnvPage, form, false)
	wantStatus(t, rec, http.StatusUnauthorized)
	wantContains(t, rec, "Senha da conta incorreta.", `name="senha"`)
	wantNotContains(t, rec, "errada-SENTINELA-conta")
	if len(cf.envImports) != 0 {
		t.Fatalf("gravou com a senha da conta errada: %d", len(cf.envImports))
	}
}
