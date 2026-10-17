package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

type tools struct {
	b   Backend
	log *slog.Logger
}

// ---- entradas ----

type whoamiIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
}

type taskListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Status  string `json:"status,omitempty" jsonschema:"status separados por vírgula: backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	Persona string `json:"persona,omitempty" jsonschema:"'me' para as suas tasks, ou o slug de uma persona"`
	Project string `json:"project,omitempty" jsonschema:"slug do projeto: só as tasks dele"`
	Limit   int    `json:"limit,omitempty" jsonschema:"máximo de tasks (padrão 50, máx 200)"`
}

type taskGetIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Key     string `json:"key" jsonschema:"chave da task (ex. HNK-12) ou id (UUID)"`
}

type taskCreateIn struct {
	Company     string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Title       string `json:"title" jsonschema:"título curto e objetivo da task"`
	Description string `json:"description,omitempty" jsonschema:"descrição em markdown: contexto, critérios de aceite"`
	Priority    string `json:"priority,omitempty" jsonschema:"critical, high, medium (padrão) ou low"`
	Assignee    string `json:"assignee,omitempty" jsonschema:"slug da persona responsável; vazio = sem responsável"`
	Project     string `json:"project,omitempty" jsonschema:"slug do projeto onde a task nasce (veja hanko_project_list); vazio = sem projeto"`
	Parent      string `json:"parent,omitempty" jsonschema:"chave da task pai; com token de run o padrão é a task do run atual"`
	// DependsOn ordena o trabalho: a task nasce bloqueada e só é despachada quando todas essas tasks fecharem.
	DependsOn []string `json:"depends_on,omitempty" jsonschema:"chaves das tasks de que esta depende (ex. [\"HNK-7\"]): ela só começa quando todas fecharem; sem ciclos"`
	EventID   string   `json:"event_id,omitempty" jsonschema:"chave de idempotência: repetir a chamada com o mesmo event_id não duplica a task"`
}

type taskUpdateIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Key      string `json:"key" jsonschema:"chave da task (ex. HNK-12) ou id (UUID)"`
	Status   string `json:"status,omitempty" jsonschema:"novo status: backlog,todo,in_progress,in_review,blocked,done,cancelled"`
	Priority string `json:"priority,omitempty" jsonschema:"nova prioridade: critical,high,medium,low"`
	Assignee string `json:"assignee,omitempty" jsonschema:"slug da nova persona responsável, ou 'none' para remover o responsável"`
	Parent   string `json:"parent,omitempty" jsonschema:"chave da nova task pai, ou 'none' para tornar a task raiz"`
	EventID  string `json:"event_id,omitempty" jsonschema:"chave de idempotência da alteração"`
}

type taskCommentIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Key      string `json:"key" jsonschema:"chave da task (ex. HNK-12) ou id (UUID)"`
	Body     string `json:"body" jsonschema:"texto do comentário em markdown"`
	EventID  string `json:"event_id,omitempty" jsonschema:"chave de idempotência: o mesmo event_id não duplica o comentário"`
	Quote    string `json:"quote,omitempty" jsonschema:"opcional: ancora o comentário a este trecho (texto exato) da descrição da task, ou do resumo da execução em quote_run; o trecho precisa existir"`
	QuoteRun string `json:"quote_run,omitempty" jsonschema:"opcional: id da execução cujo resumo (entrega) contém o trecho de quote"`
}

type personaListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
}

type projectListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	All     bool   `json:"all,omitempty" jsonschema:"inclui os projetos arquivados (padrão: só os ativos)"`
}

type searchIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Query   string `json:"query" jsonschema:"o que buscar (palavras, \"frase exata\", -excluir, OR) ou a chave de uma task (HNK-12)"`
	Types   string `json:"types,omitempty" jsonschema:"tipos separados por vírgula: task,comment,persona,run,flow,goal,project,gate,artifact,memory (padrão: todos)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"máximo de resultados por tipo (padrão 10, máx 50)"`
}

type searchHitOut struct {
	Type      string `json:"type" jsonschema:"task, comment, persona, run, flow ou memory"`
	Ref       string `json:"ref" jsonschema:"chave da task, slug da persona/fluxo, id da execução ou path da memória"`
	Title     string `json:"title"`
	Snippet   string `json:"snippet,omitempty" jsonschema:"trecho que casou; as palavras que casaram vêm entre « e »"`
	TaskKey   string `json:"task_key,omitempty"`
	TaskTitle string `json:"task_title,omitempty"`
	Status    string `json:"status,omitempty"`
	Author    string `json:"author,omitempty"`
	Exact     bool   `json:"exact,omitempty"`
}

type searchGroupOut struct {
	Type        string         `json:"type"`
	Hits        []searchHitOut `json:"hits"`
	More        bool           `json:"more,omitempty"`
	Unavailable bool           `json:"unavailable,omitempty"`
}

