//go:build integration

package api_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// seedEventsRun cria uma execução encerrada com n eventos de size bytes cada (inseridos direto no banco: o
// teste quer volume, não o caminho de escrita).
func (e *apiEnv) seedEventsRun(co domain.Company, n, size int) {
	e.t.Helper()
	ctx := context.Background()
	dev := e.persona(co, "Dev", nil)
	tk := e.task(co, "Trabalho", &dev, nil)
	run, err := e.svc.CheckoutRun(ctx, wakeOf(dev, tk))
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.MarkRunStarted(ctx, co.ID, run.ID, "tr", ""); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO run_events (id, company_id, run_id, seq, kind, text, occurred_at)
		SELECT gen_random_uuid(), $1, $2, g, 'stdout', repeat('abcdefghij ', $4 / 11), now() FROM generate_series(1, $3) g`,
		co.ID, run.ID, n, size); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.FinishRun(ctx, co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: usdRes(1)}); err != nil {
		e.t.Fatal(err)
	}
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("esperou %v por: %s", d, what)
}

// Um cliente que para de ler não pode prender para sempre a conexão do pool nem a transação de snapshot: o prazo
// de escrita por flush estoura, a escrita falha e o handler faz rollback.
func TestAPIExportLeitorParadoLiberaAConexao(t *testing.T) {
	e := newAPIEnvWith(t, func(d *api.Deps) {
		d.Transfer = api.TransferLimits{ExportWriteIdle: 300 * time.Millisecond, ExportTimeout: time.Minute}
	})
	e.seedEventsRun(e.co, 400, 100_000) // ~40 MB: bem acima dos buffers do loopback
	e.svc.ExportPageSize = 20

	conn, err := net.Dial("tcp", strings.TrimPrefix(e.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.(*net.TCPConn).SetReadBuffer(4096)
	fmt.Fprintf(conn, "GET %s/export?with_logs=true HTTP/1.1\r\nHost: x\r\n\r\n", e.base())

	// a transação de snapshot ocupa uma conexão enquanto o export está aberto...
	waitFor(t, 5*time.Second, "export abrir a transação", func() bool { return e.svc.Pool.Stat().AcquiredConns() > 0 })
	// ...e, com o cliente sem ler, é devolvida em tempo finito.
	waitFor(t, 10*time.Second, "conexão do pool liberada com o leitor parado", func() bool { return e.svc.Pool.Stat().AcquiredConns() == 0 })
}

// Os tetos SET LOCAL da transação de snapshot valem no servidor do banco: transação ociosa demais é derrubada
// mesmo se o handler travar sem nunca chamar Close.
func TestExportTxIdleTimeoutNoBanco(t *testing.T) {
	e := newAPIEnv(t)
	e.svc.ExportIdleTxTimeout = 300 * time.Millisecond
	st, err := e.svc.OpenExport(context.Background(), e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // transação ociosa além do teto
	err = st.WriteTo(context.Background(), io.Discard)
	st.Close()
	_ = err // a escrita sem logs não toca o banco; o que importa é o servidor ter encerrado a sessão
	waitFor(t, 3*time.Second, "pool sem conexão presa", func() bool { return e.svc.Pool.Stat().AcquiredConns() == 0 })

	st, err = e.svc.OpenExport(context.Background(), e.co.ID, service.ExportOptions{WithLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("esperava 1 sessão idle in transaction, tem %d (%v)", n, err)
	}
	waitFor(t, 3*time.Second, "o servidor derrubar a transação ociosa", func() bool {
		var n int
		_ = e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction'`).Scan(&n)
		return n == 0
	})
}

// Erro no meio do arquivo: o cliente tem de ver erro de transporte, não um 200 com JSON truncado.
func TestAPIExportFalhaNoMeioRompeAConexao(t *testing.T) {
	e := newAPIEnv(t)
	e.seedEventsRun(e.co, 6, 100)
	e.svc.ExportPageSize = 2
	e.svc.ExportPageHook = func(page int) error {
		if page == 2 {
			return errors.New("falha injetada na 2ª página")
		}
		return nil
	}
	resp, err := http.Get(e.url + e.base() + "/export?with_logs=true")
	if err == nil {
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
	}
	if err == nil {
		t.Fatal("o cliente leu um arquivo truncado como se estivesse completo (sem erro de transporte)")
	}
	waitFor(t, 3*time.Second, "pool sem conexão presa", func() bool { return e.svc.Pool.Stat().AcquiredConns() == 0 })
}

// Teto de exportações simultâneas: cheio, 503 com Retry-After; a vaga volta ao terminar.
func TestAPIExportTetoDeConcorrencia(t *testing.T) {
	e := newAPIEnvWith(t, func(d *api.Deps) { d.Transfer = api.TransferLimits{MaxExports: 1} })
	e.seedEventsRun(e.co, 3, 100)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	e.svc.ExportPageHook = func(int) error {
		once.Do(func() { entered <- struct{}{}; <-release })
		return nil
	}
	url := e.url + e.base() + "/export?with_logs=true"
	first := make(chan int, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			first <- -1
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		first <- resp.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("o primeiro export não começou")
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" || !bytes.Contains(b, []byte("exportações")) {
		t.Fatalf("export acima do teto: %d %v %s", resp.StatusCode, resp.Header, b)
	}
	close(release)
	if st := <-first; st != 200 {
		t.Fatalf("o primeiro export terminou com %d", st)
	}
	e.want(200, "GET", e.base()+"/export?with_logs=true", "", nil) // a vaga foi devolvida
}

// Teto de importações simultâneas: com uma importação em andamento (corpo ainda chegando), a seguinte leva 503.
func TestAPIImportTetoDeConcorrencia(t *testing.T) {
	e := newAPIEnvWith(t, func(d *api.Deps) { d.Transfer = api.TransferLimits{MaxImports: 1} })
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", e.url+"/api/v1/companies/import?slug=lenta", pr)
	done := make(chan *http.Response, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			done <- nil
			return
		}
		done <- resp
	}()
	_, _ = pw.Write([]byte(`{"format":"hanko.company.v1",`)) // a importação segura a vaga enquanto espera o resto

	var got *http.Response
	waitFor(t, 5*time.Second, "503 com a vaga ocupada", func() bool {
		r, err := http.Post(e.url+"/api/v1/companies/import?slug=outra", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		got = r
		return r.StatusCode == 503
	})
	if got.Header.Get("Retry-After") == "" {
		t.Fatal("503 sem Retry-After")
	}
	pw.Close() // JSON truncado: 400, e a vaga volta
	if r := <-done; r == nil || r.StatusCode != 400 {
		t.Fatalf("import lento: %+v", r)
	}
	if st, b := e.call("POST", "/api/v1/companies/import?slug=outra", `{}`, nil); st == 503 {
		t.Fatalf("a vaga não foi devolvida: %s", b)
	}
}

// Sem Content-Length (chunked) o limite vale durante a leitura: 413.
func TestAPIImportChunkedAcimaDoLimite413(t *testing.T) {
	e := newAPIEnv(t)
	head := `{"format":"hanko.company.v1","company":{"name":"`
	body := io.MultiReader(strings.NewReader(head), io.LimitReader(zeroA{}, 33<<20), strings.NewReader(`"}}`))
	req, _ := http.NewRequest("POST", e.url+"/api/v1/companies/import?slug=chunk", body)
	if req.ContentLength != 0 {
		t.Fatalf("o teste precisa de corpo sem tamanho conhecido (ContentLength=%d)", req.ContentLength)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 413 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

type zeroA struct{}

func (zeroA) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}
