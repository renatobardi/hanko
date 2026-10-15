package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
)

// deadlineRecorder registra os prazos de leitura que o handler pede ao servidor.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	reads []time.Time
}

func (d *deadlineRecorder) SetReadDeadline(t time.Time) error {
	d.reads = append(d.reads, t)
	return nil
}

// bigImportBody é um documento válido de cerca de 100 KiB.
func bigImportBody() string {
	return `{"format":"hanko.company.v1","company":{"name":"` + strings.Repeat("a", 100<<10) + `"}}`
}

// O padrão do huma (5 s) derrubaria com 408 o upload de um arquivo grande: o import pede um prazo próprio, renovado
// por inatividade (a cada importRenewEvery bytes lidos) em vez de um prazo absoluto fixo.
func TestImportRenovaPrazoDeLeituraPorInatividade(t *testing.T) {
	s := newServer(Deps{Transfer: TransferLimits{ImportIdle: 10 * time.Second, ImportMax: time.Hour}})
	s.svc = &fakeSvc{}
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	s.handleImport(rec, httptest.NewRequest(http.MethodPost, importPath, strings.NewReader(bigImportBody())))

	var set []time.Time
	for _, d := range rec.reads {
		if !d.IsZero() {
			set = append(set, d)
		}
	}
	// 100 KiB / 16 KiB: o prazo inicial mais ao menos 5 renovações
	if len(set) < 3 {
		t.Fatalf("prazo renovado %d vez(es), esperava o inicial e várias renovações por bytes lidos", len(set))
	}
	if d := set[0].Sub(start); d < 9*time.Second || d > 11*time.Second {
		t.Fatalf("prazo inicial = +%v, esperava cerca de +10 s (inatividade)", d)
	}
	if !set[len(set)-1].After(set[0]) {
		t.Fatal("o prazo não avançou com o progresso do upload")
	}
	if !rec.reads[len(rec.reads)-1].IsZero() {
		t.Fatal("o prazo de leitura deveria ser limpo depois de ler o corpo")
	}
}

// O teto absoluto vale mesmo com progresso: nenhuma renovação passa de start+ImportMax.
func TestImportPrazoRespeitaTetoAbsoluto(t *testing.T) {
	s := newServer(Deps{Transfer: TransferLimits{ImportIdle: time.Hour, ImportMax: 5 * time.Second}})
	s.svc = &fakeSvc{}
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Now()
	s.handleImport(rec, httptest.NewRequest(http.MethodPost, importPath, strings.NewReader(bigImportBody())))
	for _, d := range rec.reads {
		if !d.IsZero() && d.Sub(start) > 6*time.Second {
			t.Fatalf("prazo %v acima do teto absoluto de 5 s", d.Sub(start))
		}
	}
	if len(rec.reads) < 2 {
		t.Fatal("sem renovações")
	}
}

// Os padrões: inatividade de 1 min e teto de 15 min.
func TestTransferLimitsPadroes(t *testing.T) {
	l := TransferLimits{}.withDefaults()
	if l.ImportIdle != time.Minute || l.ImportMax != 15*time.Minute || l.ExportTimeout != 15*time.Minute ||
		l.ExportWriteIdle != time.Minute || l.MaxExports != 2 || l.MaxImports != 2 {
		t.Fatalf("%+v", l)
	}
}

// failReader falha o teste se alguém ler o corpo.
type failReader struct {
	t     *testing.T
	reads int
}

func (f *failReader) Read([]byte) (int, error) {
	f.reads++
	f.t.Error("o corpo foi lido apesar de Content-Length acima do limite")
	return 0, io.EOF
}

// Content-Length acima do limite é 413 sem ler o corpo.
func TestImportContentLengthAcimaDoLimite413SemLer(t *testing.T) {
	s := newServer(Deps{})
	s.svc = &fakeSvc{}
	fr := &failReader{t: t}
	req := httptest.NewRequest(http.MethodPost, importPath, fr)
	req.ContentLength = maxImportBytes + 1
	rec := httptest.NewRecorder()
	s.handleImport(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || fr.reads != 0 {
		t.Fatalf("status %d, leituras %d", rec.Code, fr.reads)
	}
	if maxImportBytes != 32<<20 {
		t.Fatalf("limite = %d, esperava 32 MiB", maxImportBytes)
	}
}

// Um ErrAbortHandler do handler é re-panicado pelo middleware (o net/http fecha o transporte), depois de registrar a requisição.
func TestMiddlewareRepanicaErrAbortHandler(t *testing.T) {
	s := newServer(Deps{})
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if r := recover(); r != http.ErrAbortHandler {
			t.Fatalf("recover = %v, esperava http.ErrAbortHandler", r)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	t.Fatal("o middleware engoliu o ErrAbortHandler")
}

// credentialCheck reverifica a credencial que abriu o stream (sessão ou API key); sem identidade, nada.
func TestCredentialCheckPorVia(t *testing.T) {
	f := &fakeSvc{failWith: domain.ErrUnauthenticated}
	s := newServer(Deps{})
	s.svc = f

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if s.credentialCheck(req) != nil {
		t.Fatal("sem identidade (modo trusted) não há o que reverificar")
	}

	sess := httptest.NewRequest(http.MethodGet, "/x", nil)
	sess.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: "tok"})
	sess = sess.WithContext(auth.WithIdentity(sess.Context(), auth.Identity{Via: auth.ViaSession}))
	chk := s.credentialCheck(sess)
	if chk == nil || chk(context.Background()) != domain.ErrUnauthenticated {
		t.Fatal("sessão encerrada deveria falhar com ErrUnauthenticated")
	}

	key := httptest.NewRequest(http.MethodGet, "/x", nil)
	key.Header.Set("Authorization", "Bearer hk_abc")
	key = key.WithContext(auth.WithIdentity(key.Context(), auth.Identity{Via: auth.ViaAPIKey}))
	chk = s.credentialCheck(key)
	if chk == nil || chk(context.Background()) != domain.ErrUnauthenticated {
		t.Fatal("API key revogada deveria falhar com ErrUnauthenticated")
	}
}
