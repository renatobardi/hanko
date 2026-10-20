package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Política de tools (ADR-0053): hanko tool-policy, o teste "como a persona X" em hanko integration tools --test e o
// hook de permissão do claude (hanko hook pretooluse).

func (e *env) policyModeLabel(m string) string {
	switch m {
	case "off":
		return e.t("desligada")
	case "ask":
		return e.t("perguntar antes")
	}
	return e.t("permitida")
}

func (e *env) policyTargetLabel(p api.ToolPolicy) string {
	switch p.Target {
	case "integration":
		return e.t("integração") + " " + p.Integration
	case "tool":
		return p.Integration + "/" + p.Tool
	case "native":
		return e.t("nativa") + " " + p.Tool
	}
	return e.t("tudo")
}

func (e *env) toolPolicyCmd() *cobra.Command {
	c := &cobra.Command{Use: "tool-policy", Aliases: []string{"tpol"}, Short: e.t("Política de tools: desligar, perguntar antes ou permitir, por persona e por empresa")}

	var lsPersona string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as políticas"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().ToolPolicies(cmd.Context(), co, lsPersona)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma política: valem os perfis de ferramentas e, nas tools do harness, o modo de permissão da persona. Crie com: hanko tool-policy set ask --native Bash"))
				return nil
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				who := e.t("empresa")
				if p.Persona != "" {
					who = p.Persona
				}
				origin := e.t("manual")
				if p.Origin == "always" {
					origin = e.t("sempre permitir")
				}
				rows = append(rows, []string{shortID(p.ID), who, e.policyTargetLabel(p), e.policyModeLabel(p.Mode), origin})
			}
			e.table([]string{e.t("ID"), e.t("ESCOPO"), e.t("ALVO"), e.t("MODO"), e.t("ORIGEM")}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&lsPersona, "persona", "", e.t("só as políticas desta persona (id ou slug)"))

	var persona, integration, tool, native string
	set := &cobra.Command{
		Use: "set <off|ask|allow>", Short: e.t("Grava uma política (admin): o alvo sai das flags; sem flag de alvo vale para tudo"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			in := client.ToolPolicyReq{Mode: a[0], Persona: persona}
			switch {
			case native != "":
				if integration != "" || tool != "" {
					return usageError{errors.New(e.t("--native não combina com --integration/--tool"))}
				}
				in.Target, in.Tool = "native", native
			case tool != "":
				if integration == "" {
					return usageError{errors.New(e.t("--tool precisa de --integration"))}
				}
				in.Target, in.Integration, in.Tool = "tool", integration, tool
			case integration != "":
				in.Target, in.Integration = "integration", integration
			default:
				in.Target = "all"
			}
			p, err := e.client().SetToolPolicy(cmd.Context(), co, in)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Política gravada: %s › %s › %s\n"), dash(p.Persona), e.policyTargetLabel(p), e.policyModeLabel(p.Mode))
			return nil
		},
	}
	sf := set.Flags()
	sf.StringVar(&persona, "persona", "", e.t("persona (id ou slug); sem ela, vale como padrão da empresa"))
	sf.StringVar(&integration, "integration", "", e.t("integração (id ou slug)"))
	sf.StringVar(&tool, "tool", "", e.t("tool da integração"))
	sf.StringVar(&native, "native", "", e.t("tool do harness ou de plugin pelo nome (ex.: Bash, Edit)"))

	del := &cobra.Command{
		Use: "delete " + e.t("<id>"), Aliases: []string{"rm"}, Short: e.t("Apaga uma política (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			id := a[0]
			if len(id) < 36 {
				ps, err := e.client().ToolPolicies(cmd.Context(), co, "")
				if err != nil {
					return err
				}
				var found []string
				for _, p := range ps {
					if strings.HasPrefix(p.ID, id) {
						found = append(found, p.ID)
					}
				}
				if len(found) != 1 {
					return fmt.Errorf(e.t("política %q não encontrada ou ambígua"), id)
				}
				id = found[0]
			}
			if err := e.client().DeleteToolPolicy(cmd.Context(), co, id); err != nil {
				return err
			}
			if !e.asJSON {
				fmt.Fprintln(e.out, e.t("Política apagada."))
			}
			return nil
		},
	}
	c.AddCommand(ls, set, del)
	return c
}

