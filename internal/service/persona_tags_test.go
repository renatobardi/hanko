package service_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func (e *env) tag(t *testing.T, name string) service.PersonaTagView {
	t.Helper()
	v, err := e.svc.CreatePersonaTag(context.Background(), domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: name})
	if err != nil {
		t.Fatalf("create tag %s: %v", name, err)
	}
	return v
}

func (e *env) tagPersona(t *testing.T, p domain.Persona, tag service.PersonaTagView) {
	t.Helper()
	if err := e.svc.TagPersona(context.Background(), domain.HumanActor, e.co.ID, p.ID, tag.ID); err != nil {
		t.Fatalf("tag %s with %s: %v", p.Name, tag.Name, err)
	}
}

// tagEvents conta os eventos de tag por verbo e devolve o último de cada verbo.
func (e *env) tagEvents(t *testing.T) (map[string]int, map[string]domain.ActivityEvent) {
	t.Helper()
	n, last := map[string]int{}, map[string]domain.ActivityEvent{}
	for _, st := range []string{"persona_tag", "persona"} {
		acts, err := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{SubjectType: st, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range acts {
			if strings.HasPrefix(a.Verb, "persona_tag.") || a.Verb == domain.VerbPersonaTagged || a.Verb == domain.VerbPersonaUntagged {
				if n[a.Verb] == 0 {
					last[a.Verb] = a
				}
				n[a.Verb]++
			}
		}
	}
	return n, last
}

func (e *env) searchPersonas(t *testing.T, q string) int {
	t.Helper()
	res, err := e.svc.Search(context.Background(), e.co.ID, q, service.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range res.Groups {
		if g.Type == service.SearchPersona {
			return len(g.Hits)
		}
	}
	return 0
}

func tagNames(p domain.Persona) []string {
	var out []string
	for _, t := range p.Tags {
		out = append(out, t.Name)
	}
	return out
}

// O cadastro segue os itens 2 e 3 do ADR-0087: nome normalizado e único sem caixa, cor da paleta, e renomear mantém o id.
func TestPersonaTagCadastro(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	tag, err := e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: "  Revisão de código ", Description: "quem revisa"})
	if err != nil {
		t.Fatal(err)
	}
	if tag.Name != "Revisão de código" || tag.Color != domain.TagGray || tag.Archived() || tag.Description != "quem revisa" {
		t.Fatalf("tag criada: %+v", tag)
	}
	_, err = e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: "REVISÃO DE CÓDIGO"})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: "infra", Color: "#ff0000"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: strings.Repeat("x", 41)})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: "x", Description: strings.Repeat("d", 201)})
	wantErr(t, err, domain.ErrInvalid)

	up, err := e.svc.UpdatePersonaTag(ctx, domain.HumanActor, e.co.ID, tag.ID, service.UpdatePersonaTagInput{Name: ptr("Revisão"), Color: ptr(domain.TagBlue)})
	if err != nil || up.ID != tag.ID || up.Name != "Revisão" || up.Color != domain.TagBlue {
		t.Fatalf("editar: %+v %v", up, err)
	}
	if _, err := e.svc.UpdatePersonaTag(ctx, domain.HumanActor, e.co.ID, tag.ID, service.UpdatePersonaTagInput{Name: ptr("Revisão")}); err != nil {
		t.Fatal(err)
	}
	other := e.tag(t, "Infra")
	_, err = e.svc.UpdatePersonaTag(ctx, domain.HumanActor, e.co.ID, other.ID, service.UpdatePersonaTagInput{Name: ptr("revisão")})
	wantErr(t, err, domain.ErrConflict)
	_, err = e.svc.UpdatePersonaTag(ctx, domain.HumanActor, e.co.ID, other.ID, service.UpdatePersonaTagInput{Color: ptr(domain.PersonaTagColor("ouro"))})
	wantErr(t, err, domain.ErrInvalid)

	n, last := e.tagEvents(t)
	if n[domain.VerbPersonaTagCreated] != 2 || n[domain.VerbPersonaTagUpdated] != 1 {
		t.Fatalf("eventos = %v, quer 2 created e 1 updated (editar sem mudança não grava)", n)
	}
	var data struct {
		Name   string   `json:"name"`
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal(last[domain.VerbPersonaTagUpdated].Data, &data); err != nil || data.Name != "Revisão" || !slices.Equal(data.Fields, []string{"name", "color"}) {
		t.Fatalf("dados do updated: %s", last[domain.VerbPersonaTagUpdated].Data)
	}
}

