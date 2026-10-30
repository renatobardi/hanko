package service

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/ingest"
	"github.com/renatobardi/hanko/internal/store"
)

// Derivação da ingestão (ADR-0075 itens 2 a 6, ADR-0077 item 2). O bruto guarda só o texto que chegou; aqui cada texto
// vira uma entidade da empresa (agente externo, host externo, emparelhamento com workspace), na mesma transação do lote.

// derived é o que um lote traz para as entidades.
type derived struct {
	agents map[string]bool
	hosts  map[string]int64 // host -> hora do fato mais novo do lote (ns)
	repos  map[string]bool
	noRepo bool // há fato sem repositório: vai para o workspace `no-repo`
}

func collectDerived(b ingest.Batch) derived {
	d := derived{agents: map[string]bool{}, hosts: map[string]int64{}, repos: map[string]bool{}}
	note := func(f ingest.Fixed, at int64) {
		if f.OuteAgent != nil && *f.OuteAgent != "" {
			d.agents[*f.OuteAgent] = true
		}
		if f.HostName != nil && *f.HostName != "" {
			if cur, ok := d.hosts[*f.HostName]; !ok || at > cur {
				d.hosts[*f.HostName] = at
			}
		}
		if f.OuteRepo != nil && *f.OuteRepo != "" {
			d.repos[*f.OuteRepo] = true
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
		if _, err := systemWorkspace(t, companyID); err != nil {
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
// cria a entidade com `activity_event`. Um apelido que já aponta para outra entidade vence (juntar move o apelido).
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
		// outra transação criou o nome entre a busca e o insert: usa o que ela criou
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

// ensurePairing liga o repositório do fato a um workspace (ADR-0075 item 4, passos 2 e 3): o workspace de mesmo slug
// ou um workspace novo, só de metadado. Repositório sem slug válido vai para o `no-repo`, e não se adivinha outro.
func ensurePairing(t *txc, companyID domain.ID, repo string) error {
	_, err := t.q.RepoPairingWorkspaceID(t.ctx, store.RepoPairingWorkspaceIDParams{CompanyID: companyID, RepoKey: repo})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("ingest: buscar emparelhamento: %w", err)
	}
	wsID, err := pairedWorkspace(t, companyID, repo)
	if err != nil {
		return err
	}
	if err := t.q.InsertRepoPairing(t.ctx, store.InsertRepoPairingParams{ID: domain.NewID(), CompanyID: companyID, RepoKey: repo, WorkspaceID: wsID, Now: t.now}); err != nil {
		return fmt.Errorf("ingest: gravar emparelhamento: %w", err)
	}
	return t.activity(domain.SystemActor, companyID, "repo_pairing.created", "repo_pairing", wsID,
		map[string]any{"repo": repo, "workspace_id": wsID.String()}, "")
}

// pairedWorkspace acha o workspace do repositório pelo slug ou cria um novo, com `activity_event`.
func pairedWorkspace(t *txc, companyID domain.ID, repo string) (domain.ID, error) {
	slug, ok := repoSlug(repo)
	if !ok {
		return systemWorkspace(t, companyID)
	}
	id, err := t.q.WorkspaceIDBySlug(t.ctx, store.WorkspaceIDBySlugParams{CompanyID: companyID, Slug: slug})
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar workspace %q: %w", slug, err)
	}
	name := repo
	if len(name) > 120 {
		name = slug
	}
	id, err = t.q.InsertRepoWorkspace(t.ctx, store.InsertRepoWorkspaceParams{ID: domain.NewID(), CompanyID: companyID, Slug: slug, Name: name, Now: t.now})
	if err != nil {
		return domain.ID{}, fmt.Errorf("ingest: criar workspace %q: %w", slug, err)
	}
	return id, t.activity(domain.SystemActor, companyID, "workspace.created", "workspace", id,
		map[string]any{"slug": slug, "name": name}, "")
}

// repoSlug é o slug que o nome do repositório dá ao workspace: o próprio nome se já é slug, ou o nome normalizado.
func repoSlug(repo string) (string, bool) {
	if domain.ValidSlug(repo) {
		return repo, true
	}
	s := domain.Slugify(repo)
	return s, domain.ValidSlug(s)
}

// systemWorkspace devolve o workspace `no-repo` da empresa, criando-o na primeira vez (ADR-0075 item 6).
func systemWorkspace(t *txc, companyID domain.ID) (domain.ID, error) {
	id, err := t.q.SystemWorkspaceID(t.ctx, companyID)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.ID{}, fmt.Errorf("ingest: buscar o workspace no-repo: %w", err)
	}
	newID := domain.NewID()
	created, err := t.q.InsertSystemWorkspace(t.ctx, store.InsertSystemWorkspaceParams{ID: newID, CompanyID: companyID, Now: t.now})
	if errors.Is(err, pgx.ErrNoRows) {
		// outra transação criou o no-repo primeiro
		return t.q.SystemWorkspaceID(t.ctx, companyID)
	}
	if err != nil {
		return domain.ID{}, fmt.Errorf("ingest: criar o workspace no-repo: %w", err)
	}
	return created, t.activity(domain.SystemActor, companyID, "workspace.created", "workspace", created,
		map[string]any{"slug": "no-repo", "system": true}, "")
}
