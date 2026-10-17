package ui

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/textdiff"
	"github.com/renatobardi/hanko/internal/ui/uitest"
)

// studioFake estende o skillsFake com versões, fontes e estúdio (ADR-0035).
type studioFake struct {
	*skillsFake
	enabled  bool
	versions map[domain.ID][]domain.SkillVersion
	sources  []service.SkillSourceView
	inputs   []domain.SkillTestInput
	tests    []domain.SkillTestRun
	pins     map[[2]domain.ID]*int
	synced   []bool // force de cada sincronização
	created  []service.CreateSkillSourceInput
	updates  []service.UpdateSkillSourceInput
	deleted  []bool // keep
	ran      []service.RunSkillTestInput
	restored []int
}

func newStudioEnv(t *testing.T, role domain.MemberRole) (*env, *studioFake) {
	t.Helper()
	sf := newSkillsFake()
	sf.MyRoleIs = role
	pr := sf.skills[0]
	srcID := uitest.ID("src:1")
	f := &studioFake{skillsFake: sf, enabled: true, pins: map[[2]domain.ID]*int{}}
	now := uitest.Now()
	f.versions = map[domain.ID][]domain.SkillVersion{pr.ID: {
		{Revision: 2, Name: pr.Name, Description: pr.Description, Body: pr.Body, BodyBytes: len(pr.Body), SHA256: strings.Repeat("b", 64), CreatedAt: now},
		{Revision: 1, Name: pr.Name, Description: pr.Description, Body: "# Antigo", BodyBytes: 8, SHA256: strings.Repeat("a", 64), CreatedAt: now},
	}}
	// uma skill importada de fonte, modificada localmente
	sf.skills = append(sf.skills, domain.Skill{ID: uitest.ID("skill:deploy"), Slug: "deploy", Name: "Deploy", Description: "Como publicar", Body: "# Deploy",
		Version: 1, SourceID: &srcID, SourcePath: "skills/deploy", SourceCommit: strings.Repeat("c", 40), SourceModified: true, CreatedAt: now, UpdatedAt: now})
	f.sources = []service.SkillSourceView{{SkillSource: domain.SkillSource{ID: srcID, URL: "https://example.com/org/skills.git", TrackingRef: "main",
		Status: domain.SourceError, LastError: "git clone: repository not found", AutoUpdate: true, Enabled: true, LastCommit: strings.Repeat("c", 40),
		ExcludedPaths: []string{}}, Label: "example.com/org/skills", Found: 2, Imported: 1,
		Entries: []domain.SkillSourceEntry{
			{Path: "skills/deploy", Name: "deploy", Selection: domain.EntrySelected, Present: true, HasScripts: true, SkillSlug: "deploy", Modified: true},
			{Path: "skills/novidade", Name: "novidade", Selection: domain.EntryNew, Present: true},
		}}}
	f.inputs = []domain.SkillTestInput{{ID: uitest.ID("in:1"), SkillID: pr.ID, Name: "pedido simples", Content: "Revise o PR 12"}}
	tid := uitest.ID("test:1")
	run := uitest.ID("run:t1")
	f.tests = []domain.SkillTestRun{
		{ID: tid, SkillID: pr.ID, SkillSlug: pr.Slug, SkillRevision: 1, Input: "Revise o PR 12", PersonaName: "Ada", PersonaSlug: "ada", RunID: &run,
			Status: domain.SkillTestDone, Output: "## Saída A\n\n<script>alert(1)</script>ok", CreatedAt: now},
		{ID: uitest.ID("test:2"), SkillID: pr.ID, SkillSlug: pr.Slug, SkillRevision: 2, Input: "Revise o PR 12", PersonaName: "Ada", PersonaSlug: "ada",
			Status: domain.SkillTestRunning, CreatedAt: now.Add(1)},
	}
	e := newEnv(t, sf.Fake)
	e.h = serveWith(t, f)
	return e, f
}

