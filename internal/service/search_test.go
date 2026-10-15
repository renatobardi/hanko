package service_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/search"
	"github.com/renatobardi/hanko/internal/service"
)

// searchSeed monta uma empresa com dados variados e acentuados.
type searchSeed struct {
	e                       *env
	pay, deploy, onboarding service.TaskView
	ana                     domain.Persona
	flow                    service.FlowView
	run                     domain.ID
}

func newSearchSeed(t *testing.T) *searchSeed {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	s := &searchSeed{e: e}
	s.ana = e.persona(t, "Ana Conceição", func(in *service.HirePersonaInput) {
		in.Title = "Engenheira de Plataforma"
		in.Instructions = "Cuide da observabilidade e dos alertas de produção. Nunca faça deploy às sextas."
	})
	e.persona(t, "Beto", func(in *service.HirePersonaInput) { in.Title = "Financeiro" })
	s.pay = e.task(t, service.CreateTaskInput{Title: "Configuração do pagamento por PIX", Description: "Integrar o gateway e testar o estorno."})
	s.deploy = e.task(t, service.CreateTaskInput{Title: "Rotina semanal", Description: "Fazer o deploy da versão nova com rollback automático."})
	s.onboarding = e.task(t, service.CreateTaskInput{Title: "Onboarding de clientes", Description: "Fluxo de boas-vindas"})
	if _, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, s.pay.ID, service.AddCommentInput{Body: "O estorno falhou na homologação, conferir certificado."}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddComment(ctx, domain.Actor{Type: domain.ActorPersona, ID: &s.ana.ID}, e.co.ID, s.deploy.ID, service.AddCommentInput{Body: "Rollback executado, pipeline verde."}); err != nil {
		t.Fatal(err)
	}
	fl, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Relatório diário de vendas", Description: "Consolida as vendas do dia anterior",
		Cron: "0 9 * * 1-5", PersonaID: s.ana.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.flow = fl
	s.run = domain.NewID()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO runs (id, company_id, task_id, persona_id, status, trigger, agent, model, name, summary, created_at, finished_at)
		VALUES ($1,$2,$3,$4,'done','manual','fake','m1','Corajoso_Falcão','Publicou a versão 2 sem incidentes de produção',$5,$5)`,
		s.run, e.co.ID, s.deploy.ID, s.ana.ID, e.svc.Now()); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *searchSeed) search(t *testing.T, q string, types ...service.SearchType) service.SearchResult {
	t.Helper()
	r, err := s.e.svc.Search(context.Background(), s.e.co.ID, q, service.SearchFilter{Types: types})
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return r
}

func group(r service.SearchResult, typ service.SearchType) []service.SearchHit {
	for _, g := range r.Groups {
		if g.Type == typ {
			return g.Hits
		}
	}
	return nil
}

func refs(hits []service.SearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Ref
	}
	return out
}

func TestSearchAcentosCaixaEPrefixo(t *testing.T) {
	s := newSearchSeed(t)
	for _, q := range []string{"configuracao", "CONFIGURAÇÃO", "configur", "Pagamento PIX", "pagam"} {
		hits := group(s.search(t, q), service.SearchTask)
		if len(hits) != 1 || hits[0].Ref != s.pay.Key {
			t.Errorf("%q: tasks = %v, quer só %s", q, refs(hits), s.pay.Key)
		}
	}
	// o termo digitado sem acento acha a persona acentuada e vice-versa
	if hits := group(s.search(t, "conceicao"), service.SearchPersona); len(hits) != 1 || hits[0].Ref != s.ana.Slug {
		t.Errorf("persona por nome sem acento: %v", refs(hits))
	}
	if hits := group(s.search(t, "Engenheira plataforma"), service.SearchPersona); len(hits) != 1 {
		t.Errorf("persona por cargo: %v", refs(hits))
	}
	if hits := group(s.search(t, "sextas"), service.SearchPersona); len(hits) != 1 {
		t.Errorf("persona por instruções: %v", refs(hits))
	}
	// o prefixo é só do último termo: "pagamen" (prefixo de nada) com "configuracao" completo não casa
	if hits := group(s.search(t, "configurac pagamento"), service.SearchTask); len(hits) != 0 {
		t.Errorf("prefixo só vale no último termo, mas casou: %v", refs(hits))
	}
}

func TestSearchTodosOsTipos(t *testing.T) {
	s := newSearchSeed(t)
	r := s.search(t, "rollback")
	if h := group(r, service.SearchTask); len(h) != 1 || h[0].Ref != s.deploy.Key {
		t.Errorf("task por descrição: %v", refs(h))
	}
	c := group(r, service.SearchComment)
	if len(c) != 1 || c[0].TaskKey != s.deploy.Key || c[0].Author != s.ana.Name {
		t.Errorf("comentário: %+v", c)
	}
	if h := group(s.search(t, "estorno"), service.SearchComment); len(h) != 1 || h[0].Author != "human" {
		t.Errorf("comentário humano: %+v", h)
	}
	if h := group(s.search(t, "corajoso falcao"), service.SearchRun); len(h) != 1 || h[0].Ref != s.run.String() || h[0].Persona != s.ana.Name {
		t.Errorf("run por nome amigável: %+v", h)
	}
	if h := group(s.search(t, "incidentes"), service.SearchRun); len(h) != 1 {
		t.Errorf("run por resumo: %+v", h)
	}
	if h := group(s.search(t, "vendas"), service.SearchFlow); len(h) != 1 || h[0].Ref != s.flow.Slug {
		t.Errorf("fluxo: %+v", h)
	}
	// "producao" está na persona (instruções) e na run (resumo)
	r = s.search(t, "producao")
	if len(group(r, service.SearchPersona)) != 1 || len(group(r, service.SearchRun)) != 1 {
		t.Errorf("grupos: %+v", r.Groups)
	}
	// filtro de tipos
	r = s.search(t, "producao", service.SearchRun)
	if len(r.Groups) != 1 || r.Groups[0].Type != service.SearchRun || r.Total != 1 {
		t.Errorf("filtro de tipos: %+v", r)
	}
}

func TestSearchOperadores(t *testing.T) {
	s := newSearchSeed(t)
	if h := group(s.search(t, `"boas vindas"`), service.SearchTask); len(h) != 1 {
		t.Errorf("frase 'boas vindas' deve casar 'boas-vindas' (pontuação vira espaço): %v", refs(h))
	}
	if h := group(s.search(t, `"boas-vindas"`), service.SearchTask); len(h) != 1 || h[0].Ref != s.onboarding.Key {
		t.Errorf("frase: %v", refs(h))
	}
	if h := group(s.search(t, `"deploy versão"`), service.SearchTask); len(h) != 0 {
		t.Errorf("frase fora de ordem/adjacência não casa: %v", refs(h))
	}
	if h := group(s.search(t, "gateway OR rollback", service.SearchTask), service.SearchTask); len(h) != 2 {
		t.Errorf("OR: %v", refs(h))
	}
	if h := group(s.search(t, "deploy -rollback", service.SearchTask), service.SearchTask); len(h) != 0 {
		t.Errorf("exclusão: %v", refs(h))
	}
	if h := group(s.search(t, "deploy -gateway", service.SearchTask), service.SearchTask); len(h) != 1 {
		t.Errorf("exclusão que não exclui: %v", refs(h))
	}
	// pontuação e sintaxe de tsquery digitadas pelo usuário não quebram nada
	for _, q := range []string{"a & b | !c", "'; DROP TABLE tasks; --", `"`, "(deploy", "deploy:*", "<->"} {
		if _, err := s.e.svc.Search(context.Background(), s.e.co.ID, q, service.SearchFilter{}); err != nil && !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%q: %v", q, err)
		}
	}
}

