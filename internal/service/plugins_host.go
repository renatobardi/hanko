package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/plugin"
	"github.com/renatobardi/hanko/internal/store"
)

// O host (ADR-0038): o que o plugin pode pedir ao Hanko. Cada chamada é checada, nesta ordem: plugin instalado
// e ativo; método conhecido; teto de chamadas por minuto; empresa existente com o plugin ligado; capacidade
// concedida. Nenhum método decide gate, mexe em orçamento, autenticação ou segredos, nem dá acesso ao banco.
// O plugin age como ator de sistema (actor_type system, actor_id = id do plugin).

// HandlePluginCall é o Handler do gerenciador de processos.
func (s *Service) HandlePluginCall(ctx context.Context, key, method string, params json.RawMessage) (any, *plugin.RPCError) {
	// o plugin nunca herda identidade de usuário: o contexto vem do gerenciador, sem auth.Identity
	if _, ok := auth.IdentityFrom(ctx); ok {
		return nil, plugin.Errorf(plugin.CodeDenied, "contexto inválido")
	}
	r, err := s.read().GetPluginByKey(ctx, key)
	if err != nil {
		return nil, plugin.Errorf(plugin.CodeDenied, "plugin não instalado")
	}
	p := pluginFromRow(r)
	if !p.Status.WantsProcess() {
		return nil, plugin.Errorf(plugin.CodeDenied, "plugin desligado")
	}
	capNeed, known := domain.PluginMethodCapability[method]
	if !known {
		return nil, plugin.Errorf(plugin.CodeMethodNotFound, "método desconhecido: %s", method)
	}
	if !s.allowPluginCall(key) {
		return nil, plugin.Errorf(plugin.CodeBusy, "limite de %d chamadas por minuto atingido", pluginCallsPerMinute)
	}
	var base struct {
		Company string `json:"company"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &base); err != nil {
			return nil, plugin.Errorf(plugin.CodeInvalidParams, "params precisa ser um objeto JSON")
		}
	}
	if method == "log" && base.Company == "" {
		res, err := s.hostLog(ctx, p, nil, params)
		if err != nil {
			return nil, hostErr(err)
		}
		return res, nil
	}
	co, rerr := s.pluginCompany(ctx, p, base.Company)
	if rerr != nil {
		return nil, rerr
	}
	if capNeed != "" && !p.Has(capNeed) {
		s.pluginLog(ctx, p.ID, &co.ID, domain.PluginLogHost, "chamada negada: "+method+" exige a capacidade "+capNeed)
		return nil, plugin.Errorf(plugin.CodeDenied, "capacidade %s não concedida", capNeed)
	}
	if capNeed != "" && !p.Status.Active() {
		return nil, plugin.Errorf(plugin.CodeDenied, "plugin com erro: aguarde ficar pronto")
	}
	res, err := s.hostDispatch(ctx, p, co, method, params)
	if err != nil {
		return nil, hostErr(err)
	}
	return res, nil
}

// pluginCompany resolve a empresa (id ou slug) e confere que ela ligou o plugin. Empresa inexistente e plugin
// desligado dão a mesma resposta, para o plugin não descobrir empresas que não o ligaram.
func (s *Service) pluginCompany(ctx context.Context, p domain.Plugin, ref string) (store.Company, *plugin.RPCError) {
	denied := plugin.Errorf(plugin.CodeDenied, "plugin não está ligado nesta empresa")
	if strings.TrimSpace(ref) == "" {
		return store.Company{}, plugin.Errorf(plugin.CodeInvalidParams, "company é obrigatório")
	}
	q := s.read()
	var co store.Company
	var err error
	if id, perr := domain.ParseID(ref); perr == nil {
		co, err = q.GetCompanyByID(ctx, id)
	} else {
		co, err = q.GetCompanyBySlug(ctx, ref)
	}
	if err != nil {
		return store.Company{}, denied
	}
	st, err := q.GetPluginSettings(ctx, store.GetPluginSettingsParams{PluginID: p.ID, CompanyID: co.ID})
	if err != nil || !st.Enabled {
		return store.Company{}, denied
	}
	return co, nil
}

func hostErr(err error) *plugin.RPCError {
	var re *plugin.RPCError
	if errors.As(err, &re) {
		return re
	}
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		return plugin.Errorf(plugin.CodeInvalid, "%s: %s", ve.Field, ve.Reason)
	case errors.Is(err, domain.ErrNotFound):
		return plugin.Errorf(plugin.CodeNotFound, "não encontrado")
	case errors.Is(err, domain.ErrConflict):
		return plugin.Errorf(plugin.CodeInvalid, "conflito: %s", pluginClip(err.Error(), 300))
	case errors.Is(err, domain.ErrNotAllowed):
		return plugin.Errorf(plugin.CodeDenied, "não permitido")
	}
	return plugin.Errorf(plugin.CodeInternalError, "erro interno do host")
}

func (s *Service) allowPluginCall(key string) bool {
	rt := s.prt()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	w := rt.calls[key]
	if w == nil {
		w = &window{start: s.Now()}
		rt.calls[key] = w
	}
	ok, _ := w.allow(s.Now(), pluginCallsPerMinute)
	return ok
}

func pluginActor(p domain.Plugin) domain.Actor {
	id := p.ID
	return domain.Actor{Type: domain.ActorSystem, ID: &id}
}

// pluginEventID prefixa a chave de idempotência do plugin (não colide com as das personas nem de outro plugin).
func pluginEventID(p domain.Plugin, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return pluginClip("plugin:"+p.Key+":"+id, 200)
}

func decode[T any](params json.RawMessage) (T, error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, plugin.Errorf(plugin.CodeInvalidParams, "params inválidos: %s", pluginClip(err.Error(), 200))
	}
	return v, nil
}

// hostTask é a task como o plugin a vê.
type hostTask struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	Persona     string `json:"persona,omitempty"`
	Project     string `json:"project,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toHostTask(t TaskView) hostTask {
	return hostTask{ID: t.ID.String(), Key: t.Key, Title: t.Title, Description: t.Description, Status: string(t.Status),
		Priority: string(t.Priority), Persona: t.PersonaName, Project: t.ProjectName,
		CreatedAt: t.CreatedAt.Format(time.RFC3339), UpdatedAt: t.UpdatedAt.Format(time.RFC3339)}
}

func (s *Service) hostDispatch(ctx context.Context, p domain.Plugin, co store.Company, method string, params json.RawMessage) (any, error) {
	cid := co.ID
	actor := pluginActor(p)
	switch method {
	case "flows.fire":
		return s.hostFlowsFire(ctx, p, co, params)
	case "tasks.list":
		in, err := decode[struct {
			Status  []string `json:"status"`
			Persona string   `json:"persona"`
			Project string   `json:"project"`
			Limit   int      `json:"limit"`
		}](params)
		if err != nil {
			return nil, err
		}
		f := TaskFilter{Limit: clamp(in.Limit, 50, 200)}
		for _, st := range in.Status {
			if !domain.TaskStatus(st).Valid() {
				return nil, domain.Invalid("status", "status inválido: "+st)
			}
			f.Statuses = append(f.Statuses, domain.TaskStatus(st))
		}
		if in.Persona != "" {
			pr, err := s.GetPersona(ctx, cid, in.Persona)
			if err != nil {
				return nil, err
			}
			f.PersonaID = &pr.ID
		}
		if in.Project != "" {
			pj, err := s.GetProject(ctx, cid, in.Project)
			if err != nil {
				return nil, err
			}
			f.ProjectID = &pj.ID
		}
		ts, err := s.ListTasks(ctx, cid, f)
		if err != nil {
			return nil, err
		}
		out := make([]hostTask, 0, len(ts))
		for _, t := range ts {
			out = append(out, toHostTask(t))
		}
		return map[string]any{"tasks": out}, nil
	case "tasks.get":
		in, err := decode[struct {
			Key string `json:"key"`
		}](params)
		if err != nil {
			return nil, err
		}
		t, err := s.GetTask(ctx, cid, in.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": toHostTask(t)}, nil
	case "tasks.create":
		in, err := decode[struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    string `json:"priority"`
			Assignee    string `json:"assignee"`
			Project     string `json:"project"`
			EventID     string `json:"event_id"`
		}](params)
		if err != nil {
			return nil, err
		}
		ci := CreateTaskInput{Title: in.Title, Description: in.Description, Priority: domain.Priority(in.Priority), EventID: pluginEventID(p, in.EventID)}
		if in.Assignee != "" {
			pr, err := s.GetPersona(ctx, cid, in.Assignee)
			if err != nil {
				return nil, err
			}
			ci.PersonaID = &pr.ID
		}
		if in.Project != "" {
			pj, err := s.GetProject(ctx, cid, in.Project)
			if err != nil {
				return nil, err
			}
			ci.ProjectID = &pj.ID
		}
		t, err := s.CreateTask(ctx, actor, cid, ci)
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": toHostTask(t)}, nil
	case "tasks.comment":
		in, err := decode[struct {
			Key     string `json:"key"`
			Body    string `json:"body"`
			EventID string `json:"event_id"`
		}](params)
		if err != nil {
			return nil, err
		}
		t, err := s.GetTask(ctx, cid, in.Key)
		if err != nil {
			return nil, err
		}
		c, err := s.AddComment(ctx, actor, cid, t.ID, AddCommentInput{Body: in.Body, EventID: pluginEventID(p, in.EventID)})
		if err != nil {
			return nil, err
		}
		return map[string]any{"comment": map[string]any{"id": c.ID.String(), "task": t.Key, "created_at": c.CreatedAt.Format(time.RFC3339)}}, nil
	case "tasks.update_status":
		in, err := decode[struct {
			Key     string `json:"key"`
			Status  string `json:"status"`
			EventID string `json:"event_id"`
		}](params)
		if err != nil {
			return nil, err
		}
		st := domain.TaskStatus(in.Status)
		if !st.Valid() {
			return nil, domain.Invalid("status", "status inválido")
		}
		t, err := s.GetTask(ctx, cid, in.Key)
		if err != nil {
			return nil, err
		}
		u, err := s.UpdateTask(ctx, actor, cid, t.ID, UpdateTaskInput{Status: &st, EventID: pluginEventID(p, in.EventID)})
		if err != nil {
			return nil, err
		}
		return map[string]any{"task": toHostTask(u)}, nil
	case "personas.list":
		ps, err := s.ListPersonas(ctx, cid)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]string, 0, len(ps))
		for _, pr := range ps {
			// nunca instruções nem adapter_config (pode ter variáveis do harness)
			out = append(out, map[string]string{"id": pr.ID.String(), "slug": pr.Slug, "name": pr.Name, "title": pr.Title, "status": string(pr.Status)})
		}
		return map[string]any{"personas": out}, nil
	case "projects.list":
		pjs, err := s.ListProjects(ctx, cid)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]string, 0, len(pjs))
		for _, pj := range pjs {
			out = append(out, map[string]string{"id": pj.ID.String(), "slug": pj.Slug, "name": pj.Name, "description": pj.Description, "status": string(pj.Status)})
		}
		return map[string]any{"projects": out}, nil
	case "activity.list":
		in, err := decode[struct {
			Limit  int    `json:"limit"`
			Before string `json:"before"`
		}](params)
		if err != nil {
			return nil, err
		}
		f := ActivityFilter{Limit: clamp(in.Limit, 50, 200)}
		if in.Before != "" {
			b, err := time.Parse(time.RFC3339, in.Before)
			if err != nil {
				return nil, domain.Invalid("before", "use RFC 3339")
			}
			f.Before = &b
		}
		evs, err := s.ListActivity(ctx, cid, f)
		if err != nil {
			return nil, err
		}
		out := make([]pluginEvent, 0, len(evs))
		for _, e := range evs {
			out = append(out, toPluginEvent(e))
		}
		return map[string]any{"events": out}, nil
	case "state.get", "state.set", "state.delete", "state.list":
		return s.hostState(ctx, p, cid, method, params)
	case "config.get":
		cfg, err := s.pluginConfigFor(ctx, p, cid)
		if err != nil {
			return nil, err
		}
		return map[string]any{"config": cfg}, nil
	case "log":
		return s.hostLog(ctx, p, &cid, params)
	}
	return nil, plugin.Errorf(plugin.CodeMethodNotFound, "método desconhecido: %s", method)
}