func (f *studioFake) ListSkillVersions(_ context.Context, _, skillID domain.ID) ([]domain.SkillVersion, error) {
	var out []domain.SkillVersion
	for _, v := range f.versions[skillID] {
		v.Body = ""
		out = append(out, v)
	}
	return out, nil
}

func (f *studioFake) GetSkillVersion(_ context.Context, _, skillID domain.ID, rev int) (domain.SkillVersion, error) {
	for _, v := range f.versions[skillID] {
		if v.Revision == rev {
			return v, nil
		}
	}
	return domain.SkillVersion{}, domain.ErrNotFound
}

func (f *studioFake) DiffSkillVersions(ctx context.Context, cid, skillID domain.ID, from, to int) (textdiff.Result, error) {
	a, err := f.GetSkillVersion(ctx, cid, skillID, from)
	if err != nil {
		return textdiff.Result{}, err
	}
	b, err := f.GetSkillVersion(ctx, cid, skillID, to)
	if err != nil {
		return textdiff.Result{}, err
	}
	return textdiff.Lines(a.Body, b.Body), nil
}

func (f *studioFake) RestoreSkillVersion(ctx context.Context, _ domain.Actor, _, skillID domain.ID, rev int) (domain.Skill, error) {
	if err := f.deny(ctx); err != nil {
		return domain.Skill{}, err
	}
	f.restored = append(f.restored, rev)
	return f.skills[0], nil
}

func (f *studioFake) SetPersonaSkillPin(ctx context.Context, _ domain.Actor, _, personaID, skillID domain.ID, rev *int) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	f.pins[[2]domain.ID{personaID, skillID}] = rev
	return nil
}

func (f *studioFake) ListSkillPersonas(ctx context.Context, cid, skillID domain.ID) ([]domain.SkillPersona, error) {
	out, err := f.skillsFake.ListSkillPersonas(ctx, cid, skillID)
	for i := range out {
		out[i].PinnedRevision = f.pins[[2]domain.ID{out[i].PersonaID, skillID}]
	}
	return out, err
}

func (f *studioFake) ListSkills(ctx context.Context, cid domain.ID) ([]service.SkillView, error) {
	return f.skillsFake.ListSkills(ctx, cid)
}

func (f *studioFake) SkillSourcesEnabled() bool { return f.enabled }

func (f *studioFake) DiscoverSkillSource(ctx context.Context, _ domain.ID, in service.SkillSourceInput) (service.SkillSourceDiscovery, error) {
	if err := f.deny(ctx); err != nil {
		return service.SkillSourceDiscovery{}, err
	}
	if !strings.HasPrefix(in.URL, "https://") {
		return service.SkillSourceDiscovery{}, domain.Invalid("url", "use uma URL https")
	}
	return service.SkillSourceDiscovery{URL: in.URL, Ref: "HEAD", Commit: strings.Repeat("d", 40), Skills: []service.DiscoveredSkill{
		{Path: "skills/revisar", Name: "revisar-pr", Slug: "revisar-pr", Conflict: true},
		{Path: "skills/ops/deploy", Name: "deploy-ops", Slug: "deploy-ops", HasScripts: true},
		{Path: "skills/quebrada", Name: "quebrada", Slug: "quebrada", Error: "frontmatter YAML inválido"},
	}}, nil
}

func (f *studioFake) CreateSkillSource(ctx context.Context, _ domain.Actor, _ domain.ID, in service.CreateSkillSourceInput) (service.SkillSourceView, service.SkillSyncResult, error) {
	if err := f.deny(ctx); err != nil {
		return service.SkillSourceView{}, service.SkillSyncResult{}, err
	}
	f.created = append(f.created, in)
	return f.sources[0], service.SkillSyncResult{Imported: 1}, nil
}

func (f *studioFake) ListSkillSources(context.Context, domain.ID) ([]service.SkillSourceView, error) {
	return f.sources, nil
}