// Papéis (ADR-0087 item 4): viewer só lê, member cria, edita, aplica e tira, admin arquiva e deleta. Persona não escreve.
func TestPersonaTagPapeis(t *testing.T) {
	e := newEnv(t)
	p := e.persona(t, "Ana", nil)
	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	viewer, member, admin := as(users[domain.MemberViewer]), as(users[domain.MemberMember]), as(users[domain.MemberAdmin])
	in := service.PersonaTagInput{Name: "Infra"}

	_, err := e.svc.CreatePersonaTag(viewer, domain.HumanActor, e.co.ID, in)
	wantErr(t, err, domain.ErrForbidden)
	tag, err := e.svc.CreatePersonaTag(member, domain.HumanActor, e.co.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ListPersonaTags(viewer, e.co.ID, false); err != nil {
		t.Fatalf("viewer lista: %v", err)
	}
	wantErr(t, e.svc.TagPersona(viewer, domain.HumanActor, e.co.ID, p.ID, tag.ID), domain.ErrForbidden)
	if err := e.svc.TagPersona(member, domain.HumanActor, e.co.ID, p.ID, tag.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.ArchivePersonaTag(member, domain.HumanActor, e.co.ID, tag.ID)
	wantErr(t, err, domain.ErrForbidden)
	_, err = e.svc.DeletePersonaTag(member, domain.HumanActor, e.co.ID, tag.ID)
	wantErr(t, err, domain.ErrForbidden)
	if _, err := e.svc.ArchivePersonaTag(admin, domain.HumanActor, e.co.ID, tag.ID); err != nil {
		t.Fatal(err)
	}

	bg := context.Background()
	_, err = e.svc.CreatePersonaTag(bg, domain.PersonaActor(p.ID), e.co.ID, service.PersonaTagInput{Name: "Agente"})
	wantErr(t, err, domain.ErrForbidden)
	wantErr(t, e.svc.UntagPersona(bg, domain.PersonaActor(p.ID), e.co.ID, p.ID, tag.ID), domain.ErrForbidden)
}

// Aplicar e tirar (ADR-0087 itens 2, 3, 5 e 10): teto de 20 por persona, tag arquivada não se aplica mas sai, criar e
// aplicar reaproveita o nome sem caixa, e a busca acompanha aplicar, tirar, renomear e deletar.
func TestPersonaTagAplicarEBusca(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana := e.persona(t, "Ana", nil)
	beto := e.persona(t, "Beto", nil)
	infra := e.tag(t, "Infraestrutura")

	e.tagPersona(t, ana, infra)
	e.tagPersona(t, ana, infra) // de novo: não muda nada
	if got := e.searchPersonas(t, "infraestrutura"); got != 1 {
		t.Fatalf("busca depois de aplicar = %d, quer 1", got)
	}

	got, err := e.svc.CreateAndTagPersona(ctx, domain.HumanActor, e.co.ID, beto.ID, service.PersonaTagInput{Name: "INFRAESTRUTURA"})
	if err != nil || got.ID != infra.ID {
		t.Fatalf("criar e aplicar com nome existente: %+v %v", got, err)
	}
	dados, err := e.svc.CreateAndTagPersona(ctx, domain.HumanActor, e.co.ID, beto.ID, service.PersonaTagInput{Name: "Dados", Color: domain.TagGreen})
	if err != nil || dados.Color != domain.TagGreen {
		t.Fatalf("criar e aplicar com nome novo: %+v %v", dados, err)
	}
	b, err := e.svc.GetPersona(ctx, e.co.ID, beto.Slug)
	if err != nil || !slices.Equal(tagNames(b), []string{"Dados", "Infraestrutura"}) {
		t.Fatalf("tags do beto: %v %v", tagNames(b), err)
	}

	if _, err := e.svc.UpdatePersonaTag(ctx, domain.HumanActor, e.co.ID, infra.ID, service.UpdatePersonaTagInput{Name: ptr("Plataforma")}); err != nil {
		t.Fatal(err)
	}
	if e.searchPersonas(t, "infraestrutura") != 0 || e.searchPersonas(t, "plataforma") != 2 {
		t.Fatal("a busca deve seguir o nome novo da tag")
	}
	if err := e.svc.UntagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, infra.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.UntagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, infra.ID); err != nil {
		t.Fatalf("tirar o que não está: %v", err)
	}
	if got := e.searchPersonas(t, "plataforma"); got != 1 {
		t.Fatalf("busca depois de tirar = %d, quer 1", got)
	}

	if _, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, dados.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.svc.TagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, dados.ID), domain.ErrConflict)
	_, err = e.svc.CreateAndTagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, service.PersonaTagInput{Name: "dados"})
	wantErr(t, err, domain.ErrConflict)
	if err := e.svc.UntagPersona(ctx, domain.HumanActor, e.co.ID, beto.ID, dados.ID); err != nil {
		t.Fatalf("tirar tag arquivada: %v", err)
	}

	n, last := e.tagEvents(t)
	if n[domain.VerbPersonaTagged] != 3 || n[domain.VerbPersonaUntagged] != 2 || n[domain.VerbPersonaTagArchived] != 1 {
		t.Fatalf("eventos = %v, quer 3 tagged, 2 untagged e 1 archived", n)
	}
	if ev := last[domain.VerbPersonaUntagged]; ev.SubjectID != beto.ID || ev.SubjectType != "persona" {
		t.Fatalf("untagged: %+v", ev)
	}

	for i := range domain.MaxTagsPerPersona {
		e.tagPersona(t, ana, e.tag(t, "t"+string(rune('a'+i))))
	}
	wantErr(t, e.svc.TagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, infra.ID), domain.ErrInvalid)
	if err := e.svc.TagPersona(ctx, domain.HumanActor, e.co.ID, ana.ID, mustTagByName(t, e, "ta").ID); err != nil {
		t.Fatalf("reaplicar no teto não deve falhar: %v", err)
	}
}

