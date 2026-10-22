package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/domain"
)

// Plugins, M18 (ADR-0045): capacidades, abas, gatilhos de fluxo, fontes de skills e o esqueleto de plugin.

func (e *env) pluginM18Cmds() []*cobra.Command {
	capCmd := &cobra.Command{Use: "cap", Aliases: []string{"capability"}, Short: e.t("Concede ou revoga capacidades de um plugin (dono da instalação)")}
	for _, grant := range []bool{true, false} {
		use, short := "revoke", e.t("Revoga uma capacidade: o que depende dela deixa de funcionar na hora")
		if grant {
			use, short = "grant", e.t("Concede uma capacidade declarada no manifesto do plugin")
		}
		capCmd.AddCommand(&cobra.Command{
			Use: use + " " + e.t("<id> <capacidade>"), Short: short, Args: cobra.ExactArgs(2),
			RunE: func(cmd *cobra.Command, a []string) error {
				p, err := e.client().PluginCapability(cmd.Context(), a[0], a[1], grant)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				e.printPlugin(p)
				return nil
			},
		})
	}

	tabs := &cobra.Command{
		Use: "tabs", Short: e.t("Lista as abas que os plugins acrescentam ao menu da empresa"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ts, err := e.client().PluginTabs(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ts)
			}
			if len(ts) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma aba de plugin nesta empresa."))
				return nil
			}
			rows := make([][]string, 0, len(ts))
			for _, t := range ts {
				rows = append(rows, []string{t.Plugin, t.Key, t.Title, t.URL})
			}
			e.table([]string{e.t("PLUGIN"), e.t("ABA"), e.t("TÍTULO"), e.t("CAMINHO")}, rows)
			return nil
		},
	}

	trigger := &cobra.Command{Use: "trigger", Aliases: []string{"triggers"}, Short: e.t("Gatilhos de fluxo dos plugins (admin da empresa liga cada gatilho a fluxos)")}
	trigger.AddCommand(&cobra.Command{
		Use: "list " + e.t("<id>"), Aliases: []string{"ls"}, Short: e.t("Mostra os gatilhos do plugin e os fluxos ligados a cada um"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			t, err := e.client().PluginFlowTriggers(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(t)
			}
			if len(t.Triggers) == 0 {
				fmt.Fprintln(e.out, e.t("O plugin não declara gatilhos de fluxo."))
				return nil
			}
			rows := make([][]string, 0, len(t.Triggers))
			for _, tr := range t.Triggers {
				var flows []string
				for _, l := range t.Links {
					if l.Trigger == tr.Key {
						flows = append(flows, l.FlowSlug)
					}
				}
				rows = append(rows, []string{tr.Key, tr.Title, dash(strings.Join(flows, ", "))})
			}
			e.table([]string{e.t("GATILHO"), e.t("TÍTULO"), e.t("FLUXOS LIGADOS")}, rows)
			return nil
		},
	})
	for _, link := range []bool{true, false} {
		use, short := "unlink", e.t("Desliga o gatilho de um fluxo")
		if link {
			use, short = "link", e.t("Liga o gatilho a um fluxo da empresa (o fluxo roda quando o plugin dispara o gatilho)")
		}
		trigger.AddCommand(&cobra.Command{
			Use: use + " " + e.t("<id> <gatilho> <fluxo>"), Short: short, Args: cobra.ExactArgs(3),
			RunE: func(cmd *cobra.Command, a []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				if err := e.client().LinkPluginFlowTrigger(cmd.Context(), co, a[0], a[1], a[2], link); err != nil {
					return err
				}
				if link {
					fmt.Fprintf(e.out, e.t("Gatilho %s do plugin %s ligado ao fluxo %s.\n"), a[1], a[0], a[2])
				} else {
					fmt.Fprintf(e.out, e.t("Gatilho %s do plugin %s desligado do fluxo %s.\n"), a[1], a[0], a[2])
				}
				return nil
			},
		})
	}

	sources := &cobra.Command{
		Use: "skill-sources", Short: e.t("Lista as fontes de skills oferecidas pelos plugins (conecte com: hanko skill source add <url>)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ss, err := e.client().PluginSkillSources(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ss)
			}
			if len(ss) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum plugin oferece fontes de skills nesta empresa."))
				return nil
			}
			rows := make([][]string, 0, len(ss))
			for _, s := range ss {
				state := e.t("não conectada")
				if s.SourceID != nil {
					state = e.t("conectada")
				}
				rows = append(rows, []string{s.URL, s.Title, state})
			}
			e.table([]string{e.t("URL"), e.t("TÍTULO"), e.t("SITUAÇÃO")}, rows)
			return nil
		},
	}

	var dir, author string
	newCmd := &cobra.Command{
		Use: "new " + e.t("<id>"), Short: e.t("Cria o esqueleto de um plugin em Go (manifesto, main.go com o SDK e README)"), Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			target := dir
			if target == "" {
				target = a[0]
			}
			files, err := e.scaffoldPlugin(a[0], author, target)
			if err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Plugin %s criado em %s:\n"), a[0], target)
			for _, f := range files {
				fmt.Fprintln(e.out, "  "+f)
			}
			fmt.Fprintf(e.out, e.t("Próximo passo: cd %s && go mod tidy && go build -o %s . e copie o diretório para HANKO_PLUGINS_DIR/%s\n"), target, a[0], a[0])
			return nil
		},
	}
	newCmd.Flags().StringVar(&dir, "dir", "", e.t("diretório de destino (padrão: ./<id>; precisa estar vazio ou não existir)"))
	newCmd.Flags().StringVar(&author, "author", "", e.t("autor no manifesto"))

	return []*cobra.Command{capCmd, tabs, trigger, sources, newCmd}
}

