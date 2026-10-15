//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Corpo inválido nunca volta na resposta: o erro descreve o problema, não ecoa o arquivo (que traz instruções,
// caminhos e comentários).
func TestAPIImportNaoEcoaOCorpo(t *testing.T) {
	e := newAPIEnv(t)
	const marker = "SEGREDO-DO-ARQUIVO-12345"
	for name, body := range map[string]string{
		"JSON truncado":      `{"format":"hanko.company.v1","company":{"name":"` + marker + `"`,
		"tipo errado":        `{"format":"hanko.company.v1","company":"` + marker + `"}`,
		"lixo depois do doc": `{"format":"hanko.company.v1","company":{"name":"x"}} ` + marker,
	} {
		st, b := e.call("POST", "/api/v1/companies/import?slug=eco", body, nil)
		if st != 400 {
			t.Fatalf("%s: status %d: %s", name, st, b)
		}
		if bytes.Contains(b, []byte(marker)) {
			t.Fatalf("%s: a resposta ecoou o corpo: %s", name, b)
		}
	}
	if st, _ := e.call("POST", "/api/v1/companies/import?slug=eco", "", nil); st != 400 {
		t.Fatalf("corpo vazio: %d", st)
	}
}

// Acima do limite é 413, sem alocar o arquivo inteiro.
func TestAPIImportAcimaDoLimite413(t *testing.T) {
	e := newAPIEnv(t)
	big := `{"format":"hanko.company.v1","company":{"name":"` + strings.Repeat("a", 65<<20) + `"}}`
	st, b := e.call("POST", "/api/v1/companies/import?slug=grande", big, nil)
	if st != 413 {
		t.Fatalf("status %d: %.200s", st, b)
	}
	if bytes.Contains(b, []byte("aaaaaaaaaa")) {
		t.Fatal("a resposta ecoou o corpo")
	}
}

// O export com logs sai em páginas por execução: nunca monta o log inteiro, e o arquivo continua completo,
// em ordem e idêntico ao do modo em memória.
func TestAPIExportComLogsEmPaginas(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	co := e.company("Logs", "logs")
	dev := e.persona(co, "Dev", nil)
	tk := e.task(co, "Trabalho", &dev, nil)
	for r := 0; r < 2; r++ {
		run, err := e.svc.CheckoutRun(ctx, wakeOf(dev, tk))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.MarkRunStarted(ctx, co.ID, run.ID, "tr", ""); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			if _, err := e.svc.AppendRunEvent(ctx, co.ID, run.ID, adapter.Event{Kind: domain.EventAssistant, Text: "linha", OccurredAt: e.now()}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.svc.FinishRun(ctx, co.ID, run.ID, service.FinishRunInput{Status: domain.RunDone, Result: usdRes(1)}); err != nil {
			t.Fatal(err)
		}
	}
	e.svc.ExportPageSize = 2

	st, err := e.svc.OpenExport(ctx, co.ID, service.ExportOptions{WithLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := st.WriteTo(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	st.Close()
	// 2 runs x 5 eventos, páginas de 2: 3 páginas por run (2+2+1). Uma leitura única daria 1 por run.
	if st.Pages() != 6 {
		t.Fatalf("páginas lidas = %d, esperava 6 (paginado por execução)", st.Pages())
	}
	var streamed struct {
		Runs []struct {
			Key    string
			Events []struct{ Seq int64 }
		}
	}
	if err := json.Unmarshal(buf.Bytes(), &streamed); err != nil {
		t.Fatalf("JSON de streaming inválido: %v", err)
	}
	if len(streamed.Runs) != 2 {
		t.Fatalf("runs = %d", len(streamed.Runs))
	}
	for _, r := range streamed.Runs {
		if len(r.Events) != 5 {
			t.Fatalf("run %s: %d eventos", r.Key, len(r.Events))
		}
		for i, ev := range r.Events {
			if ev.Seq != int64(i+1) {
				t.Fatalf("run %s: eventos fora de ordem: %+v", r.Key, r.Events)
			}
		}
	}

	// o mesmo arquivo pela API, e o modo em memória, dão o mesmo documento
	viaAPI := normalized(t, e.exportRaw("logs", "?with_logs=true"))
	mem, err := e.svc.ExportCompany(ctx, co.ID, service.ExportOptions{WithLogs: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(mem)
	var inMem map[string]any
	_ = json.Unmarshal(b, &inMem)
	delete(inMem, "exported_at")
	inMem["company"].(map[string]any)["slug"] = "-"
	if ja, jb := mustJSON(viaAPI), mustJSON(inMem); ja != jb {
		t.Fatalf("streaming difere do modo em memória\n%s\n---\n%s", ja, jb)
	}
	// execuções sem logs não ganham a chave events
	if strings.Contains(string(e.exportRaw("logs", "?with_runs=true")), `"events"`) {
		t.Fatal("with_runs não traz eventos")
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// Textos livres passam pelo redator: credenciais coladas em instruções, descrições e comentários não saem no
// arquivo (que não é anônimo, mas também não deve carregar segredo).
func TestAPIExportRedigeTextosLivres(t *testing.T) {
	e := newAPIEnv(t)
	ctx := context.Background()
	co := e.company("Segredos", "segredos")
	dev := e.persona(co, "Dev", func(in *service.HirePersonaInput) {
		in.Instructions = "chame a API com Authorization: Bearer abcdefghijklmnop1234 sempre"
	})
	tk := e.task(co, "Deploy", &dev, func(in *service.CreateTaskInput) {
		in.Description = "banco em postgres://app:senhasecreta@db:5432/x e chave sk-ant-api03-ABCDEFGHIJKLMNOP"
	})
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, co.ID, tk.ID, service.AddCommentInput{Body: "token ghp_abcdefghijklmnopqrstuvwxyz0123"}); err != nil {
		t.Fatal(err)
	}
	raw := string(e.exportRaw("segredos", ""))
	for _, leak := range []string{"abcdefghijklmnop1234", "senhasecreta", "sk-ant-api03", "ghp_abcdefghijklmnop"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("o arquivo vazou %q: %s", leak, raw)
		}
	}
	if !strings.Contains(raw, "[REDACTED]") || !strings.Contains(raw, "chame a API") {
		t.Fatalf("o texto deveria permanecer, só sem o segredo: %s", raw)
	}
}