type searchOut struct {
	Query  string           `json:"query"`
	Total  int              `json:"total"`
	Groups []searchGroupOut `json:"groups"`
}

type runGetIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	ID      string `json:"id,omitempty" jsonschema:"id (UUID) da execução; padrão = a execução do seu token"`
}

type gateRequestIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Title   string `json:"title" jsonschema:"o que você precisa que o humano decida, em uma frase"`
	Body    string `json:"body,omitempty" jsonschema:"markdown: o que será feito, evidências, impacto e alternativas"`
	Kind    string `json:"kind,omitempty" jsonschema:"approval (padrão)"`
	Task    string `json:"task,omitempty" jsonschema:"chave da task; com token de run o padrão é a task da execução atual"`
	EventID string `json:"event_id,omitempty" jsonschema:"chave de idempotência: repetir a chamada com o mesmo event_id devolve o mesmo gate"`
}

type gateStatusIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	ID      string `json:"id,omitempty" jsonschema:"id (UUID) do gate; vazio = gates pendentes da task"`
	Task    string `json:"task,omitempty" jsonschema:"chave da task; com token de run o padrão é a task da execução atual"`
}

// ---- saídas ----

type gateOut struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Status       string `json:"status" jsonschema:"pending, approved, rejected, changes_requested ou expired"`
	Title        string `json:"title"`
	TaskKey      string `json:"task_key,omitempty"`
	DecisionNote string `json:"decision_note,omitempty"`
	CreatedAt    string `json:"created_at"`
	DecidedAt    string `json:"decided_at,omitempty"`
}

type gateWriteOut struct {
	Gate gateOut `json:"gate"`
}

type gateStatusOut struct {
	Gates []gateOut `json:"gates"`
}

type whoamiOut struct {
	Actor       string `json:"actor" jsonschema:"human ou persona"`
	CompanyID   string `json:"company_id,omitempty"`
	CompanySlug string `json:"company_slug,omitempty"`
	PersonaID   string `json:"persona_id,omitempty"`
	PersonaSlug string `json:"persona_slug,omitempty"`
	PersonaName string `json:"persona_name,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	TaskKey     string `json:"task_key,omitempty"`
	// ConversationID is set instead of the task when the run is a chat.
	ConversationID string `json:"conversation_id,omitempty"`
}

type taskOut struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	Priority    string `json:"priority"`
	PersonaID   string `json:"persona_id,omitempty"`
	PersonaName string `json:"persona_name,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	ActiveRunID string `json:"active_run_id,omitempty"`
	UpdatedAt   string `json:"updated_at"`
	// Coordenação (ADR-0040): derivados.
	Blocked          bool   `json:"blocked,omitempty" jsonschema:"tem dependência aberta: nenhuma execução abre até todas fecharem"`
	OpenDependencies int    `json:"open_dependencies,omitempty"`
	ChildrenOpen     int    `json:"children_open,omitempty" jsonschema:"subtasks ainda abertas; o pai só conclui quando fecharem"`
	HoldReason       string `json:"hold_reason,omitempty" jsonschema:"a task (ou um ancestral) está pausada por um humano: nenhuma execução abre"`
}

type commentOut struct {
	ID        string `json:"id"`
	Author    string `json:"author" jsonschema:"human, persona:<id> ou system"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	Quote     string `json:"quote,omitempty" jsonschema:"trecho a que o comentário está ancorado, se houver"`
	Anchor    string `json:"anchor_status,omitempty" jsonschema:"exact, moved ou lost (âncora perdida)"`
}

type taskListOut struct {
	Tasks []taskOut `json:"tasks"`
}

type taskGetOut struct {
	Task   taskOut      `json:"task"`
	Thread []commentOut `json:"thread" jsonschema:"comentários mais recentes, do mais antigo para o mais novo"`
}

type taskWriteOut struct {
	Task taskOut `json:"task"`
}

type commentWriteOut struct {
	Comment commentOut `json:"comment"`
}

type personaOut struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Title  string `json:"title"`
	Agent  string `json:"agent"`
	Status string `json:"status"`
}

type personaListOut struct {
	Personas []personaOut `json:"personas"`
}

type runOut struct {
	ID         string   `json:"id"`
	TaskKey    string   `json:"task_key"`
	TaskTitle  string   `json:"task_title"`
	Persona    string   `json:"persona"`
	Status     string   `json:"status"`
	Trigger    string   `json:"trigger"`
	Agent      string   `json:"agent"`
	Error      string   `json:"error,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	CostUSD    *float64 `json:"cost_usd,omitempty"`
	CreatedAt  string   `json:"created_at"`
	StartedAt  string   `json:"started_at,omitempty"`
	FinishedAt string   `json:"finished_at,omitempty"`
}

type runGetOut struct {
	Run runOut `json:"run"`
}

