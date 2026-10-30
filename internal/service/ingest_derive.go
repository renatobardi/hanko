package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Derivação da ingestão (ADR-0075 itens 2 a 6, ADR-0077 item 2). O bruto guarda só o texto que chegou; aqui cada texto
// vira uma entidade da empresa (agente externo, host externo, emparelhamento com workspace), na mesma transação do lote.
// Texto que não cabe no CHECK do banco (00208) não é derivado, mas o bruto grava: o lote nunca cai por causa dele.

const (
	maxEntityName    = 100 // external_agent.name e external_host.name: 1 a 100, sem espaço nas pontas
	maxRepoKey       = 500 // repo_pairing.repo_key: 1 a 500
	maxWorkspaceName = 120 // nome de workspace, como em CreateWorkspace
	systemSlug       = "no-repo"
)

// derived é o que um lote traz para as entidades.
type derived struct {
	agents map[string]bool
	hosts  map[string]int64 // host -> hora do fato mais novo do lote (ns)
	repos  map[string]bool
	noRepo bool // há fato sem repositório válido: vai para o workspace de sistema
}

// entityText devolve o texto aparado e dentro do limite, ou falso quando o texto não serve como entidade.
func entityText(raw string, max int) (string, bool) {
	t := strings.TrimSpace(raw)
	if t == "" || utf8.RuneCountInString(t) > max {
		return "", false
	}
	return t, true
}

func textOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func collectDerived(b ingest.Batch) derived {
	d := derived{agents: map[string]bool{}, hosts: map[string]int64{}, repos: map[string]bool{}}
	note := func(f ingest.Fixed, at int64) {
		if t, ok := entityText(textOf(f.OuteAgent), maxEntityName); ok {
			d.agents[t] = true
		}
		if t, ok := entityText(textOf(f.HostName), maxEntityName); ok {
			if cur, seen := d.hosts[t]; !seen || at > cur {
				d.hosts[t] = at
			}
		}
		if t, ok := entityText(textOf(f.OuteRepo), maxRepoKey); ok {
			d.repos[t] = true
		} else {
			d.noRepo = true
		}
	}
	for _, r := range b.Spans {
		note(r.Fixed, r.TimeUnixNano)
	}
	for _, r := range b.Logs {
		note(r.Fixed, r.TimeUnixNano)
	}
	for _, r := range b.Metrics {
		note(r.Fixed, r.TimeUnixNano)
	}
	return d
}

// deriveFromBatch cria ou liga as entidades do lote. Ordem fixa (texto ordenado) para os eventos saírem sempre iguais.
func deriveFromBatch(t *txc, companyID domain.ID, d derived) error {
	for _, name := range sortedTexts(d.agents) {
		if _, err := resolveEntity(t, companyID, name, agentEntity); err != nil {
			return err
		}
	}
	for _, name := range sortedTexts(d.hosts) {
		id, err := resolveEntity(t, companyID, name, hostEntity)
		if err != nil {
			return err
		}
		seen := time.Unix(0, d.hosts[name]).UTC()
		if err := t.q.TouchExternalHost(t.ctx, store.TouchExternalHostParams{ID: id, SeenAt: &seen}); err != nil {
			return fmt.Errorf("ingest: atualizar o host %q: %w", name, err)
		}
	}
	for _, repo := range sortedTexts(d.repos) {
		if err := ensurePairing(t, companyID, repo); err != nil {
			return err
		}
	}
	if d.noRepo {
		if _, _, err := systemWorkspace(t, companyID); err != nil {
			return err
		}
	}
	return nil
}

