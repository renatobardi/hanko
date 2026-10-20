package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Telas de desenvolvimento (ADR-0058 §E, #70): /design-guide (catálogo do Kubo DS em claro e escuro, base do
// screenshot diff do e2e) e /dev/ux (ux-labs: cenários sintéticos sob carga). Só no modo trusted (desenvolvimento
// local) ou para o dono/admin da instalação; os demais recebem 404, como se a rota não existisse. Nada é gravado.

const (
	designGuidePath = "/design-guide"
	uxPath          = "/dev/ux"
	uxRunTotal      = 10_000
	uxThreadCount   = 500
	uxRunID         = "run-10k"
	uxMaxPage       = 1000
)

// themeOverrideKey carrega o tema forçado por uma rota (o cookie não vale), para a captura do catálogo ser determinística.
type themeOverrideKey struct{}

// devAllowed: trusted (sem login) ou usuário da instalação com papel owner/admin.
func (s *server) devAllowed(r *http.Request) bool {
	if s.auth == nil {
		return true
	}
	id, ok := auth.IdentityFrom(r.Context())
	return ok && (id.User.Role == domain.RoleOwner || id.User.Role == domain.RoleAdmin)
}

func (s *server) devOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.devAllowed(r) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Robots-Tag", "noindex")
		next(w, r)
	}
}

func (s *server) devRoutes(mux handlerRegistry) {
	mux.HandleFunc("GET "+designGuidePath, s.devOnly(s.handleDesignGuide))
	mux.HandleFunc("GET "+uxPath, s.devOnly(s.handleUXIndex))
	mux.HandleFunc("GET "+uxPath+"/thread-longo", s.devOnly(s.handleUXThread))
	mux.HandleFunc("GET "+uxPath+"/run-10k", s.devOnly(s.handleUXRun))
	mux.HandleFunc("GET "+uxPath+"/api/runs/{run}/events", s.devOnly(s.handleUXRunEvents))
}

func (s *server) devNav(ctx context.Context, active string) []views.DevNavItem {
	return []views.DevNavItem{
		{Href: designGuidePath, Label: i18n.T(ctx, "Design guide"), On: active == "dg"},
		{Href: uxPath, Label: i18n.T(ctx, "UX labs"), On: active == "ux"},
	}
}

func (s *server) handleDesignGuide(w http.ResponseWriter, r *http.Request) {
	mode := views.DGBoth
	forced := "light"
	switch r.URL.Query().Get("tema") {
	case "claro", "light":
		mode = views.DGLight
	case "escuro", "dark":
		mode, forced = views.DGDark, "dark"
	}
	r = r.WithContext(context.WithValue(r.Context(), themeOverrideKey{}, forced))
	s.render(w, r, http.StatusOK, views.DesignGuidePage(views.DesignGuideVM{Mode: mode, Version: s.version, Nav: s.devNav(r.Context(), "dg")}))
}

func (s *server) handleUXIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.render(w, r, http.StatusOK, views.UXIndexPage(views.UXIndexVM{Version: s.version, Nav: s.devNav(ctx, "ux"), Labs: []views.UXLab{
		{Href: uxPath + "/thread-longo", Title: i18n.T(ctx, "Thread longo"),
			Blurb: fmt.Sprintf(i18n.T(ctx, "Uma task com %d comentários (persona e humano) no markup real do thread."), uxThreadCount)},
		{Href: uxPath + "/run-10k", Title: i18n.T(ctx, "Run com 10 mil linhas"),
			Blurb: fmt.Sprintf(i18n.T(ctx, "O log virtualizado da run com %d eventos, paginados por uma API sintética."), uxRunTotal)},
	}}))
}