type projectOut struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Status      string  `json:"status"`
	Workspace   string  `json:"workspace,omitempty"`
	Tasks       int     `json:"tasks"`
	OpenTasks   int     `json:"open_tasks"`
	Progress    float64 `json:"progress_percent"`
	BudgetState string  `json:"budget_state" jsonschema:"none, ok, warning ou exceeded (estourado: nenhuma execução do projeto abre)"`
}

type projectListOut struct {
	Projects []projectOut `json:"projects"`
}

func registerTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_whoami",
		Description: "Mostra quem você é no Hanko: persona, empresa, execução (run) e a task atual do seu token. Use para se orientar antes de agir.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.whoami)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_task_list",
		Description: "Lista tasks da empresa, mais recentes primeiro. Filtre por status (csv) e por persona ('me' = suas tasks, ou o slug de outra persona).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.taskList)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_task_get",
		Description: "Detalha uma task pela chave (ex. HNK-12) ou id, incluindo os comentários mais recentes da thread.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.taskGet)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_task_create",
		Description: "Cria uma task (ex. para delegar trabalho a outra persona). Com token de run, a task nasce como subtask da task atual. Use event_id para tornar a chamada idempotente.",
	}, t.taskCreate)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_task_update",
		Description: "Atualiza status, prioridade ou responsável de uma task. Marque in_review ou done ao concluir; blocked quando depender de alguém. Use event_id para idempotência.",
	}, t.taskUpdate)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_task_comment",
		Description: "Comenta na thread de uma task (progresso, perguntas, resultado). Use event_id para não duplicar o comentário em retentativas. " +
			"Com quote (texto exato) o comentário fica ancorado a esse trecho da descrição da task — ou do resumo de uma execução, com quote_run. " +
			"Para chamar a atenção de um humano, cite-o com @ (a parte local do e-mail, em minúsculas: @renato-bardi): o comentário chega na Caixa dele. " +
			"Citar outra persona (@slug) só a acorda quando o comentário é de um humano.",
	}, t.taskComment)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_gate_request",
		Description: "Pede a decisão de um humano (gate) e pausa o seu trabalho nesta task até ele responder. " +
			"Peça ANTES de: ação irreversível ou destrutiva, merge, deploy ou publicação, gasto de dinheiro, contratação de persona, " +
			"mudança de escopo ou qualquer coisa que você não deva decidir sozinha. Explique no body o que fará, o impacto e as alternativas. " +
			"Depois de pedir, encerre a execução: você será acordada quando houver decisão (aprovado, mudanças pedidas ou rejeitado, com a nota). " +
			"Use event_id para não duplicar o pedido em retentativas.",
	}, t.gateRequest)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_gate_status",
		Description: "Consulta um gate pelo id, ou os gates pendentes da task (padrão: a da sua execução). " +
			"Use para saber se a decisão que você pediu já saiu e qual foi a nota do humano.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.gateStatus)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_persona_list",
		Description: "Lista as personas da empresa (slug, cargo, agente, status) para saber a quem delegar.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.personaList)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_project_list",
		Description: "Lista os projetos da empresa (slug, status, progresso, estado do orçamento) para saber onde criar uma task (hanko_task_create com project).",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.projectList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_search",
		Description: "Busca na empresa inteira de uma vez: tasks (chave, título, descrição), comentários, personas, execuções, fluxos e a memória da empresa. " +
			"Várias palavras = todas; \"frase exata\"; -excluir; OR entre alternativas; a última palavra casa por prefixo; a chave exata de uma task (HNK-12) vai direto a ela. " +
			"Devolve grupos por tipo, com trecho do que casou. Use antes de criar uma task (já existe algo parecido?) ou de repetir um trabalho.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.search)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_run_get",
		Description: "Consulta uma execução (run): status, resumo, custo. Sem id, devolve a execução do seu token.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.runGet)
	registerArtifactTools(s, t)
	registerSkillTools(s, t)
	registerStatusCardTools(s, t)
	registerCaseTools(s, t)
	registerCoordinationTools(s, t)
	registerInstructionTools(s, t)
	registerAttachmentTools(s, t)
	registerProjectGoalTools(s, t)
}

// ---- resolução de empresa ----

// scope resolve a empresa da chamada e valida o argumento company.
func (t *tools) scope(ctx context.Context, arg string) (identity, domain.ID, error) {
	id, ok := identityFrom(ctx)
	if !ok {
		return identity{}, domain.ID{}, errors.New("chamada sem identidade")
	}
	arg = strings.TrimSpace(arg)
	if id.CompanyID != nil {
		if arg != "" {
			c, err := t.b.GetCompany(ctx, arg)
			if err != nil {
				return id, domain.ID{}, toolErr("empresa", arg, err)
			}
			if c.ID != *id.CompanyID {
				return id, domain.ID{}, fmt.Errorf("a empresa %q não é a do seu token; omita o argumento company", arg)
			}
		}
		return id, *id.CompanyID, nil
	}
	if arg == "" {
		return id, domain.ID{}, errors.New("argumento company (slug ou id da empresa) é obrigatório nesta conexão")
	}
	c, err := t.b.GetCompany(ctx, arg)
	if err != nil {
		return id, domain.ID{}, toolErr("empresa", arg, err)
	}
	return id, c.ID, nil
}