func sortedTexts[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// entityOps são as consultas de uma entidade (agente ou host): ela tem apelidos e um nome único sem caixa.
type entityOps struct {
	verb     string // evento de criação
	kind     string // tipo do sujeito no activity_event
	byAlias  func(t *txc, companyID domain.ID, alias string) (domain.ID, error)
	byName   func(t *txc, companyID domain.ID, name string) (domain.ID, error)
	insert   func(t *txc, companyID domain.ID, id domain.ID, name string) (domain.ID, error)
	addAlias func(t *txc, companyID domain.ID, alias string, id domain.ID) error
}

var agentEntity = entityOps{
	verb: "external_agent.created", kind: "external_agent",
	byAlias: func(t *txc, c domain.ID, alias string) (domain.ID, error) {
		return t.q.ExternalAgentIDByAlias(t.ctx, store.ExternalAgentIDByAliasParams{CompanyID: c, Alias: alias})
	},
	byName: func(t *txc, c domain.ID, name string) (domain.ID, error) {
		return t.q.ExternalAgentIDByName(t.ctx, store.ExternalAgentIDByNameParams{CompanyID: c, Name: name})
	},
	insert: func(t *txc, c domain.ID, id domain.ID, name string) (domain.ID, error) {
		return t.q.InsertExternalAgent(t.ctx, store.InsertExternalAgentParams{ID: id, CompanyID: c, Name: name, Now: t.now})
	},
	addAlias: func(t *txc, c domain.ID, alias string, id domain.ID) error {
		return t.q.InsertExternalAgentAlias(t.ctx, store.InsertExternalAgentAliasParams{CompanyID: c, Alias: alias, AgentID: id, Now: t.now})
	},
}

var hostEntity = entityOps{
	verb: "external_host.created", kind: "external_host",
	byAlias: func(t *txc, c domain.ID, alias string) (domain.ID, error) {
		return t.q.ExternalHostIDByAlias(t.ctx, store.ExternalHostIDByAliasParams{CompanyID: c, Alias: alias})
	},
	byName: func(t *txc, c domain.ID, name string) (domain.ID, error) {
		return t.q.ExternalHostIDByName(t.ctx, store.ExternalHostIDByNameParams{CompanyID: c, Name: name})
	},
	insert: func(t *txc, c domain.ID, id domain.ID, name string) (domain.ID, error) {
		return t.q.InsertExternalHost(t.ctx, store.InsertExternalHostParams{ID: id, CompanyID: c, Name: name, Now: t.now})
	},
	addAlias: func(t *txc, c domain.ID, alias string, id domain.ID) error {
		return t.q.InsertExternalHostAlias(t.ctx, store.InsertExternalHostAliasParams{CompanyID: c, Alias: alias, HostID: id, Now: t.now})
	},
}

// resolveEntity acha a entidade pelo apelido; sem apelido, pelo nome sem caixa (o texto novo vira apelido); sem nome,
// cria a entidade com `activity_event`. Corrida: se outra transação criou o nome antes, usa a dela.
func resolveEntity(t *txc, companyID domain.ID, text string, e entityOps) (domain.ID, error) {
	id, err := e.byAlias(t, companyID, text)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar %s por apelido: %w", e.kind, err)
	}
	id, err = e.byName(t, companyID, text)
	if err == nil {
		return id, e.addAlias(t, companyID, text, id)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar %s pelo nome: %w", e.kind, err)
	}
	newID := domain.NewID()
	created, err := e.insert(t, companyID, newID, text)
	if errors.Is(err, pgx.ErrNoRows) {
		created, err = e.byName(t, companyID, text)
	}
	if err != nil {
		return domain.ID{}, fmt.Errorf("ingest: criar %s: %w", e.kind, err)
	}
	if created == newID {
		if err := t.activity(domain.SystemActor, companyID, e.verb, e.kind, created, map[string]any{"name": text}, ""); err != nil {
			return domain.ID{}, err
		}
	}
	return created, e.addAlias(t, companyID, text, created)
}

// ensurePairing liga o repositório do fato a um workspace (ADR-0075 item 4, passos 2 e 3). Sem workspace possível, não
// emparelha: o bruto segue e o fato fica sem workspace.
func ensurePairing(t *txc, companyID domain.ID, repo string) error {
	_, err := t.q.RepoPairingWorkspaceID(t.ctx, store.RepoPairingWorkspaceIDParams{CompanyID: companyID, RepoKey: repo})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("ingest: buscar emparelhamento: %w", err)
	}
	wsID, ok, err := pairedWorkspace(t, companyID, repo)
	if err != nil || !ok {
		return err
	}
	pairingID, err := t.q.InsertRepoPairing(t.ctx, store.InsertRepoPairingParams{ID: domain.NewID(), CompanyID: companyID, RepoKey: repo, WorkspaceID: wsID, Now: t.now})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // outra transação emparelhou antes: o evento já saiu dela
	}
	if err != nil {
		return fmt.Errorf("ingest: gravar emparelhamento: %w", err)
	}
	return t.activity(domain.SystemActor, companyID, "repo_pairing.created", "repo_pairing", pairingID,
		map[string]any{"repo": repo, "workspace_id": wsID.String()}, "")
}

