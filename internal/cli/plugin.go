package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Plugins de terceiros (ADR-0038): instalar, atualizar e desinstalar são do dono da instalação; ligar,
// desligar e configurar por empresa são do admin dela (--company).

func (e *env) pluginStatus(s string) string {
	switch s {
	case "ready":
		return e.t("pronto")
	case "installed":
		return e.t("instalado")
	case "disabled":
		return e.t("desligado")
	case "error":
		return e.t("com erro")
	case "upgrade_pending":
		return e.t("atualização pendente")
	case "uninstalled":
		return e.t("desinstalado")
	}
	return s
}

func (e *env) printPlugin(p api.Plugin) {
	pairs := [][2]string{{e.t("Plugin"), p.Name}, {e.t("Id"), p.Key}, {e.t("Versão"), p.Version}, {e.t("Estado"), e.pluginStatus(p.Status)},
		{e.t("Capacidades"), dash(strings.Join(p.Capabilities, ", "))}, {e.t("Empresas"), fmt.Sprint(p.Companies)}}
	if p.PendingVersion != "" {
		pairs = append(pairs, [2]string{e.t("Versão pendente"), p.PendingVersion + " (" + strings.Join(p.PendingCapabilities, ", ") + ")"})
	}
	if p.LastError != "" {
		pairs = append(pairs, [2]string{e.t("Último erro"), p.LastError})
	}
	e.kv(pairs...)
}

