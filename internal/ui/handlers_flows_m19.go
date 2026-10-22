package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Variáveis, gatilhos, revisões e arquivar na tela do fluxo (ADR-0047).

// parseVarLines lê a área de texto das variáveis: uma por linha, "nome", "nome!", "nome=padrão" ou "nome!=padrão".
func parseVarLines(text string) []domain.FlowVariable {
	var out []domain.FlowVariable
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, def, hasDef := strings.Cut(line, "=")
		v := domain.FlowVariable{Name: strings.TrimSpace(name)}
		if n, ok := strings.CutSuffix(v.Name, "!"); ok {
			v.Name, v.Required = strings.TrimSpace(n), true
		}
		if hasDef {
			v.Default = strings.TrimSpace(def)
		}
		out = append(out, v)
	}
	if out == nil {
		out = []domain.FlowVariable{}
	}
	return out
}

// formatVarLines é o inverso de parseVarLines.
func formatVarLines(vs []domain.FlowVariable) string {
	var sb strings.Builder
	for _, v := range vs {
		sb.WriteString(v.Name)
		if v.Required {
			sb.WriteString("!")
		}
		if v.Default != "" {
			sb.WriteString("=" + v.Default)
		}
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// parseKVLines lê "chave=valor" por linha.
func parseKVLines(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, domain.Invalid("mapping", "use chave=valor, uma por linha")
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func formatKVLines(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	lines := make([]string, len(keys))
	for i, k := range keys {
		lines[i] = k + "=" + m[k]
	}
	return strings.Join(lines, "\n")
}

func fireSourceLabel(ctx context.Context, src domain.FlowFireSource, trigger string) string {
	var l string
	switch src {
	case domain.FireManual:
		l = i18n.T(ctx, "manual")
	case domain.FireWebhook:
		l = i18n.T(ctx, "webhook")
	case domain.FireEvent:
		l = i18n.T(ctx, "evento")
	default:
		l = i18n.T(ctx, "agenda")
	}
	if trigger != "" {
		l += " · " + trigger
	}
	return l
}

// fireValuesText resume os valores das variáveis de um disparo ("repo=acme/api, ambiente=prod").
func fireValuesText(v map[string]string) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := v[k]
		if r := []rune(val); len(r) > 60 {
			val = string(r[:60]) + "…"
		}
		parts = append(parts, k+"="+val)
	}
	return strings.Join(parts, ", ")
}

func runErrorText(ctx context.Context, err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		return i18n.T(ctx, "Preencha as variáveis obrigatórias.") + " " + ve.Reason + "."
	}
	return err.Error()
}