// pairedWorkspace acha o workspace do repositório pelo slug ou cria um novo, com `activity_event`. Repositório sem slug
// válido, ou com o slug reservado `no-repo`, vai para o workspace de sistema.
func pairedWorkspace(t *txc, companyID domain.ID, repo string) (domain.ID, bool, error) {
	slug, ok := repoSlug(repo)
	if !ok {
		return systemWorkspace(t, companyID)
	}
	id, err := workspaceBySlug(t, companyID, slug)
	if err != nil || id != (domain.ID{}) {
		return id, err == nil, err
	}
	name := repo
	if utf8.RuneCountInString(name) > maxWorkspaceName {
		name = slug
	}
	created, err := t.q.InsertRepoWorkspace(t.ctx, store.InsertRepoWorkspaceParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name, Now: t.now})
	if errors.Is(err, pgx.ErrNoRows) {
		// corrida: outra transação criou o slug entre a busca e o insert
		id, err := workspaceBySlug(t, companyID, slug)
		return id, err == nil && id != (domain.ID{}), err
	}
	if err != nil {
		return domain.ID{}, false, fmt.Errorf("ingest: criar workspace %q: %w", slug, err)
	}
	if err := t.activity(domain.SystemActor, companyID, "workspace.created", "workspace", created,
		map[string]any{"slug": slug, "name": name}, ""); err != nil {
		return domain.ID{}, false, err
	}
	return created, true, nil
}

// workspaceBySlug devolve o workspace comum de um slug; ausente vira ID vazio sem erro.
func workspaceBySlug(t *txc, companyID domain.ID, slug string) (domain.ID, error) {
	id, err := t.q.WorkspaceIDBySlug(t.ctx, store.WorkspaceIDBySlugParams{CompanyID: companyID, Slug: slug})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, nil
	}
	if err != nil {
		return domain.ID{}, fmt.Errorf("ingest: buscar workspace %q: %w", slug, err)
	}
	return id, nil
}

// repoSlug é o slug que o nome do repositório dá ao workspace: o próprio nome se já é slug, ou o nome normalizado. O
// slug `no-repo` é reservado ao workspace de sistema: um repositório com esse nome vai para ele, nunca a um workspace comum.
func repoSlug(repo string) (string, bool) {
	s := repo
	if !domain.ValidSlug(repo) {
		s = domain.Slugify(repo)
	}
	if s == systemSlug || !domain.ValidSlug(s) {
		return "", false
	}
	return s, true
}

// systemWorkspace devolve o workspace `no-repo` da empresa, criando-o na primeira vez (ADR-0075 item 6). Se um workspace
// comum já tem o slug `no-repo`, o de sistema não nasce: devolve falso e o fato fica sem workspace, sem erro.
func systemWorkspace(t *txc, companyID domain.ID) (domain.ID, bool, error) {
	id, err := t.q.SystemWorkspaceID(t.ctx, companyID)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, false, fmt.Errorf("ingest: buscar o workspace no-repo: %w", err)
	}
	newID := domain.NewID()
	created, err := t.q.InsertSystemWorkspace(t.ctx, store.InsertSystemWorkspaceParams{ID: newID, CompanyID: companyID, Now: t.now})
	if errors.Is(err, pgx.ErrNoRows) {
		// ou outra transação criou o no-repo antes, ou um workspace comum já usa o slug
		id, err := t.q.SystemWorkspaceID(t.ctx, companyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ID{}, false, nil
		}
		return id, err == nil, err
	}
	if err != nil {
		return domain.ID{}, false, fmt.Errorf("ingest: criar o workspace no-repo: %w", err)
	}
	if err := t.activity(domain.SystemActor, companyID, "workspace.created", "workspace", created,
		map[string]any{"slug": systemSlug, "system": true}, ""); err != nil {
		return domain.ID{}, false, err
	}
	return created, true, nil
}