func (s *server) handleUXThread(w http.ResponseWriter, r *http.Request) {
	n := uxThreadCount
	if v, err := strconv.Atoi(r.URL.Query().Get("n")); err == nil && v > 0 && v <= 5000 {
		n = v
	}
	msgs := make([]views.Msg, 0, n)
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		m := views.Msg{When: base.Add(time.Duration(i) * 7 * time.Minute).Format("02/01 15:04")}
		if i%3 == 2 {
			m.Author, m.Human = "Você", true
			m.Body = views.HTML(fmt.Sprintf("<p>Comentário humano #%d: ajuste o escopo e siga.</p>", i+1))
		} else {
			m.Author = []string{"Ada", "Bento", "Cora"}[i%3]
			m.Body = views.HTML(fmt.Sprintf("<p>Atualização #%d da persona. Li o briefing, rodei os testes e registrei o resultado no artefato <code>relatorio-%d.md</code>.</p>", i+1, i%17))
		}
		msgs = append(msgs, m)
	}
	s.render(w, r, http.StatusOK, views.UXThreadPage(views.UXThreadVM{Count: n, Version: s.version, Nav: s.devNav(r.Context(), "ux"), Thread: msgs}))
}

func (s *server) handleUXRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lines := make([]views.LogLine, 0, 5)
	for _, e := range uxEvents(uxRunTotal-4, uxRunTotal) { // o fallback sem JS mostra só o fim (o resto vem pela ilha)
		lines = append(lines, views.LogLine{Seq: e.Seq, Time: e.At.Format("15:04:05"), Kind: e.Kind, Label: e.Kind, Text: e.Text})
	}
	s.render(w, r, http.StatusOK, views.UXRunPage(views.UXRunVM{Total: uxRunTotal, RunID: uxRunID, API: uxPath + "/api", Version: s.version,
		Nav: s.devNav(ctx, "ux"), LogMsgs: runLogMsgs(ctx), Lang: string(i18n.FromContext(ctx)), Lines: lines}))
}

// uxEvent é um evento sintético no formato que a ilha do log lê (api RunEvent).
type uxEvent struct {
	ID         string          `json:"id"`
	RunID      string          `json:"run_id"`
	Seq        int64           `json:"seq"`
	Kind       string          `json:"kind"`
	Text       string          `json:"text"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	OccurredAt string          `json:"occurred_at"`
	At         time.Time       `json:"-"`
}

var uxKinds = []string{"system", "assistant", "thinking", "tool_use", "tool_result", "stdout", "stderr", "result", "status"}

// uxEvents gera, de forma determinística, os eventos com seq em [from, to].
func uxEvents(from, to int64) []uxEvent {
	if from < 1 {
		from = 1
	}
	if to > uxRunTotal {
		to = uxRunTotal
	}
	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	out := make([]uxEvent, 0, max(0, to-from+1))
	for seq := from; seq <= to; seq++ {
		k := uxKinds[(seq*7)%int64(len(uxKinds))]
		e := uxEvent{ID: fmt.Sprintf("ux-%05d", seq), RunID: uxRunID, Seq: seq, Kind: k, At: base.Add(time.Duration(seq) * 300 * time.Millisecond)}
		e.OccurredAt = e.At.Format(time.RFC3339Nano)
		switch k {
		case "tool_use":
			e.Text = fmt.Sprintf("Read src/modulo_%d/arquivo_%d.go", seq%23, seq%97)
			e.Payload = json.RawMessage(fmt.Sprintf(`{"tool":"Read","input":{"path":"src/modulo_%d/arquivo_%d.go"}}`, seq%23, seq%97))
		case "stderr":
			e.Text = fmt.Sprintf("aviso %d: arquivo temporário não removido", seq)
		case "assistant":
			e.Text = fmt.Sprintf("Passo %d: %s", seq, strings.Repeat("analisando o resultado e planejando a próxima leitura; ", 1+int(seq%4)))
		default:
			e.Text = fmt.Sprintf("linha %d de %s", seq, k)
		}
		out = append(out, e)
	}
	return out
}

// handleUXRunEvents imita GET /api/v1/companies/{co}/runs/{id}/events (tail, before, after) sobre os 10 mil eventos.
func (s *server) handleUXRunEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > uxMaxPage {
		limit = 200
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	var from, to int64
	switch {
	case q.Get("tail") == "true":
		from, to = uxRunTotal-int64(limit)+1, uxRunTotal
	case before > 0:
		from, to = before-int64(limit), before-1
	default:
		from, to = after+1, after+int64(limit)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": uxEvents(from, to)})
}