func (e *env) pluginCmd() *cobra.Command {
	c := &cobra.Command{Use: "plugin", Aliases: []string{"plugins"},
		Short: e.t("Plugins de terceiros: instalar, ligar por empresa e configurar")}

	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os plugins (com --company, o estado na empresa)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.company != "" {
				ps, err := e.client().CompanyPlugins(cmd.Context(), e.company)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(ps)
				}
				if len(ps) == 0 {
					fmt.Fprintln(e.out, e.t("Nenhum plugin instalado nesta instalação."))
					return nil
				}
				rows := make([][]string, 0, len(ps))
				for _, p := range ps {
					rows = append(rows, []string{p.Key, p.Version, e.pluginStatus(p.Status), e.onOff2(p.Enabled), fmt.Sprint(len(p.Tools))})
				}
				e.table([]string{e.t("ID"), e.t("VERSÃO"), e.t("INSTALAÇÃO"), e.t("NA EMPRESA"), e.t("TOOLS")}, rows)
				return nil
			}
			ov, err := e.client().Plugins(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ov)
			}
			if !ov.Enabled {
				fmt.Fprintln(e.out, e.t("Plugins desligados neste servidor: defina HANKO_PLUGINS_DIR e reinicie."))
				return nil
			}
			rows := [][]string{}
			for _, p := range ov.Installed {
				rows = append(rows, []string{p.Key, p.Version, e.pluginStatus(p.Status), fmt.Sprint(p.Companies), dash(p.LastError)})
			}
			if len(rows) > 0 {
				e.table([]string{e.t("ID"), e.t("VERSÃO"), e.t("ESTADO"), e.t("EMPRESAS"), e.t("ÚLTIMO ERRO")}, rows)
			} else {
				fmt.Fprintln(e.out, e.t("Nenhum plugin instalado."))
			}
			avail := [][]string{}
			for _, a := range ov.Available {
				state := e.t("disponível")
				switch {
				case a.Error != "":
					state = e.t("inválido") + ": " + oneLine(a.Error, 60)
				case a.Upgrade:
					state = e.t("versão nova")
				case a.Installed != "":
					continue
				}
				avail = append(avail, []string{a.Key, dash(a.Version), state, dash(strings.Join(a.Capabilities, ", "))})
			}
			if len(avail) > 0 {
				fmt.Fprintln(e.out)
				fmt.Fprintf(e.out, e.t("Em %s:\n"), ov.Dir)
				e.table([]string{e.t("ID"), e.t("VERSÃO"), e.t("SITUAÇÃO"), e.t("CAPACIDADES")}, avail)
			}
			return nil
		},
	}

	install := &cobra.Command{
		Use: "install " + e.t("<id>"), Short: e.t("Instala um plugin de HANKO_PLUGINS_DIR, concedendo as capacidades que ele pede"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			p, err := e.client().InstallPlugin(cmd.Context(), a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.printPlugin(p)
			return nil
		},
	}

	var global bool
	toggle := func(on bool) func(cmd *cobra.Command, a []string) error {
		return func(cmd *cobra.Command, a []string) error {
			if global {
				action := "disable"
				if on {
					action = "enable"
				}
				p, err := e.client().PluginAction(cmd.Context(), a[0], action)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				e.printPlugin(p)
				return nil
			}
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().UpdateCompanyPlugin(cmd.Context(), co, a[0], client.UpdateCompanyPluginReq{Enabled: &on})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Plugin %s nesta empresa: %s\n"), p.Key, e.onOff2(p.Enabled))
			return nil
		}
	}
	enable := &cobra.Command{Use: "enable " + e.t("<id>"), Short: e.t("Liga o plugin na empresa (--global: na instalação)"), Args: cobra.ExactArgs(1), RunE: toggle(true)}
	disable := &cobra.Command{Use: "disable " + e.t("<id>"), Short: e.t("Desliga o plugin na empresa (--global: na instalação inteira)"), Args: cobra.ExactArgs(1), RunE: toggle(false)}
	enable.Flags().BoolVar(&global, "global", false, e.t("na instalação inteira (dono da instalação)"))
	disable.Flags().BoolVar(&global, "global", false, e.t("na instalação inteira (dono da instalação)"))

	var approve bool
	upgrade := &cobra.Command{
		Use: "upgrade " + e.t("<id>"), Short: e.t("Aplica a versão nova do diretório (--approve aprova capacidades novas)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			action := "upgrade"
			if approve {
				action = "approve-upgrade"
			}
			p, err := e.client().PluginAction(cmd.Context(), a[0], action)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.printPlugin(p)
			if p.Status == "upgrade_pending" {
				fmt.Fprintf(e.out, e.t("A versão %s pede capacidades novas; aprove com: hanko plugin upgrade %s --approve\n"), p.PendingVersion, p.Key)
			}
			return nil
		},
	}
	upgrade.Flags().BoolVar(&approve, "approve", false, e.t("aprova as capacidades da versão pendente"))

	uninstall := &cobra.Command{
		Use: "uninstall " + e.t("<id>"), Short: e.t("Desinstala: para o processo e apaga configuração e estado em todas as empresas"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			if err := e.client().UninstallPlugin(cmd.Context(), a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Plugin %s desinstalado.\n"), a[0])
			return nil
		},
	}

	var limit int
	logs := &cobra.Command{
		Use: "logs " + e.t("<id>"), Short: e.t("Mostra a saúde e as últimas linhas do log do plugin"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			h, err := e.client().PluginHealth(cmd.Context(), a[0])
			if err != nil {
				return err
			}
			ls, err := e.client().PluginLogs(cmd.Context(), a[0], limit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"health": h, "logs": ls})
			}
			proc := e.t("parado")
			if h.Process.Running {
				proc = fmt.Sprintf("PID %d, %s %s", h.Process.PID, e.t("desde"), e.fmtTimePtr(h.Process.StartedAt))
			}
			e.kv([][2]string{{e.t("Estado"), e.pluginStatus(h.Status)}, {e.t("Processo"), proc}, {e.t("Reinícios"), fmt.Sprint(h.Process.Restarts)},
				{e.t("Último erro"), dash(h.LastError)}}...)
			fmt.Fprintln(e.out)
			rows := make([][]string, 0, len(ls))
			for i := len(ls) - 1; i >= 0; i-- {
				rows = append(rows, []string{e.fmtTime(ls[i].CreatedAt), ls[i].Level, oneLine(ls[i].Message, 160)})
			}
			e.table([]string{e.t("QUANDO"), e.t("NÍVEL"), e.t("MENSAGEM")}, rows)
			return nil
		},
	}
	logs.Flags().IntVar(&limit, "limit", 50, e.t("quantas linhas (mais recentes)"))

	var sets, clears []string
	config := &cobra.Command{
		Use: "config " + e.t("<id>"), Short: e.t("Mostra ou altera a configuração do plugin na empresa (segredos nunca são exibidos)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().CompanyPlugin(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if len(sets)+len(clears) > 0 {
				kv, err := e.kvMap(sets)
				if err != nil {
					return err
				}
				cfg := map[string]any{}
				for k, v := range kv {
					cfg[k] = v
				}
				if p, err = e.client().UpdateCompanyPlugin(cmd.Context(), co, a[0], client.UpdateCompanyPluginReq{Config: cfg, ClearSecrets: clears}); err != nil {
					return err
				}
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			set := map[string]bool{}
			for _, s := range p.SecretsSet {
				set[s] = true
			}
			rows := make([][]string, 0, len(p.Fields))
			for _, f := range p.Fields {
				val := "-"
				switch {
				case f.Secret && set[f.Key]:
					val = e.t("(definido)")
				case f.Secret:
					val = e.t("(não definido)")
				case p.Config[f.Key] != nil:
					val = fmt.Sprint(p.Config[f.Key])
				case f.Default != nil:
					val = fmt.Sprint(f.Default) + " " + e.t("(padrão)")
				}
				req := ""
				if f.Required {
					req = "*"
				}
				rows = append(rows, []string{f.Key + req, f.Type, val})
			}
			fmt.Fprintf(e.out, e.t("Plugin %s nesta empresa: %s\n"), p.Key, e.onOff2(p.Enabled))
			if len(rows) == 0 {
				fmt.Fprintln(e.out, e.t("O plugin não tem campos de configuração."))
				return nil
			}
			e.table([]string{e.t("CAMPO"), e.t("TIPO"), e.t("VALOR")}, rows)
			return nil
		},
	}
	config.Flags().StringArrayVar(&sets, "set", nil, e.t("define CAMPO=VALOR (repita a flag; vale para campos secretos)"))
	config.Flags().StringArrayVar(&clears, "clear", nil, e.t("apaga um campo secreto (repita a flag)"))

	c.AddCommand(list, install, enable, disable, upgrade, uninstall, logs, config)
	return c
}
