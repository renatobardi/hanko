package ui

import (
	"context"
	"fmt"
	"net/http"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// teamMember é uma persona de um time do catálogo; Parent é o slug do superior dentro do time ("" = topo).
type teamMember struct {
	Slug, Name, Title, Icon, Model, Parent, Instructions string
	Review                                               bool
}

// teamTemplate é um time pronto: personas com organograma, glifo, modelo e instruções iniciais.
type teamTemplate struct {
	Key, Name, Desc, Icon string
	Members               []teamMember
}

// teamCatalog é o catálogo de times prontos (tudo editável depois de instalado). Instruções curtas e
// genéricas: o dono ajusta ao contexto da empresa.
var teamCatalog = []teamTemplate{
	{Key: "produto", Name: i18n.N("Produto de software"), Icon: "code",
		Desc: i18n.N("Planeja, implementa, revisa e testa mudanças de código com gate humano antes de concluir."),
		Members: []teamMember{
			{Slug: "cto", Name: "Tess", Title: "CTO", Icon: "cpu", Model: "opus",
				Instructions: "Você é a CTO. Quebre metas em tasks pequenas, delegue para quem reporta a você e só conclua com evidência (diff, teste ou link). Decisões irreversíveis vão para um gate humano."},
			{Slug: "engenharia", Name: "Bruno", Title: "Engenheiro full-stack", Icon: "code", Model: "sonnet", Parent: "cto", Review: true,
				Instructions: "Você é engenheiro full-stack. Implemente a task no workspace, rode os testes e descreva o que mudou. Pergunte quando o pedido for ambíguo."},
			{Slug: "revisao", Name: "Iris", Title: "Revisora de código", Icon: "shield-check", Model: "sonnet", Parent: "cto",
				Instructions: "Você revisa código. Aponte riscos, falta de teste e quebras de contrato; aprove só o que você colocaria em produção."},
			{Slug: "qa", Name: "Caio", Title: "QA", Icon: "search", Model: "haiku", Parent: "cto",
				Instructions: "Você testa entregas: reproduza o cenário, registre o que falhou com passos claros e reabra a task quando necessário."},
		}},
	{Key: "conteudo", Name: i18n.N("Conteúdo e marketing"), Icon: "megaphone",
		Desc: i18n.N("Pauta, escreve, revisa e prepara a distribuição de textos, com aprovação humana antes de publicar."),
		Members: []teamMember{
			{Slug: "editoria", Name: "Lia", Title: "Editora-chefe", Icon: "book-open", Model: "sonnet",
				Instructions: "Você é a editora-chefe. Defina a pauta a partir das metas, distribua as tasks e mantenha a voz da marca. Nada é publicado sem gate."},
			{Slug: "redacao", Name: "Davi", Title: "Redator", Icon: "pen-line", Model: "sonnet", Parent: "editoria", Review: true,
				Instructions: "Você escreve textos claros e diretos a partir do briefing da task. Entregue o rascunho como artefato."},
			{Slug: "revisao-texto", Name: "Nina", Title: "Revisora de texto", Icon: "shield-check", Model: "haiku", Parent: "editoria",
				Instructions: "Você revisa gramática, clareza e consistência. Sugira cortes e aponte afirmações sem fonte."},
			{Slug: "distribuicao", Name: "Rafa", Title: "Distribuição", Icon: "megaphone", Model: "haiku", Parent: "editoria",
				Instructions: "Você adapta o texto aprovado para cada canal e prepara o calendário de publicação."},
		}},
	{Key: "pesquisa", Name: i18n.N("Pesquisa e análise"), Icon: "search",
		Desc: i18n.N("Levanta fontes, cruza dados e entrega relatórios com proveniência rastreável."),
		Members: []teamMember{
			{Slug: "pesquisa", Name: "Hana", Title: "Pesquisadora líder", Icon: "search", Model: "sonnet",
				Instructions: "Você lidera a pesquisa. Defina perguntas, divida o trabalho e consolide o relatório final com fontes citadas."},
			{Slug: "dados", Name: "Otto", Title: "Analista de dados", Icon: "database", Model: "sonnet", Parent: "pesquisa",
				Instructions: "Você analisa dados: limpe, calcule e explique os números com o método usado. Entregue tabelas como artefatos."},
			{Slug: "fontes", Name: "Vera", Title: "Verificadora de fontes", Icon: "scale", Model: "haiku", Parent: "pesquisa",
				Instructions: "Você verifica fontes e afirmações. Marque o que não se sustenta e o que precisa de confirmação humana."},
		}},
	{Key: "operacoes", Name: i18n.N("Operações"), Icon: "briefcase",
		Desc: i18n.N("Coordena o dia a dia: triagem, suporte e documentação, escalando ao humano o que é decisão."),
		Members: []teamMember{
			{Slug: "chief-of-staff", Name: "Ada", Title: "Chief of staff", Icon: "briefcase", Model: "sonnet",
				Instructions: "Você é a chief of staff. Faça a triagem do que chega, delegue para a persona certa e acompanhe até fechar. Abra gate quando a decisão for do humano."},
			{Slug: "suporte", Name: "Leo", Title: "Analista de suporte", Icon: "headphones", Model: "haiku", Parent: "chief-of-staff",
				Instructions: "Você responde dúvidas e problemas com base na documentação; quando não souber, diga e escale."},
			{Slug: "documentacao", Name: "Mika", Title: "Documentação", Icon: "book-open", Model: "haiku", Parent: "chief-of-staff",
				Instructions: "Você mantém a documentação viva: registre decisões, processos e respostas recorrentes como artefatos."},
		}},
}

func findTeam(key string) (teamTemplate, bool) {
	for _, t := range teamCatalog {
		if t.Key == key {
			return t, true
		}
	}
	return teamTemplate{}, false
}

func teamVM(ctx context.Context, base string, t teamTemplate) views.TeamCard {
	c := views.TeamCard{Key: t.Key, Name: i18n.T(ctx, t.Name), Desc: i18n.T(ctx, t.Desc), Icon: t.Icon, Action: base + "/personas/catalogo"}
	for _, m := range t.Members {
		c.Members = append(c.Members, views.TeamMemberVM{Name: m.Name, Title: m.Title, Icon: m.Icon, Model: m.Model, Lead: m.Parent == ""})
	}
	return c
}

func (s *server) handleTeamCatalog(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.TeamCatalogVM{Notice: r.URL.Query().Get("instalado")}
	if t, ok := findTeam(vm.Notice); ok {
		vm.Notice = i18n.Tf(ctx, "Time %s instalado. As personas já aparecem no organograma e acordam quando tiverem tasks.", i18n.T(ctx, t.Name))
	} else {
		vm.Notice = ""
	}
	for _, t := range teamCatalog {
		vm.Teams = append(vm.Teams, teamVM(ctx, base, t))
	}
	vm.Packages = s.packagesVM(ctx, co)
	sh := s.shell(r, co, "personas", i18n.T(ctx, "Catálogo de times"), views.Crumb{Label: i18n.T(ctx, "Personas"), Href: base + "/personas"}, views.Crumb{Label: i18n.T(ctx, "Catálogo de times")})
	s.render(w, r, http.StatusOK, views.TeamCatalogPage(sh, vm))
}

// handleInstallTeam contrata as personas do time em ordem (superior antes de quem reporta a ele). Slug já
// usado na empresa ganha sufixo (-2, -3…). Se uma contratação falhar, as anteriores ficam e a mensagem diz
// quantas entraram.
func (s *server) handleInstallTeam(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	t, ok := findTeam(trimForm(r, "team"))
	if !ok {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	existing, err := s.be.ListPersonas(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	used := map[string]bool{}
	for _, p := range existing {
		used[p.Slug] = true
	}
	free := func(slug string) string {
		if !used[slug] {
			used[slug] = true
			return slug
		}
		for i := 2; ; i++ {
			c := fmt.Sprintf("%s-%d", slug, i)
			if !used[c] {
				used[c] = true
				return c
			}
		}
	}
	ids := map[string]domain.ID{}
	for i, m := range t.Members {
		in := service.HirePersonaInput{Name: m.Name, Slug: free(m.Slug), Title: m.Title, Icon: m.Icon, Instructions: m.Instructions,
			Agent: "claude", Model: m.Model, HeartbeatIntervalSec: 15 * 60, RequireReview: m.Review}
		if m.Parent != "" {
			if id, ok := ids[m.Parent]; ok {
				in.ReportsTo = &id
			}
		}
		p, err := s.be.HirePersona(ctx, actorFrom(ctx), co.ID, in)
		if err != nil {
			if i > 0 {
				err = fmt.Errorf("%w (%s)", err, i18n.Tf(ctx, "%d de %d personas do time entraram antes do erro", i, len(t.Members)))
			}
			s.fail(w, r, &co, err)
			return
		}
		ids[m.Slug] = p.ID
	}
	redirect(w, r, "/c/"+co.Slug+"/personas/catalogo?instalado="+t.Key)
}