// toolErr traduz erros de domínio em mensagens úteis para o modelo.
func toolErr(what, ref string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%s %q não encontrada(o)", what, ref)
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("entrada inválida (%s %q): %s", what, ref, err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrActiveRun):
		return fmt.Errorf("conflito (%s %q): %s", what, ref, err.Error())
	case errors.Is(err, domain.ErrNotAllowed):
		return fmt.Errorf("sem permissão (%s %q): %s", what, ref, err.Error())
	}
	return fmt.Errorf("%s %q: %w", what, ref, err)
}

func opErr(op string, err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return fmt.Errorf("%s: não encontrado: %s", op, err.Error())
	case errors.Is(err, domain.ErrInvalid):
		return fmt.Errorf("%s: entrada inválida: %s", op, err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrActiveRun):
		return fmt.Errorf("%s: conflito: %s", op, err.Error())
	case errors.Is(err, domain.ErrNotAllowed):
		return fmt.Errorf("%s: sem permissão: %s", op, err.Error())
	}
	return fmt.Errorf("%s: %w", op, err)
}

func textResult(format string, a ...any) *sdk.CallToolResult {
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf(format, a...)}}}
}

// ---- conversões ----

func idStr(id *domain.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}

func toTask(v service.TaskView) taskOut {
	o := taskOut{
		ID: v.ID.String(), Key: v.Key, Title: v.Title, Description: v.Description,
		Status: string(v.Status), Priority: string(v.Priority),
		PersonaID: idStr(v.PersonaID), PersonaName: v.PersonaName, ProjectID: idStr(v.ProjectID), ProjectName: v.ProjectName,
		ParentID: idStr(v.ParentID), ActiveRunID: idStr(v.ActiveRunID),
		UpdatedAt: v.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Blocked:   v.Blocked, OpenDependencies: v.OpenDependencies, ChildrenOpen: v.ChildrenOpen,
	}
	if v.Hold != nil {
		o.HoldReason = v.Hold.Reason
	}
	return o
}

func actorStr(a domain.Actor) string {
	if a.Type == domain.ActorPersona && a.ID != nil {
		return "persona:" + a.ID.String()
	}
	return string(a.Type)
}

func toComment(c domain.Comment) commentOut {
	out := commentOut{ID: c.ID.String(), Author: actorStr(c.Author), Body: c.Body, CreatedAt: c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}
	if c.Anchor != nil {
		out.Quote = c.Anchor.Quote
		if c.AnchorState != nil {
			out.Anchor = c.AnchorState.Status
		}
	}
	return out
}

func taskLine(v service.TaskView) string {
	who := "sem responsável"
	if v.PersonaName != "" {
		who = "@" + v.PersonaName
	}
	if v.ProjectName != "" {
		who += " · projeto " + v.ProjectName
	}
	return fmt.Sprintf("%s [%s/%s] %s — %s", v.Key, v.Status, v.Priority, v.Title, who)
}

// resolveAssignee devolve o id da persona pelo slug.
func (t *tools) resolveAssignee(ctx context.Context, company domain.ID, slug string) (*domain.ID, error) {
	p, err := t.b.GetPersona(ctx, company, slug)
	if err != nil {
		return nil, toolErr("persona", slug, err)
	}
	return &p.ID, nil
}

// ---- handlers ----

func (t *tools) whoami(ctx context.Context, _ *sdk.CallToolRequest, in whoamiIn) (*sdk.CallToolResult, whoamiOut, error) {
	id, ok := identityFrom(ctx)
	if !ok {
		return nil, whoamiOut{}, errors.New("chamada sem identidade")
	}
	out := whoamiOut{Actor: string(id.Actor.Type), PersonaID: idStr(id.PersonaID), RunID: idStr(id.RunID), TaskID: idStr(id.TaskID),
		ConversationID: idStr(id.ConversationID)}
	var company domain.ID
	if id.CompanyID != nil {
		company = *id.CompanyID
		out.CompanyID = company.String()
	} else if strings.TrimSpace(in.Company) != "" {
		c, err := t.b.GetCompany(ctx, in.Company)
		if err != nil {
			return nil, out, toolErr("empresa", in.Company, err)
		}
		company = c.ID
		out.CompanyID, out.CompanySlug = c.ID.String(), c.Slug
	}
	if id.CompanyID != nil {
		if c, err := t.b.GetCompany(ctx, company.String()); err == nil {
			out.CompanySlug = c.Slug
		}
	}
	if id.PersonaID != nil && id.CompanyID != nil {
		if p, err := t.b.GetPersona(ctx, company, id.PersonaID.String()); err == nil {
			out.PersonaSlug, out.PersonaName = p.Slug, p.Name
		}
	}
	if id.TaskID != nil && id.CompanyID != nil {
		if tk, err := t.b.GetTask(ctx, company, id.TaskID.String()); err == nil {
			out.TaskKey = tk.Key
		}
	}
	var sb strings.Builder
	if out.PersonaSlug != "" {
		fmt.Fprintf(&sb, "Você é a persona %s (%s)", out.PersonaName, out.PersonaSlug)
	} else if id.PersonaID != nil {
		fmt.Fprintf(&sb, "Você é a persona %s", out.PersonaID)
	} else {
		sb.WriteString("Você é um humano (conexão confiável)")
	}
	if out.CompanySlug != "" {
		fmt.Fprintf(&sb, " na empresa %s", out.CompanySlug)
	}
	if out.TaskKey != "" {
		fmt.Fprintf(&sb, "; task atual %s", out.TaskKey)
	}
	if out.ConversationID != "" {
		fmt.Fprintf(&sb, "; conversa %s (chat, sem task)", out.ConversationID)
	}
	if out.RunID != "" {
		fmt.Fprintf(&sb, "; run %s", out.RunID)
	}
	return textResult("%s.", sb.String()), out, nil
}

