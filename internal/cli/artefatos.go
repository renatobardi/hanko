package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Artefatos e metas (ADR-0028).

// maxArtifactRead é o teto do que a CLI lê de um arquivo ou baixa: o maior limite que a instalação pode configurar.
const maxArtifactRead int64 = 16 << 20

func (e *env) artifactCmd() *cobra.Command {
	c := &cobra.Command{Use: "artifact", Aliases: []string{"artifacts"}, Short: e.t("Publica e consulta artefatos (arquivos com versões)")}

	var name, mime, task, runID, project string
	put := &cobra.Command{
		Use: "put " + e.t("<arquivo|->"), Short: e.t("Publica um arquivo como artefato (o mesmo nome na mesma task cria a versão seguinte)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var b []byte
			if args[0] == "-" {
				if name == "" {
					return usageError{errors.New(e.t("com entrada padrão (-), informe --name"))}
				}
				b, err = io.ReadAll(io.LimitReader(e.in, maxArtifactRead+1))
			} else {
				if name == "" {
					name = filepath.Base(args[0])
				}
				b, err = readLimited(args[0], maxArtifactRead)
			}
			if err != nil {
				return fmt.Errorf(e.t("ler %s: %w"), args[0], err)
			}
			if int64(len(b)) > maxArtifactRead {
				return fmt.Errorf(e.t("o arquivo passa de %d bytes"), maxArtifactRead)
			}
			a, err := e.client().PutArtifact(cmd.Context(), co, client.PutArtifactReq{Name: name, Mime: mime, Content: b, Task: task, RunID: runID, Project: project})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(a)
			}
			fmt.Fprintf(e.out, e.t("Artefato %s v%d publicado (%s, %s)\n"), a.Name, a.Version, a.Mime, humanBytes(a.SizeBytes))
			fmt.Fprintf(e.out, e.t("id: %s\n"), a.ID)
			return nil
		},
	}
	pf := put.Flags()
	pf.StringVar(&name, "name", "", e.t("nome do artefato (padrão: o nome do arquivo)"))
	pf.StringVar(&mime, "mime", "", e.t("tipo do conteúdo (padrão: deduzido do nome e do conteúdo)"))
	pf.StringVar(&task, "task", "", e.t("task a que pertence (id ou chave)"))
	pf.StringVar(&runID, "run", "", e.t("execução de origem (id)"))
	pf.StringVar(&project, "project", "", e.t("projeto (id ou slug); padrão: o da task"))

	var q client.ArtifactQuery
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista artefatos (a versão mais nova de cada um)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			as, err := e.client().Artifacts(cmd.Context(), co, q)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(as)
			}
			rows := make([][]string, 0, len(as))
			for _, a := range as {
				rows = append(rows, []string{short(a.ID), a.Name, "v" + strconv.Itoa(a.Version), a.Kind, humanBytes(a.SizeBytes),
					dash(a.TaskKey), dash(a.PersonaSlug), e.fmtTime(a.CreatedAt)})
			}
			e.table([]string{"ID", e.t("NOME"), e.t("VERSÃO"), e.t("TIPO"), e.t("TAMANHO"), "TASK", "PERSONA", e.t("CRIADO EM")}, rows)
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&q.Project, "project", "", e.t("filtra por projeto (id ou slug)"))
	lf.StringVar(&q.Task, "task", "", e.t("filtra por task (id ou chave)"))
	lf.StringVar(&q.Persona, "persona", "", e.t("filtra pela persona autora (id ou slug)"))
	lf.StringVar(&q.Run, "run", "", e.t("filtra por execução (id)"))
	lf.StringVar(&q.Kind, "kind", "", e.t("tipo: markdown, text, image ou other"))
	lf.StringVarP(&q.Q, "query", "q", "", e.t("busca pelo nome"))
	lf.BoolVar(&q.AllVersions, "all-versions", false, e.t("lista todas as versões, não só a mais nova"))
	lf.IntVar(&q.Limit, "limit", 0, e.t("máximo de itens"))

	var out string
	var showVersions bool
	get := &cobra.Command{
		Use: "get " + e.t("<id>"), Short: e.t("Baixa o conteúdo de uma versão (saída padrão ou -o arquivo); --info mostra os metadados e as versões"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cl := e.client()
			if showVersions {
				a, err := cl.Artifact(cmd.Context(), co, args[0])
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(a)
				}
				e.kv([2]string{e.t("Nome"), a.Name}, [2]string{e.t("Tipo"), a.Mime}, [2]string{e.t("Tamanho"), humanBytes(a.SizeBytes)},
					[2]string{e.t("Versão"), fmt.Sprintf("%d/%d", a.Version, a.Versions)}, [2]string{"Task", dash(a.TaskKey)},
					[2]string{e.t("Projeto"), dash(a.ProjectName)}, [2]string{"Persona", dash(a.PersonaName)}, [2]string{"SHA-256", a.SHA256},
					[2]string{e.t("Criado em"), e.fmtTime(a.CreatedAt)})
				rows := make([][]string, 0, len(a.History))
				for _, h := range a.History {
					rows = append(rows, []string{"v" + strconv.Itoa(h.Version), short(h.ID), humanBytes(h.SizeBytes), e.authorLabel(h.Author), e.fmtTime(h.CreatedAt)})
				}
				fmt.Fprintln(e.out)
				e.table([]string{e.t("VERSÃO"), "ID", e.t("TAMANHO"), e.t("AUTOR"), e.t("CRIADO EM")}, rows)
				return nil
			}
			b, err := cl.ArtifactContent(cmd.Context(), co, args[0], maxArtifactRead)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = e.out.Write(b)
				return err
			}
			if err := writeFileAtomic(out, b); err != nil {
				return fmt.Errorf(e.t("gravar %s: %w"), out, err)
			}
			fmt.Fprintf(e.errOut, e.t("gravado em %s (%s)\n"), out, humanBytes(int64(len(b))))
			return nil
		},
	}
	get.Flags().StringVarP(&out, "output", "o", "", e.t("grava neste arquivo (permissão 0600) em vez da saída padrão"))
	get.Flags().BoolVar(&showVersions, "info", false, e.t("mostra os metadados e o histórico de versões em vez do conteúdo"))

	var against string
	diff := &cobra.Command{
		Use: "diff " + e.t("<id>"), Short: e.t("Compara o texto de uma versão com a anterior (ou com --against)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			d, err := e.client().ArtifactDiff(cmd.Context(), co, args[0], against)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(d)
			}
			fmt.Fprintf(e.out, e.t("v%d → v%d: +%d −%d\n"), d.From, d.To, d.Added, d.Removed)
			for _, l := range d.Lines {
				switch l.Op {
				case "insert":
					fmt.Fprintf(e.out, "+ %s\n", l.Text)
				case "delete":
					fmt.Fprintf(e.out, "- %s\n", l.Text)
				}
			}
			return nil
		},
	}
	diff.Flags().StringVar(&against, "against", "", e.t("id da versão a comparar (padrão: a anterior)"))

	rm := &cobra.Command{
		Use: "rm " + e.t("<id>"), Aliases: []string{"delete"}, Short: e.t("Apaga o artefato com todas as versões (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteArtifact(cmd.Context(), co, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Artefato %s apagado.\n"), args[0])
			return nil
		},
	}

	usage := &cobra.Command{
		Use: "usage", Short: e.t("Mostra o espaço de artefatos usado e a cota da empresa"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			u, err := e.client().ArtifactUsage(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(u)
			}
			e.kv([2]string{e.t("Usado"), humanBytes(u.UsedBytes)}, [2]string{e.t("Cota"), humanBytes(u.QuotaBytes)},
				[2]string{e.t("Versões"), strconv.Itoa(u.Versions)}, [2]string{e.t("Máximo por arquivo"), humanBytes(u.MaxFileBytes)})
			return nil
		},
	}
	c.AddCommand(put, ls, get, diff, rm, usage)
	return c
}

