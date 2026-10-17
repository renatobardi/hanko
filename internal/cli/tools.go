package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Apps, gateway de tools e perfis de ferramentas (ADR-0036): hanko integration tools, hanko tool-profile e
// hanko tool-gateway.

func (e *env) riskLabel(r string) string {
	switch r {
	case "read":
		return e.t("leitura")
	case "write":
		return e.t("escrita")
	case "destructive":
		return e.t("destrutiva")
	}
	return e.t("desconhecido")
}

func (e *env) toolStatusLabel(s string) string {
	switch s {
	case "active":
		return e.t("ativa")
	case "quarantined":
		return e.t("quarentena")
	case "disabled":
		return e.t("desligada")
	}
	return e.t("removida")
}

func (e *env) verdictLabel(v api.ToolVerdict) string {
	if !v.Allowed {
		if v.Source == "exclude" {
			return e.t("excluída por regra")
		}
		return e.t("negada pelo padrão")
	}
	mode := e.t("permitida")
	if v.Mode == "ask" {
		mode = e.t("perguntar antes")
	}
	switch v.Source {
	case "no_profile":
		return mode + " (" + e.t("sem perfil") + ")"
	case "default":
		return mode + " (" + e.t("padrão") + ")"
	}
	return mode + " (" + e.t("regra") + ")"
}

// integrationToolsCmd é "hanko integration tools <integração>".
func (e *env) integrationToolsCmd() *cobra.Command {
	var refresh, review bool
	var release []string
	var disable, enable string
	c := &cobra.Command{
		Use: "tools " + e.t("<integração>"), Short: e.t("Catálogo de tools de um servidor MCP (risco, estado, quarentena)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cl, ctx := e.client(), cmd.Context()
			if refresh {
				r, err := cl.RefreshIntegrationTools(ctx, co, a[0])
				if err != nil {
					return err
				}
				if !r.OK {
					return fmt.Errorf(e.t("a descoberta falhou: %s"), r.Error)
				}
				if !e.asJSON {
					fmt.Fprintf(e.out, e.t("Catálogo atualizado: %d tools, %d novas, %d alteradas, %d em quarentena, %d removidas.\n"),
						r.Tools, r.New, r.Changed, r.Quarantined, r.Removed)
				}
			}
			if review || len(release) > 0 {
				r, err := cl.ReviewIntegrationTools(ctx, co, a[0], release)
				if err != nil {
					return err
				}
				if !e.asJSON {
					fmt.Fprintf(e.out, e.t("%d tool(s) liberada(s) da quarentena.\n"), r.Released)
				}
			}
			for _, x := range []struct{ tool, status string }{{disable, "disabled"}, {enable, "active"}} {
				if x.tool == "" {
					continue
				}
				if _, err := cl.SetIntegrationToolStatus(ctx, co, a[0], x.tool, x.status); err != nil {
					return err
				}
			}
			cat, err := cl.IntegrationTools(ctx, co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cat)
			}
			if cat.Stdio {
				fmt.Fprintln(e.out, e.t("MCP stdio não passa pelo gateway: não há catálogo; o perfil só inclui ou exclui a integração inteira."))
				return nil
			}
			if len(cat.Tools) == 0 {
				if cat.LastError != "" {
					fmt.Fprintf(e.out, e.t("Última descoberta falhou: %s\n"), cat.LastError)
				}
				fmt.Fprintln(e.out, e.t("Nenhuma tool catalogada. Descubra com: hanko integration tools <integração> --refresh"))
				return nil
			}
			rows := make([][]string, 0, len(cat.Tools))
			for _, t := range cat.Tools {
				rows = append(rows, []string{t.Name, e.riskLabel(t.Risk), e.toolStatusLabel(t.Status), e.fmtTime(t.LastSeenAt), oneLine(t.Description, 50)})
			}
			e.table([]string{e.t("TOOL"), e.t("RISCO"), e.t("ESTADO"), e.t("VISTA EM"), e.t("DESCRIÇÃO")}, rows)
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&refresh, "refresh", false, e.t("redescobre o catálogo antes de mostrar"))
	f.BoolVar(&review, "review", false, e.t("libera todas as tools em quarentena (admin)"))
	f.StringArrayVar(&release, "release", nil, e.t("libera uma tool da quarentena (repita a flag)"))
	f.StringVar(&disable, "disable", "", e.t("desliga uma tool"))
	f.StringVar(&enable, "enable", "", e.t("liga uma tool desligada"))
	return c
}

