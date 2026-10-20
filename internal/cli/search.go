package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/i18n"
)

// searchTypeLabels dá o título de cada grupo no texto da CLI.
var searchTypeLabels = map[string]string{
	"task":     i18n.N("Tasks"),
	"comment":  i18n.N("Comentários"),
	"persona":  i18n.N("Personas"),
	"run":      i18n.N("Execuções"),
	"flow":     i18n.N("Fluxos"),
	"goal":     i18n.N("Metas"),
	"project":  i18n.N("Projetos"),
	"gate":     i18n.N("Gates"),
	"artifact": i18n.N("Artefatos"),
	"memory":   i18n.N("Memória"),
}

func (e *env) searchCmd() *cobra.Command {
	var types []string
	var limit int
	c := &cobra.Command{
		Use: "search " + e.t("<termo...>"), Short: e.t("Busca na empresa: tasks, comentários, personas, execuções, fluxos, metas, projetos, gates, artefatos e memória"),
		Long: e.t("Busca em tudo da empresa de uma vez. Várias palavras = todas; \"frase exata\"; -excluir; OR entre alternativas; a última palavra casa por prefixo. A chave exata de uma task (HNK-12) vai direto a ela."),
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var ts []string
			for _, t := range types {
				for _, p := range strings.Split(t, ",") {
					if p = strings.TrimSpace(p); p != "" {
						ts = append(ts, p)
					}
				}
			}
			r, err := e.client().Search(cmd.Context(), co, client.SearchQuery{Q: strings.Join(args, " "), Types: ts, Limit: limit})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			if len(r.Groups) == 0 {
				fmt.Fprintln(e.out, e.t("Nada encontrado."))
				return nil
			}
			for i, g := range r.Groups {
				if i > 0 {
					fmt.Fprintln(e.out)
				}
				e.printSearchGroup(g)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringSliceVar(&types, "type", nil, e.t("só estes tipos: task, comment, persona, run, flow, goal, project, gate, artifact, memory (repita ou separe por vírgula)"))
	f.IntVar(&limit, "limit", 10, e.t("máximo de resultados por tipo"))
	return c
}

func segText(segs []api.SearchSegment) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

func (e *env) printSearchGroup(g api.SearchGroup) {
	label := e.t(searchTypeLabels[g.Type]) // i18n:dyn
	if g.Unavailable {
		fmt.Fprintf(e.out, e.t("%s: indisponível agora\n"), label)
		return
	}
	more := ""
	if g.More {
		more = "+"
	}
	fmt.Fprintf(e.out, "%s (%d%s)\n", label, len(g.Hits), more)
	for _, h := range g.Hits {
		ref, title := h.Ref, oneLine(segText(h.Title), 80)
		switch g.Type {
		case "comment":
			ref, title = h.TaskKey, oneLine(h.TaskTitle, 60)+" · "+h.Author
		case "run":
			ref, title = segText(h.Title), h.TaskKey+" "+oneLine(h.TaskTitle, 60)
		case "memory":
			ref = ""
		}
		fmt.Fprintf(e.out, "  %-14s %s\n", oneLine(ref, 40), strings.TrimSpace(title))
		if sn := oneLine(segText(h.Snippet), 140); sn != "" {
			fmt.Fprintf(e.out, "  %-14s %s\n", "", sn)
		}
	}
}