func parseStatuses(csv string) ([]domain.TaskStatus, error) {
	var out []domain.TaskStatus
	for _, p := range strings.Split(csv, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		s := domain.TaskStatus(p)
		if !s.Valid() {
			return nil, fmt.Errorf("status %q inválido; use backlog,todo,in_progress,in_review,blocked,done,cancelled", p)
		}
		out = append(out, s)
	}
	return out, nil
}

func (t *tools) taskList(ctx context.Context, _ *sdk.CallToolRequest, in taskListIn) (*sdk.CallToolResult, taskListOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, taskListOut{}, err
	}
	f := service.TaskFilter{Limit: in.Limit}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if f.Statuses, err = parseStatuses(in.Status); err != nil {
		return nil, taskListOut{}, err
	}
	switch p := strings.TrimSpace(in.Persona); p {
	case "":
	case "me":
		if id.PersonaID == nil {
			return nil, taskListOut{}, errors.New("persona=me só vale com token de persona; informe o slug da persona")
		}
		f.PersonaID = id.PersonaID
	default:
		if f.PersonaID, err = t.resolveAssignee(ctx, company, p); err != nil {
			return nil, taskListOut{}, err
		}
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, taskListOut{}, toolErr("projeto", p, err)
		}
		f.ProjectID = &pr.ID
	}
	list, err := t.b.ListTasks(ctx, company, f)
	if err != nil {
		return nil, taskListOut{}, opErr("listar tasks", err)
	}
	out := taskListOut{Tasks: make([]taskOut, 0, len(list))}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d task(s)", len(list))
	for _, v := range list {
		out.Tasks = append(out.Tasks, toTask(v))
		sb.WriteString("\n- " + taskLine(v))
	}
	return textResult("%s", sb.String()), out, nil
}

func (t *tools) taskGet(ctx context.Context, _ *sdk.CallToolRequest, in taskGetIn) (*sdk.CallToolResult, taskGetOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, taskGetOut{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return nil, taskGetOut{}, errors.New("key é obrigatório")
	}
	v, err := t.b.GetTask(ctx, company, strings.TrimSpace(in.Key))
	if err != nil {
		return nil, taskGetOut{}, toolErr("task", in.Key, err)
	}
	comments, err := t.b.ListComments(ctx, company, v.ID)
	if err != nil {
		return nil, taskGetOut{}, opErr("listar comentários", err)
	}
	const maxThread = 20
	if len(comments) > maxThread {
		comments = comments[len(comments)-maxThread:]
	}
	out := taskGetOut{Task: toTask(v), Thread: make([]commentOut, 0, len(comments))}
	var sb strings.Builder
	sb.WriteString(taskLine(v))
	if v.Description != "" {
		sb.WriteString("\n\n" + v.Description)
	}
	if len(comments) > 0 {
		sb.WriteString("\n\nThread:")
	}
	for _, c := range comments {
		out.Thread = append(out.Thread, toComment(c))
		fmt.Fprintf(&sb, "\n[%s] %s: %s", c.CreatedAt.UTC().Format("2006-01-02 15:04"), actorStr(c.Author), c.Body)
	}
	return textResult("%s", sb.String()), out, nil
}

