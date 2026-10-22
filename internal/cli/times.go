package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Pacotes de time hanko.team.v1 (ADR-0044): hanko teams catalog|show|export|preview|install|history.

const maxTeamFileBytes = 2 << 20

// loadTeamSource lê o pacote de um arquivo, de uma URL https/http ou da entrada padrão ("-"). Quem baixa é a CLI (o
// servidor nunca busca URL de pacote), com teto de tamanho e de tempo.
func (e *env) loadTeamSource(ctx context.Context, src string) (map[string]any, error) {
	var raw []byte
	var err error
	switch {
	case src == "-":
		raw, err = io.ReadAll(io.LimitReader(e.in, maxTeamFileBytes+1))
	case strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://"):
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, rerr := http.NewRequestWithContext(cctx, http.MethodGet, src, nil)
		if rerr != nil {
			return nil, fmt.Errorf("%s: %w", e.t("URL inválida"), rerr)
		}
		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			return nil, fmt.Errorf("%s: %w", e.t("não consegui baixar o pacote"), derr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: HTTP %d", e.t("não consegui baixar o pacote"), resp.StatusCode)
		}
		raw, err = io.ReadAll(io.LimitReader(resp.Body, maxTeamFileBytes+1))
	default:
		raw, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > maxTeamFileBytes {
		return nil, errors.New(e.t("pacote maior que 2 MiB"))
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", e.t("o pacote não é um JSON válido"), err)
	}
	return m, nil
}

func (e *env) printTeamInstall(r api.TeamInstalled) {
	switch {
	case r.DryRun:
		fmt.Fprintf(e.out, e.t("Prévia (nada foi gravado): instalar %s %s\n"), r.Package.Name, r.Package.Version)
	default:
		fmt.Fprintf(e.out, e.t("Pacote %s %s instalado (personas pausadas, rotinas desligadas)\n"), r.Package.Name, r.Package.Version)
	}
	rows := make([][]string, 0, len(r.Items))
	for _, it := range r.Items {
		target := ""
		if it.Target != it.Key {
			target = "→ " + it.Target
		}
		rows = append(rows, []string{it.Kind, it.Key, e.actionLabel(it.Action), target})
	}
	if len(rows) > 0 {
		e.table([]string{e.t("TIPO"), e.t("ITEM"), e.t("AÇÃO"), e.t("DESTINO")}, rows)
		fmt.Fprintln(e.out)
	}
	for _, a := range r.Agents {
		subs := strings.Join(a.Subscriptions, ", ")
		if subs == "" {
			subs = "—"
		}
		chosen := ""
		if a.Chosen != "" {
			chosen = " · " + e.t("escolhida") + ": " + a.Chosen
		}
		fmt.Fprintf(e.out, e.t("harness %s · assinaturas ativas: %s%s\n"), a.Agent, subs, chosen)
	}
	for _, s := range r.Secrets {
		st := e.t("existe")
		if !s.Exists {
			st = e.t("FALTA")
		}
		fmt.Fprintf(e.out, e.t("segredo %s: %s\n"), s.Name, st)
	}
	for _, w := range r.Warnings {
		msg := e.t(w.Message) // i18n:dyn (os formatos estão marcados com i18n.N em internal/ui/times.go e lifecycle.go)
		fmt.Fprintf(e.out, e.t("aviso: ")+msg+"\n", w.Count)
	}
}

func kvPairs(flag string, pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := map[string]string{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok || k == "" || v == "" {
			return nil, usageError{fmt.Errorf("--%s: use chave=valor (%q)", flag, p)}
		}
		m[k] = v
	}
	return m, nil
}

func (e *env) teamsCmd() *cobra.Command {
	c := &cobra.Command{Use: "teams", Aliases: []string{"team"}, Short: e.t("Pacotes de time (hanko.team.v1): exportar e instalar personas, skills e rotinas")}

	catalog := &cobra.Command{
		Use: "catalog", Short: e.t("Lista os pacotes do catálogo embutido"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := e.client().TeamCatalog(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			rows := make([][]string, 0, len(l))
			for _, p := range l {
				rows = append(rows, []string{p.Slug, p.Name, fmt.Sprint(p.Personas), fmt.Sprint(p.Skills), fmt.Sprint(p.Flows), strings.Join(p.Agents, ",")})
			}
			e.table([]string{e.t("SLUG"), e.t("NOME"), e.t("PERSONAS"), "SKILLS", e.t("ROTINAS"), "HARNESS"}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<pacote-do-catálogo>"), Short: e.t("Mostra um pacote do catálogo (JSON)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			p, err := e.client().TeamCatalogPackage(cmd.Context(), a[0])
			if err != nil {
				return err
			}
			return e.printJSON(p)
		},
	}

	var exName, exSlug, exDesc, exOut string
	exp := &cobra.Command{
		Use: "export [" + e.t("persona...") + "]", Short: e.t("Exporta personas (todas se nenhuma for citada), as skills delas e as rotinas como pacote"), Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().ExportTeam(cmd.Context(), co, a, exName, exSlug, exDesc)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(p, "", "  ")
			if err != nil {
				return err
			}
			if exOut != "" && exOut != "-" {
				if err := os.WriteFile(exOut, append(b, '\n'), 0o600); err != nil {
					return err
				}
				fmt.Fprintf(e.out, e.t("Pacote gravado em %s\n"), exOut)
				return nil
			}
			fmt.Fprintln(e.out, string(b))
			return nil
		},
	}
	exp.Flags().StringVar(&exName, "name", "", e.t("nome do pacote"))
	exp.Flags().StringVar(&exSlug, "slug", "", e.t("slug do pacote"))
	exp.Flags().StringVar(&exDesc, "description", "", e.t("descrição do pacote"))
	exp.Flags().StringVarP(&exOut, "output", "o", "", e.t("arquivo de saída (padrão: a saída padrão)"))

	var onConflict string
	var resolves, subs []string
	build := func(cmd *cobra.Command, src string) (client.TeamInstallReq, error) {
		rs, err := kvPairs("resolve", resolves)
		if err != nil {
			return client.TeamInstallReq{}, err
		}
		ss, err := kvPairs("subscription", subs)
		if err != nil {
			return client.TeamInstallReq{}, err
		}
		req := client.TeamInstallReq{OnConflict: onConflict, Resolve: rs, Subscriptions: ss}
		if strings.HasPrefix(src, "catalog:") {
			req.Catalog = strings.TrimPrefix(src, "catalog:")
			return req, nil
		}
		if req.Package, err = e.loadTeamSource(cmd.Context(), src); err != nil {
			return req, err
		}
		return req, nil
	}
	flags := func(c *cobra.Command) {
		f := c.Flags()
		f.StringVar(&onConflict, "on-conflict", "skip", e.t("ação padrão para o que já existe: skip, rename ou replace"))
		f.StringArrayVar(&resolves, "resolve", nil, e.t("exceção por item: tipo/chave=ação (repetível; ex.: personas/dev=replace)"))
		f.StringArrayVar(&subs, "subscription", nil, e.t("assinatura por harness: harness=NOME DA ASSINATURA (repetível; ex.: claude=Plano Claude)"))
	}
	source := e.t("<catalog:slug | arquivo | URL | ->")
	preview := &cobra.Command{
		Use: "preview " + source, Short: e.t("Mostra o que a instalação faria, sem gravar nada"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req, err := build(cmd, a[0])
			if err != nil {
				return err
			}
			r, err := e.client().PreviewTeam(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			e.printTeamInstall(r)
			return nil
		},
	}
	flags(preview)
	install := &cobra.Command{
		Use: "install " + source, Short: e.t("Instala o pacote na empresa: personas pausadas e rotinas desligadas (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req, err := build(cmd, a[0])
			if err != nil {
				return err
			}
			r, err := e.client().InstallTeam(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			e.printTeamInstall(r)
			fmt.Fprintln(e.out, e.t("Nada trabalha até você liberar: retome as personas na interface e ligue as rotinas com hanko flow enable <rotina>."))
			return nil
		},
	}
	flags(install)

	history := &cobra.Command{
		Use: "history", Aliases: []string{"installs"}, Short: e.t("Lista as instalações de pacotes na empresa"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().TeamInstalls(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			rows := make([][]string, 0, len(l))
			for _, r := range l {
				keys := make([]string, 0, len(r.Counts))
				for k := range r.Counts {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				parts := make([]string, 0, len(keys))
				for _, k := range keys {
					parts = append(parts, fmt.Sprintf("%s:%d", k, r.Counts[k]))
				}
				rows = append(rows, []string{r.CreatedAt, r.Slug, r.Version, r.Source, strings.Join(parts, " ")})
			}
			e.table([]string{e.t("QUANDO"), e.t("PACOTE"), e.t("VERSÃO"), e.t("ORIGEM"), e.t("ITENS")}, rows)
			return nil
		},
	}
	c.AddCommand(catalog, show, exp, preview, install, history)
	return c
}