func (e *env) toolProfileCmd() *cobra.Command {
	c := &cobra.Command{Use: "tool-profile", Aliases: []string{"tp"}, Short: e.t("Perfis de ferramentas: o que cada persona pode chamar nos servidores MCP")}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os perfis"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().ToolProfiles(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum perfil: as personas veem todas as tools das integrações delas. Crie com: hanko tool-profile create <nome>"))
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				rows = append(rows, []string{p.Slug, p.Name, e.defaultLabel(p.DefaultAction), fmt.Sprint(len(p.Rules)), fmt.Sprint(len(p.Bindings)),
					fmt.Sprintf("%d/min", p.RateLimitPerMin)})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("PADRÃO"), e.t("REGRAS"), e.t("VÍNCULOS"), e.t("TETO")}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<perfil>"), Short: e.t("Mostra um perfil com regras e vínculos"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().ToolProfile(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.kv([2]string{e.t("Perfil"), p.Name}, [2]string{e.t("Slug"), p.Slug}, [2]string{e.t("Padrão"), e.defaultLabel(p.DefaultAction)},
				[2]string{e.t("Teto"), fmt.Sprintf("%d/min", p.RateLimitPerMin)}, [2]string{e.t("Gateways"), fmt.Sprint(p.Gateways)})
			if len(p.Rules) > 0 {
				fmt.Fprintln(e.out)
				rows := make([][]string, 0, len(p.Rules))
				for _, r := range p.Rules {
					rows = append(rows, []string{short(r.ID), e.ruleTarget(r), r.Effect, r.Mode})
				}
				e.table([]string{e.t("REGRA"), e.t("ALVO"), e.t("EFEITO"), e.t("MODO")}, rows)
			}
			if len(p.Bindings) > 0 {
				fmt.Fprintln(e.out)
				rows := make([][]string, 0, len(p.Bindings))
				for _, b := range p.Bindings {
					rows = append(rows, []string{short(b.ID), b.Scope, dash(b.TargetName), fmt.Sprint(b.Priority)})
				}
				e.table([]string{e.t("VÍNCULO"), e.t("ESCOPO"), e.t("ALVO"), e.t("PRIORIDADE")}, rows)
			}
			return nil
		},
	}

	var slug, def, desc string
	var rate int
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um perfil (padrão: nega o que nenhuma regra inclui)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().CreateToolProfile(cmd.Context(), co, client.CreateToolProfileReq{Name: a[0], Slug: slug, Description: desc,
				DefaultAction: def, RateLimitPerMin: rate})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Perfil criado: %s (%s)\n"), p.Name, p.Slug)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&slug, "slug", "", e.t("identificador (padrão: derivado do nome)"))
	cf.StringVar(&def, "default", "", e.t("ação padrão: deny (padrão) ou allow"))
	cf.StringVar(&desc, "description", "", e.t("descrição"))
	cf.IntVar(&rate, "rate", 0, e.t("chamadas por minuto por persona e integração (padrão 120)"))

	var rInteg, rTool, rRisk, rDelete string
	var rExclude, rAsk bool
	rule := &cobra.Command{
		Use: "rule " + e.t("<perfil>"), Short: e.t("Acrescenta, troca ou tira uma regra (--integration, --tool, --risk)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if rDelete != "" {
				p, err := e.client().ToolProfile(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				id := ""
				for _, r := range p.Rules {
					if strings.HasPrefix(r.ID, rDelete) {
						id = r.ID
					}
				}
				if id == "" {
					return usageError{fmt.Errorf(e.t("regra não encontrada: %s"), rDelete)}
				}
				if err := e.client().DeleteToolProfileRule(cmd.Context(), co, a[0], id); err != nil {
					return err
				}
				fmt.Fprintf(e.out, e.t("Regra %s removida.\n"), short(id))
				return nil
			}
			req := client.ToolRuleReq{Integration: rInteg, Tool: rTool, Risk: rRisk, Effect: "include", Mode: "allow"}
			switch {
			case rRisk != "":
				req.Target = "risk"
			case rTool != "":
				req.Target = "tool"
			case rInteg != "":
				req.Target = "integration"
			default:
				return usageError{errors.New(e.t("informe --integration, --tool (com --integration) ou --risk"))}
			}
			if rExclude {
				req.Effect = "exclude"
			}
			if rAsk {
				req.Mode = "ask"
			}
			r, err := e.client().AddToolProfileRule(cmd.Context(), co, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("Regra gravada: %s %s (%s).\n"), r.Effect, e.ruleTarget(r), r.Mode)
			return nil
		},
	}
	rf := rule.Flags()
	rf.StringVar(&rInteg, "integration", "", e.t("integração MCP (sozinha: a integração inteira)"))
	rf.StringVar(&rTool, "tool", "", e.t("tool da integração"))
	rf.StringVar(&rRisk, "risk", "", e.t("nível de risco: read, write, destructive ou unknown"))
	rf.BoolVar(&rExclude, "exclude", false, e.t("exclui em vez de incluir (excluir vence)"))
	rf.BoolVar(&rAsk, "ask", false, e.t("pergunta antes de cada chamada nova (abre um gate)"))
	rf.StringVar(&rDelete, "delete", "", e.t("tira a regra com este id (ou início do id)"))

	var bCompany bool
	var bProject, bPersona, bRemove string
	var bPriority int
	bind := &cobra.Command{
		Use: "bind " + e.t("<perfil>"), Short: e.t("Liga o perfil à empresa, a um projeto ou a uma persona"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if bRemove != "" {
				p, err := e.client().ToolProfile(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				for _, b := range p.Bindings {
					if strings.HasPrefix(b.ID, bRemove) {
						if err := e.client().UnbindToolProfile(cmd.Context(), co, a[0], b.ID); err != nil {
							return err
						}
						fmt.Fprintf(e.out, e.t("Vínculo %s removido.\n"), short(b.ID))
						return nil
					}
				}
				return usageError{fmt.Errorf(e.t("vínculo não encontrado: %s"), bRemove)}
			}
			req := client.ToolBindingReq{Project: bProject, Persona: bPersona, Priority: bPriority}
			switch {
			case bCompany && bProject == "" && bPersona == "":
				req.Scope = "company"
			case !bCompany && bProject != "" && bPersona == "":
				req.Scope = "project"
			case !bCompany && bProject == "" && bPersona != "":
				req.Scope = "persona"
			default:
				return usageError{errors.New(e.t("escolha um escopo: --company-wide, --project ou --persona"))}
			}
			b, err := e.client().BindToolProfile(cmd.Context(), co, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(b)
			}
			fmt.Fprintf(e.out, e.t("Perfil %s ligado (%s %s, prioridade %d).\n"), a[0], b.Scope, dash(b.TargetName), b.Priority)
			return nil
		},
	}
	bf := bind.Flags()
	bf.BoolVar(&bCompany, "company-wide", false, e.t("vale para a empresa toda"))
	bf.StringVar(&bProject, "project", "", e.t("vale para as tasks do projeto"))
	bf.StringVar(&bPersona, "persona", "", e.t("vale para a persona"))
	bf.IntVar(&bPriority, "priority", 0, e.t("no mesmo escopo, a maior vence"))
	bf.StringVar(&bRemove, "remove", "", e.t("tira o vínculo com este id (ou início do id)"))

	var ePersona, eProject string
	effective := &cobra.Command{
		Use: "effective", Short: e.t("O que uma persona enxerga e de onde vem cada permissão"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if ePersona == "" {
				return usageError{errors.New(e.t("informe --persona"))}
			}
			v, err := e.client().EffectiveTools(cmd.Context(), co, ePersona, eProject)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			prof := e.t("nenhum (todas as tools das integrações da persona)")
			if v.Profile != "" {
				prof = v.Profile
				if v.Binding != nil {
					prof += " (" + v.Binding.Scope + " " + dash(v.Binding.TargetName) + ")"
				}
			}
			e.kv([2]string{e.t("Perfil"), prof})
			var rows [][]string
			for _, i := range v.Integrations {
				if i.Transport == "stdio" {
					rows = append(rows, []string{i.Slug, "*", "-", "-", e.verdictLabel(i.Verdict)})
					continue
				}
				for _, t := range i.Tools {
					vis := e.t("não")
					if t.Visible {
						vis = e.t("sim")
					}
					rows = append(rows, []string{i.Slug, t.Name, e.riskLabel(t.Risk), vis, e.verdictLabel(t.Verdict)})
				}
			}
			if len(rows) == 0 {
				fmt.Fprintln(e.out, e.t("A persona não tem servidores MCP (ou nenhum catálogo foi descoberto ainda)."))
				return nil
			}
			fmt.Fprintln(e.out)
			e.table([]string{e.t("INTEGRAÇÃO"), e.t("TOOL"), e.t("RISCO"), e.t("VISÍVEL"), e.t("DECISÃO")}, rows)
			return nil
		},
	}
	effective.Flags().StringVar(&ePersona, "persona", "", e.t("persona (obrigatório)"))
	effective.Flags().StringVar(&eProject, "project", "", e.t("projeto, para valer o perfil do projeto"))

	del := &cobra.Command{
		Use: "delete " + e.t("<perfil>"), Aliases: []string{"rm"}, Short: e.t("Apaga um perfil (não pode estar em uso por gateway)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteToolProfile(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Perfil %s apagado.\n"), a[0])
			return nil
		},
	}

	c.AddCommand(ls, show, create, rule, bind, effective, del)
	return c
}