func (f *studioFake) GetSkillSource(_ context.Context, _, id domain.ID) (service.SkillSourceView, error) {
	for _, v := range f.sources {
		if v.ID == id {
			return v, nil
		}
	}
	return service.SkillSourceView{}, domain.ErrNotFound
}

func (f *studioFake) UpdateSkillSource(ctx context.Context, _ domain.Actor, _, id domain.ID, in service.UpdateSkillSourceInput) (service.SkillSourceView, error) {
	if err := f.deny(ctx); err != nil {
		return service.SkillSourceView{}, err
	}
	f.updates = append(f.updates, in)
	return f.sources[0], nil
}

func (f *studioFake) DeleteSkillSource(ctx context.Context, _ domain.Actor, _, _ domain.ID, keep bool) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	f.deleted = append(f.deleted, keep)
	return nil
}

func (f *studioFake) SyncSkillSource(ctx context.Context, _ domain.Actor, _, _ domain.ID, force bool) (service.SkillSyncResult, error) {
	if err := f.deny(ctx); err != nil {
		return service.SkillSyncResult{}, err
	}
	f.synced = append(f.synced, force)
	return service.SkillSyncResult{Commit: strings.Repeat("e", 40), Updated: 1}, nil
}

func (f *studioFake) ListSkillTestInputs(_ context.Context, _, skillID domain.ID) ([]domain.SkillTestInput, error) {
	var out []domain.SkillTestInput
	for _, in := range f.inputs {
		if in.SkillID == skillID {
			out = append(out, in)
		}
	}
	return out, nil
}

func (f *studioFake) CreateSkillTestInput(ctx context.Context, _ domain.Actor, _, skillID domain.ID, name, content string) (domain.SkillTestInput, error) {
	if err := f.deny(ctx); err != nil {
		return domain.SkillTestInput{}, err
	}
	in := domain.SkillTestInput{ID: uitest.ID("in:" + name), SkillID: skillID, Name: name, Content: content}
	f.inputs = append(f.inputs, in)
	return in, nil
}

