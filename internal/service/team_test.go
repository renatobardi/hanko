package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/teams"
)

func catalogPkg(t *testing.T, slug string) export.TeamPackage {
	t.Helper()
	p, ok, err := teams.Get(slug)
	if err != nil || !ok {
		t.Fatalf("catálogo %s: %v %v", slug, ok, err)
	}
	return p
}

func personaBySlug(t *testing.T, e *env, slug string) domain.Persona {
	t.Helper()
	p, err := e.svc.GetPersona(context.Background(), e.co.ID, slug)
	if err != nil {
		t.Fatalf("persona %s: %v", slug, err)
	}
	return p
}

// Todo pacote do catálogo embutido é válido e instala: personas pausadas, skills ligadas, rotinas desligadas.
func TestInstallCatalogPackages(t *testing.T) {
	ctx := context.Background()
	list, err := teams.List()
	if err != nil || len(list) < 3 {
		t.Fatalf("catálogo: %d %v", len(list), err)
	}
	for _, pkg := range list {
		t.Run(pkg.Package.Slug, func(t *testing.T) {
			e := newEnv(t)
			// prévia: nada é gravado
			prev, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{DryRun: true, Source: service.TeamSourceCatalog})
			if err != nil || !prev.DryRun || len(prev.Result.Items) == 0 {
				t.Fatalf("prévia: %+v %v", prev, err)
			}
			if ps, _ := e.svc.ListPersonas(ctx, e.co.ID); len(ps) != 0 {
				t.Fatalf("a prévia gravou %d personas", len(ps))
			}
			if n := e.count(t, `SELECT count(*) FROM team_installs WHERE company_id = $1`, e.co.ID); n != 0 {
				t.Fatal("a prévia registrou instalação")
			}

			res, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{Source: service.TeamSourceCatalog})
			if err != nil {
				t.Fatal(err)
			}
			if res.InstallID == nil || res.SHA256 == "" {
				t.Fatalf("resultado: %+v", res)
			}
			ps, _ := e.svc.ListPersonas(ctx, e.co.ID)
			if len(ps) != len(pkg.Personas) {
				t.Fatalf("personas = %d, quer %d", len(ps), len(pkg.Personas))
			}
			for _, p := range ps {
				if p.Status != domain.PersonaPaused {
					t.Errorf("%s deveria entrar pausada: %s", p.Slug, p.Status)
				}
			}
			fl, _ := e.svc.ListFlows(ctx, e.co.ID)
			if len(fl) != len(pkg.Flows) {
				t.Fatalf("fluxos = %d", len(fl))
			}
			for _, f := range fl {
				if f.Enabled {
					t.Errorf("a rotina %s deveria entrar desligada", f.Slug)
				}
			}
			sk, _ := e.svc.ListSkills(ctx, e.co.ID)
			if len(sk) != len(pkg.Skills) {
				t.Fatalf("skills = %d", len(sk))
			}
			for _, p := range pkg.Personas {
				got, _ := e.svc.PersonaSkills(ctx, e.co.ID, personaBySlug(t, e, p.Slug).ID)
				if len(got) != len(p.Skills) {
					t.Errorf("%s: skills %d, quer %d", p.Slug, len(got), len(p.Skills))
				}
			}
			// registro e atividade
			if n := e.count(t, `SELECT count(*) FROM team_installs WHERE company_id = $1 AND package_slug = $2 AND source = 'catalog'`, e.co.ID, pkg.Package.Slug); n != 1 {
				t.Fatalf("team_installs = %d", n)
			}
			if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id = $1 AND verb = 'team.installed'`, e.co.ID); n != 1 {
				t.Fatalf("team.installed = %d", n)
			}
			recs, err := e.svc.ListTeamInstalls(ctx, e.co.ID)
			if err != nil || len(recs) != 1 || recs[0].Slug != pkg.Package.Slug {
				t.Fatalf("lista de instalações: %+v %v", recs, err)
			}
		})
	}
}

// Instalar de novo respeita a ação de conflito por item.
func TestInstallTeamConflicts(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	pkg := catalogPkg(t, "mesa-de-conteudo")
	if _, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{}); err != nil {
		t.Fatal(err)
	}
	// o padrão é pular: nada novo
	r, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range r.Result.Items {
		if it.Action != domain.ItemSkip {
			t.Errorf("%s/%s: %s", it.Kind, it.Key, it.Action)
		}
	}
	if ps, _ := e.svc.ListPersonas(ctx, e.co.ID); len(ps) != 3 {
		t.Fatalf("personas = %d", len(ps))
	}
	// renomear: entram "-2"
	if _, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{OnConflict: domain.ConflictRename}); err != nil {
		t.Fatal(err)
	}
	ps, _ := e.svc.ListPersonas(ctx, e.co.ID)
	if len(ps) != 6 {
		t.Fatalf("personas = %d, quer 6", len(ps))
	}
	if _, err := e.svc.GetPersona(ctx, e.co.ID, "editora-2"); err != nil {
		t.Fatalf("editora-2: %v", err)
	}
	// exceção por item que o pacote não tem: erro
	_, err = e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{Resolve: map[string]domain.ConflictAction{"personas/ninguem": domain.ConflictReplace}})
	if !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("exceção desconhecida: %v", err)
	}
}

// Passo de assinaturas: a escolha vale para as personas do harness e a assinatura da empresa nunca é alterada.
func TestInstallTeamSubscriptionStep(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	pkg := catalogPkg(t, "suporte-nivel-1") // harness claude
	// sem assinatura: aviso
	prev, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Agents) != 1 || prev.Agents[0].Agent != "claude" || len(prev.Agents[0].Subscriptions) != 0 {
		t.Fatalf("agentes: %+v", prev.Agents)
	}
	found := false
	for _, w := range prev.Warnings {
		if w.Code == "team_no_subscription" && w.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("aviso de assinatura ausente: %+v", prev.Warnings)
	}
	// assinatura de outro harness não vale
	e.sub(t, func(in *service.CreateSubscriptionInput) { in.Name = "Plano Codex"; in.Agent = "codex" })
	if _, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{Subscriptions: map[string]string{"claude": "Plano Codex"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("assinatura de outro harness: %v", err)
	}
	if _, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{Subscriptions: map[string]string{"codex": "Plano Codex"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("harness que o pacote não usa: %v", err)
	}
	sub := e.sub(t, func(in *service.CreateSubscriptionInput) {
		in.Name = "Plano Claude"
		in.Agent = "claude"
		in.MonthlyUSD = 100
	})
	res, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{Subscriptions: map[string]string{"claude": "Plano Claude"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Agents[0].Chosen != "Plano Claude" {
		t.Fatalf("escolha: %+v", res.Agents)
	}
	for _, slug := range []string{"triadora", "especialista"} {
		p := personaBySlug(t, e, slug)
		if p.SubscriptionID == nil || *p.SubscriptionID != sub.ID {
			t.Errorf("%s deveria usar a assinatura escolhida: %+v", slug, p.SubscriptionID)
		}
	}
	subs, _ := e.svc.ListSubscriptions(ctx, e.co.ID)
	if len(subs) != 2 {
		t.Fatalf("o pacote não pode criar assinatura: %d", len(subs))
	}
}

// Segredos citados e validações do pacote.
func TestInstallTeamValidationAndSecrets(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	base := catalogPkg(t, "squad-de-produto")

	bad := func(mod func(*export.TeamPackage)) error {
		p := base
		p.Personas = append([]export.Persona(nil), base.Personas...)
		p.Skills = append([]export.Skill(nil), base.Skills...)
		p.Flows = append([]export.Flow(nil), base.Flows...)
		mod(&p)
		_, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, p, service.TeamInstallOptions{DryRun: true})
		return err
	}
	for name, mod := range map[string]func(*export.TeamPackage){
		"formato":       func(p *export.TeamPackage) { p.Format = "hanko.company.v1" },
		"sem persona":   func(p *export.TeamPackage) { p.Personas = nil },
		"agente":        func(p *export.TeamPackage) { p.Personas[0].Agent = "gpt" },
		"workspace":     func(p *export.TeamPackage) { p.Personas[0].Workspace = "w" },
		"assinatura":    func(p *export.TeamPackage) { p.Personas[0].Subscription = "x" },
		"reports_to":    func(p *export.TeamPackage) { p.Personas[1].ReportsTo = "fantasma" },
		"skill ausente": func(p *export.TeamPackage) { p.Personas[0].Skills = []string{"nao-existe"} },
		"fluxo órfão":   func(p *export.TeamPackage) { p.Flows[0].Persona = "fantasma" },
		"cron":          func(p *export.TeamPackage) { p.Flows[0].Cron = "isto não é cron" },
		"slug":          func(p *export.TeamPackage) { p.Package.Slug = "Ruim Slug" },
	} {
		if err := bad(mod); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: esperava erro de validação, veio %v", name, err)
		}
	}

	// segredo citado na configuração: aviso até existir
	p := base
	p.Personas = append([]export.Persona(nil), base.Personas...)
	p.Personas[0].AdapterConfig = map[string]any{"env": map[string]any{"GH_TOKEN": "${secret:gh-token}"}}
	prev, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, p, service.TeamInstallOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Secrets) != 1 || prev.Secrets[0].Name != "gh-token" || prev.Secrets[0].Exists {
		t.Fatalf("segredos: %+v", prev.Secrets)
	}
	// o que o arquivo diz sobre o que exige não vale: o instalador recalcula
	p.Requires = export.TeamRequires{Secrets: []string{"inventado"}}
	prev, _ = e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, p, service.TeamInstallOptions{DryRun: true})
	if len(prev.Secrets) != 1 || prev.Secrets[0].Name != "gh-token" {
		t.Fatalf("o instalador deveria recalcular os requisitos: %+v", prev.Secrets)
	}
}

func TestInstallTeamRoles(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	pkg := catalogPkg(t, "mesa-de-conteudo")
	u := mkUser(t, e, "m@example.com", domain.RoleMember)
	join(t, e, u, domain.MemberMember)
	if _, err := e.svc.InstallTeam(as(u), domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro instalando: %v", err)
	}
	p := e.persona(t, "Ana", nil)
	if _, err := e.svc.InstallTeam(ctx, domain.Actor{Type: domain.ActorPersona, ID: &p.ID}, e.co.ID, pkg, service.TeamInstallOptions{}); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("persona instalando: %v", err)
	}
	if _, err := e.svc.ExportTeam(as(u), e.co.ID, service.TeamExportOptions{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("membro exportando: %v", err)
	}
	// instalar em empresa arquivada é recusado
	if _, err := e.svc.InstallTeam(ctx, domain.HumanActor, e.co.ID, pkg, service.TeamInstallOptions{}); err != nil {
		t.Fatal(err)
	}
}

// Exportar um time e instalar em outra empresa preserva personas, hierarquia, skills e rotinas.
func TestExportTeamRoundTrip(t *testing.T) {
	ctx := context.Background()
	src := newEnv(t)
	sk, err := src.svc.CreateSkill(ctx, domain.HumanActor, src.co.ID, service.CreateSkillInput{Name: "Triagem", Description: "d", Body: "passos"})
	if err != nil {
		t.Fatal(err)
	}
	lead := src.persona(t, "Lead", func(in *service.HirePersonaInput) { in.Instructions = "coordene" })
	dev := src.persona(t, "Dev", func(in *service.HirePersonaInput) { in.ReportsTo = &lead.ID })
	out := src.persona(t, "Fora", nil) // não vai no pacote
	if err := src.svc.SetPersonaSkill(ctx, domain.HumanActor, src.co.ID, dev.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := src.svc.SetPersonaSkill(ctx, domain.HumanActor, src.co.ID, out.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}
	disabled := false
	if _, err := src.svc.CreateFlow(ctx, domain.HumanActor, src.co.ID, service.CreateFlowInput{Name: "Rotina do dev", Cron: "0 9 * * 1", PersonaID: dev.ID, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.svc.CreateFlow(ctx, domain.HumanActor, src.co.ID, service.CreateFlowInput{Name: "Rotina de fora", Cron: "0 9 * * 2", PersonaID: out.ID, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	pkg, err := src.svc.ExportTeam(ctx, src.co.ID, service.TeamExportOptions{Personas: []string{"lead", "dev"}, Name: "Meu time"})
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Format != export.TeamFormat || pkg.Package.Slug != "meu-time" || len(pkg.Personas) != 2 || len(pkg.Skills) != 1 || len(pkg.Flows) != 1 {
		t.Fatalf("pacote: %+v", pkg)
	}
	if pkg.Flows[0].Persona != "dev" || pkg.Flows[0].Enabled {
		t.Fatalf("rotina: %+v", pkg.Flows[0])
	}
	if len(pkg.Requires.Agents) != 1 {
		t.Fatalf("requisitos: %+v", pkg.Requires)
	}
	// subset: sem reports_to para quem ficou de fora
	only, err := src.svc.ExportTeam(ctx, src.co.ID, service.TeamExportOptions{Personas: []string{"dev"}})
	if err != nil || only.Personas[0].ReportsTo != "" {
		t.Fatalf("subset: %+v %v", only.Personas, err)
	}
	if _, err := src.svc.ExportTeam(ctx, src.co.ID, service.TeamExportOptions{Personas: []string{"ninguem"}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("persona desconhecida: %v", err)
	}
	// o arquivo sobrevive a ida e volta por JSON (inclusive a recusa de campo desconhecido)
	raw, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	back, err := teams.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := teams.Parse([]byte(strings.Replace(string(raw), `"personas"`, `"personass"`, 1))); err == nil {
		t.Fatal("campo desconhecido deveria ser recusado")
	}

	dst := newEnv(t)
	if _, err := dst.svc.InstallTeam(ctx, domain.HumanActor, dst.co.ID, back, service.TeamInstallOptions{}); err != nil {
		t.Fatal(err)
	}
	d := personaBySlug(t, dst, "dev")
	l := personaBySlug(t, dst, "lead")
	if d.ReportsTo == nil || *d.ReportsTo != l.ID || d.Status != domain.PersonaPaused {
		t.Fatalf("hierarquia: %+v", d)
	}
	if got, _ := dst.svc.PersonaSkills(ctx, dst.co.ID, d.ID); len(got) != 1 || got[0].Slug != "triagem" {
		t.Fatalf("skills do dev: %+v", got)
	}
	if _, err := dst.svc.GetPersona(ctx, dst.co.ID, "fora"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a persona de fora não deveria ter ido: %v", err)
	}
	fl, _ := dst.svc.ListFlows(ctx, dst.co.ID)
	if len(fl) != 1 || fl[0].Enabled {
		t.Fatalf("rotinas: %+v", fl)
	}
}
