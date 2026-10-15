package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/store"
)

// ImportOptions controla a criação da empresa a partir de um arquivo.
type ImportOptions struct {
	Slug string // slug da empresa nova; vazio = o do arquivo (ou derivado do nome)
	// Active liga o que o arquivo traz ligado. O padrão (false) é seguro: personas entram pausadas e fluxos
	// desligados, e nada trabalha até alguém liberar.
	Active bool
}

// ImportResult resume o que foi criado.
type ImportResult struct {
	Company domain.Company
	Counts  map[string]int
}

func iv(path string, i int, field string) string { return fmt.Sprintf("%s[%d].%s", path, i, field) }

// ImportCompany cria uma empresa NOVA a partir do arquivo (ADR-0022). Tudo numa transação: se qualquer
// parte do arquivo é inválida ou o slug já existe, nada é gravado. Os ids são novos, as relações
// (organograma, revisor, assinatura, task→persona, task→pai) e a numeração das tasks são preservadas.
// Só humanos: no modo authenticated, só quem tem users.role owner ou admin (como criar empresa); ele
// vira owner da empresa nova.
func (s *Service) ImportCompany(ctx context.Context, actor domain.Actor, doc export.Document, opts ImportOptions) (ImportResult, error) {
	var creator *domain.User
	if id, ok := auth.IdentityFrom(ctx); ok {
		if id.User.Role == domain.RoleMember {
			return ImportResult{}, domain.ErrForbidden
		}
		creator = &id.User
	}
	p, err := s.planImport(ctx, doc, opts)
	if err != nil {
		return ImportResult{}, err
	}
	var res ImportResult
	err = s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.GetCompanyBySlug(ctx, p.slug); err == nil {
			return fmt.Errorf("slug %q já está em uso (use outro com --slug): %w", p.slug, domain.ErrConflict)
		} else if !isNoRows(err) {
			return fmt.Errorf("check slug: %w", err)
		}
		row, err := t.q.CreateCompany(ctx, store.CreateCompanyParams{ID: domain.NewID(), Slug: p.slug, Name: p.name,
			TaskPrefix: p.prefix, Timezone: p.tz, Now: t.now})
		if err != nil {
			return mapErr(err, "create company")
		}
		cid := row.ID
		if err := t.q.SetCompanyImportState(ctx, store.SetCompanyImportStateParams{ID: cid, NextTaskNumber: p.nextTask,
			BudgetMonthlyCents: doc.Company.BudgetMonthlyCents}); err != nil {
			return fmt.Errorf("set company state: %w", err)
		}
		if row, err = t.q.GetCompanyByID(ctx, cid); err != nil {
			return mapErr(err, "get company")
		}
		if creator != nil {
			if _, err := t.q.AddMember(ctx, store.AddMemberParams{CompanyID: cid, UserID: creator.ID,
				Role: string(domain.MemberOwner), Now: t.now}); err != nil {
				return mapErr(err, "add owner")
			}
		}
		if err := t.runImport(cid, doc, p, opts); err != nil {
			return err
		}
		res = ImportResult{Company: companyFromRow(row), Counts: p.counts(doc)}
		return t.activity(actor, cid, "company.imported", "company", cid, map[string]any{
			"slug": p.slug, "source_slug": doc.Company.Slug, "format": export.Format, "paused": !opts.Active, "counts": res.Counts}, "")
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

// importPlan é o resultado da validação: valores normalizados e a ordem de inserção das tasks.
type importPlan struct {
	slug, name, prefix, tz string
	nextTask               int64
	taskOrder              []int // índices de doc.Tasks, pais antes dos filhos
	personaCfg             []json.RawMessage
	// clearedPaths: slugs dos workspaces cujo caminho esta instalação não aceita (outra máquina, fora da raiz):
	// entram só como metadado, sem caminho (ADR-0025).
	clearedPaths map[string]bool
}

func (p importPlan) counts(d export.Document) map[string]int {
	comments, events := 0, 0
	for _, t := range d.Tasks {
		comments += len(t.Comments)
	}
	for _, r := range d.Runs {
		events += len(r.Events)
	}
	out := map[string]int{"workspaces": len(d.Workspaces), "projects": len(d.Projects), "subscriptions": len(d.Subscriptions),
		"personas": len(d.Personas), "tasks": len(d.Tasks), "comments": comments, "flows": len(d.Flows), "runs": len(d.Runs), "run_events": events}
	if n := len(d.Artifacts); n > 0 {
		out["artifacts"] = n
	}
	if n := len(p.clearedPaths); n > 0 {
		out["workspace_paths_cleared"] = n
	}
	return out
}

func uniqueIdx[T any](path string, items []T, key func(T) string, what string) (map[string]int, error) {
	m := make(map[string]int, len(items))
	for i, it := range items {
		k := key(it)
		if k == "" {
			return nil, domain.Invalid(iv(path, i, what), "required")
		}
		if _, dup := m[k]; dup {
			return nil, domain.Invalid(iv(path, i, what), fmt.Sprintf("duplicate %q", k))
		}
		m[k] = i
	}
	return m, nil
}

// planImport valida o arquivo inteiro sem tocar no banco.
func (s *Service) planImport(ctx context.Context, doc export.Document, opts ImportOptions) (importPlan, error) {
	var p importPlan
	if doc.Format != export.Format {
		return p, domain.Invalid("format", fmt.Sprintf("unsupported format %q (want %q)", doc.Format, export.Format))
	}
	c := doc.Company
	p.name = strings.TrimSpace(c.Name)
	if p.name == "" {
		return p, domain.Invalid("company.name", "required")
	}
	slug := opts.Slug
	if slug == "" {
		slug = c.Slug
	}
	var err error
	if p.slug, err = resolveSlug(slug, p.name); err != nil {
		return p, err
	}
	p.prefix = strings.ToUpper(strings.TrimSpace(c.TaskPrefix))
	if p.prefix == "" {
		p.prefix = "HNK"
	}
	if !prefixRe.MatchString(p.prefix) {
		return p, domain.Invalid("company.task_prefix", "must be 1-10 uppercase letters/digits starting with a letter")
	}
	if p.tz = strings.TrimSpace(c.Timezone); p.tz == "" {
		p.tz = "America/Sao_Paulo"
	}
	if _, err := ParseCron("* * * * *", p.tz); err != nil {
		return p, domain.Invalid("company.timezone", "unknown IANA time zone")
	}
	if c.BudgetMonthlyCents != nil && *c.BudgetMonthlyCents < 0 {
		return p, domain.Invalid("company.budget_monthly_cents", "must be >= 0")
	}
	if c.NextTaskNumber < 0 || c.NextTaskNumber > maxTaskNumber+1 {
		return p, domain.Invalid("company.next_task_number", fmt.Sprintf("must be between 0 and %d", maxTaskNumber+1))
	}

	// workspaces e projetos
	ws, err := uniqueIdx("workspaces", doc.Workspaces, func(w export.Workspace) string { return w.Slug }, "slug")
	if err != nil {
		return p, err
	}
	for i, w := range doc.Workspaces {
		if !domain.ValidSlug(w.Slug) {
			return p, domain.Invalid(iv("workspaces", i, "slug"), "must be lowercase letters, digits and dashes (1-63 chars)")
		}
		if strings.TrimSpace(w.Name) == "" {
			return p, domain.Invalid(iv("workspaces", i, "name"), "required")
		}
		// O caminho é da máquina de origem: vale se esta instalação o aceita (absoluto, limpo, dentro da raiz de
		// workspaces). Senão o workspace entra sem caminho (só nome, repositório e branch) e o import conta isso em
		// workspace_paths_cleared: um arquivo de fora nunca aponta para fora da raiz, e mudar de máquina não
		// derruba a importação inteira. Quem importa corrige o caminho depois, na tela do workspace.
		if _, err := s.checkWorkspacePath(w.Path); err != nil {
			if p.clearedPaths == nil {
				p.clearedPaths = map[string]bool{}
			}
			p.clearedPaths[w.Slug] = true
		}
		if _, err := checkRepoURL(w.RepoURL); err != nil {
			return p, domain.Invalid(iv("workspaces", i, "repo_url"), invalidReason(err))
		}
		if _, err := checkBranch(w.DefaultBranch); err != nil {
			return p, domain.Invalid(iv("workspaces", i, "default_branch"), invalidReason(err))
		}
	}
	pr, err := uniqueIdx("projects", doc.Projects, func(x export.Project) string { return x.Slug }, "slug")
	if err != nil {
		return p, err
	}
	for i, x := range doc.Projects {
		if !domain.ValidSlug(x.Slug) {
			return p, domain.Invalid(iv("projects", i, "slug"), "must be lowercase letters, digits and dashes (1-63 chars)")
		}
		if strings.TrimSpace(x.Name) == "" {
			return p, domain.Invalid(iv("projects", i, "name"), "required")
		}
		if err := ref("projects", i, "workspace", x.Workspace, ws); err != nil {
			return p, err
		}
		if x.Status != "" && !domain.ProjectStatus(x.Status).Valid() {
			return p, domain.Invalid(iv("projects", i, "status"), "want active or archived")
		}
		if x.BudgetMonthlyCents != nil && *x.BudgetMonthlyCents < 0 {
			return p, domain.Invalid(iv("projects", i, "budget_monthly_cents"), "must be >= 0")
		}
	}

	// assinaturas
	sub, err := uniqueIdx("subscriptions", doc.Subscriptions, func(x export.Subscription) string { return strings.TrimSpace(x.Name) }, "name")
	if err != nil {
		return p, err
	}
	for i, x := range doc.Subscriptions {
		pct := x.WeeklyGuardPct
		if pct == 0 {
			pct = defaultGuardPct
		}
		if err := validateSubscription(strings.TrimSpace(x.Name), x.Agent, x.MonthlyUSD, x.WeeklyLimitUSD, pct); err != nil {
			return p, domain.Invalid(iv("subscriptions", i, "name"), err.Error())
		}
		for j, per := range x.Periods {
			if per.StartedAt.IsZero() {
				return p, domain.Invalid(fmt.Sprintf("subscriptions[%d].periods[%d].started_at", i, j), "required")
			}
			if per.EndedAt != nil && per.EndedAt.Before(per.StartedAt) {
				return p, domain.Invalid(fmt.Sprintf("subscriptions[%d].periods[%d].ended_at", i, j), "before started_at")
			}
		}
	}

	// personas
	pe, err := uniqueIdx("personas", doc.Personas, func(x export.Persona) string { return x.Slug }, "slug")
	if err != nil {
		return p, err
	}
	parents := map[domain.ID]domain.ID{}
	ids := make([]domain.ID, len(doc.Personas)) // só para o teste de ciclo
	for i := range ids {
		ids[i] = domain.NewID()
	}
	p.personaCfg = make([]json.RawMessage, len(doc.Personas))
	for i, x := range doc.Personas {
		if !domain.ValidSlug(x.Slug) {
			return p, domain.Invalid(iv("personas", i, "slug"), "must be lowercase letters, digits and dashes (1-63 chars)")
		}
		if strings.TrimSpace(x.Name) == "" {
			return p, domain.Invalid(iv("personas", i, "name"), "required")
		}
		if !domain.ValidAgent(x.Agent) {
			return p, domain.Invalid(iv("personas", i, "agent"), fmt.Sprintf("unknown agent %q (want one of %v)", x.Agent, domain.KnownAgents))
		}
		if !domain.PersonaStatus(x.Status).Valid() {
			return p, domain.Invalid(iv("personas", i, "status"), fmt.Sprintf("unknown status %q", x.Status))
		}
		if x.HeartbeatIntervalSec < 0 || x.HeartbeatIntervalSec > maxHeartbeatIntervalSec {
			return p, domain.Invalid(iv("personas", i, "heartbeat_interval_sec"), fmt.Sprintf("must be between 0 and %d", maxHeartbeatIntervalSec))
		}
		if x.BudgetMonthlyCents != nil && *x.BudgetMonthlyCents < 0 {
			return p, domain.Invalid(iv("personas", i, "budget_monthly_cents"), "must be >= 0")
		}
		if err := ref("personas", i, "workspace", x.Workspace, ws); err != nil {
			return p, err
		}
		if err := ref("personas", i, "subscription", x.Subscription, sub); err != nil {
			return p, err
		}
		if err := ref("personas", i, "reports_to", x.ReportsTo, pe); err != nil {
			return p, err
		}
		if x.ReportsTo == x.Slug {
			return p, domain.Invalid(iv("personas", i, "reports_to"), "a persona cannot report to itself")
		}
		if x.ReportsTo != "" {
			parents[ids[i]] = ids[pe[x.ReportsTo]]
		}
		switch {
		case x.Reviewer != "" && (x.ReviewerAgent != "" || x.ReviewerModel != ""):
			return p, domain.Invalid(iv("personas", i, "reviewer"), "choose a reviewer persona or an alternate harness, not both")
		case x.Reviewer != "":
			if err := ref("personas", i, "reviewer", x.Reviewer, pe); err != nil {
				return p, err
			}
			if x.Reviewer == x.Slug {
				return p, domain.Invalid(iv("personas", i, "reviewer"), "a persona cannot review itself")
			}
		case x.ReviewerAgent == "" && x.ReviewerModel != "":
			return p, domain.Invalid(iv("personas", i, "reviewer_agent"), "required when reviewer_model is set")
		case x.ReviewerAgent != "":
			if !domain.ValidReviewerAgent(x.ReviewerAgent) {
				return p, domain.Invalid(iv("personas", i, "reviewer_agent"), fmt.Sprintf("unknown or unsupported reviewer agent %q (want one of %v)", x.ReviewerAgent, domain.ReviewerAgents))
			}
			if x.ReviewerAgent == x.Agent && strings.TrimSpace(x.ReviewerModel) == strings.TrimSpace(x.Model) {
				return p, domain.Invalid(iv("personas", i, "reviewer_agent"), "the alternate harness is the same as the author's")
			}
		}
		cfg := []byte(`{}`)
		if sc := export.ScrubConfig(x.AdapterConfig); sc != nil { // arquivo editado à mão também não traz segredo
			if cfg, err = json.Marshal(sc); err != nil {
				return p, domain.Invalid(iv("personas", i, "adapter_config"), "not valid JSON")
			}
		}
		if err := adapterConfigProblem(x.Agent, cfg); err != nil {
			return p, domain.Invalid(iv("personas", i, "adapter_config"), err.Error())
		}
		p.personaCfg[i] = cfg
	}
	if cyc := orgCycle(parents); cyc != "" {
		return p, domain.Invalid("personas.reports_to", "would create a cycle in the org chart")
	}

	// tasks
	tk, err := uniqueNum("tasks", doc.Tasks)
	if err != nil {
		return p, err
	}
	var maxNum int64
	runKeys := map[string]int{}
	for i, r := range doc.Runs {
		if r.Key == "" {
			return p, domain.Invalid(iv("runs", i, "key"), "required")
		}
		if _, dup := runKeys[r.Key]; dup {
			return p, domain.Invalid(iv("runs", i, "key"), fmt.Sprintf("duplicate %q", r.Key))
		}
		runKeys[r.Key] = i
	}
	for i, x := range doc.Tasks {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		maxNum = max(maxNum, x.Number)
		if n := utf8.RuneCountInString(x.Title); n < 1 || n > maxTaskTitleRunes {
			return p, domain.Invalid(iv("tasks", i, "title"), "must have 1 to 500 characters")
		}
		if !domain.TaskStatus(x.Status).Valid() {
			return p, domain.Invalid(iv("tasks", i, "status"), fmt.Sprintf("unknown status %q", x.Status))
		}
		if !domain.Priority(x.Priority).Valid() {
			return p, domain.Invalid(iv("tasks", i, "priority"), fmt.Sprintf("unknown priority %q", x.Priority))
		}
		if err := ref("tasks", i, "persona", x.Persona, pe); err != nil {
			return p, err
		}
		if err := ref("tasks", i, "project", x.Project, pr); err != nil {
			return p, err
		}
		if err := ref("tasks", i, "workspace", x.Workspace, ws); err != nil {
			return p, err
		}
		if x.Parent != 0 {
			if _, ok := tk[x.Parent]; !ok || x.Parent == x.Number {
				return p, domain.Invalid(iv("tasks", i, "parent"), fmt.Sprintf("task %d not found (or is the task itself)", x.Parent))
			}
		}
		if err := checkActor(iv("tasks", i, "created_by"), x.CreatedBy, pe); err != nil {
			return p, err
		}
		for j, cm := range x.Comments {
			path := fmt.Sprintf("tasks[%d].comments[%d]", i, j)
			if n := utf8.RuneCountInString(cm.Body); n < 1 || n > maxCommentBodyRunes {
				return p, domain.Invalid(path+".body", "must have 1 to 100000 characters")
			}
			if err := checkActor(path+".author", cm.Author, pe); err != nil {
				return p, err
			}
			if cm.Run != "" {
				if _, ok := runKeys[cm.Run]; !ok {
					return p, domain.Invalid(path+".run", fmt.Sprintf("run %q not in the file", cm.Run))
				}
			}
		}
	}
	if p.taskOrder, err = orderTasks(doc.Tasks, tk); err != nil {
		return p, err
	}
	p.nextTask = max(c.NextTaskNumber, maxNum+1)

	// fluxos
	if _, err := uniqueIdx("flows", doc.Flows, func(x export.Flow) string { return x.Slug }, "slug"); err != nil {
		return p, err
	}
	for i, x := range doc.Flows {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if !domain.ValidSlug(x.Slug) {
			return p, domain.Invalid(iv("flows", i, "slug"), "must be lowercase letters, digits and dashes (1-63 chars)")
		}
		if strings.TrimSpace(x.Name) == "" {
			return p, domain.Invalid(iv("flows", i, "name"), "required")
		}
		if _, err := ParseCron(x.Cron, x.Timezone); err != nil {
			return p, domain.Invalid(iv("flows", i, "cron"), err.Error())
		}
		if x.Persona == "" {
			return p, domain.Invalid(iv("flows", i, "persona"), "required")
		}
		if err := ref("flows", i, "persona", x.Persona, pe); err != nil {
			return p, err
		}
		if err := ref("flows", i, "project", x.Project, pr); err != nil {
			return p, err
		}
		if !domain.Priority(x.Priority).Valid() {
			return p, domain.Invalid(iv("flows", i, "priority"), fmt.Sprintf("unknown priority %q", x.Priority))
		}
	}

	// execuções
	names := map[string]bool{}
	for i, r := range doc.Runs {
		if err := ctx.Err(); err != nil {
			return p, err
		}
		if err := nonNegativeRun(i, r); err != nil {
			return p, err
		}
		if _, ok := tk[r.Task]; !ok {
			return p, domain.Invalid(iv("runs", i, "task"), fmt.Sprintf("task %d not found", r.Task))
		}
		if err := ref("runs", i, "persona", r.Persona, pe); err != nil {
			return p, err
		}
		if r.Persona == "" {
			return p, domain.Invalid(iv("runs", i, "persona"), "required")
		}
		if st := domain.RunStatus(r.Status); !st.Valid() || st.Active() {
			return p, domain.Invalid(iv("runs", i, "status"), fmt.Sprintf("status %q is not a finished run", r.Status))
		}
		if !validTrigger(r.Trigger) {
			return p, domain.Invalid(iv("runs", i, "trigger"), fmt.Sprintf("unknown trigger %q", r.Trigger))
		}
		if r.Agent == "" {
			return p, domain.Invalid(iv("runs", i, "agent"), "required")
		}
		if r.ModelSource != "" && r.ModelSource != string(domain.ModelFromTask) && r.ModelSource != string(domain.ModelFromPersona) &&
			r.ModelSource != string(domain.ModelFromHarnessDefault) {
			return p, domain.Invalid(iv("runs", i, "model_source"), fmt.Sprintf("unknown model_source %q", r.ModelSource))
		}
		if r.CostSource != "" && r.CostSource != string(domain.CostReported) && r.CostSource != string(domain.CostEstimated) && r.CostSource != string(domain.CostNone) {
			return p, domain.Invalid(iv("runs", i, "cost_source"), fmt.Sprintf("unknown cost_source %q", r.CostSource))
		}
		if err := ref("runs", i, "subscription", r.Subscription, sub); err != nil {
			return p, err
		}
		if r.Name != "" {
			if names[r.Name] {
				return p, domain.Invalid(iv("runs", i, "name"), fmt.Sprintf("duplicate %q", r.Name))
			}
			names[r.Name] = true
		}
		var last int64
		for j, e := range r.Events {
			if j%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return p, err
				}
			}
			path := fmt.Sprintf("runs[%d].events[%d]", i, j)
			if e.Seq <= last {
				return p, domain.Invalid(path+".seq", "must be positive and strictly increasing")
			}
			last = e.Seq
			switch domain.RunEventKind(e.Kind) {
			case domain.EventSystem, domain.EventAssistant, domain.EventThinking, domain.EventToolUse, domain.EventToolResult,
				domain.EventStdout, domain.EventStderr, domain.EventResult, domain.EventStatus:
			default:
				return p, domain.Invalid(path+".kind", fmt.Sprintf("unknown kind %q", e.Kind))
			}
			if len(e.Payload) > 0 && !json.Valid(e.Payload) {
				return p, domain.Invalid(path+".payload", "not valid JSON")
			}
		}
	}
	if err := s.planArtifacts(doc, tk, pe, pr, runKeys); err != nil {
		return p, err
	}
	return p, nil
}