func (t *tools) taskCreate(ctx context.Context, _ *sdk.CallToolRequest, in taskCreateIn) (*sdk.CallToolResult, taskWriteOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, taskWriteOut{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, taskWriteOut{}, errors.New("title é obrigatório")
	}
	ci := service.CreateTaskInput{Title: strings.TrimSpace(in.Title), Description: in.Description, EventID: in.EventID}
	if in.Priority != "" {
		p := domain.Priority(strings.TrimSpace(in.Priority))
		if !p.Valid() {
			return nil, taskWriteOut{}, fmt.Errorf("priority %q inválida; use critical, high, medium ou low", in.Priority)
		}
		ci.Priority = p
	}
	if a := strings.TrimSpace(in.Assignee); a != "" {
		if ci.PersonaID, err = t.resolveAssignee(ctx, company, a); err != nil {
			return nil, taskWriteOut{}, err
		}
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, taskWriteOut{}, toolErr("projeto", p, err)
		}
		ci.ProjectID = &pr.ID
	}
	switch parent := strings.TrimSpace(in.Parent); {
	case parent != "":
		pv, err := t.b.GetTask(ctx, company, parent)
		if err != nil {
			return nil, taskWriteOut{}, toolErr("task pai", parent, err)
		}
		ci.ParentID = &pv.ID
	case id.ViaToken && id.TaskID != nil:
		ci.ParentID = id.TaskID
	}
	for _, ref := range in.DependsOn {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		dv, err := t.b.GetTask(ctx, company, ref)
		if err != nil {
			return nil, taskWriteOut{}, toolErr("dependência", ref, err)
		}
		ci.DependsOn = append(ci.DependsOn, dv.ID)
	}
	if id.ViaToken && id.ConversationID != nil {
		ci.Description = t.conversationOrigin(ctx, company, *id.ConversationID, ci.Description)
	}
	v, err := t.b.CreateTask(ctx, id.Actor, company, ci)
	if err != nil {
		return nil, taskWriteOut{}, opErr("criar task", err)
	}
	return textResult("Task criada: %s", taskLine(v)), taskWriteOut{Task: toTask(v)}, nil
}

// conversationOrigin appends to a task description the conversation it was opened from.
func (t *tools) conversationOrigin(ctx context.Context, company, conversation domain.ID, description string) string {
	ref := "/chat/" + conversation.String()
	if c, err := t.b.GetCompany(ctx, company.String()); err == nil {
		ref = "/c/" + c.Slug + ref
	}
	note := fmt.Sprintf("Aberta a partir da conversa %s (%s).", conversation, ref)
	if strings.TrimSpace(description) == "" {
		return note
	}
	return strings.TrimRight(description, "\n ") + "\n\n---\n" + note
}

func (t *tools) taskUpdate(ctx context.Context, _ *sdk.CallToolRequest, in taskUpdateIn) (*sdk.CallToolResult, taskWriteOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, taskWriteOut{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return nil, taskWriteOut{}, errors.New("key é obrigatório")
	}
	ui := service.UpdateTaskInput{EventID: in.EventID}
	changed := false
	if s := strings.TrimSpace(in.Status); s != "" {
		st := domain.TaskStatus(s)
		if !st.Valid() {
			return nil, taskWriteOut{}, fmt.Errorf("status %q inválido; use backlog,todo,in_progress,in_review,blocked,done,cancelled", s)
		}
		ui.Status, changed = &st, true
	}
	if p := strings.TrimSpace(in.Priority); p != "" {
		pr := domain.Priority(p)
		if !pr.Valid() {
			return nil, taskWriteOut{}, fmt.Errorf("priority %q inválida; use critical, high, medium ou low", p)
		}
		ui.Priority, changed = &pr, true
	}
	switch a := strings.TrimSpace(in.Assignee); a {
	case "":
	case "none":
		ui.Unassign, changed = true, true
	default:
		if ui.PersonaID, err = t.resolveAssignee(ctx, company, a); err != nil {
			return nil, taskWriteOut{}, err
		}
		changed = true
	}
	switch pa := strings.TrimSpace(in.Parent); pa {
	case "":
	case "none":
		ui.ClearParent, changed = true, true
	default:
		pv, err := t.b.GetTask(ctx, company, pa)
		if err != nil {
			return nil, taskWriteOut{}, toolErr("task pai", pa, err)
		}
		ui.ParentID, changed = &pv.ID, true
	}
	if !changed {
		return nil, taskWriteOut{}, errors.New("nada a alterar: informe status, priority, assignee ou parent")
	}
	cur, err := t.b.GetTask(ctx, company, strings.TrimSpace(in.Key))
	if err != nil {
		return nil, taskWriteOut{}, toolErr("task", in.Key, err)
	}
	v, err := t.b.UpdateTask(ctx, id.Actor, company, cur.ID, ui)
	if err != nil {
		return nil, taskWriteOut{}, opErr("atualizar task "+cur.Key, err)
	}
	return textResult("Task atualizada: %s", taskLine(v)), taskWriteOut{Task: toTask(v)}, nil
}