// publicBase é a URL pública da instalação (HANKO_PUBLIC_URL) ou, sem ela, a do pedido atual.
func (s *server) publicBase(r *http.Request) string {
	if u := strings.TrimRight(s.platform.PublicURL, "/"); u != "" {
		return u
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (s *server) flowM19VM(r *http.Request, co domain.Company, f service.FlowView, fires []service.FlowFireView, base string, loc *time.Location, now time.Time) views.FlowM19VM {
	ctx := r.Context()
	fl := base + "/fluxos/" + url.PathEscape(f.Slug)
	vm := views.FlowM19VM{ArchiveHref: fl + "/arquivo", Archived: f.ArchivedAt != nil, HasSchedule: f.Cron != ""}
	if f.ArchivedAt != nil {
		vm.ArchivedWhen = fmtDateTime(ctx, *f.ArchivedAt, loc)
	}
	vm.Run.Href = fl + "/executar"
	for _, v := range f.Variables {
		vm.Run.Fields = append(vm.Run.Fields, views.RunVarField{Name: v.Name, Label: v.Label, Default: v.Default, Required: v.Required})
	}
	vm.Triggers.AddHref = fl + "/gatilhos"
	for _, t := range f.Triggers {
		row := views.TriggerRow{Name: t.Name, Kind: string(t.Kind), Enabled: t.Enabled, Rate: t.RatePerMin, Last: i18n.T(ctx, "nunca"),
			ToggleHref: fl + "/gatilhos/" + t.ID.String() + "/ligar", DeleteHref: fl + "/gatilhos/" + t.ID.String() + "/remover"}
		if t.Enabled {
			row.ToggleHref = fl + "/gatilhos/" + t.ID.String() + "/desligar"
		}
		if t.LastFiredAt != nil {
			row.Last = agoCtx(ctx, *t.LastFiredAt, now, loc)
		}
		var detail []string
		if t.Kind == domain.TriggerKindWebhook {
			row.KindLabel = i18n.T(ctx, "Webhook")
			row.Target = s.publicBase(r) + service.FlowWebhookPath(t.ID)
			row.Secret = t.SecretRef
		} else {
			row.KindLabel = i18n.T(ctx, "Evento")
			row.Target = t.EventVerb
			if len(t.EventFilter) > 0 {
				detail = append(detail, i18n.T(ctx, "filtro")+": "+strings.ReplaceAll(formatKVLines(t.EventFilter), "\n", ", "))
			}
		}
		if len(t.Mapping) > 0 {
			detail = append(detail, i18n.T(ctx, "variáveis")+": "+strings.ReplaceAll(formatKVLines(t.Mapping), "\n", ", "))
		}
		row.Detail = strings.Join(detail, " · ")
		vm.Triggers.Rows = append(vm.Triggers.Rows, row)
	}
	revs, err := s.be.FlowRevisions(ctx, co.ID, f.ID, 50)
	if err == nil {
		for _, rv := range revs {
			vm.Revisions = append(vm.Revisions, views.RevisionRow{N: rv.Revision, When: fmtDateTime(ctx, rv.CreatedAt, loc),
				Origin: revisionOriginLabel(ctx, rv.Origin), Actor: actorLabelUI(ctx, rv.Actor), Summary: revisionSummary(ctx, rv),
				Current: rv.Revision == f.Revision, RestoreHref: fl + "/revisoes/" + strconv.Itoa(rv.Revision) + "/restaurar"})
		}
	}
	return vm
}

func revisionOriginLabel(ctx context.Context, o string) string {
	switch o {
	case "create":
		return i18n.T(ctx, "criação")
	case "edit":
		return i18n.T(ctx, "edição")
	case "restore":
		return i18n.T(ctx, "restauração")
	case "import":
		return i18n.T(ctx, "importação")
	}
	return i18n.T(ctx, "estado anterior")
}

func revisionSummary(ctx context.Context, rv domain.FlowRevision) string {
	if rv.RestoredFrom != nil {
		return i18n.Tf(ctx, "voltou à revisão %d", *rv.RestoredFrom)
	}
	if rv.Summary == "" {
		return "—"
	}
	return rv.Summary
}

func actorLabelUI(ctx context.Context, a domain.Actor) string {
	switch a.Type {
	case domain.ActorPersona:
		return i18n.T(ctx, "persona")
	case domain.ActorSystem:
		return i18n.T(ctx, "sistema")
	}
	return i18n.T(ctx, "pessoa")
}

func (s *server) flowByParam(w http.ResponseWriter, r *http.Request, co domain.Company) (service.FlowView, bool) {
	f, err := s.be.GetFlow(r.Context(), co.ID, r.PathValue("flow"))
	if err != nil {
		s.fail(w, r, &co, err)
		return service.FlowView{}, false
	}
	return f, true
}

// handleFlowArchive arquiva ou desarquiva (campo acao = arquivar | desarquivar).
func (s *server) handleFlowArchive(w http.ResponseWriter, r *http.Request, co domain.Company) {
	f, ok := s.flowByParam(w, r, co)
	if !ok {
		return
	}
	on := trimForm(r, "acao") != "desarquivar"
	if _, err := s.be.ArchiveFlow(r.Context(), actorFrom(r.Context()), co.ID, f.ID, on); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if on {
		redirect(w, r, "/c/"+co.Slug+"/fluxos?ok=arquivado")
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(f.Slug)+"?ok=desarquivado")
}

func (s *server) handleCreateFlowTrigger(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	f, ok := s.flowByParam(w, r, co)
	if !ok {
		return
	}
	kind := domain.FlowTriggerKind(trimForm(r, "kind"))
	in := service.CreateFlowTriggerInput{Kind: kind, Name: trimForm(r, "name"), EventVerb: trimForm(r, "event_verb"), SecretRef: trimForm(r, "secret_ref")}
	if rt := trimForm(r, "rate"); rt != "" {
		n, err := strconv.Atoi(rt)
		if err != nil {
			s.triggerFormError(w, r, co, f, in, domain.Invalid("rate_per_min", "número inválido"))
			return
		}
		in.RatePerMin = n
	}
	var err error
	if in.EventFilter, err = parseKVLines(r.PostFormValue("filter")); err != nil {
		s.triggerFormError(w, r, co, f, in, err)
		return
	}
	if in.Mapping, err = parseKVLines(r.PostFormValue("mapping")); err != nil {
		s.triggerFormError(w, r, co, f, in, err)
		return
	}
	tv, err := s.be.CreateFlowTrigger(ctx, actorFrom(ctx), co.ID, f.ID, in)
	if err != nil {
		s.triggerFormError(w, r, co, f, in, err)
		return
	}
	// a resposta mostra o segredo gerado uma vez; sem segredo novo, volta para a lista com aviso
	if tv.SecretValue == "" {
		redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(f.Slug)+"?ok=gatilho-criado#gatilhos")
		return
	}
	f2, ok := s.flowByParam(w, r, co)
	if !ok {
		return
	}
	vm, err := s.buildFlowVM(r, co, f2)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Notice = ""
	vm.M19.Triggers.SecretOnce, vm.M19.Triggers.SecretOnceName = tv.SecretValue, tv.Name
	vm.M19.Triggers.SecretOnceURL = s.publicBase(r) + tv.Path
	s.renderFlow(w, r, co, http.StatusCreated, f2, vm)
}

func (s *server) triggerFormError(w http.ResponseWriter, r *http.Request, co domain.Company, f service.FlowView, in service.CreateFlowTriggerInput, err error) {
	ctx := r.Context()
	vm, verr := s.buildFlowVM(r, co, f)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	vm.Notice = ""
	t := &vm.M19.Triggers
	t.FormKind, t.FormName, t.FormVerb, t.FormSecret = string(in.Kind), in.Name, in.EventVerb, in.SecretRef
	t.FormFilter, t.FormMapping = formatKVLines(in.EventFilter), formatKVLines(in.Mapping)
	if in.RatePerMin > 0 {
		t.FormRate = strconv.Itoa(in.RatePerMin)
	}
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		t.Error = ve.Field + ": " + ve.Reason + "."
	} else {
		_, title, msg := friendly(ctx, err)
		t.Error = title + ". " + msg
	}
	s.renderFlow(w, r, co, statusFor(err), f, vm)
}