// nonNegativeRun recusa custos e tokens negativos: eles entrariam nos totais de custo da empresa nova.
func nonNegativeRun(i int, r export.Run) error {
	for _, f := range []struct {
		name string
		v    *float64
	}{{"cost_usd", r.CostUSD}, {"list_cost_usd", r.ListCostUSD}} {
		if f.v != nil && (*f.v < 0 || math.IsNaN(*f.v) || math.IsInf(*f.v, 0)) {
			return domain.Invalid(iv("runs", i, f.name), "must be >= 0")
		}
	}
	u := r.Usage
	for _, f := range []struct {
		name string
		v    int64
	}{{"input_tokens", u.InputTokens}, {"output_tokens", u.OutputTokens}, {"cache_read_tokens", u.CacheReadTokens},
		{"cache_creation_tokens", u.CacheCreationTokens}} {
		if f.v < 0 {
			return domain.Invalid(fmt.Sprintf("runs[%d].usage.%s", i, f.name), "must be >= 0")
		}
	}
	return nil
}

func validTrigger(t string) bool {
	switch domain.RunTrigger(t) {
	case domain.TriggerAssign, domain.TriggerComment, domain.TriggerManual, domain.TriggerHeartbeat, domain.TriggerGate,
		domain.TriggerFlow, domain.TriggerReview:
		return true
	}
	return false
}