func (t *tools) taskComment(ctx context.Context, _ *sdk.CallToolRequest, in taskCommentIn) (*sdk.CallToolResult, commentWriteOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, commentWriteOut{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return nil, commentWriteOut{}, errors.New("key é obrigatório")
	}
	if strings.TrimSpace(in.Body) == "" {
		return nil, commentWriteOut{}, errors.New("body é obrigatório")
	}
	cur, err := t.b.GetTask(ctx, company, strings.TrimSpace(in.Key))
	if err != nil {
		return nil, commentWriteOut{}, toolErr("task", in.Key, err)
	}
	ai := service.AddCommentInput{Body: in.Body, EventID: in.EventID}
	if in.Quote != "" {
		a := &domain.Anchor{Target: domain.AnchorTaskDescription, Quote: in.Quote}
		if rid := strings.TrimSpace(in.QuoteRun); rid != "" {
			id, perr := domain.ParseID(rid)
			if perr != nil {
				return nil, commentWriteOut{}, errors.New("quote_run: não é um uuid")
			}
			a.Target, a.RunID = domain.AnchorRunSummary, &id
		}
		ai.Anchor = a
	} else if strings.TrimSpace(in.QuoteRun) != "" {
		return nil, commentWriteOut{}, errors.New("quote_run exige quote")
	}
	if id.ViaToken {
		ai.RunID = id.RunID
	}
	c, err := t.b.AddComment(ctx, id.Actor, company, cur.ID, ai)
	if err != nil {
		return nil, commentWriteOut{}, opErr("comentar em "+cur.Key, err)
	}
	return textResult("Comentário adicionado em %s.", cur.Key), commentWriteOut{Comment: toComment(c)}, nil
}

func (t *tools) personaList(ctx context.Context, _ *sdk.CallToolRequest, in personaListIn) (*sdk.CallToolResult, personaListOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, personaListOut{}, err
	}
	list, err := t.b.ListPersonas(ctx, company)
	if err != nil {
		return nil, personaListOut{}, opErr("listar personas", err)
	}
	out := personaListOut{Personas: make([]personaOut, 0, len(list))}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d persona(s)", len(list))
	for _, p := range list {
		out.Personas = append(out.Personas, personaOut{ID: p.ID.String(), Slug: p.Slug, Name: p.Name, Title: p.Title, Agent: p.Agent, Status: string(p.Status)})
		fmt.Fprintf(&sb, "\n- %s (%s) — %s [%s, %s]", p.Slug, p.Name, p.Title, p.Agent, p.Status)
	}
	return textResult("%s", sb.String()), out, nil
}

func (t *tools) projectList(ctx context.Context, _ *sdk.CallToolRequest, in projectListIn) (*sdk.CallToolResult, projectListOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, projectListOut{}, err
	}
	list, err := t.b.ListProjectViews(ctx, company)
	if err != nil {
		return nil, projectListOut{}, opErr("listar projetos", err)
	}
	out := projectListOut{Projects: make([]projectOut, 0, len(list))}
	var sb strings.Builder
	for _, p := range list {
		if p.Status == domain.ProjectArchived && !in.All {
			continue
		}
		open := p.TasksTotal - p.Tasks[domain.TaskDone] - p.Tasks[domain.TaskCancelled]
		out.Projects = append(out.Projects, projectOut{ID: p.ID.String(), Slug: p.Slug, Name: p.Name, Description: p.Description,
			Status: string(p.Status), Workspace: p.WorkspaceSlug, Tasks: p.TasksTotal, OpenTasks: open, Progress: p.Progress, BudgetState: p.Budget.State})
		fmt.Fprintf(&sb, "\n- %s (%s) — %s, %d task(s) abertas de %d, progresso %.0f%%, orçamento %s", p.Slug, p.Name, p.Status, open, p.TasksTotal, p.Progress, p.Budget.State)
	}
	return textResult("%d projeto(s)%s", len(out.Projects), sb.String()), out, nil
}