func (s *Service) hostLog(ctx context.Context, p domain.Plugin, companyID *domain.ID, params json.RawMessage) (any, error) {
	in, err := decode[struct {
		Level   string `json:"level"`
		Message string `json:"message"`
	}](params)
	if err != nil {
		return nil, err
	}
	if in.Level == "" {
		in.Level = domain.PluginLogInfo
	}
	if !domain.ValidPluginLogLevel(in.Level) {
		return nil, domain.Invalid("level", "use debug, info, warn ou error")
	}
	s.pluginLog(ctx, p.ID, companyID, in.Level, in.Message)
	return map[string]any{"ok": true}, nil
}

func (s *Service) hostState(ctx context.Context, p domain.Plugin, cid domain.ID, method string, params json.RawMessage) (any, error) {
	in, err := decode[struct {
		Key    string          `json:"key"`
		Value  json.RawMessage `json:"value"`
		Prefix string          `json:"prefix"`
		Limit  int             `json:"limit"`
	}](params)
	if err != nil {
		return nil, err
	}
	if method != "state.list" && (in.Key == "" || len(in.Key) > domain.PluginStateKeyMax || !utf8.ValidString(in.Key)) {
		return nil, domain.Invalid("key", fmt.Sprintf("obrigatória, até %d bytes", domain.PluginStateKeyMax))
	}
	switch method {
	case "state.get":
		r, err := s.read().GetPluginState(ctx, store.GetPluginStateParams{PluginID: p.ID, CompanyID: cid, Key: in.Key})
		if isNoRows(err) {
			return map[string]any{"found": false}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"found": true, "value": json.RawMessage(r.Value)}, nil
	case "state.set":
		if len(in.Value) == 0 || !json.Valid(in.Value) {
			return nil, domain.Invalid("value", "obrigatório (qualquer JSON)")
		}
		if len(in.Value) > domain.PluginStateValueMax {
			return nil, domain.Invalid("value", fmt.Sprintf("maior que %d KiB", domain.PluginStateValueMax>>10))
		}
		err := s.withTx(ctx, func(t *txc) error {
			if err := t.q.LockPluginStateScope(t.ctx, store.LockPluginStateScopeParams{PluginID: p.ID.String(), CompanyID: cid.String()}); err != nil {
				return err
			}
			if _, err := t.q.GetPluginState(t.ctx, store.GetPluginStateParams{PluginID: p.ID, CompanyID: cid, Key: in.Key}); isNoRows(err) {
				n, err := t.q.CountPluginState(t.ctx, store.CountPluginStateParams{PluginID: p.ID, CompanyID: cid})
				if err != nil {
					return err
				}
				if n >= domain.PluginStateMaxKeys {
					return domain.Invalid("key", fmt.Sprintf("limite de %d chaves por empresa atingido", domain.PluginStateMaxKeys))
				}
			} else if err != nil {
				return err
			}
			return t.q.UpsertPluginState(t.ctx, store.UpsertPluginStateParams{PluginID: p.ID, CompanyID: cid, Key: in.Key, Value: in.Value, Now: t.now})
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "state.delete":
		n, err := store.New(s.Pool).DeletePluginState(ctx, store.DeletePluginStateParams{PluginID: p.ID, CompanyID: cid, Key: in.Key})
		if err != nil {
			return nil, err
		}
		return map[string]any{"deleted": n > 0}, nil
	default:
		items, err := s.listState(ctx, p.ID, cid, in.Prefix, in.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": items}, nil
	}
}

// ---------- log ----------

// PluginLogLine é o OnLog do gerenciador: stderr do processo e avisos do host.
func (s *Service) PluginLogLine(key, level, msg string) {
	ctx := context.Background()
	r, err := s.read().GetPluginByKey(ctx, key)
	if err != nil {
		return
	}
	s.pluginLog(ctx, r.ID, nil, level, msg)
}

// pluginLog grava uma linha do log do plugin, com os segredos conhecidos dele mascarados, no máximo
// pluginLogsPerMinute por minuto (o excedente é contado e avisado na janela seguinte), e poda para as últimas
// PluginLogsKeep linhas.
func (s *Service) pluginLog(ctx context.Context, pluginID domain.ID, companyID *domain.ID, level, msg string) {
	rt := s.prt()
	now := s.Now().UTC()
	rt.mu.Lock()
	w := rt.logs[pluginID.String()]
	if w == nil {
		w = &window{start: now}
		rt.logs[pluginID.String()] = w
	}
	ok, dropped := w.allow(now, pluginLogsPerMinute)
	prune := w.n%50 == 1
	rt.mu.Unlock()
	if !ok && level != domain.PluginLogHost {
		return
	}
	q := store.New(s.Pool)
	if dropped > 0 {
		_ = q.InsertPluginLog(ctx, store.InsertPluginLogParams{ID: domain.NewID(), PluginID: pluginID, Level: domain.PluginLogHost,
			Message: fmt.Sprintf("%d linha(s) de log descartadas (limite de %d por minuto)", dropped, pluginLogsPerMinute), Now: now})
	}
	msg = strings.ToValidUTF8(msg, "�")
	msg = pluginClip(adapter.Redact(msg, s.pluginSecretValues(ctx, pluginID)...), domain.PluginLogMessageMax)
	if err := q.InsertPluginLog(ctx, store.InsertPluginLogParams{ID: domain.NewID(), PluginID: pluginID, CompanyID: companyID,
		Level: level, Message: msg, Now: now}); err != nil {
		if s.Log != nil {
			s.Log.Warn("plugin: gravar log", "plugin_id", pluginID, "err", err)
		}
		return
	}
	if prune {
		_ = q.PrunePluginLogs(ctx, store.PrunePluginLogsParams{PluginID: pluginID, Keep: domain.PluginLogsKeep})
	}
}

// pluginSecretValues devolve os valores secretos do plugin em todas as empresas (cache até a próxima mudança
// de configuração), para mascarar o log.
func (s *Service) pluginSecretValues(ctx context.Context, pluginID domain.ID) []string {
	rt := s.prt()
	rt.mu.Lock()
	v, ok := rt.secrets[pluginID]
	rt.mu.Unlock()
	if ok {
		return v
	}
	var out []string
	if rows, err := s.read().ListPluginSettingsForPlugin(ctx, pluginID); err == nil {
		for _, r := range rows {
			sec, err := s.openPluginSecrets(pluginID, r.CompanyID, r.SecretEnc)
			if err != nil {
				continue
			}
			for _, x := range sec {
				out = append(out, x)
			}
			if rs, _, err := s.expandStringMap(ctx, r.CompanyID, sec); err == nil { // valores das referências, para mascarar o log
				for _, x := range rs {
					out = append(out, x)
				}
			}
		}
	}
	rt.mu.Lock()
	rt.secrets[pluginID] = out
	rt.mu.Unlock()
	return out
}

func (s *Service) dropSecretsCache(pluginID domain.ID) {
	rt := s.prt()
	rt.mu.Lock()
	delete(rt.secrets, pluginID)
	rt.mu.Unlock()
}

// rekeyPluginSecrets reescreve com a chave atual os segredos de plugin cifrados com a anterior.
func (s *Service) rekeyPluginSecrets(ctx context.Context) (int, error) {
	rows, err := s.read().ListAllPluginSecrets(ctx)
	if err != nil {
		return 0, fmt.Errorf("list plugin secrets: %w", err)
	}
	n := 0
	for _, r := range rows {
		if !s.Integrations.Secrets.NeedsReseal(r.SecretEnc) {
			continue
		}
		aad := pluginAAD(r.PluginID, r.CompanyID)
		plain, err := s.Integrations.Secrets.Open(r.SecretEnc, aad)
		if err != nil {
			return n, fmt.Errorf("plugin %s: %w", r.PluginID, err)
		}
		enc, err := s.Integrations.Secrets.Seal(plain, aad)
		if err != nil {
			return n, fmt.Errorf("plugin %s: %w", r.PluginID, err)
		}
		if err := store.New(s.Pool).SetPluginSecretEnc(ctx, store.SetPluginSecretEncParams{PluginID: r.PluginID, CompanyID: r.CompanyID, SecretEnc: enc}); err != nil {
			return n, fmt.Errorf("plugin %s: %w", r.PluginID, err)
		}
		n++
	}
	return n, nil
}