func (s *server) triggerByParam(w http.ResponseWriter, r *http.Request, co domain.Company) (service.FlowView, domain.ID, bool) {
	f, ok := s.flowByParam(w, r, co)
	if !ok {
		return f, domain.ID{}, false
	}
	id, err := domain.ParseID(r.PathValue("trigger"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return f, domain.ID{}, false
	}
	return f, id, true
}

func (s *server) handleFlowTriggerEnabled(on bool) companyHandler {
	return func(w http.ResponseWriter, r *http.Request, co domain.Company) {
		f, id, ok := s.triggerByParam(w, r, co)
		if !ok {
			return
		}
		if _, err := s.be.UpdateFlowTrigger(r.Context(), actorFrom(r.Context()), co.ID, f.ID, id, service.UpdateFlowTriggerInput{Enabled: &on}); err != nil {
			s.fail(w, r, &co, err)
			return
		}
		to := "gatilho-desligado"
		if on {
			to = "gatilho-ligado"
		}
		redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(f.Slug)+"?ok="+to+"#gatilhos")
	}
}

func (s *server) handleDeleteFlowTrigger(w http.ResponseWriter, r *http.Request, co domain.Company) {
	f, id, ok := s.triggerByParam(w, r, co)
	if !ok {
		return
	}
	if err := s.be.DeleteFlowTrigger(r.Context(), actorFrom(r.Context()), co.ID, f.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(f.Slug)+"?ok=gatilho-removido#gatilhos")
}

func (s *server) handleRestoreFlowRevision(w http.ResponseWriter, r *http.Request, co domain.Company) {
	f, ok := s.flowByParam(w, r, co)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("rev"))
	if err != nil || n < 1 {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	up, err := s.be.RestoreFlowRevision(r.Context(), actorFrom(r.Context()), co.ID, f.ID, n)
	if err != nil {
		s.fail(w, r, &co, fmt.Errorf("restaurar revisão: %w", err))
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/fluxos/"+url.PathEscape(up.Slug)+"?ok=restaurado")
}