// ref confere que uma referência opcional (slug/nome) existe no arquivo.
func ref(path string, i int, field, v string, idx map[string]int) error {
	if v == "" {
		return nil
	}
	if _, ok := idx[v]; !ok {
		return domain.Invalid(iv(path, i, field), fmt.Sprintf("%q not found in the file", v))
	}
	return nil
}

func checkActor(path string, a export.Actor, personas map[string]int) error {
	if !domain.ActorType(a.Type).Valid() {
		return domain.Invalid(path+".type", fmt.Sprintf("unknown actor type %q", a.Type))
	}
	if a.Type == string(domain.ActorPersona) {
		if _, ok := personas[a.Persona]; !ok {
			return domain.Invalid(path+".persona", fmt.Sprintf("%q not found in the file", a.Persona))
		}
	}
	return nil
}

// maxTaskNumber é o maior número de task aceito na importação (evita overflow da numeração e de "HNK-n").
const maxTaskNumber = 1_000_000_000

func uniqueNum(path string, tasks []export.Task) (map[int64]int, error) {
	m := make(map[int64]int, len(tasks))
	for i, t := range tasks {
		if t.Number < 1 || t.Number > maxTaskNumber {
			return nil, domain.Invalid(iv(path, i, "number"), fmt.Sprintf("must be between 1 and %d", maxTaskNumber))
		}
		if _, dup := m[t.Number]; dup {
			return nil, domain.Invalid(iv(path, i, "number"), fmt.Sprintf("duplicate %d", t.Number))
		}
		m[t.Number] = i
	}
	return m, nil
}