func TestSearchChaveExata(t *testing.T) {
	s := newSearchSeed(t)
	r := s.search(t, strings.ToLower(s.pay.Key))
	h := group(r, service.SearchTask)
	if len(h) == 0 || h[0].Ref != s.pay.Key || !h[0].Exact {
		t.Fatalf("a chave exata deve vir primeiro: %+v", h)
	}
	// chave de outro prefixo ou inexistente: nada de exato
	for _, q := range []string{"ZZZ-1", "HNK-9999"} {
		if h := group(s.search(t, q), service.SearchTask); len(h) != 0 {
			t.Errorf("%s: %v", q, refs(h))
		}
	}
	// a task citada por chave no texto de um comentário continua achável por texto
	if _, err := s.e.svc.AddComment(context.Background(), domain.HumanActor, s.e.co.ID, s.deploy.ID, service.AddCommentInput{Body: "Depende de " + s.pay.Key + " para liberar."}); err != nil {
		t.Fatal(err)
	}
	r = s.search(t, s.pay.Key)
	if c := group(r, service.SearchComment); len(c) != 1 {
		t.Errorf("menção da chave no comentário: %+v", c)
	}
}

func TestSearchRanking(t *testing.T) {
	ctx := context.Background()
	s := newSearchSeed(t)
	e := s.e
	titulo := e.task(t, service.CreateTaskInput{Title: "Migrar kubernetes", Description: "sem detalhes"})
	corpo := e.task(t, service.CreateTaskInput{Title: "Revisar infraestrutura", Description: "Precisamos migrar kubernetes até o fim do mês"})
	// termo no título pesa mais que no corpo
	if h := group(s.search(t, "kubernetes"), service.SearchTask); len(h) != 2 || h[0].Ref != titulo.Key || h[1].Ref != corpo.Key {
		t.Fatalf("título antes do corpo: %v", refs(h))
	}
	// com o mesmo peso, o mais recente primeiro
	velho := e.task(t, service.CreateTaskInput{Title: "Auditoria antiga de segurança"})
	novo := e.task(t, service.CreateTaskInput{Title: "Auditoria nova de segurança"})
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE tasks SET updated_at = $2 WHERE id = $1`, velho.ID, e.svc.Now().Add(-400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if h := group(s.search(t, "auditoria seguranca"), service.SearchTask); len(h) != 2 || h[0].Ref != novo.Key {
		t.Fatalf("recência desempata: %v", refs(h))
	}
	// limite por tipo + More
	for i := 0; i < 5; i++ {
		e.task(t, service.CreateTaskInput{Title: fmt.Sprintf("Relatório mensal %d", i)})
	}
	r, err := e.svc.Search(ctx, e.co.ID, "relatorio", service.SearchFilter{Types: []service.SearchType{service.SearchTask}, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Groups) != 1 || len(r.Groups[0].Hits) != 3 || !r.Groups[0].More {
		t.Fatalf("limite/More: %+v", r.Groups)
	}
}

func TestSearchTrechoComMarcas(t *testing.T) {
	s := newSearchSeed(t)
	h := group(s.search(t, "estorno"), service.SearchTask)
	if len(h) != 1 {
		t.Fatalf("hits: %+v", h)
	}
	var marked []string
	for _, sg := range h[0].Snippet {
		if sg.Match {
			marked = append(marked, sg.Text)
		}
	}
	if len(marked) != 1 || marked[0] != "estorno" {
		t.Errorf("marcas = %q", marked)
	}
	if h[0].Title[0].Match {
		t.Errorf("o título não casou e não deve ter marca: %+v", h[0].Title)
	}
	// o título casado é marcado com o texto original (acento preservado)
	h = group(s.search(t, "configuracao"), service.SearchTask)
	if len(h[0].Title) < 1 || !h[0].Title[0].Match || h[0].Title[0].Text != "Configuração" {
		t.Errorf("título: %+v", h[0].Title)
	}
	// HTML do usuário volta como texto (quem renderiza escapa)
	if _, err := s.e.svc.AddComment(context.Background(), domain.HumanActor, s.e.co.ID, s.pay.ID, service.AddCommentInput{Body: `<img src=x onerror=alert(1)> zebrafish`}); err != nil {
		t.Fatal(err)
	}
	c := group(s.search(t, "zebrafish"), service.SearchComment)
	if len(c) != 1 || !strings.Contains(search.Plain(c[0].Snippet), "<img") {
		t.Errorf("snippet: %+v", c)
	}
}

func TestSearchMultiEmpresa(t *testing.T) {
	ctx := context.Background()
	s := newSearchSeed(t)
	other, err := s.e.svc.CreateCompany(ctx, domain.HumanActor, service.CreateCompanyInput{Name: "Outra SA", TaskPrefix: "OUT"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.e.svc.HirePersona(ctx, domain.HumanActor, other.ID, service.HirePersonaInput{Name: "Zeca", Title: "Pagamento", Agent: "fake", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	ot, err := s.e.svc.CreateTask(ctx, domain.HumanActor, other.ID, service.CreateTaskInput{Title: "Configuração do pagamento (outra empresa)", Description: "rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.e.svc.AddComment(ctx, domain.HumanActor, other.ID, ot.ID, service.AddCommentInput{Body: "estorno também aqui"}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"pagamento", "rollback", "estorno"} {
		mine, err := s.e.svc.Search(ctx, s.e.co.ID, q, service.SearchFilter{})
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := s.e.svc.Search(ctx, other.ID, q, service.SearchFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range mine.Groups {
			for _, h := range g.Hits {
				if h.ID == ot.ID || h.ID == op.ID || strings.HasPrefix(h.Ref, "OUT-") {
					t.Errorf("%q vazou da outra empresa para a primeira: %+v", q, h)
				}
			}
		}
		for _, g := range theirs.Groups {
			for _, h := range g.Hits {
				if h.ID == s.pay.ID || h.ID == s.deploy.ID || h.ID == s.ana.ID || strings.HasPrefix(h.Ref, "HNK-") {
					t.Errorf("%q vazou da primeira empresa para a outra: %+v", q, h)
				}
			}
		}
	}
	// a chave HNK-1 da primeira empresa não resolve na outra (prefixo diferente)
	if h := group(mustSearch(t, s, other.ID, s.pay.Key), service.SearchTask); len(h) != 0 {
		t.Errorf("chave de outra empresa: %v", refs(h))
	}
}

func mustSearch(t *testing.T, s *searchSeed, co domain.ID, q string) service.SearchResult {
	t.Helper()
	r, err := s.e.svc.Search(context.Background(), co, q, service.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSearchPapeis(t *testing.T) {
	s := newSearchSeed(t)
	e := s.e
	viewer := mkUser(t, e, "viewer@x.io", domain.RoleMember)
	join(t, e, viewer, domain.MemberViewer)
	stranger := mkUser(t, e, "stranger@x.io", domain.RoleMember)
	owner := mkUser(t, e, "dono@x.io", domain.RoleOwner)

	r, err := e.svc.Search(as(viewer), e.co.ID, "pagamento", service.SearchFilter{})
	if err != nil || len(group(r, service.SearchTask)) != 1 {
		t.Fatalf("viewer lê a busca: %+v %v", r, err)
	}
	if _, err := e.svc.Search(as(stranger), e.co.ID, "pagamento", service.SearchFilter{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("não-membro deve ver 404, veio %v", err)
	}
	if r, err := e.svc.Search(as(owner), e.co.ID, "pagamento", service.SearchFilter{}); err != nil || len(group(r, service.SearchTask)) != 1 {
		t.Errorf("dono da instalação busca em todas as empresas: %v", err)
	}
}

func TestSearchEntradasInvalidas(t *testing.T) {
	s := newSearchSeed(t)
	ctx := context.Background()
	for _, q := range []string{"", "   ", "-x", "!!!"} {
		if _, err := s.e.svc.Search(ctx, s.e.co.ID, q, service.SearchFilter{}); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%q: %v", q, err)
		}
	}
	if _, err := s.e.svc.Search(ctx, s.e.co.ID, "x", service.SearchFilter{Types: []service.SearchType{"bogus"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("tipo inválido: %v", err)
	}
}

// Um texto enorme com milhares de palavras distintas não derruba o INSERT (teto do tsvector) e continua achável.
func TestSearchTextoGigante(t *testing.T) {
	s := newSearchSeed(t)
	var b strings.Builder
	for i := 0; b.Len() < 99000; i++ {
		fmt.Fprintf(&b, "tok%dx ", i)
	}
	b.WriteString("agulha")
	if _, err := s.e.svc.AddComment(context.Background(), domain.HumanActor, s.e.co.ID, s.pay.ID, service.AddCommentInput{Body: b.String()}); err != nil {
		t.Fatalf("comentário gigante: %v", err)
	}
	// o teto de 100000 caracteres do tsvector corta o fim; a cauda dentro do limite continua achável
	if h := group(s.search(t, "tok5x"), service.SearchComment); len(h) != 1 {
		t.Errorf("início do texto gigante: %v", refs(h))
	}
}

// A dobra em Go (usada na consulta e no destaque) tem de ser idêntica à hanko_fold do banco (usada nos documentos).
func TestFoldIgualAoSQL(t *testing.T) {
	e := newEnv(t)
	samples := []string{
		"Ação ÇÃO ÀÉÎÕÜ áàâãäå éèêë íìîï óòôõö úùûü çñýÿ ÝÑÇ",
		"src/main.go a@b.com HNK-12 foo_bar (x) [y] {z} <w> \"q\" 'r' !? *+=&%$|^~`#.,;:\\-",
		"straße œuvre İstanbul ΑΘΗΝΑ 日本語 emoji 😀 tab\tnewline\n",
	}
	for _, in := range samples {
		var sqlOut string
		if err := e.svc.Pool.QueryRow(context.Background(), `SELECT hanko_fold($1)`, in).Scan(&sqlOut); err != nil {
			t.Fatal(err)
		}
		// no SQL o resultado é minúsculo pelo lower() do banco; compara só o que as duas dobras prometem (Latin + ASCII)
		if got := search.Fold(in); !strings.EqualFold(sqlOut, got) && onlyKnown(in) {
			t.Errorf("dobra diverge para %q:\n go  = %q\n sql = %q", in, got, sqlOut)
		}
	}
}

