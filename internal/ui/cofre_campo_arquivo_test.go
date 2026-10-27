package ui

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// Campo arquivo na tela do segredo (ADR-0068 §5.1, #117): "Novo campo" aceita o tipo file com envio de até 1 MiB, como
// a CLI. O conteúdo vai só ao service; nenhuma resposta da tela o devolve. Campo arquivo não se troca: apaga-se e cria-se
// outro.

const (
	camposPage        = "/c/hanko/configuracoes/segredos/openrouter-key/campos"
	conteudoArquivo   = "SENTINELA-kubeconfig-conteudo-5d3b"
	nomeArquivoCampo  = "kubeconfig-prod.yaml"
	limiteArquivoByte = domain.MaxSecretFieldFileBytes
)

// postCampo manda o formulário "Novo campo" como o navegador manda: multipart com os campos e, se houver, o arquivo.
func postCampo(t *testing.T, e *env, fields map[string]string, file []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if file != nil {
		fw, err := mw.CreateFormFile("arquivo", nomeArquivoCampo)
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
	rq := httptest.NewRequest(http.MethodPost, camposPage, &body)
	rq.Header.Set("Content-Type", mw.FormDataContentType())
	rq.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, rq)
	return rec
}

func TestNovoCampoOferecerArquivoEEnvioMultipart(t *testing.T) {
	e, _ := newCofreEnv(t, domain.MemberAdmin)
	rec := e.get(t, camposPage)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, `<option value="file"`, `name="arquivo"`, `type="file"`, `enctype="multipart/form-data"`)
}

func TestNovoCampoArquivoChegaAoServiceESemVoltarAPagina(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := postCampo(t, e, map[string]string{"name": "kubeconfig", "kind": "file", "visibilidade": "visivel"}, []byte(conteudoArquivo))
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.created) != 1 {
		t.Fatalf("criações no backend = %d, quer 1", len(cf.created))
	}
	in := cf.created[0]
	if in.Kind != domain.SecretFieldFile || string(in.File) != conteudoArquivo || in.FileName != nomeArquivoCampo {
		t.Fatalf("entrada = kind %q, nome %q, %d bytes", in.Kind, in.FileName, len(in.File))
	}
	if in.Sensitive == nil || !*in.Sensitive {
		t.Fatalf("campo arquivo tem de ser secreto mesmo com visibilidade=visivel: %v", in.Sensitive)
	}
	if in.Value != "" || in.Generate {
		t.Fatalf("campo arquivo não leva valor de texto nem geração: %q %v", in.Value, in.Generate)
	}
	wantNotContains(t, rec, conteudoArquivo)
	if loc := rec.Header().Get("Location"); strings.Contains(loc, conteudoArquivo) {
		t.Fatalf("redirecionamento leva o conteúdo: %q", loc)
	}
}

func TestListaDeCamposNuncaTrazOConteudoDoArquivo(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.fileField = true
	rec := e.get(t, camposPage)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "config.yaml", "2 KiB", camposPage+"/kubeconfig/apagar")
	wantNotContains(t, rec, conteudoArquivo, "novo valor") // o campo arquivo não tem formulário de valor
}

func TestNovoCampoArquivoAcimaDe1MiBNaoChegaAoService(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := postCampo(t, e, map[string]string{"name": "grande", "kind": "file"}, bytes.Repeat([]byte("x"), limiteArquivoByte+1))
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if len(cf.created) != 0 {
		t.Fatalf("o arquivo acima de 1 MiB chegou ao service: %d criações", len(cf.created))
	}
	// Exatamente 1 MiB passa o limite da tela.
	rec = postCampo(t, e, map[string]string{"name": "cabe", "kind": "file"}, bytes.Repeat([]byte("x"), limiteArquivoByte))
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.created) != 1 || len(cf.created[0].File) != limiteArquivoByte {
		t.Fatalf("o arquivo de 1 MiB não chegou inteiro: %d criações", len(cf.created))
	}
}

func TestNovoCampoArquivoSemArquivoEhRecusadoPeloService(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.createErr = domain.Invalid("file", "o arquivo tem de ter de 1 byte a 1 MiB")
	rec := postCampo(t, e, map[string]string{"name": "vazio", "kind": "file"}, nil)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, `value="vazio"`, `<option value="file" selected`) // o formulário volta, sem arquivo
	if len(cf.created) != 1 || len(cf.created[0].File) != 0 {
		t.Fatalf("o service devia receber a tentativa sem conteúdo: %+v", cf.created)
	}
}

func TestNovoCampoTextoSegueUrlencoded(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, camposPage, url.Values{"name": {"usuario"}, "kind": {"text"}, "value": {"ana"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.created) != 1 || cf.created[0].File != nil || cf.created[0].Value != "ana" {
		t.Fatalf("entrada = %+v", cf.created)
	}
}

func TestApagarCampoArquivoParaTrocar(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	rec := e.post(t, camposPage+"/kubeconfig/apagar", url.Values{}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(cf.deletedField) != 1 || cf.deletedField[0] != "kubeconfig" {
		t.Fatalf("campos apagados = %v", cf.deletedField)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "ok=campo-apagado") {
		t.Fatalf("Location = %q", loc)
	}
}

func TestNovoCampoArquivoComErroNaoDevolveOConteudo(t *testing.T) {
	e, cf := newCofreEnv(t, domain.MemberAdmin)
	cf.createErr = domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada")
	rec := postCampo(t, e, map[string]string{"name": "kubeconfig", "kind": "file"}, []byte(conteudoArquivo))
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantNotContains(t, rec, conteudoArquivo)
}