// readLimited lê um arquivo local sem passar de max+1 bytes.
func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // o caminho é do próprio usuário da CLI
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max+1))
}

func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1f GiB", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	}
	return fmt.Sprintf("%d B", n)
}

// ---------- metas ----------

func fmtNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// goalLine resume o andamento: "42% (55 de 100 usuários)".
func (e *env) goalProgress(g api.Goal) string {
	unit := ""
	if g.Unit != "" {
		unit = " " + g.Unit
	}
	return fmt.Sprintf(e.t("%.0f%% (%s de %s%s)"), g.ProgressPercent, fmtNum(g.CurrentValue), fmtNum(g.TargetValue), unit)
}

// goalChain é a cadeia até a meta: "Visão › Meta-mãe › Esta"; "-" numa raiz sem filhos no caminho.
func (e *env) goalChain(g api.Goal) string {
	if len(g.Path) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(g.Path)+1)
	for _, p := range g.Path {
		parts = append(parts, p.Title)
	}
	return strings.Join(append(parts, g.Title), " › ")
}

func (e *env) goalStatus(s string) string {
	switch s {
	case "active":
		return e.t("ativa")
	case "achieved":
		return e.t("atingida")
	case "abandoned":
		return e.t("abandonada")
	}
	return s
}

func (e *env) goalCmd() *cobra.Command {
	c := &cobra.Command{Use: "goal", Aliases: []string{"goals"}, Short: e.t("Gerencia metas (objetivos mensuráveis)")}

	var slug, desc, metric, unit, due, project, owner, parent string
	var start, target, current float64
	create := &cobra.Command{
		Use: "create " + e.t("<título>"), Short: e.t("Cria uma meta: sai do valor inicial e deve chegar ao alvo (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("target") {
				return usageError{errors.New(e.t("informe o alvo com --target"))}
			}
			req := client.CreateGoalReq{Title: args[0], Slug: slug, Description: desc, Metric: metric, Unit: unit, Start: start, Target: target,
				DueDate: due, Project: project, Owner: owner, Parent: parent}
			if cmd.Flags().Changed("current") {
				req.Current = &current
			}
			g, err := e.client().CreateGoal(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Meta criada: %s (%s)\n"), g.Title, g.Slug)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&slug, "slug", "", e.t("slug (derivado do título se omitido)"))
	cf.StringVarP(&desc, "description", "d", "", e.t("descrição"))
	cf.StringVar(&metric, "metric", "", e.t("o que se mede (ex.: usuários ativos)"))
	cf.StringVar(&unit, "unit", "", e.t("unidade (ex.: usuários, ms, %)"))
	cf.Float64Var(&start, "start", 0, e.t("valor inicial"))
	cf.Float64Var(&target, "target", 0, e.t("valor a atingir (pode ser menor que o inicial)"))
	cf.Float64Var(&current, "current", 0, e.t("valor atual (padrão: o inicial)"))
	cf.StringVar(&due, "due", "", e.t("prazo, AAAA-MM-DD"))
	cf.StringVar(&project, "project", "", e.t("projeto (id ou slug)"))
	cf.StringVar(&owner, "owner", "", e.t("persona dona da meta (id ou slug)"))
	cf.StringVar(&parent, "parent", "", e.t("meta-pai (id ou slug): põe a meta na árvore, até 4 níveis"))

	var status, lproject string
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista metas com progresso"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			gs, err := e.client().Goals(cmd.Context(), co, status, lproject)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(gs)
			}
			rows := make([][]string, 0, len(gs))
			for _, g := range gs {
				due := "-"
				if g.DueDate != "" {
					due = g.DueDate
				}
				title := oneLine(g.Title, 40)
				if g.Depth > 1 { // submeta: recuo por nível (ADR-0042)
					title = strings.Repeat("  ", g.Depth-1) + "└ " + title
				}
				rows = append(rows, []string{g.Slug, title, e.goalStatus(g.Status), e.goalProgress(g), due, dash(g.ProjectSlug),
					strconv.Itoa(g.TasksTotal)})
			}
			e.table([]string{"SLUG", e.t("TÍTULO"), "STATUS", e.t("PROGRESSO"), e.t("PRAZO"), e.t("PROJETO"), "TASKS"}, rows)
			return nil
		},
	}
	ls.Flags().StringVar(&status, "status", "", e.t("filtra: active, achieved ou abandoned"))
	ls.Flags().StringVar(&lproject, "project", "", e.t("filtra por projeto (id ou slug)"))

	show := &cobra.Command{
		Use: "show " + e.t("<meta>"), Short: e.t("Mostra uma meta, as tasks ligadas e o histórico de progresso"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			g, err := e.client().Goal(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, "%s (%s)\n\n", g.Title, g.Slug)
			e.kv([2]string{"Status", e.goalStatus(g.Status)}, [2]string{e.t("Progresso"), e.goalProgress(g)},
				[2]string{e.t("Métrica"), dash(g.Metric)}, [2]string{e.t("Inicial"), fmtNum(g.StartValue)}, [2]string{e.t("Prazo"), dash(g.DueDate)},
				[2]string{e.t("Projeto"), dash(g.ProjectName)}, [2]string{e.t("Dona"), dash(g.OwnerName)},
				[2]string{"Tasks", fmt.Sprintf("%d", g.TasksTotal)}, [2]string{e.t("Cadeia"), e.goalChain(g)})
			if len(g.Children) > 0 {
				fmt.Fprintf(e.out, e.t("\nSubmetas (%d):\n"), len(g.Children))
				for _, c := range g.Children {
					fmt.Fprintf(e.out, "  %s — %s\n", c.Slug, oneLine(c.Title, 60))
				}
			}
			if strings.TrimSpace(g.Description) != "" {
				fmt.Fprintf(e.out, e.t("\nDescrição:\n%s\n"), strings.TrimSpace(g.Description))
			}
			if len(g.Updates) > 0 {
				fmt.Fprintf(e.out, e.t("\nHistórico (%d):\n"), len(g.Updates))
				for _, u := range g.Updates {
					note := ""
					if u.Note != "" {
						note = " — " + oneLine(u.Note, 120)
					}
					fmt.Fprintf(e.out, "  [%s] %s (%s)%s\n", e.fmtTime(u.CreatedAt), fmtNum(u.Value), e.authorLabel(u.Author), note)
				}
			}
			return nil
		},
	}

	var utitle, udesc, umetric, uunit, udue, uproject, uowner, ustatus, uparent string
	var ustart, utarget float64
	var clearDue, clearProject, clearOwner, clearParent bool
	update := &cobra.Command{
		Use: "update " + e.t("<meta>"), Short: e.t("Altera uma meta, reabre ou abandona (admin)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			req := client.UpdateGoalReq{DueDate: udue, ClearDue: clearDue, Project: uproject, ClearProject: clearProject, Owner: uowner, ClearOwner: clearOwner,
				Parent: uparent, ClearParent: clearParent}
			if f.Changed("title") {
				req.Title = &utitle
			}
			if f.Changed("description") {
				req.Description = &udesc
			}
			if f.Changed("metric") {
				req.Metric = &umetric
			}
			if f.Changed("unit") {
				req.Unit = &uunit
			}
			if f.Changed("start") {
				req.Start = &ustart
			}
			if f.Changed("target") {
				req.Target = &utarget
			}
			if f.Changed("status") {
				req.Status = &ustatus
			}
			g, err := e.client().UpdateGoal(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, e.t("Meta atualizada: %s (%s) — %s\n"), g.Title, g.Slug, e.goalStatus(g.Status))
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&utitle, "title", "", e.t("novo título"))
	uf.StringVarP(&udesc, "description", "d", "", e.t("nova descrição"))
	uf.StringVar(&umetric, "metric", "", e.t("o que se mede"))
	uf.StringVar(&uunit, "unit", "", e.t("unidade"))
	uf.Float64Var(&ustart, "start", 0, e.t("valor inicial"))
	uf.Float64Var(&utarget, "target", 0, e.t("alvo"))
	uf.StringVar(&udue, "due", "", e.t("prazo, AAAA-MM-DD"))
	uf.BoolVar(&clearDue, "clear-due", false, e.t("remove o prazo"))
	uf.StringVar(&uproject, "project", "", e.t("projeto (id ou slug)"))
	uf.BoolVar(&clearProject, "clear-project", false, e.t("tira a meta do projeto"))
	uf.StringVar(&uowner, "owner", "", e.t("persona dona (id ou slug)"))
	uf.BoolVar(&clearOwner, "clear-owner", false, e.t("remove a dona"))
	uf.StringVar(&ustatus, "status", "", e.t("active (reabre) ou abandoned"))
	uf.StringVar(&uparent, "parent", "", e.t("nova meta-pai (id ou slug)"))
	uf.BoolVar(&clearParent, "clear-parent", false, e.t("torna a meta uma raiz"))

	var note string
	progress := &cobra.Command{
		Use: "progress " + e.t("<meta>") + " " + e.t("<valor>"), Short: e.t("Registra o valor atual da meta; ao cruzar o alvo ela é atingida"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := strconv.ParseFloat(strings.ReplaceAll(args[1], ",", "."), 64)
			if err != nil {
				return usageError{fmt.Errorf(e.t("valor inválido: %q"), args[1])}
			}
			g, err := e.client().RecordGoalProgress(cmd.Context(), co, args[0], v, note)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(g)
			}
			fmt.Fprintf(e.out, "%s: %s\n", g.Slug, e.goalProgress(g))
			if g.Status == "achieved" {
				fmt.Fprintln(e.out, e.t("Meta atingida."))
			}
			return nil
		},
	}
	progress.Flags().StringVar(&note, "note", "", e.t("nota sobre este registro"))

	c.AddCommand(create, ls, show, update, progress)
	return c
}