func mustTagByName(t *testing.T, e *env, name string) service.PersonaTagView {
	t.Helper()
	all, err := e.svc.ListPersonaTags(context.Background(), e.co.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range all {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("tag %q não existe", name)
	return service.PersonaTagView{}
}

// Arquivar e deletar (ADR-0087 item 3): a arquivada some da lista padrão e fica nas personas; deletar tira de todas,
// devolve a contagem e grava um evento só, com o nome e a contagem.
func TestPersonaTagArquivarEDeletar(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	ana, beto := e.persona(t, "Ana", nil), e.persona(t, "Beto", nil)
	legado := e.tag(t, "Legado")
	e.tagPersona(t, ana, legado)
	e.tagPersona(t, beto, legado)

	v, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID)
	if err != nil || !v.Archived() || v.PersonaCount != 2 {
		t.Fatalf("arquivar: %+v %v", v, err)
	}
	if _, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID); err != nil {
		t.Fatalf("arquivar de novo: %v", err)
	}
	if l, _ := e.svc.ListPersonaTags(ctx, e.co.ID, false); len(l) != 0 {
		t.Fatalf("lista padrão com arquivada: %+v", l)
	}
	if l, _ := e.svc.ListPersonaTags(ctx, e.co.ID, true); len(l) != 1 || l[0].PersonaCount != 2 {
		t.Fatalf("lista com arquivadas: %+v", l)
	}
	if a, _ := e.svc.GetPersona(ctx, e.co.ID, ana.Slug); len(a.Tags) != 1 || !a.Tags[0].Archived() {
		t.Fatalf("a arquivada continua na persona: %+v", a.Tags)
	}
	if v, err := e.svc.UnarchivePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID); err != nil || v.Archived() {
		t.Fatalf("desarquivar: %+v %v", v, err)
	}

	n, err := e.svc.DeletePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID)
	if err != nil || n != 2 {
		t.Fatalf("deletar: %d %v", n, err)
	}
	_, err = e.svc.GetPersonaTag(ctx, e.co.ID, legado.ID)
	wantErr(t, err, domain.ErrNotFound)
	if e.searchPersonas(t, "legado") != 0 {
		t.Fatal("a busca não deve achar a tag deletada")
	}
	counts, last := e.tagEvents(t)
	if counts[domain.VerbPersonaTagDeleted] != 1 || counts[domain.VerbPersonaUntagged] != 0 || counts[domain.VerbPersonaTagArchived] != 1 || counts[domain.VerbPersonaTagUnarchived] != 1 {
		t.Fatalf("eventos = %v", counts)
	}
	var data struct {
		Name     string `json:"name"`
		Personas int    `json:"personas"`
	}
	if err := json.Unmarshal(last[domain.VerbPersonaTagDeleted].Data, &data); err != nil || data.Name != "Legado" || data.Personas != 2 {
		t.Fatalf("dados do deleted: %s", last[domain.VerbPersonaTagDeleted].Data)
	}
	_, err = e.svc.DeletePersonaTag(ctx, domain.HumanActor, e.co.ID, legado.ID)
	wantErr(t, err, domain.ErrNotFound)
}

