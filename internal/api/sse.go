package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
)

const (
	// pingEvery é o intervalo do heartbeat de comentário `: ping`.
	pingEvery = 15 * time.Second
	// replayPage é o tamanho de cada página lida no replay.
	replayPage = 500
)

// runSource é o que o stream precisa do service (substituível em testes).
type runSource interface {
	GetRun(ctx context.Context, companyID, runID domain.ID) (service.RunView, error)
	ListRunEvents(ctx context.Context, companyID, runID domain.ID, afterSeq int64, limit int) ([]domain.RunEvent, error)
}

const streamPath = Prefix + "/companies/{company}/runs/{run}/stream"

// registerStream monta o SSE como handler net/http puro (fora do huma) e o documenta.
func (s *server) registerStream(wm wrapMux, api huma.API) {
	s.registerStreamDoc(api)
	wm.HandleFunc("GET "+streamPath, s.handleStream)
}

// registerStreamDoc descreve o SSE no OpenAPI (a rota em si não passa pelo huma).
func (s *server) registerStreamDoc(api huma.API) {
	api.OpenAPI().AddOperation(&huma.Operation{
		OperationID: "stream-run",
		Method:      http.MethodGet,
		Path:        streamPath,
		Tags:        []string{"runs"},
		Summary:     "Acompanha uma execução ao vivo (SSE)",
		Description: "Replay do log a partir de `after` (ou do header `Last-Event-ID`) e depois eventos ao vivo. " +
			"Eventos: `run.event` (`id` = seq; `data` = RunEvent) e `run.status` (`data` = Run), este último ao ficar terminal, " +
			"quando a conexão é encerrada. Comentários `: ping` a cada 15 s.",
		Parameters: []*huma.Param{
			{Name: "company", In: "path", Required: true, Description: "Id ou slug da empresa.", Schema: &huma.Schema{Type: "string"}},
			{Name: "run", In: "path", Required: true, Description: "Id da execução.", Schema: &huma.Schema{Type: "string", Format: "uuid"}},
			{Name: "after", In: "query", Description: "Só eventos com seq maior que este valor.", Schema: &huma.Schema{Type: "integer", Minimum: ptr(0.0), Default: 0}},
			{Name: "Last-Event-ID", In: "header", Description: "Último seq recebido; tem precedência sobre `after`.", Schema: &huma.Schema{Type: "integer"}},
		},
		Responses: map[string]*huma.Response{
			"200": {Description: "Fluxo `text/event-stream`.", Content: map[string]*huma.MediaType{
				"text/event-stream": {Schema: &huma.Schema{Type: "string"}}}},
			"404": {Description: "Execução não encontrada."},
		},
	})
}

func ptr[T any](v T) *T { return &v }

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc, err := s.scope(ctx, r.PathValue("company"))
	if err != nil {
		p, _ := toProblem(err)
		writeProblem(w, p)
		return
	}
	rid, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		writeProblem(w, problem(http.StatusUnprocessableEntity, "run: não é um uuid"))
		return
	}
	after, err := streamCursor(r)
	if err != nil {
		p, _ := toProblem(err)
		writeProblem(w, p)
		return
	}
	var broker *events.Broker
	if s.d.Broker != nil {
		broker = s.d.Broker
	} else {
		broker = events.NewBroker()
	}
	streamRun(ctx, w, s.svc, broker, s.log, sc.Company.ID, rid, after, pingEvery)
}

// streamCursor lê o ponto de partida: Last-Event-ID (reconexão) vence `after`.
func streamCursor(r *http.Request) (int64, error) {
	raw := r.Header.Get("Last-Event-ID")
	field := "Last-Event-ID"
	if raw == "" {
		raw, field = r.URL.Query().Get("after"), "after"
	}
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, domain.Invalid(field, "esperado inteiro >= 0")
	}
	return n, nil
}

