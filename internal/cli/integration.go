package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// kvMap lê pares K=V repetidos de uma flag.
func (e *env) kvMap(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, usageError{fmt.Errorf(e.t("esperado NOME=VALOR, recebi %q"), p)}
		}
		out[k] = v
	}
	return out, nil
}

func (e *env) integrationTarget(i api.Integration) string {
	switch {
	case i.Kind == "webhook":
		return dash(i.Config.URL)
	case i.Config.Transport == "stdio":
		return strings.TrimSpace(i.Config.Command + " " + strings.Join(i.Config.Args, " "))
	}
	return dash(i.Config.URL)
}

func (e *env) deliveryLine(d api.WebhookDelivery) []string {
	code, lat := "-", "-"
	if d.HTTPStatus != nil {
		code = fmt.Sprint(*d.HTTPStatus)
	}
	if d.LatencyMs != nil {
		lat = fmt.Sprintf("%d ms", *d.LatencyMs)
	}
	ev := d.Event
	if d.Test {
		ev += " (" + e.t("teste") + ")"
	}
	return []string{e.fmtTime(d.CreatedAt), ev, d.Status, fmt.Sprint(d.Attempts), code, lat, oneLine(d.Error, 50)}
}

func (e *env) revealSecret(i api.Integration) {
	if i.Secret == "" {
		return
	}
	fmt.Fprintf(e.out, e.t("Segredo do webhook (mostrado só agora; guarde-o): %s\n"), i.Secret)
}