// O teto de 200 tags por empresa conta as arquivadas (ADR-0087 item 2).
func TestPersonaTagTetoDaEmpresa(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO persona_tags (id, company_id, name, archived_at)
		SELECT gen_random_uuid(), $1, 'tag ' || g, CASE WHEN g % 2 = 0 THEN now() END FROM generate_series(1, $2) g`,
		e.co.ID, domain.MaxPersonaTagsPerCompany); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.CreatePersonaTag(ctx, domain.HumanActor, e.co.ID, service.PersonaTagInput{Name: "mais uma"})
	wantErr(t, err, domain.ErrInvalid)
}

// O recorte do organograma (ADR-0087 itens 6 e 7): any, all, sem tag, níveis e profundidade máxima, combinados por E.
func TestListOrgPersonas(t *testing.T) {
	ctx := context.Background()
	e, ceo, _, eng, qa := orgFixture(t)
	e.persona(t, "Solo", nil)
	back, test := e.tag(t, "Backend"), e.tag(t, "Testes")
	e.tagPersona(t, eng, back)
	e.tagPersona(t, eng, test)
	e.tagPersona(t, qa, test)
	e.tagPersona(t, ceo, back)

	slugs := func(f service.OrgPersonaFilter) []string {
		t.Helper()
		ps, err := e.svc.ListOrgPersonas(ctx, e.co.ID, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range ps {
			out = append(out, p.Persona.Name+":"+string(rune('0'+p.Level)))
		}
		return out
	}
	cases := []struct {
		name string
		f    service.OrgPersonaFilter
		want []string
	}{
		{"tudo", service.OrgPersonaFilter{}, []string{"Ceo:1", "Cto:2", "Eng:3", "Qa:3", "Solo:1"}},
		{"any", service.OrgPersonaFilter{TagIDs: []domain.ID{back.ID, test.ID}}, []string{"Ceo:1", "Eng:3", "Qa:3"}},
		{"all", service.OrgPersonaFilter{TagIDs: []domain.ID{back.ID, test.ID}, Match: domain.OrgTagAll}, []string{"Eng:3"}},
		{"sem tag", service.OrgPersonaFilter{Untagged: true}, []string{"Cto:2", "Solo:1"}},
		{"sem tag ou testes", service.OrgPersonaFilter{Untagged: true, TagIDs: []domain.ID{test.ID}}, []string{"Cto:2", "Eng:3", "Qa:3", "Solo:1"}},
		{"nível", service.OrgPersonaFilter{Levels: []int{1, 3}}, []string{"Ceo:1", "Eng:3", "Qa:3", "Solo:1"}},
		{"tag e nível", service.OrgPersonaFilter{TagIDs: []domain.ID{back.ID}, Levels: []int{3}}, []string{"Eng:3"}},
		{"profundidade", service.OrgPersonaFilter{MaxDepth: 2}, []string{"Ceo:1", "Cto:2", "Solo:1"}},
		{"vazio", service.OrgPersonaFilter{TagIDs: []domain.ID{test.ID}, MaxDepth: 2}, nil},
	}
	for _, c := range cases {
		if got := slugs(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, quer %v", c.name, got, c.want)
		}
	}

	if _, err := e.svc.ArchivePersonaTag(ctx, domain.HumanActor, e.co.ID, test.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.ListOrgPersonas(ctx, e.co.ID, service.OrgPersonaFilter{TagIDs: []domain.ID{test.ID}})
	wantErr(t, err, domain.ErrInvalid)
	if got := slugs(service.OrgPersonaFilter{TagIDs: []domain.ID{test.ID}, IncludeArchived: true}); len(got) != 2 {
		t.Fatalf("filtro com arquivada e mostrar arquivadas: %v", got)
	}
	_, err = e.svc.ListOrgPersonas(ctx, e.co.ID, service.OrgPersonaFilter{Match: "or"})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.ListOrgPersonas(ctx, e.co.ID, service.OrgPersonaFilter{Levels: []int{0}})
	wantErr(t, err, domain.ErrInvalid)
	_, err = e.svc.ListOrgPersonas(ctx, e.co.ID, service.OrgPersonaFilter{TagIDs: []domain.ID{domain.NewID()}})
	wantErr(t, err, domain.ErrNotFound)

	chart, err := e.svc.OrgChart(ctx, e.co.ID)
	if err != nil || len(chart.Roots[0].Persona.Tags) != 1 {
		t.Fatalf("o organograma traz as tags: %v", err)
	}
}