// streamRun faz replay a partir de after e depois repassa eventos ao vivo do broker,
// até a execução ficar terminal ou o cliente sair.
func streamRun(ctx context.Context, w http.ResponseWriter, src runSource, broker *events.Broker, log *slog.Logger,
	companyID, runID domain.ID, after int64, ping time.Duration) {
	// Assina antes de ler: nada se perde entre o replay e o ao vivo (duplicatas saem pelo seq).
	ch, cancel := broker.Subscribe(events.RunTopic(runID.String()), 256)
	defer cancel()

	run, err := src.GetRun(ctx, companyID, runID)
	if err != nil {
		p, known := toProblem(err)
		if !known {
			log.ErrorContext(ctx, "stream: GetRun", "err", err, "request_id", requestIDFrom(ctx))
		}
		writeProblem(w, p)
		return
	}

	fl, ok := w.(http.Flusher)
	if !ok {
		fl, ok = flusherOf(w)
	}
	if !ok {
		writeProblem(w, problem(http.StatusInternalServerError, "streaming não suportado"))
		return
	}
	// Streams longos não devem sofrer o WriteTimeout do servidor.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "retry: 3000\n\n")
	fl.Flush()

	last := after
	var werr error
	send := func(event, id string, v any) {
		if werr != nil {
			return
		}
		b, err := json.Marshal(v)
		if err != nil {
			werr = err
			return
		}
		if id != "" {
			_, werr = fmt.Fprintf(w, "id: %s\n", id)
		}
		if werr == nil {
			_, werr = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		}
		fl.Flush()
	}
	emit := func(e domain.RunEvent) {
		if e.Seq <= last {
			return
		}
		send("run.event", strconv.FormatInt(e.Seq, 10), runEventOut(e))
		last = e.Seq
	}
	// catchUp lê do banco tudo o que passou de `last` (replay inicial, lacunas e descartes do broker).
	catchUp := func() bool {
		for werr == nil {
			es, err := src.ListRunEvents(ctx, companyID, runID, last, replayPage)
			if err != nil {
				if ctx.Err() == nil {
					log.ErrorContext(ctx, "stream: ListRunEvents", "err", err, "run_id", runID.String())
				}
				return false
			}
			for _, e := range es {
				emit(e)
			}
			if len(es) < replayPage {
				break
			}
		}
		return werr == nil
	}
	finish := func(status domain.RunStatus) {
		if !catchUp() {
			return
		}
		cur := run
		if r2, err := src.GetRun(ctx, companyID, runID); err == nil {
			cur = r2
		}
		cur.Status = status
		send("run.status", "", runOut(cur))
	}

	if !catchUp() {
		return
	}
	if run.Status.Terminal() {
		finish(run.Status)
		return
	}

	tick := time.NewTicker(ping)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, open := <-ch:
			if !open {
				return
			}
			switch msg.Type {
			case "run.event":
				e, ok := asRunEvent(msg.Data)
				switch {
				case !ok, e.Seq > last+1:
					if !catchUp() {
						return
					}
				default:
					emit(e)
				}
			case "run.status":
				st := statusOf(msg.Data)
				if st.Terminal() && st.Valid() {
					finish(st)
					return
				}
				if st.Valid() {
					cur := run
					if r2, err := src.GetRun(ctx, companyID, runID); err == nil {
						cur = r2
					}
					cur.Status = st
					send("run.status", "", runOut(cur))
				}
			}
		case <-tick.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
			// Rede de segurança: recupera descartes do broker e perda de run.status.
			if !catchUp() {
				return
			}
			if cur, err := src.GetRun(ctx, companyID, runID); err == nil && cur.Status.Terminal() {
				finish(cur.Status)
				return
			}
		}
		if werr != nil {
			return
		}
	}
}

func flusherOf(w http.ResponseWriter) (http.Flusher, bool) {
	for {
		if f, ok := w.(http.Flusher); ok {
			return f, true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		w = u.Unwrap()
	}
}

// asRunEvent extrai o evento publicado pelo service (domain.RunEvent ou equivalente JSON).
func asRunEvent(data any) (domain.RunEvent, bool) {
	switch v := data.(type) {
	case domain.RunEvent:
		return v, true
	case *domain.RunEvent:
		if v != nil {
			return *v, true
		}
		return domain.RunEvent{}, false
	}
	b, err := json.Marshal(data)
	if err != nil {
		return domain.RunEvent{}, false
	}
	var e domain.RunEvent
	if json.Unmarshal(b, &e) != nil || e.Seq == 0 {
		return domain.RunEvent{}, false
	}
	return e, true
}

// statusOf extrai o status de um run.status (domain.Run, RunView ou JSON com "status").
func statusOf(data any) domain.RunStatus {
	switch v := data.(type) {
	case domain.RunStatus:
		return v
	case string:
		return domain.RunStatus(v)
	case domain.Run:
		return v.Status
	case *domain.Run:
		if v != nil {
			return v.Status
		}
	case service.RunView:
		return v.Status
	case *service.RunView:
		if v != nil {
			return v.Status
		}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var s struct {
		Status domain.RunStatus `json:"status"`
	}
	_ = json.Unmarshal(b, &s)
	return s.Status
}