func (f *studioFake) DeleteSkillTestInput(ctx context.Context, _ domain.Actor, _, _, id domain.ID) error {
	if err := f.deny(ctx); err != nil {
		return err
	}
	for i := range f.inputs {
		if f.inputs[i].ID == id {
			f.inputs = append(f.inputs[:i], f.inputs[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *studioFake) ListSkillTestRuns(_ context.Context, _ domain.ID, skillID *domain.ID, _ int) ([]domain.SkillTestRun, error) {
	var out []domain.SkillTestRun
	for i := len(f.tests) - 1; i >= 0; i-- {
		if skillID == nil || f.tests[i].SkillID == *skillID {
			out = append(out, f.tests[i])
		}
	}
	return out, nil
}

func (f *studioFake) GetSkillTestRun(_ context.Context, _, id domain.ID) (domain.SkillTestRun, error) {
	for _, t := range f.tests {
		if t.ID == id {
			return t, nil
		}
	}
	return domain.SkillTestRun{}, domain.ErrNotFound
}

func (f *studioFake) RunSkillTest(ctx context.Context, _ domain.Actor, _, skillID domain.ID, in service.RunSkillTestInput) (domain.SkillTestRun, error) {
	if err := f.deny(ctx); err != nil {
		return domain.SkillTestRun{}, err
	}
	if in.InputID == nil && strings.TrimSpace(in.Input) == "" {
		return domain.SkillTestRun{}, domain.Invalid("input", "escreva a entrada do teste")
	}
	f.ran = append(f.ran, in)
	t := domain.SkillTestRun{ID: uuid.New(), SkillID: skillID, SkillSlug: "revisar-pr", SkillRevision: 2, Input: in.Input, Status: domain.SkillTestQueued}
	f.tests = append(f.tests, t)
	return t, nil
}

func (f *studioFake) RerunSkillTest(ctx context.Context, _ domain.Actor, _, id domain.ID) (domain.SkillTestRun, error) {
	if err := f.deny(ctx); err != nil {
		return domain.SkillTestRun{}, err
	}
	prev, err := f.GetSkillTestRun(ctx, uuid.Nil, id)
	if err != nil {
		return prev, err
	}
	prev.ID, prev.Status = uuid.New(), domain.SkillTestQueued
	f.tests = append(f.tests, prev)
	return prev, nil
}

func (f *studioFake) CancelSkillTest(ctx context.Context, _ domain.Actor, _, id domain.ID) (domain.SkillTestRun, error) {
	if err := f.deny(ctx); err != nil {
		return domain.SkillTestRun{}, err
	}
	for i := range f.tests {
		if f.tests[i].ID == id {
			f.tests[i].Status = domain.SkillTestCancelled
			return f.tests[i], nil
		}
	}
	return domain.SkillTestRun{}, domain.ErrNotFound
}

func TestSkillLibraryTabsAndFilters(t *testing.T) {
	e, _ := newStudioEnv(t, domain.MemberViewer)
	rec := e.get(t, "/c/hanko/skills")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Biblioteca", `href="/c/hanko/skills/fontes"`, `href="/c/hanko/skills/estudio"`, "de fonte", "modificada", "Locais", "De fonte")
	rec = e.get(t, "/c/hanko/skills?origem=fonte")
	wantContains(t, rec, "Deploy")
	wantNotContains(t, rec, "Revisar PR</a>")
	rec = e.get(t, "/c/hanko/skills?origem=local")
	wantContains(t, rec, "Revisar PR")
	wantNotContains(t, rec, ">Deploy</a>")
	// a skill de fonte mostra o aviso e as abas
	rec = e.get(t, "/c/hanko/skills/deploy")
	wantContains(t, rec, "Skill de fonte", "Modificada localmente", `href="/c/hanko/skills/deploy/versoes"`, `href="/c/hanko/skills/deploy/personas"`)
}

func TestSkillSourcesPages(t *testing.T) {
	e, f := newStudioEnv(t, domain.MemberAdmin)
	rec := e.get(t, "/c/hanko/skills/fontes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "example.com/org/skills", "Erro", "repository not found", "ccccccc", "Sincronizar", "Configurar", "Conectar fonte")

	// descoberta: árvore com seleção, conflito, scripts e erro
	rec = e.post(t, "/c/hanko/skills/fontes/descobrir", url.Values{"url": {"https://example.com/org/skills.git"}, "path_prefix": {"skills"}}, false)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Skills encontradas", "skills/ops/deploy", "tem scripts", "frontmatter YAML inválido", "Conectar e importar", `data-depth="2"`)
	rec = e.post(t, "/c/hanko/skills/fontes/descobrir", url.Values{"url": {"http://x"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "use uma URL https")

	rec = e.post(t, "/c/hanko/skills/fontes/nova", url.Values{"url": {"https://example.com/org/skills.git"}, "ref": {"main"},
		"select": {"skills/ops/deploy"}, "auto_update": {"1"}, "excluded": {"a\nb"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.created) != 1 || len(f.created[0].Select) != 1 || f.created[0].Select[0] != "skills/ops/deploy" || !*f.created[0].AutoUpdate ||
		len(f.created[0].Excluded) != 2 || f.created[0].Ref != "main" {
		t.Fatalf("criar: %+v", f.created)
	}

	// detalhe: entradas com seleção, sincronizar (normal e forçado), configurar, seleção e desconectar
	src := "/c/hanko/skills/fontes/f/" + f.sources[0].ID.String()
	rec = e.get(t, src)
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "skills/deploy", "Nova: revisar", "tem scripts — só o SKILL.md foi importado", "Manter cópias", "Forçar")
	wantStatus(t, e.post(t, src+"/sincronizar", url.Values{}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, src+"/sincronizar", url.Values{"force": {"1"}}, false), http.StatusSeeOther)
	if len(f.synced) != 2 || f.synced[0] || !f.synced[1] {
		t.Fatalf("sincronizações: %v", f.synced)
	}
	wantStatus(t, e.post(t, src+"/selecao", url.Values{"sel:skills/novidade": {"selected"}, "sel:skills/deploy": {"excluded"}}, false), http.StatusSeeOther)
	if u := f.updates[len(f.updates)-1]; u.Selection["skills/novidade"] != domain.EntrySelected || u.Selection["skills/deploy"] != domain.EntryExcluded {
		t.Fatalf("seleção: %+v", u)
	}
	wantStatus(t, e.post(t, src+"/configurar", url.Values{"path_prefix": {"skills"}, "auto_update": {"1"}}, false), http.StatusSeeOther)
	if u := f.updates[len(f.updates)-1]; *u.PathPrefix != "skills" || !*u.AutoUpdate || *u.ImportNew || *u.Enabled {
		t.Fatalf("configurar: %+v", u)
	}
	wantStatus(t, e.post(t, src+"/desconectar", url.Values{"keep": {"0"}}, false), http.StatusSeeOther)
	if len(f.deleted) != 1 || f.deleted[0] {
		t.Fatalf("desconectar: %v", f.deleted)
	}

	// interruptor desligado: a aba explica o motivo e o botão some
	f.enabled = false
	rec = e.get(t, "/c/hanko/skills/fontes")
	wantContains(t, rec, "HANKO_SKILL_SOURCES=true", "Fontes desligadas neste servidor")
	wantNotContains(t, rec, `href="/c/hanko/skills/fontes/nova"`)
}

func TestSkillStudioPages(t *testing.T) {
	e, f := newStudioEnv(t, domain.MemberAdmin)
	rec := e.get(t, "/c/hanko/skills/estudio")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Rodar teste", "pedido simples", "Entradas salvas", "revisar-pr v1", "revisar-pr v2", "Comparar os dois marcados",
		`hx-trigger="every 3s"`, `/skills/estudio/t/`+f.tests[1].ID.String()+`/cancelar`)
	// o fragmento da lista (polling)
	rec = e.get(t, "/c/hanko/skills/estudio?skill=revisar-pr&fragmento=execucoes")
	wantContains(t, rec, `id="skill-runs"`)
	wantNotContains(t, rec, "Rodar teste")

	rec = e.post(t, "/c/hanko/skills/estudio/executar?skill=revisar-pr", url.Values{"persona": {"ada"}, "revision": {"1"}, "input": {"Revise"}}, false)
	wantStatus(t, rec, http.StatusSeeOther)
	if len(f.ran) != 1 || *f.ran[0].Revision != 1 || f.ran[0].Input != "Revise" {
		t.Fatalf("rodar: %+v", f.ran)
	}
	rec = e.post(t, "/c/hanko/skills/estudio/executar?skill=revisar-pr", url.Values{"persona": {"ada"}}, false)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	wantContains(t, rec, "escreva a entrada do teste")

	wantStatus(t, e.post(t, "/c/hanko/skills/estudio/entradas?skill=revisar-pr", url.Values{"name": {"outra"}, "content": {"x"}}, false), http.StatusSeeOther)
	wantStatus(t, e.post(t, "/c/hanko/skills/estudio/entradas/"+f.inputs[0].ID.String()+"/remover?skill=revisar-pr", url.Values{}, false), http.StatusSeeOther)
	if len(f.inputs) != 1 || f.inputs[0].Name != "outra" {
		t.Fatalf("entradas: %+v", f.inputs)
	}

	// detalhe: saída em markdown sanitizado; ao vivo enquanto roda
	rec = e.get(t, "/c/hanko/skills/estudio/t/"+f.tests[0].ID.String())
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Saída A", "Reexecutar", "/c/hanko/execucoes/")
	wantNotContains(t, rec, "<script>alert(1)</script>")
	rec = e.get(t, "/c/hanko/skills/estudio/t/"+f.tests[1].ID.String())
	wantContains(t, rec, `hx-trigger="every 3s"`, "A persona ainda está trabalhando")

	wantStatus(t, e.post(t, "/c/hanko/skills/estudio/t/"+f.tests[1].ID.String()+"/cancelar", url.Values{}, false), http.StatusSeeOther)
	if f.tests[1].Status != domain.SkillTestCancelled {
		t.Fatalf("cancelar: %s", f.tests[1].Status)
	}
	wantStatus(t, e.post(t, "/c/hanko/skills/estudio/t/"+f.tests[0].ID.String()+"/reexecutar", url.Values{}, false), http.StatusSeeOther)

	// comparar dois testes lado a lado
	rec = e.get(t, "/c/hanko/skills/estudio/comparar?t="+f.tests[0].ID.String()+"&t="+f.tests[1].ID.String())
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Comparar testes", "Saída A", `class="diff"`)
	wantStatus(t, e.get(t, "/c/hanko/skills/estudio/comparar?t="+f.tests[0].ID.String()), http.StatusUnprocessableEntity)
}

func TestSkillVersionsAndPinsTabs(t *testing.T) {
	e, f := newStudioEnv(t, domain.MemberAdmin)
	rec := e.get(t, "/c/hanko/skills/revisar-pr/versoes")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "v2", "v1", "atual", "Restaurar esta versão", "comparar com a anterior", "bbbbbbbbbbbb")
	rec = e.get(t, "/c/hanko/skills/revisar-pr/versoes?de=1&para=2")
	wantContains(t, rec, `class="diff"`, "Antigo")
	rec = e.get(t, "/c/hanko/skills/revisar-pr/versoes?ver=1")
	wantContains(t, rec, `id="versao"`, "Antigo")
	wantStatus(t, e.post(t, "/c/hanko/skills/revisar-pr/restaurar", url.Values{"revision": {"1"}}, false), http.StatusSeeOther)
	if len(f.restored) != 1 || f.restored[0] != 1 {
		t.Fatalf("restaurar: %v", f.restored)
	}

	rec = e.get(t, "/c/hanko/skills/revisar-pr/personas")
	wantStatus(t, rec, http.StatusOK)
	wantContains(t, rec, "Sempre a mais nova", "/skills/revisar-pr/personas/ada/fixar", "Fixar")
	wantStatus(t, e.post(t, "/c/hanko/skills/revisar-pr/personas/ada/fixar", url.Values{"revision": {"1"}}, false), http.StatusSeeOther)
	var pinned bool
	for _, v := range f.pins {
		pinned = pinned || (v != nil && *v == 1)
	}
	if !pinned {
		t.Fatalf("fixar: %+v", f.pins)
	}
	wantContains(t, e.get(t, "/c/hanko/skills/revisar-pr/personas"), "fixada em", "v1")
}

func TestSkillStudioControlsByRole(t *testing.T) {
	e, f := newStudioEnv(t, domain.MemberViewer)
	rec := e.get(t, "/c/hanko/skills/estudio")
	wantStatus(t, rec, http.StatusOK)
	wantNotContains(t, rec, "Rodar teste", "Salvar entrada")
	rec = e.get(t, "/c/hanko/skills/fontes/f/"+f.sources[0].ID.String())
	wantNotContains(t, rec, "Salvar seleção", "Sincronizar agora", "Manter cópias")
	wantNotContains(t, e.get(t, "/c/hanko/skills/revisar-pr/versoes"), "Restaurar esta versão</button>")
	// o service recusa mesmo se alguém forjar o POST
	wantStatus(t, e.post(t, "/c/hanko/skills/estudio/executar?skill=revisar-pr", url.Values{"persona": {"ada"}, "input": {"x"}}, false), http.StatusForbidden)
	wantStatus(t, e.post(t, "/c/hanko/skills/fontes/f/"+f.sources[0].ID.String()+"/sincronizar", url.Values{}, false), http.StatusForbidden)
}