// scaffoldPlugin grava o esqueleto de um plugin (sem tocar em diretório que já tem coisas) e devolve os arquivos.
func (e *env) scaffoldPlugin(id, author, dir string) ([]string, error) {
	if !domain.ValidPluginID(id) {
		return nil, errors.New(e.t("id inválido: use ^[a-z][a-z0-9-]{1,62}$"))
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf(e.t("o diretório %s não está vazio"), dir)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	files, err := PluginScaffold(id, author)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		p := filepath.Join(dir, f.Name)
		if err := os.WriteFile(p, []byte(f.Content), 0o600); err != nil { //nolint:gosec // arquivo do usuário, no diretório que ele escolheu
			return nil, err
		}
		names = append(names, f.Name)
	}
	return names, nil
}

// ScaffoldFile é um arquivo do esqueleto.
type ScaffoldFile struct{ Name, Content string }

// PluginScaffold gera os arquivos do esqueleto de um plugin: manifesto válido (confere com o ParsePluginManifest),
// main.go com o SDK (uma tool, uma aba com ponte, um gatilho), go.mod, .gitignore e README.
func PluginScaffold(id, author string) ([]ScaffoldFile, error) {
	if !domain.ValidPluginID(id) {
		return nil, fmt.Errorf("id inválido %q", id)
	}
	if strings.ContainsAny(author, "\"\\\n\r") {
		return nil, errors.New("autor com caracteres inválidos")
	}
	if author == "" {
		author = "Seu nome"
	}
	r := strings.NewReplacer("__ID__", id, "__AUTHOR__", author, "__NAME__", pluginTitle(id))
	manifest := r.Replace(scaffoldManifest)
	if _, err := domain.ParsePluginManifest([]byte(manifest)); err != nil {
		return nil, fmt.Errorf("esqueleto inválido: %w", err)
	}
	return []ScaffoldFile{
		{Name: domain.ManifestFile, Content: manifest},
		{Name: "main.go", Content: r.Replace(scaffoldMain)},
		{Name: "go.mod", Content: r.Replace(scaffoldGoMod)},
		{Name: ".gitignore", Content: id + "\n"},
		{Name: "README.md", Content: r.Replace(scaffoldReadme)},
	}, nil
}