// orgCycle devolve não-vazio quando o mapa filho→pai tem ciclo.
func orgCycle(parents map[domain.ID]domain.ID) string {
	for start := range parents {
		seen := map[domain.ID]bool{start: true}
		for cur, ok := parents[start]; ok; cur, ok = parents[cur] {
			if seen[cur] {
				return cur.String()
			}
			seen[cur] = true
		}
	}
	return ""
}

// orderTasks ordena os índices por número, com cada pai antes dos filhos; ciclo de pais é erro.
func orderTasks(tasks []export.Task, byNum map[int64]int) ([]int, error) {
	idx := make([]int, len(tasks))
	for i := range idx {
		idx[i] = i
	}
	// por número, estável (O(n log n): o arquivo pode ter dezenas de milhares de tasks)
	slices.SortStableFunc(idx, func(a, b int) int {
		switch {
		case tasks[a].Number < tasks[b].Number:
			return -1
		case tasks[a].Number > tasks[b].Number:
			return 1
		}
		return 0
	})
	state := make([]int, len(tasks)) // 0 novo, 1 visitando, 2 pronto
	out := make([]int, 0, len(tasks))
	var visit func(i int) error
	visit = func(i int) error {
		switch state[i] {
		case 2:
			return nil
		case 1:
			return domain.Invalid("tasks.parent", fmt.Sprintf("parent cycle at task %d", tasks[i].Number))
		}
		state[i] = 1
		if par := tasks[i].Parent; par != 0 {
			if err := visit(byNum[par]); err != nil {
				return err
			}
		}
		state[i] = 2
		out = append(out, i)
		return nil
	}
	for _, i := range idx {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// runImport grava tudo na transação aberta. O arquivo já foi validado por planImport.
func (t *txc) runImport(cid domain.ID, doc export.Document, p importPlan, opts ImportOptions) error {
	ctx := t.ctx
	wsID := map[string]domain.ID{}
	for _, w := range doc.Workspaces {
		id := domain.NewID()
		path := strings.TrimSpace(w.Path)
		if p.clearedPaths[w.Slug] {
			path = ""
		}
		if _, err := t.q.ImportWorkspace(ctx, store.ImportWorkspaceParams{ID: id, CompanyID: cid, Slug: w.Slug, Name: strings.TrimSpace(w.Name),
			Path: path, RepoUrl: strings.TrimSpace(w.RepoURL), DefaultBranch: strings.TrimSpace(w.DefaultBranch),
			CreatedAt: w.CreatedAt, UpdatedAt: w.CreatedAt}); err != nil {
			return mapErr(err, "import workspace "+w.Slug)
		}
		wsID[w.Slug] = id
	}
	prID := map[string]domain.ID{}
	for _, x := range doc.Projects {
		id := domain.NewID()
		status := x.Status
		if status == "" {
			status = string(domain.ProjectActive)
		}
		if _, err := t.q.ImportProject(ctx, store.ImportProjectParams{ID: id, CompanyID: cid, Slug: x.Slug, Name: strings.TrimSpace(x.Name),
			Description: x.Description, WorkspaceID: optID(wsID, x.Workspace), Status: status, BudgetMonthlyCents: x.BudgetMonthlyCents,
			CreatedAt: x.CreatedAt, UpdatedAt: x.CreatedAt}); err != nil {
			return mapErr(err, "import project "+x.Slug)
		}
		prID[x.Slug] = id
	}
	subID := map[string]domain.ID{}
	for _, x := range doc.Subscriptions {
		id := domain.NewID()
		pct := x.WeeklyGuardPct
		if pct == 0 {
			pct = defaultGuardPct
		}
		name := strings.TrimSpace(x.Name)
		if _, err := t.q.ImportSubscription(ctx, store.ImportSubscriptionParams{ID: id, CompanyID: cid, Name: name, Agent: x.Agent,
			MonthlyUsd: x.MonthlyUSD, ModelPrefixes: normalizePrefixes(x.ModelPrefixes), WeeklyLimitUsd: x.WeeklyLimitUSD,
			WeeklyGuardPct: i32(pct), Active: x.Active, EndedAt: x.EndedAt, CreatedAt: x.CreatedAt, UpdatedAt: x.CreatedAt}); err != nil {
			return mapErr(err, "import subscription "+name)
		}
		for _, per := range x.Periods {
			if err := t.q.ImportSubscriptionPeriod(ctx, store.ImportSubscriptionPeriodParams{SubscriptionID: id,
				StartedAt: per.StartedAt, EndedAt: per.EndedAt}); err != nil {
				return mapErr(err, "import subscription period")
			}
		}
		subID[name] = id
	}
	peID := map[string]domain.ID{}
	for i, x := range doc.Personas {
		id := domain.NewID()
		status := x.Status
		if !opts.Active && (status == string(domain.PersonaActive) || status == string(domain.PersonaIdle)) {
			status = string(domain.PersonaPaused)
		}
		if _, err := t.q.ImportPersona(ctx, store.ImportPersonaParams{ID: id, CompanyID: cid, Slug: x.Slug, Name: strings.TrimSpace(x.Name),
			Title: strings.TrimSpace(x.Title), Instructions: x.Instructions, Agent: x.Agent, Model: strings.TrimSpace(x.Model),
			AdapterConfig: p.personaCfg[i], Status: exportStatus(status), WorkspaceID: optID(wsID, x.Workspace),
			HeartbeatIntervalSec: i32(x.HeartbeatIntervalSec), BudgetMonthlyCents: x.BudgetMonthlyCents,
			SubscriptionID: optID(subID, strings.TrimSpace(x.Subscription)), RequireReview: x.RequireReview,
			ReviewerAgent: strings.TrimSpace(x.ReviewerAgent), ReviewerModel: strings.TrimSpace(x.ReviewerModel),
			CreatedAt: x.CreatedAt, UpdatedAt: x.CreatedAt}); err != nil {
			return mapErr(err, "import persona "+x.Slug)
		}
		peID[x.Slug] = id
	}
	for _, x := range doc.Personas {
		if x.ReportsTo == "" && x.Reviewer == "" {
			continue
		}
		if err := t.q.LinkImportedPersona(ctx, store.LinkImportedPersonaParams{ID: peID[x.Slug],
			ReportsTo: optID(peID, x.ReportsTo), ReviewerPersonaID: optID(peID, x.Reviewer)}); err != nil {
			return mapErr(err, "link persona "+x.Slug)
		}
	}

	runID := map[string]domain.ID{}
	for _, r := range doc.Runs {
		runID[r.Key] = domain.NewID()
	}
	taskID := make(map[int64]domain.ID, len(doc.Tasks))
	for _, i := range p.taskOrder {
		x := doc.Tasks[i]
		id := domain.NewID()
		taskID[x.Number] = id
		var parent *domain.ID
		if x.Parent != 0 {
			pid := taskID[x.Parent]
			parent = &pid
		}
		if _, err := t.q.ImportTask(ctx, store.ImportTaskParams{ID: id, CompanyID: cid, Number: x.Number, Title: x.Title,
			Description: x.Description, Status: x.Status, Priority: x.Priority, PersonaID: optID(peID, x.Persona),
			ProjectID: optID(prID, x.Project), WorkspaceID: optID(wsID, x.Workspace), ParentID: parent,
			CreatedByType: x.CreatedBy.Type, CreatedByID: optID(peID, x.CreatedBy.Persona), Model: x.Model,
			CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt, ClosedAt: x.ClosedAt}); err != nil {
			return mapErr(err, fmt.Sprintf("import task %d", x.Number))
		}
	}
	// comentários referenciam execuções por run_id sem FK; entram depois das tasks, na ordem do arquivo
	for _, x := range doc.Tasks {
		for _, cm := range x.Comments {
			if _, err := t.q.ImportComment(ctx, store.ImportCommentParams{ID: domain.NewID(), CompanyID: cid, TaskID: taskID[x.Number],
				AuthorType: cm.Author.Type, AuthorID: optID(peID, cm.Author.Persona), RunID: optID(runID, cm.Run),
				Body: cm.Body, CreatedAt: cm.CreatedAt}); err != nil {
				return mapErr(err, "import comment")
			}
		}
	}

	for _, x := range doc.Flows {
		enabled := x.Enabled && opts.Active
		next, err := NextCron(x.Cron, x.Timezone, t.now)
		if err != nil {
			return err
		}
		if _, err := t.q.ImportFlow(ctx, store.ImportFlowParams{ID: domain.NewID(), CompanyID: cid, Slug: x.Slug, Name: strings.TrimSpace(x.Name),
			Description: x.Description, Cron: strings.TrimSpace(x.Cron), Timezone: x.Timezone, Enabled: enabled, PersonaID: peID[x.Persona],
			ProjectID: optID(prID, x.Project), Priority: x.Priority, TaskTitle: x.TaskTitle, TaskDescription: x.TaskDescription,
			NextFireAt: &next, CreatedAt: x.CreatedAt, UpdatedAt: x.CreatedAt}); err != nil {
			return mapErr(err, "import flow "+x.Slug)
		}
	}

	for _, r := range doc.Runs {
		var exit *int32
		if r.ExitCode != nil {
			v := i32(*r.ExitCode)
			exit = &v
		}
		var nextSeq int64 = 1
		if n := len(r.Events); n > 0 {
			nextSeq = r.Events[n-1].Seq + 1
		}
		modelSource, costSource := r.ModelSource, r.CostSource
		if modelSource == "" {
			modelSource = string(domain.ModelFromHarnessDefault)
		}
		if costSource == "" {
			costSource = string(domain.CostNone)
		}
		if _, err := t.q.ImportRun(ctx, store.ImportRunParams{ID: runID[r.Key], CompanyID: cid, TaskID: taskID[r.Task], PersonaID: peID[r.Persona],
			Status: r.Status, Trigger: r.Trigger, Agent: r.Agent, Model: r.Model, ModelSource: modelSource,
			SubscriptionID: optID(subID, r.Subscription), Name: r.Name, ExitCode: exit, Error: r.Error, Summary: r.Summary,
			CostUsd: r.CostUSD, CostSource: costSource, ListCostUsd: r.ListCostUSD, InputTokens: r.Usage.InputTokens,
			OutputTokens: r.Usage.OutputTokens, CacheReadTokens: r.Usage.CacheReadTokens, CacheCreationTokens: r.Usage.CacheCreationTokens,
			NextEventSeq: nextSeq, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}); err != nil {
			return mapErr(err, "import run "+r.Key)
		}
		for _, e := range r.Events {
			var payload []byte
			if len(e.Payload) > 0 {
				payload = e.Payload
			}
			if err := t.q.ImportRunEvent(ctx, store.ImportRunEventParams{ID: domain.NewID(), CompanyID: cid, RunID: runID[r.Key],
				Seq: e.Seq, Kind: e.Kind, Text: e.Text, Payload: payload, OccurredAt: e.OccurredAt}); err != nil {
				return mapErr(err, "import run event")
			}
		}
	}
	return t.importArtifacts(cid, doc, taskID, prID, peID, runID)
}

// optID resolve um slug opcional no id novo; vazio ou desconhecido = nil.
func optID(m map[string]domain.ID, key string) *uuid.UUID {
	if key == "" {
		return nil
	}
	if id, ok := m[key]; ok {
		return &id
	}
	return nil
}

// invalidReason é o motivo de um erro de validação, para reaproveitá-lo sob o caminho do campo do arquivo.
func invalidReason(err error) string {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		return ve.Reason
	}
	return err.Error()
}
