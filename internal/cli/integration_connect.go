package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// connectOpts são as flags de `hanko integration connect` (ADR-0089).
type connectOpts struct {
	url, clientID, template       string
	headers, scopes, personas     []string
	oauth, discover, discard, yes bool
}

// integrationConnectCmd é o assistente em um comando: `<nome> --url …` começa (passos 1 e 2); `<integração>`
// sozinho retoma. Sem --template e --persona nada é gravado; a conclusão exige --yes.
func (e *env) integrationConnectCmd() *cobra.Command {
	var o connectOpts
	c := &cobra.Command{Use: "connect " + e.t("<nome|integração>"),
		Short: e.t("Assistente: liga um servidor MCP a personas com um modelo de acesso (ADR-0089)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			return e.runConnect(cmd, co, a[0], o)
		},
	}
	f := c.Flags()
	f.StringVar(&o.url, "url", "", e.t("começa: endereço do servidor MCP http (omitido, retoma a integração do <ref>)"))
	f.StringArrayVar(&o.headers, "header", nil, e.t("header NOME=VALOR com o segredo do servidor (repita a flag)"))
	f.BoolVar(&o.oauth, "oauth", false, e.t("conecta por OAuth 2.1 com PKCE em vez de header secreto"))
	f.StringArrayVar(&o.scopes, "scope", nil, e.t("OAuth: escopo pedido (repita a flag)"))
	f.StringVar(&o.clientID, "client-id", "", e.t("OAuth: cliente já registrado, para servidor sem registro dinâmico"))
	f.BoolVar(&o.discover, "discover", false, e.t("repete a descoberta de tools (depois de um erro)"))
	f.BoolVar(&o.discard, "discard", false, e.t("descarta o rascunho: apaga a integração criada pelo assistente"))
	f.StringVar(&o.template, "template", "", e.t("passo 4: id ou slug do modelo de acesso"))
	f.StringArrayVar(&o.personas, "persona", nil, e.t("passo 3: persona que recebe a ferramenta (repita a flag; nunca todas)"))
	f.BoolVar(&o.yes, "yes", false, e.t("aprova a gravação; sem ela, o comando só mostra o que cada persona poderá fazer"))
	return c
}

func (e *env) runConnect(cmd *cobra.Command, co, ref string, o connectOpts) error {
	if o.discard {
		if err := e.client().ConnectDiscard(cmd.Context(), co, ref); err != nil {
			return err
		}
		fmt.Fprintf(e.out, e.t("Rascunho descartado: %s\n"), ref)
		return nil
	}
	w, err := e.connectStep(cmd, co, ref, o)
	if err != nil {
		return err
	}
	if !e.asJSON {
		e.printConnectState(w)
	}
	if w.CatalogError != "" {
		return fmt.Errorf(e.t("descoberta falhou: %s. Tente de novo com --discover, ou descarte com --discard"), w.CatalogError)
	}
	return e.connectChoose(cmd, co, w, o)
}

// connectStep faz o passo 1 (começar) ou o 2 (descobrir de novo), conforme as flags.
func (e *env) connectStep(cmd *cobra.Command, co, ref string, o connectOpts) (api.ConnectWizard, error) {
	cl := e.client()
	switch {
	case o.url != "":
		h, err := e.kvMap(o.headers)
		if err != nil {
			return api.ConnectWizard{}, err
		}
		return cl.ConnectBegin(cmd.Context(), co, client.ConnectBeginReq{Name: ref, URL: o.url, Headers: h,
			OAuth: o.oauth, Scopes: o.scopes, ClientID: o.clientID})
	case o.discover:
		return cl.ConnectDiscover(cmd.Context(), co, ref)
	default:
		return cl.ConnectState(cmd.Context(), co, ref)
	}
}