func (e *env) defaultLabel(d string) string {
	if d == "allow" {
		return e.t("permite")
	}
	return e.t("nega")
}

func (e *env) ruleTarget(r api.ToolRule) string {
	switch r.Target {
	case "tool":
		return r.IntegrationSlug + "/" + r.Tool
	case "risk":
		if r.IntegrationSlug != "" {
			return r.IntegrationSlug + " " + e.t("risco") + " " + r.Risk
		}
		return e.t("risco") + " " + r.Risk
	}
	return r.IntegrationSlug + " (" + e.t("inteira") + ")"
}

// resolveGatewayToken aceita o id, o início do id ou o prefixo hkgw_ visível.
func (e *env) resolveGatewayToken(ctx context.Context, company, gateway, ref string) (string, error) {
	g, err := e.client().ToolGateway(ctx, company, gateway)
	if err != nil {
		return "", err
	}
	for _, t := range g.Tokens {
		if t.ID == ref || strings.HasPrefix(t.ID, ref) || t.Prefix == ref {
			return t.ID, nil
		}
	}
	return "", usageError{fmt.Errorf(e.t("token não encontrado: %s"), ref)}
}

func (e *env) toolGatewayCmd() *cobra.Command {
	c := &cobra.Command{Use: "tool-gateway", Aliases: []string{"tg"}, Short: e.t("Gateways nomeados: endpoint MCP para clientes de fora, com perfil e tokens")}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os gateways"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			gs, err := e.client().ToolGateways(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(gs)
			}
			if len(gs) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum gateway. Crie com: hanko tool-gateway create <nome> --profile <perfil>"))
				return nil
			}
			rows := make([][]string, 0, len(gs))
			for _, g := range gs {
				rows = append(rows, []string{g.Slug, g.Profile, fmt.Sprint(g.ActiveTokens), e.fmtTimePtr(g.LastUsedAt), e.onOff(g.Enabled), g.Endpoint})
			}
			e.table([]string{e.t("GATEWAY"), e.t("PERFIL"), e.t("TOKENS"), e.t("ÚLTIMO USO"), e.t("LIGADO"), e.t("ENDPOINT")}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<gateway>"), Short: e.t("Mostra um gateway com os tokens (sem os valores)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().ToolGateway(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			e.kv([2]string{e.t("Gateway"), g.Name}, [2]string{e.t("Slug"), g.Slug}, [2]string{e.t("Perfil"), g.Profile},
				[2]string{e.t("Estado"), e.onOff(g.Enabled)}, [2]string{e.t("Endpoint"), strings.TrimRight(e.url, "/") + g.Endpoint})
			if len(g.Tokens) > 0 {
				fmt.Fprintln(e.out)
				rows := make([][]string, 0, len(g.Tokens))
				for _, t := range g.Tokens {
					state := e.t("ativo")
					if t.RevokedAt != nil {
						state = e.t("revogado")
					}
					rows = append(rows, []string{short(t.ID), t.Prefix + "…", dash(t.Label), strings.Join(t.Actions, ","), e.fmtTime(t.ExpiresAt),
						e.fmtTimePtr(t.LastUsedAt), state})
				}
				e.table([]string{e.t("TOKEN"), e.t("PREFIXO"), e.t("RÓTULO"), e.t("AÇÕES"), e.t("EXPIRA"), e.t("ÚLTIMO USO"), e.t("ESTADO")}, rows)
			}
			return nil
		},
	}

	var gSlug, gProfile string
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um gateway com um perfil"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if gProfile == "" {
				return usageError{errors.New(e.t("informe --profile"))}
			}
			g, err := e.client().CreateToolGateway(cmd.Context(), co, client.CreateToolGatewayReq{Name: a[0], Slug: gSlug, Profile: gProfile})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Gateway criado: %s (%s). Gere um token com: hanko tool-gateway token %s\n"), g.Name, g.Slug, g.Slug)
			return nil
		},
	}
	create.Flags().StringVar(&gSlug, "slug", "", e.t("identificador (padrão: derivado do nome)"))
	create.Flags().StringVar(&gProfile, "profile", "", e.t("perfil de ferramentas do gateway (obrigatório)"))

	var tLabel string
	var tActions []string
	var tDays int
	token := &cobra.Command{
		Use: "token " + e.t("<gateway>"), Short: e.t("Gera um token hkgw_ (mostrado uma vez)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().CreateGatewayToken(cmd.Context(), co, a[0], client.CreateGatewayTokenReq{Label: tLabel, Actions: tActions, ExpiresInDays: tDays})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			g, err := e.client().ToolGateway(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Token do gateway (mostrado só agora; guarde-o): %s\n"), t.Token)
			fmt.Fprintf(e.out, e.t("Expira em: %s · Ações: %s\n"), e.fmtTime(t.ExpiresAt), strings.Join(t.Actions, ", "))
			fmt.Fprintf(e.out, e.t("Conecte o cliente MCP (streamable http) a %s com o header Authorization: Bearer <token>\n"), strings.TrimRight(e.url, "/")+g.Endpoint)
			return nil
		},
	}
	tf := token.Flags()
	tf.StringVar(&tLabel, "label", "", e.t("rótulo para reconhecer o token"))
	tf.StringArrayVar(&tActions, "action", nil, e.t("ação permitida: tools/list ou tools/call (repita; padrão as duas)"))
	tf.IntVar(&tDays, "days", 0, e.t("validade em dias (padrão 90, máximo 366)"))

	revoke := &cobra.Command{
		Use: "revoke " + e.t("<gateway> <token>"), Short: e.t("Revoga um token (id, início do id ou prefixo hkgw_)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id, err := e.resolveGatewayToken(cmd.Context(), co, a[0], a[1])
			if err != nil {
				return err
			}
			t, err := e.client().RevokeGatewayToken(cmd.Context(), co, a[0], id)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			fmt.Fprintf(e.out, e.t("Token %s revogado.\n"), t.Prefix)
			return nil
		},
	}

	del := &cobra.Command{
		Use: "delete " + e.t("<gateway>"), Aliases: []string{"rm"}, Short: e.t("Apaga um gateway e seus tokens"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteToolGateway(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Gateway %s apagado.\n"), a[0])
			return nil
		},
	}

	c.AddCommand(ls, show, create, token, revoke, del)
	return c
}