func (e *env) integrationCmd() *cobra.Command {
	c := &cobra.Command{Use: "integration", Aliases: []string{"int"},
		Short: e.t("Integrações: servidores MCP da empresa e webhooks de saída")}

	var (
		kind, slug, url, transport, command, secret string
		args_, headers, envs, personas, evs         []string
		allPersonas, disabled                       bool
	)
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria uma integração (--kind mcp ou webhook)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			h, err := e.kvMap(headers)
			if err != nil {
				return err
			}
			ev, err := e.kvMap(envs)
			if err != nil {
				return err
			}
			req := client.CreateIntegrationReq{Kind: kind, Name: a[0], Slug: slug, URL: url, Transport: transport, Command: command,
				Args: args_, Headers: h, Env: ev, AllPersonas: allPersonas, Personas: personas, Events: evs, Secret: secret}
			if disabled {
				off := false
				req.Enabled = &off
			}
			i, err := e.client().CreateIntegration(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(i)
			}
			fmt.Fprintf(e.out, e.t("Integração criada: %s (%s, %s)\n"), i.Name, i.Kind, i.Slug)
			e.revealSecret(i)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&kind, "kind", "", e.t("tipo: mcp ou webhook (obrigatório)"))
	cf.StringVar(&slug, "slug", "", e.t("identificador (padrão: derivado do nome)"))
	cf.StringVar(&url, "url", "", e.t("MCP http: endereço do servidor; webhook: destino https"))
	cf.StringVar(&transport, "transport", "", e.t("MCP: http (padrão) ou stdio (só se o operador liberou)"))
	cf.StringVar(&command, "command", "", e.t("MCP stdio: executável"))
	cf.StringArrayVar(&args_, "arg", nil, e.t("MCP stdio: argumento do comando (repita a flag)"))
	cf.StringArrayVar(&headers, "header", nil, e.t("MCP http: header NOME=VALOR enviado ao servidor (segredo; repita a flag)"))
	cf.StringArrayVar(&envs, "env", nil, e.t("MCP stdio: variável NOME=VALOR do processo (segredo; repita a flag)"))
	cf.BoolVar(&allPersonas, "all-personas", false, e.t("MCP: vale para todas as personas"))
	cf.StringArrayVar(&personas, "persona", nil, e.t("MCP: persona que recebe o servidor (repita a flag)"))
	cf.StringArrayVar(&evs, "event", nil, e.t("webhook: evento assinado (task.created, task.completed, gate.opened, gate.decided, run.failed, alert.opened)"))
	cf.StringVar(&secret, "secret", "", e.t("webhook: segredo HMAC (omitido, o servidor gera e mostra uma vez)"))
	cf.BoolVar(&disabled, "disabled", false, e.t("cria desligada"))
	_ = create.MarkFlagRequired("kind")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as integrações"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			is, err := e.client().Integrations(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(is)
			}
			if len(is) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma integração. Crie uma com: hanko integration create <nome> --kind mcp --url https://..."))
				return nil
			}
			rows := make([][]string, 0, len(is))
			for _, i := range is {
				last := "-"
				if i.LastDelivery != nil {
					last = i.LastDelivery.Status
				}
				rows = append(rows, []string{i.Slug, i.Kind, e.onOff2(i.Enabled), oneLine(e.integrationTarget(i), 50), last})
			}
			e.table([]string{e.t("SLUG"), e.t("TIPO"), e.t("ESTADO"), e.t("DESTINO"), e.t("ÚLTIMA ENTREGA")}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<integração>"), Short: e.t("Mostra uma integração (sem segredos)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			i, err := e.client().Integration(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(i)
			}
			pairs := [][2]string{{e.t("Integração"), i.Name}, {e.t("Slug"), i.Slug}, {e.t("ID"), i.ID}, {e.t("Tipo"), i.Kind},
				{e.t("Estado"), e.onOff2(i.Enabled)}, {e.t("Destino"), e.integrationTarget(i)}}
			if i.Kind == "mcp" {
				scope := e.t("personas ligadas")
				if i.AllPersonas {
					scope = e.t("todas as personas")
				}
				pairs = append(pairs, [2]string{e.t("Vale para"), scope})
				if len(i.Config.HeaderNames) > 0 {
					pairs = append(pairs, [2]string{e.t("Headers"), strings.Join(i.Config.HeaderNames, ", ") + " " + e.t("(valores guardados cifrados)")})
				}
				if len(i.Config.EnvNames) > 0 {
					pairs = append(pairs, [2]string{e.t("Variáveis"), strings.Join(i.Config.EnvNames, ", ") + " " + e.t("(valores guardados cifrados)")})
				}
			} else {
				pairs = append(pairs, [2]string{e.t("Eventos"), strings.Join(i.Events, ", ")})
				sec := e.t("definido")
				if i.SecretHint != "" {
					sec = i.SecretHint + "…"
				}
				pairs = append(pairs, [2]string{e.t("Segredo"), sec})
				if d := i.LastDelivery; d != nil {
					pairs = append(pairs, [2]string{e.t("Última entrega"), e.fmtTime(d.CreatedAt) + " — " + d.Status})
				}
			}
			e.kv(pairs...)
			return nil
		},
	}

	var (
		upName, upURL, upCommand, upSecret         string
		upArgs, upSet, upDrop, upSetEnv, upDropEnv []string
		upPersonas, upEvents                       []string
		upOn, upOff, upAll, upNoAll, upRotate      bool
	)
	update := &cobra.Command{
		Use: "update " + e.t("<integração>"), Short: e.t("Altera uma integração"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			if upOn && upOff {
				return usageError{errors.New(e.t("--on e --off não combinam"))}
			}
			if upAll && upNoAll {
				return usageError{errors.New(e.t("--all-personas e --no-all-personas não combinam"))}
			}
			sh, err := e.kvMap(upSet)
			if err != nil {
				return err
			}
			se, err := e.kvMap(upSetEnv)
			if err != nil {
				return err
			}
			req := client.UpdateIntegrationReq{SetHeaders: sh, DropHeaders: upDrop, SetEnv: se, DropEnv: upDropEnv, RotateSecret: upRotate}
			changed := sh != nil || se != nil || len(upDrop) > 0 || len(upDropEnv) > 0 || upRotate
			if f.Changed("name") {
				req.Name, changed = &upName, true
			}
			if f.Changed("url") {
				req.URL, changed = &upURL, true
			}
			if f.Changed("command") {
				req.Command, changed = &upCommand, true
			}
			if f.Changed("arg") {
				req.Args, changed = &upArgs, true
			}
			if f.Changed("persona") {
				req.Personas, changed = &upPersonas, true
			}
			if f.Changed("event") {
				req.Events, changed = &upEvents, true
			}
			if f.Changed("secret") {
				req.Secret, changed = &upSecret, true
			}
			if upOn || upOff {
				on := upOn
				req.Enabled, changed = &on, true
			}
			if upAll || upNoAll {
				v := upAll
				req.AllPersonas, changed = &v, true
			}
			if !changed {
				return usageError{errors.New(e.t("nada a alterar: veja hanko integration update --help"))}
			}
			i, err := e.client().UpdateIntegration(cmd.Context(), co, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(i)
			}
			fmt.Fprintf(e.out, e.t("Integração atualizada: %s (%s)\n"), i.Name, e.onOff2(i.Enabled))
			e.revealSecret(i)
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&upName, "name", "", e.t("novo nome"))
	uf.StringVar(&upURL, "url", "", e.t("novo endereço"))
	uf.StringVar(&upCommand, "command", "", e.t("MCP stdio: novo executável"))
	uf.StringArrayVar(&upArgs, "arg", nil, e.t("MCP stdio: substitui os argumentos (repita a flag)"))
	uf.StringArrayVar(&upSet, "header", nil, e.t("MCP http: define ou troca um header NOME=VALOR (repita a flag)"))
	uf.StringArrayVar(&upDrop, "drop-header", nil, e.t("MCP http: remove um header pelo nome (repita a flag)"))
	uf.StringArrayVar(&upSetEnv, "env", nil, e.t("MCP stdio: define ou troca uma variável NOME=VALOR (repita a flag)"))
	uf.StringArrayVar(&upDropEnv, "drop-env", nil, e.t("MCP stdio: remove uma variável pelo nome (repita a flag)"))
	uf.StringArrayVar(&upPersonas, "persona", nil, e.t("MCP: substitui o conjunto de personas (repita a flag)"))
	uf.StringArrayVar(&upEvents, "event", nil, e.t("webhook: substitui os eventos assinados (repita a flag)"))
	uf.StringVar(&upSecret, "secret", "", e.t("webhook: novo segredo HMAC"))
	uf.BoolVar(&upRotate, "rotate-secret", false, e.t("webhook: gera um novo segredo e o mostra uma vez"))
	uf.BoolVar(&upOn, "on", false, e.t("liga a integração"))
	uf.BoolVar(&upOff, "off", false, e.t("desliga a integração"))
	uf.BoolVar(&upAll, "all-personas", false, e.t("MCP: vale para todas as personas"))
	uf.BoolVar(&upNoAll, "no-all-personas", false, e.t("MCP: vale só para as personas ligadas"))

	del := &cobra.Command{
		Use: "delete " + e.t("<integração>"), Aliases: []string{"rm"}, Short: e.t("Apaga uma integração e o histórico de entregas"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteIntegration(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Integração %s apagada.\n"), a[0])
			return nil
		},
	}

	test := &cobra.Command{
		Use: "test " + e.t("<integração>"), Short: e.t("Testa a conexão (MCP) ou envia um evento ping assinado (webhook)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().TestIntegration(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			status := e.t("FALHOU")
			if r.OK {
				status = e.t("OK")
			}
			pairs := [][2]string{{e.t("Resultado"), status}}
			if r.Status > 0 {
				pairs = append(pairs, [2]string{e.t("HTTP"), fmt.Sprint(r.Status)})
			}
			if r.LatencyMs > 0 {
				pairs = append(pairs, [2]string{e.t("Latência"), fmt.Sprintf("%d ms", r.LatencyMs)})
			}
			if r.Error != "" {
				pairs = append(pairs, [2]string{e.t("Erro"), r.Error})
			}
			if r.Note != "" {
				pairs = append(pairs, [2]string{e.t("Obs."), r.Note})
			}
			e.kv(pairs...)
			if !r.OK {
				return fmt.Errorf("%s", e.t("o teste da integração falhou"))
			}
			return nil
		},
	}

	var limit int
	deliveries := &cobra.Command{
		Use: "deliveries " + e.t("<integração>"), Short: e.t("Mostra as últimas entregas de um webhook"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ds, err := e.client().IntegrationDeliveries(cmd.Context(), co, a[0], limit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ds)
			}
			if len(ds) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma entrega ainda."))
				return nil
			}
			rows := make([][]string, 0, len(ds))
			for _, d := range ds {
				rows = append(rows, e.deliveryLine(d))
			}
			e.table([]string{e.t("QUANDO"), e.t("EVENTO"), e.t("ESTADO"), e.t("TENTATIVAS"), e.t("HTTP"), e.t("LATÊNCIA"), e.t("ERRO")}, rows)
			return nil
		},
	}
	deliveries.Flags().IntVar(&limit, "limit", 20, e.t("máximo de entregas"))

	attach := func(use, short string, on bool) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<integração> <persona>..."), Short: short, Args: cobra.MinimumNArgs(2),
			RunE: func(cmd *cobra.Command, a []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				for _, p := range a[1:] {
					if err := e.client().SetPersonaIntegration(cmd.Context(), co, p, a[0], on); err != nil {
						return err
					}
					if on {
						fmt.Fprintf(e.out, e.t("Servidor MCP %s ligado para %s.\n"), a[0], p)
					} else {
						fmt.Fprintf(e.out, e.t("Servidor MCP %s desligado para %s.\n"), a[0], p)
					}
				}
				return nil
			},
		}
	}

	rekey := &cobra.Command{
		Use: "rekey", Short: e.t("Reescreve os segredos com a chave atual depois de rotacionar HANKO_SECRET_KEY"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := e.client().RekeyIntegrationSecrets(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("%d segredo(s) reescrito(s) com a chave atual.\n"), r.Rewritten)
			return nil
		},
	}

	c.AddCommand(create, ls, show, update, del, test, deliveries,
		attach("attach", e.t("Liga um servidor MCP para uma ou mais personas"), true),
		attach("detach", e.t("Desliga um servidor MCP para uma ou mais personas"), false),
		rekey)
	return c
}