// connectChoose faz os passos 3 a 5: exige modelo e personas explícitos, mostra a prévia e grava com --yes.
func (e *env) connectChoose(cmd *cobra.Command, co string, w api.ConnectWizard, o connectOpts) error {
	if o.template == "" && len(o.personas) == 0 {
		return e.pendingChoices(cmd, co)
	}
	if o.template == "" {
		return e.missingTemplate(cmd, co)
	}
	if len(o.personas) == 0 {
		return errors.New(e.t("escolha as personas com --persona (passo 3; nunca todas por padrão)"))
	}
	cl := e.client()
	ref := w.Integration.Slug
	pv, err := cl.ConnectPreview(cmd.Context(), co, ref, o.template, o.personas)
	if err != nil {
		return err
	}
	if !o.yes {
		return e.showPreview(pv)
	}
	res, err := cl.ConnectComplete(cmd.Context(), co, ref, client.ConnectCompleteReq{Template: o.template, Personas: o.personas})
	if err != nil {
		return err
	}
	if e.asJSON {
		return e.printJSON(res)
	}
	fmt.Fprintf(e.out, e.t("Conectado: %s, perfil %s, %d persona(s), %d tool(s) liberada(s) da quarentena.\n"),
		res.Integration.Name, res.Profile.Name, len(res.Personas), len(res.Released))
	return nil
}

// showPreview mostra a prévia do passo 5. Sem --yes nada é gravado, e o comando devolve erro para não parecer concluído.
func (e *env) showPreview(pv api.ConnectPreview) error {
	if e.asJSON {
		return e.printJSON(pv)
	}
	fmt.Fprintf(e.out, e.t("Modelo %s:\n"), pv.Template.Name)
	for _, p := range pv.Personas {
		fmt.Fprintf(e.out, e.t("Persona %s:\n"), p.Name)
		for _, t := range p.Tools {
			state := e.t("bloqueada")
			if t.Effective {
				state = fmt.Sprintf(e.t("liberada (%s)"), t.Mode)
			}
			fmt.Fprintf(e.out, "  %-12s %-30s %s\n", t.Risk, t.Name, state)
		}
	}
	return errors.New(e.t("nada foi gravado: repita com --yes para aplicar estas permissões"))
}

// templateSlugs lista os slugs dos modelos da empresa, o sugerido primeiro.
func (e *env) templateSlugs(cmd *cobra.Command, co string) ([]string, error) {
	list, err := e.client().AccessTemplates(cmd.Context(), co)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, t := range list {
		names = append(names, t.Slug)
	}
	return names, nil
}

// pendingChoices: sem modelo nem persona, o assistente mostra o próximo passo e não grava nada (sai com 0).
func (e *env) pendingChoices(cmd *cobra.Command, co string) error {
	names, err := e.templateSlugs(cmd, co)
	if err != nil {
		return err
	}
	fmt.Fprintln(e.out, e.t("Próximo passo: escolha o modelo com --template (passo 4) e as personas com --persona (passo 3). Nada foi gravado."))
	fmt.Fprintln(e.out, e.t("modelos disponíveis: ")+strings.Join(names, ", "))
	return nil
}

// missingTemplate para com erro quando há personas mas falta o modelo: nada é gravado.
func (e *env) missingTemplate(cmd *cobra.Command, co string) error {
	names, err := e.templateSlugs(cmd, co)
	if err != nil {
		return err
	}
	return fmt.Errorf("%s: %s", e.t("escolha o modelo de acesso com --template (passo 4)"),
		e.t("modelos disponíveis: ")+strings.Join(names, ", "))
}

// printConnectState mostra o passo 2: as tools, com as de escrita em quarentena marcadas.
func (e *env) printConnectState(w api.ConnectWizard) {
	fmt.Fprintf(e.out, e.t("Integração: %s (%s), %d tool(s) descoberta(s)\n"), w.Integration.Name, w.Integration.Slug, len(w.Tools))
	for _, t := range w.Tools {
		mark := ""
		if t.Quarantined {
			mark = e.t(" [quarentena]")
		}
		fmt.Fprintf(e.out, "  %-12s %s%s\n", t.Risk, t.Name, mark)
	}
}