// onlyKnown: as amostras com alfabetos fora da tabela (grego, CJK, İ) dependem do locale do banco e não são comparadas.
func onlyKnown(s string) bool {
	for _, r := range s {
		if r > 0x24F { // além do Latin Extended-B
			return false
		}
		if r == 'ß' || r == 'œ' || r == 'İ' {
			return false
		}
	}
	return true
}

type memStub struct {
	memory.Noop
	hits []memory.Hit
	err  error
	got  []memory.Scope
}

func (m *memStub) Name() string { return "stub" }
func (m *memStub) Search(_ context.Context, s memory.Scope, _ string, _ int) ([]memory.Hit, error) {
	m.got = append(m.got, s)
	return m.hits, m.err
}

func TestSearchMemoria(t *testing.T) {
	s := newSearchSeed(t)
	ctx := context.Background()
	// sem provider (ou Noop): nenhum grupo de memória
	if r := s.search(t, "deploy"); len(group(r, service.SearchMemory)) != 0 {
		t.Errorf("sem provider: %+v", r.Groups)
	}
	s.e.svc.Memory = memory.Noop{}
	if r := s.search(t, "deploy"); len(group(r, service.SearchMemory)) != 0 {
		t.Errorf("Noop: %+v", r.Groups)
	}
	m := &memStub{hits: []memory.Hit{{Path: "hanko/runs/x.md", Title: "Deploy de maio", Snippet: "O deploy falhou no passo 3"}}}
	s.e.svc.Memory = m
	r := s.search(t, "deploy")
	h := group(r, service.SearchMemory)
	if len(h) != 1 || h[0].Ref != "hanko/runs/x.md" || !anyMarked(h[0].Snippet) || !anyMarked(h[0].Title) {
		t.Fatalf("memória: %+v", h)
	}
	if len(m.got) != 1 || m.got[0].Workspace != s.e.co.Slug {
		t.Errorf("escopo da memória deve ser a empresa: %+v", m.got)
	}
	// só tipos que não incluem memória: o provider nem é chamado
	m.got = nil
	s.search(t, "deploy", service.SearchTask)
	if len(m.got) != 0 {
		t.Error("memória consultada sem ser pedida")
	}
	// memória fora do ar: o grupo fica indisponível e o resto da busca vale
	m.err = errors.New("fora do ar")
	r, err := s.e.svc.Search(ctx, s.e.co.ID, "deploy", service.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var unavailable bool
	for _, g := range r.Groups {
		if g.Type == service.SearchMemory {
			unavailable = g.Unavailable && len(g.Hits) == 0
		}
	}
	if !unavailable || len(group(r, service.SearchTask)) == 0 {
		t.Errorf("degradação: %+v", r.Groups)
	}
}

func anyMarked(segs []search.Segment) bool {
	for _, s := range segs {
		if s.Match {
			return true
		}
	}
	return false
}