// testTool é "hanko integration tools <i> --test <tool> --as <persona>".
func (e *env) testTool(ctx context.Context, cl *client.Client, company, integration, tool, persona, argsJSON string, execute bool) error {
	if persona == "" {
		return usageError{errors.New(e.t("--test precisa de --as <persona>"))}
	}
	var args map[string]any
	if strings.TrimSpace(argsJSON) != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return usageError{fmt.Errorf(e.t("--args não é um objeto JSON: %w"), err)}
		}
	}
	r, err := cl.TestIntegrationTool(ctx, company, integration, tool, client.TestToolReq{Persona: persona, Arguments: args, Execute: execute})
	if err != nil {
		return err
	}
	if e.asJSON {
		return e.printJSON(r)
	}
	fmt.Fprintf(e.out, e.t("Como %s: %s/%s\n"), r.Persona, r.Integration, r.Tool)
	if r.Status != "" {
		fmt.Fprintf(e.out, e.t("  estado: %s · risco: %s\n"), e.toolStatusLabel(r.Status), e.riskLabel(r.Risk))
		fmt.Fprintf(e.out, e.t("  veredito: %s · origem: %s\n"), e.verdictLabel(r.Verdict), r.Verdict.Source)
	}
	fmt.Fprintf(e.out, e.t("  decisão: %s\n"), r.Decision)
	if r.Message != "" {
		fmt.Fprintf(e.out, "  %s\n", r.Message)
	}
	if r.GateID != "" {
		fmt.Fprintf(e.out, e.t("  gate: %s\n"), r.GateID)
	}
	if r.Output != "" {
		fmt.Fprintf(e.out, e.t("  resultado (redigido): %s\n"), oneLine(r.Output, 400))
	}
	return nil
}

// ---------- hook PreToolUse do claude ----------

// hookPollEvery é o intervalo padrão com que o hook consulta o gate; hookWait, o prazo máximo de espera (o do gate nativo
// é de 10 min).
const (
	hookPollEvery = time.Second
	hookWait      = 11 * time.Minute
)

func (e *env) hookCmd() *cobra.Command {
	c := &cobra.Command{Use: "hook", Short: e.t("Hooks de harness (uso interno: o Hanko os liga sozinho nas runs)")}
	pre := &cobra.Command{
		Use: "pretooluse", Short: e.t("Hook PreToolUse do claude: aplica a política de tools do Hanko (lê o JSON do hook em stdin)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return e.hookPreToolUse(cmd.Context()) },
	}
	c.AddCommand(pre)
	return c
}

type hookInput struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

func hookReply(w io.Writer, decision, reason string) error {
	return json.NewEncoder(w).Encode(map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName": "PreToolUse", "permissionDecision": decision, "permissionDecisionReason": reason}})
}

// companyFromRunToken lê o id da empresa do run token (o campo cid do payload). Não verifica a assinatura: o servidor
// é quem confere o token em cada chamada; aqui só se descobre a rota.
func companyFromRunToken(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		CID string `json:"cid"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	return claims.CID
}

func (e *env) hookPreToolUse(ctx context.Context) error {
	raw, err := io.ReadAll(io.LimitReader(e.in, 4<<20))
	if err != nil {
		return fmt.Errorf("%s: %w", e.t("ler o hook"), err)
	}
	var in hookInput
	if json.Unmarshal(raw, &in) != nil || in.ToolName == "" {
		return nil // não é o que esperamos: sem opinião
	}
	// as tools de servidores MCP (integrações e plugins) já passam pelo gateway, que aplica a mesma política
	if strings.HasPrefix(in.ToolName, "mcp__") {
		return nil
	}
	token := e.token
	if token == "" {
		token = e.getenv("HANKO_RUN_TOKEN")
	}
	company := e.company
	if company == "" {
		company = companyFromRunToken(token)
	}
	if token == "" || company == "" {
		return nil // fora de uma run do Hanko: nada a decidir
	}
	cl := client.New(e.url, token)
	var d api.ToolPermissionDecision
	for i := 0; ; i++ {
		d, err = cl.ToolPermission(ctx, company, in.ToolName, in.ToolInput)
		if err == nil {
			break
		}
		if i >= 2 || ctx.Err() != nil {
			return hookReply(e.out, "deny", "política de tools indisponível: "+err.Error())
		}
		e.sleep(500 * time.Millisecond)
	}
	switch d.Decision {
	case "allow":
		return hookReply(e.out, "allow", "liberada pela política de tools do Hanko")
	case "deny":
		return hookReply(e.out, "deny", d.Reason)
	case "ask":
		deadline := time.Now().Add(hookWait)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			st, err := cl.ToolPermissionState(ctx, company, d.GateID)
			if err != nil {
				return hookReply(e.out, "deny", "não foi possível consultar o gate "+d.GateID+": "+err.Error())
			}
			switch st.State {
			case "allow":
				return hookReply(e.out, "allow", "aprovada no gate "+d.GateID)
			case "deny":
				return hookReply(e.out, "deny", "negada no gate "+d.GateID+": "+st.Reason)
			}
			e.sleep(e.hookPoll())
		}
		return hookReply(e.out, "deny", "o gate "+d.GateID+" não foi decidido a tempo")
	}
	return nil // none: vale o modo de permissão do claude
}

func (e *env) hookPoll() time.Duration {
	if e.followWait > 0 {
		return e.followWait // o mesmo intervalo do --follow; os testes o encurtam
	}
	return hookPollEvery
}