func pluginTitle(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

const scaffoldManifest = `{
  "id": "__ID__",
  "api_version": 1,
  "version": "0.1.0",
  "name": "__NAME__",
  "description": "O que o plugin faz.",
  "author": "__AUTHOR__",
  "command": "__ID__",
  "capabilities": ["agent.tools", "pages", "state", "flows.trigger"],
  "tools": [
    {
      "name": "ping",
      "description": "Devolve pong com o texto recebido.",
      "input_schema": {"type": "object", "properties": {"text": {"type": "string"}}}
    }
  ],
  "pages": [
    {"key": "painel", "title": "__NAME__"}
  ],
  "flow_triggers": [
    {"key": "evento", "title": "Evento do plugin", "description": "Disparado pelo botão da aba; o admin liga a fluxos."}
  ]
}
`

const scaffoldGoMod = `module __ID__

go 1.27
`

const scaffoldMain = "// Command __ID__ é um plugin do Hanko feito com o SDK Go (github.com/renatobardi/hanko/sdk/plugin).\n" +
	"package main\n\n" +
	"import (\n\t\"context\"\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"html\"\n\t\"log\"\n\n\t\"github.com/renatobardi/hanko/sdk/plugin\"\n)\n\n" +
	"// A aba roda num iframe isolado (sem rede, sem cookies): a única saída é window.hanko.call.\n" +
	"const painel = `<!doctype html><meta charset=\"utf-8\"><body style=\"font:14px system-ui;margin:16px\">\n" +
	"<h2>__NAME__</h2><p>Olá, %s. Disparos: <b id=\"n\">%d</b>.</p><button id=\"b\">Disparar</button>\n" +
	"<script>document.getElementById(\"b\").onclick=async()=>{const r=await window.hanko.call(\"disparar\",{});document.getElementById(\"n\").textContent=r.total}</script>`\n\n" +
	"func main() {\n\tp := plugin.New(\"__ID__\")\n\n" +
	"\tp.Tool(\"ping\", func(_ context.Context, _ *plugin.Host, c plugin.ToolCall) (plugin.ToolResult, error) {\n" +
	"\t\tvar in struct {\n\t\t\tText string `json:\"text\"`\n\t\t}\n\t\t_ = json.Unmarshal(c.Arguments, &in)\n" +
	"\t\treturn plugin.ToolResult{Text: \"pong: \" + in.Text}, nil\n\t})\n\n" +
	"\tp.Page(\"painel\", func(ctx context.Context, h *plugin.Host, r plugin.PageRequest) (string, error) {\n" +
	"\t\tvar total int\n\t\t_, _ = h.StateGet(ctx, r.Company, \"disparos\", &total)\n" +
	"\t\treturn fmt.Sprintf(painel, html.EscapeString(r.User.Name), total), nil\n\t})\n\n" +
	"\t// A ponte: o Hanko entrega quem chamou (c.User.Role) e o plugin decide o que permitir.\n" +
	"\tp.Bridge(func(ctx context.Context, h *plugin.Host, c plugin.BridgeCall) (any, error) {\n" +
	"\t\tif c.Method != \"disparar\" {\n\t\t\treturn nil, plugin.Errorf(plugin.CodeInvalidParams, \"método desconhecido: %s\", c.Method)\n\t\t}\n" +
	"\t\tif c.User.Role == \"viewer\" {\n\t\t\treturn nil, plugin.Errorf(plugin.CodeDenied, \"só membros disparam\")\n\t\t}\n" +
	"\t\tvar total int\n\t\t_, _ = h.StateGet(ctx, c.Company, \"disparos\", &total)\n\t\ttotal++\n" +
	"\t\tif err := h.StateSet(ctx, c.Company, \"disparos\", total); err != nil {\n\t\t\treturn nil, err\n\t\t}\n" +
	"\t\tfired, err := h.FireTrigger(ctx, c.Company, \"evento\")\n\t\tif err != nil {\n\t\t\treturn nil, err\n\t\t}\n" +
	"\t\treturn map[string]any{\"total\": total, \"fluxos\": fired}, nil\n\t})\n\n" +
	"\tif err := p.Run(context.Background()); err != nil {\n\t\tlog.Fatal(err)\n\t}\n}\n"

const scaffoldReadme = "# __NAME__\n\n" +
	"Plugin do Hanko (ADR-0038, ADR-0045). Protocolo e capacidades: `docs/PLUGINS.md` do Hanko.\n\n" +
	"## Compilar e instalar\n\n" +
	"```sh\ngo mod tidy        # baixa o SDK (github.com/renatobardi/hanko/sdk/plugin)\ngo build -o __ID__ .\n" +
	"cp -r . $HANKO_PLUGINS_DIR/__ID__\nhanko plugin install __ID__   # o dono da instalação vê e aceita as capacidades\n" +
	"hanko plugin enable __ID__ --company <empresa>\n```\n\n" +
	"## O que tem aqui\n\n" +
	"- `hanko-plugin.json`: o manifesto (capacidades pedidas; tool, aba e gatilho declarados).\n" +
	"- `main.go`: a tool `ping`, a aba `painel` com ponte (`window.hanko.call`) e o gatilho `evento`.\n\n" +
	"Para ligar o gatilho a um fluxo: `hanko plugin trigger link __ID__ evento <fluxo> --company <empresa>`.\n" +
	"O dono da instalação pode revogar qualquer capacidade a qualquer momento: `hanko plugin cap revoke __ID__ pages`.\n"