func (t *tools) runGet(ctx context.Context, _ *sdk.CallToolRequest, in runGetIn) (*sdk.CallToolResult, runGetOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, runGetOut{}, err
	}
	var runID domain.ID
	if s := strings.TrimSpace(in.ID); s != "" {
		if runID, err = domain.ParseID(s); err != nil {
			return nil, runGetOut{}, fmt.Errorf("id de run inválido %q: esperado UUID", s)
		}
	} else if id.RunID != nil {
		runID = *id.RunID
	} else {
		return nil, runGetOut{}, errors.New("id é obrigatório sem token de run")
	}
	v, err := t.b.GetRun(ctx, company, runID)
	if err != nil {
		return nil, runGetOut{}, toolErr("run", runID.String(), err)
	}
	o := runOut{
		ID: v.ID.String(), TaskKey: v.TaskKey, TaskTitle: v.TaskTitle, Persona: v.PersonaName,
		Status: string(v.Status), Trigger: string(v.Trigger), Agent: v.Agent, Error: v.Error, Summary: v.Summary,
		CostUSD: v.CostUSD, CreatedAt: v.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	if v.StartedAt != nil {
		o.StartedAt = v.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	if v.FinishedAt != nil {
		o.FinishedAt = v.FinishedAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	txt := fmt.Sprintf("Run %s da task %s (%s): %s", o.ID, o.TaskKey, o.Persona, o.Status)
	if o.TaskKey == "" {
		txt = fmt.Sprintf("Run %s de conversa (%s): %s", o.ID, o.Persona, o.Status)
	}
	if o.Error != "" {
		txt += "; erro: " + o.Error
	}
	if o.Summary != "" {
		txt += "\n" + o.Summary
	}
	return textResult("%s", txt), runGetOut{Run: o}, nil
}

func toGate(g service.GateView) gateOut {
	o := gateOut{ID: g.ID.String(), Kind: string(g.Kind), Status: string(g.Status), Title: g.Title, TaskKey: g.TaskKey,
		DecisionNote: g.DecisionNote, CreatedAt: g.CreatedAt.UTC().Format(time.RFC3339)}
	if g.DecidedAt != nil {
		o.DecidedAt = g.DecidedAt.UTC().Format(time.RFC3339)
	}
	return o
}

func gateLine(g service.GateView) string {
	s := fmt.Sprintf("Gate %s [%s] %s", g.ID, g.Status, g.Title)
	if g.TaskKey != "" {
		s += " (" + g.TaskKey + ")"
	}
	if g.DecisionNote != "" {
		s += "\nNota: " + g.DecisionNote
	}
	return s
}

// gateTask resolve a task do pedido: argumento explícito ou a task do token.
func (t *tools) gateTask(ctx context.Context, id identity, company domain.ID, ref string) (*domain.ID, error) {
	if ref = strings.TrimSpace(ref); ref != "" {
		v, err := t.b.GetTask(ctx, company, ref)
		if err != nil {
			return nil, toolErr("task", ref, err)
		}
		return &v.ID, nil
	}
	if id.ViaToken && id.TaskID != nil {
		return id.TaskID, nil
	}
	return nil, nil
}

func (t *tools) gateRequest(ctx context.Context, _ *sdk.CallToolRequest, in gateRequestIn) (*sdk.CallToolResult, gateWriteOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, gateWriteOut{}, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, gateWriteOut{}, errors.New("title é obrigatório")
	}
	kind := domain.GateKind(strings.TrimSpace(in.Kind))
	if kind != "" && kind != domain.GateApproval {
		return nil, gateWriteOut{}, errors.New("kind: use approval (os demais tipos são abertos pelo Hanko)")
	}
	task, err := t.gateTask(ctx, id, company, in.Task)
	if err != nil {
		return nil, gateWriteOut{}, err
	}
	if task == nil && id.ConversationID != nil {
		return nil, gateWriteOut{}, errors.New("esta execução é uma conversa (chat) e não tem task: informe o argumento task com a chave de uma task existente ou abra uma com hanko_task_create e peça o gate nela")
	}
	req := service.RequestGateInput{Kind: domain.GateApproval, Title: in.Title, Body: in.Body, TaskID: task, EventID: in.EventID}
	if id.ViaToken {
		req.RunID = id.RunID
	}
	g, err := t.b.RequestGate(ctx, id.Actor, company, req)
	if err != nil {
		return nil, gateWriteOut{}, opErr("pedir gate", err)
	}
	return textResult("Gate pedido (%s). Encerre esta execução: você será acordada quando o humano decidir.\n%s", g.ID, gateLine(g)),
		gateWriteOut{Gate: toGate(g)}, nil
}

func (t *tools) gateStatus(ctx context.Context, _ *sdk.CallToolRequest, in gateStatusIn) (*sdk.CallToolResult, gateStatusOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, gateStatusOut{}, err
	}
	var gates []service.GateView
	if raw := strings.TrimSpace(in.ID); raw != "" {
		gid, err := domain.ParseID(raw)
		if err != nil {
			return nil, gateStatusOut{}, errors.New("id do gate inválido (esperado UUID)")
		}
		g, err := t.b.GetGate(ctx, company, gid)
		if err != nil {
			return nil, gateStatusOut{}, toolErr("gate", raw, err)
		}
		gates = []service.GateView{g}
	} else {
		task, err := t.gateTask(ctx, id, company, in.Task)
		if err != nil {
			return nil, gateStatusOut{}, err
		}
		if task == nil {
			return nil, gateStatusOut{}, errors.New("informe id do gate ou task")
		}
		gates, err = t.b.ListGates(ctx, company, service.GateFilter{Statuses: []domain.GateStatus{domain.GatePending}, TaskID: task, Limit: 50})
		if err != nil {
			return nil, gateStatusOut{}, opErr("listar gates", err)
		}
	}
	out := gateStatusOut{Gates: make([]gateOut, 0, len(gates))}
	var sb strings.Builder
	for i, g := range gates {
		out.Gates = append(out.Gates, toGate(g))
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(gateLine(g))
	}
	if len(gates) == 0 {
		sb.WriteString("Nenhum gate pendente.")
	}
	return textResult("%s", sb.String()), out, nil
}
