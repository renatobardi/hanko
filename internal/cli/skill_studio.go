package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/client"
)

// Versões, fontes e estúdio de skills (ADR-0035): subcomandos de `hanko skill`.

// skillTestPoll é o intervalo de consulta do `hanko skill test` enquanto espera (os testes encurtam).
var skillTestPoll = 2 * time.Second

func (e *env) skillStudioCmds() []*cobra.Command {
	return []*cobra.Command{e.skillVersionsCmd(), e.skillRestoreCmd(), e.skillPinCmd(), e.skillForkCmd(), e.skillSourceCmd(), e.skillTestCmd()}
}

func (e *env) skillVersionsCmd() *cobra.Command {
	var show, against int
	c := &cobra.Command{
		Use: "versions " + e.t("<skill>"), Short: e.t("Lista as versões de uma skill (ou mostra uma com --show)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if show > 0 {
				v, err := e.client().SkillVersion(cmd.Context(), co, args[0], show, against)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(v)
				}
				e.kv([2]string{e.t("Versão"), strconv.Itoa(v.Revision)}, [2]string{e.t("Nome"), v.Name}, [2]string{e.t("Descrição"), v.Description},
					[2]string{"sha256", v.SHA256}, [2]string{e.t("Commit da fonte"), v.SourceCommit}, [2]string{e.t("Criada"), e.fmtTime(v.CreatedAt)})
				if v.Diff != nil {
					fmt.Fprintf(e.out, "\n"+e.t("Diff da versão %d para a %d (+%d -%d):")+"\n", v.Diff.From, v.Diff.To, v.Diff.Added, v.Diff.Removed)
					for _, l := range v.Diff.Lines {
						mark := " "
						switch l.Op {
						case "insert":
							mark = "+"
						case "delete":
							mark = "-"
						}
						fmt.Fprintf(e.out, "%s %s\n", mark, l.Text)
					}
					return nil
				}
				fmt.Fprintf(e.out, "\n%s\n", v.Body)
				return nil
			}
			vs, err := e.client().SkillVersions(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(vs)
			}
			rows := make([][]string, 0, len(vs))
			for _, v := range vs {
				src := ""
				if v.SourceCommit != "" {
					src = v.SourceCommit[:min(12, len(v.SourceCommit))]
				}
				rows = append(rows, []string{strconv.Itoa(v.Revision), v.Name, fmt.Sprintf("%d B", v.BodyBytes), v.SHA256[:12], src, e.fmtTime(v.CreatedAt)})
			}
			e.table([]string{e.t("VERSÃO"), e.t("NOME"), e.t("CORPO"), "SHA256", e.t("COMMIT"), e.t("CRIADA")}, rows)
			return nil
		},
	}
	c.Flags().IntVar(&show, "show", 0, e.t("mostra esta versão, com o corpo"))
	c.Flags().IntVar(&against, "against", 0, e.t("com --show, mostra o diff desta versão para a pedida"))
	return c
}

func (e *env) skillRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use: "restore " + e.t("<skill> <versão>"), Short: e.t("Restaura o conteúdo de uma versão (cria uma versão nova)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rev, err := strconv.Atoi(args[1])
			if err != nil || rev < 1 {
				return usageError{errors.New(e.t("versão inválida: use um número a partir de 1"))}
			}
			k, err := e.client().RestoreSkill(cmd.Context(), co, args[0], rev)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(k)
			}
			fmt.Fprintf(e.out, e.t("Skill %s restaurada da versão %d: agora na versão %d\n"), k.Slug, rev, k.Version)
			return nil
		},
	}
}

func (e *env) skillPinCmd() *cobra.Command {
	var unpin bool
	c := &cobra.Command{
		Use: "pin " + e.t("<skill> <persona> [versão]"), Short: e.t("Fixa a versão de uma skill para uma persona (--unpin solta)"), Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			rev := 0
			switch {
			case unpin && len(args) == 3, !unpin && len(args) == 2:
				return usageError{errors.New(e.t("informe a versão, ou use --unpin sem versão"))}
			case !unpin:
				if rev, err = strconv.Atoi(args[2]); err != nil || rev < 1 {
					return usageError{errors.New(e.t("versão inválida: use um número a partir de 1"))}
				}
			}
			links, err := e.client().PersonaSkills(cmd.Context(), co, args[1])
			if err != nil {
				return err
			}
			enabled := false
			for _, l := range links {
				if l.Slug == args[0] || l.SkillID == args[0] {
					enabled = l.Enabled
				}
			}
			if err := e.client().PinPersonaSkill(cmd.Context(), co, args[1], args[0], enabled, rev); err != nil {
				return err
			}
			if unpin {
				fmt.Fprintf(e.out, e.t("Skill %s solta para %s: usa sempre a versão mais nova.\n"), args[0], args[1])
			} else {
				fmt.Fprintf(e.out, e.t("Skill %s fixada na versão %d para %s.\n"), args[0], rev, args[1])
			}
			return nil
		},
	}
	c.Flags().BoolVar(&unpin, "unpin", false, e.t("solta a versão fixada"))
	return c
}

func (e *env) sourceStatus(status, lastErr string, auto bool) string {
	switch status {
	case "ok":
		if !auto {
			return e.t("Atualização automática pausada")
		}
		return e.t("Sincronizada")
	case "paused":
		return e.t("Atualização automática pausada")
	case "error":
		return e.t("Erro:") + " " + oneLine(lastErr, 60)
	case "syncing":
		return e.t("Sincronizando")
	}
	return e.t("Nunca sincronizada")
}

func (e *env) skillSourceCmd() *cobra.Command {
	c := &cobra.Command{Use: "source", Short: e.t("Fontes git de skills (o operador liga com HANKO_SKILL_SOURCES)")}
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as fontes de skills"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ss, err := e.client().SkillSources(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ss)
			}
			if len(ss) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma fonte. Conecte uma com: hanko skill source add <url>"))
				return nil
			}
			rows := make([][]string, 0, len(ss))
			for _, s := range ss {
				commit := ""
				if s.LastCommit != "" {
					commit = s.LastCommit[:7]
				}
				rows = append(rows, []string{s.ID, s.Label, s.Ref, e.sourceStatus(s.Status, s.LastError, s.AutoUpdate), commit, fmt.Sprintf("%d/%d", s.Imported, s.Found)})
			}
			e.table([]string{"ID", e.t("FONTE"), "REF", e.t("ESTADO"), e.t("COMMIT"), e.t("SKILLS")}, rows)
			return nil
		},
	}

	var (
		ref, prefix               string
		excluded, selected        []string
		importNew, noAuto, dryRun bool
	)
	add := &cobra.Command{
		Use: "add " + e.t("<url>"), Short: e.t("Conecta uma fonte e importa as skills (--select escolhe; --dry-run só lista)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req := client.SkillSourceReq{URL: args[0], Ref: ref, PathPrefix: prefix, Excluded: excluded, ImportNew: importNew}
			if dryRun { // --dry-run: descobre sem gravar
				d, err := e.client().DiscoverSkillSource(cmd.Context(), co, req)
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(d)
				}
				rows := make([][]string, 0, len(d.Skills))
				for _, s := range d.Skills {
					note := s.Error
					switch {
					case note != "":
					case s.Conflict:
						note = e.t("já existe uma skill com este slug")
					case s.HasScripts:
						note = e.t("tem scripts — só o SKILL.md seria importado")
					}
					rows = append(rows, []string{s.Path, s.Slug, oneLine(s.Description, 50), note})
				}
				e.table([]string{e.t("CAMINHO"), "SLUG", e.t("DESCRIÇÃO"), e.t("OBSERVAÇÃO")}, rows)
				fmt.Fprintf(e.out, e.t("Commit %s: %d skill(s) encontrada(s).\n"), d.Commit[:min(7, len(d.Commit))], len(d.Skills))
				return nil
			}
			if noAuto {
				f := false
				req.AutoUpdate = &f
			}
			if cmd.Flags().Changed("select") {
				req.Select = selected
			}
			s, err := e.client().CreateSkillSource(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("Fonte conectada: %s (%s)\n"), s.Label, s.ID)
			if s.Sync != nil && s.Sync.Error != "" {
				fmt.Fprintf(e.out, e.t("A primeira sincronização falhou: %s\n"), s.Sync.Error)
			} else if s.Sync != nil {
				fmt.Fprintf(e.out, e.t("%d skill(s) importada(s).\n"), s.Sync.Imported)
			}
			return nil
		},
	}
	af := add.Flags()
	af.StringVar(&ref, "ref", "", e.t("branch acompanhado (padrão: HEAD)"))
	af.StringVar(&prefix, "path", "", e.t("só diretórios dentro deste caminho"))
	af.StringSliceVar(&excluded, "exclude", nil, e.t("caminhos ignorados (repita ou separe por vírgula)"))
	af.StringSliceVar(&selected, "select", nil, e.t("caminhos a importar (padrão: todos)"))
	af.BoolVar(&importNew, "import-new", false, e.t("skills novas na fonte entram sozinhas"))
	af.BoolVar(&noAuto, "no-auto-update", false, e.t("não sincroniza sozinha"))
	af.BoolVar(&dryRun, "dry-run", false, e.t("só lista as skills encontradas, sem gravar"))

	var force bool
	sync := &cobra.Command{
		Use: "sync " + e.t("<fonte>"), Short: e.t("Sincroniza uma fonte agora"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			r, err := e.client().SyncSkillSource(cmd.Context(), co, args[0], force)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			if r.Error != "" {
				return fmt.Errorf("%s %s", e.t("sincronização falhou:"), r.Error)
			}
			fmt.Fprintf(e.out, e.t("Sincronizada no commit %s: %d importada(s), %d atualizada(s), %d sem mudança, %d ignorada(s).\n"),
				r.Commit[:min(7, len(r.Commit))], r.Imported, r.Updated, r.Unchanged, r.Skipped)
			for _, n := range r.Notes {
				fmt.Fprintln(e.out, "- "+n)
			}
			return nil
		},
	}
	sync.Flags().BoolVar(&force, "force", false, e.t("reimporta e sobrescreve skills modificadas localmente"))

	var keep, del bool
	rm := &cobra.Command{
		Use: "rm " + e.t("<fonte>"), Aliases: []string{"disconnect"}, Short: e.t("Desconecta uma fonte (--keep mantém as skills, --delete-skills as apaga)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if keep == del {
				return usageError{errors.New(e.t("escolha --keep (manter as skills importadas) ou --delete-skills (apagá-las)"))}
			}
			if err := e.client().DeleteSkillSource(cmd.Context(), co, args[0], keep); err != nil {
				return err
			}
			if keep {
				fmt.Fprintln(e.out, e.t("Fonte desconectada; as skills importadas ficaram como skills locais."))
			} else {
				fmt.Fprintln(e.out, e.t("Fonte desconectada e skills importadas apagadas."))
			}
			return nil
		},
	}
	rm.Flags().BoolVar(&keep, "keep", false, e.t("mantém as skills importadas como skills locais"))
	rm.Flags().BoolVar(&del, "delete-skills", false, e.t("apaga as skills importadas"))

	c.AddCommand(ls, add, sync, rm)
	return c
}

func (e *env) skillTestCmd() *cobra.Command {
	var (
		persona, inputFile, text string
		revision                 int
		noWait                   bool
		timeout                  time.Duration
	)
	c := &cobra.Command{
		Use: "test " + e.t("<skill>"), Short: e.t("Roda um teste da skill com uma persona e imprime a saída (gasta orçamento)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if persona == "" {
				return usageError{errors.New(e.t("informe a persona com --persona"))}
			}
			if (inputFile == "") == (text == "") {
				return usageError{errors.New(e.t("informe a entrada com --input <arquivo> (- lê a entrada padrão) ou --text"))}
			}
			input := text
			if inputFile != "" {
				if input, err = e.readSkillSource(inputFile); err != nil {
					return err
				}
			}
			req := client.SkillTestReq{Persona: persona, Input: input}
			if revision > 0 {
				req.Revision = &revision
			}
			tr, err := e.client().RunSkillTest(cmd.Context(), co, args[0], req)
			if err != nil {
				return err
			}
			if noWait {
				if e.asJSON {
					return e.printJSON(tr)
				}
				fmt.Fprintf(e.out, e.t("Teste %s na fila (skill %s v%d, persona %s).\n"), tr.ID, tr.Skill, tr.Revision, tr.Persona)
				return nil
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			t := time.NewTicker(skillTestPoll)
			defer t.Stop()
			for tr.Status == "queued" || tr.Status == "running" {
				select {
				case <-ctx.Done():
					return fmt.Errorf("%s %s", e.t("tempo de espera esgotado; acompanhe com o id"), tr.ID)
				case <-t.C:
				}
				if tr, err = e.client().SkillTestRun(ctx, co, tr.ID); err != nil {
					return err
				}
			}
			if e.asJSON {
				return e.printJSON(tr)
			}
			if tr.Status != "done" {
				return fmt.Errorf(e.t("teste terminou como %s: %s"), tr.Status, tr.Error)
			}
			_, err = fmt.Fprintln(e.out, tr.Output)
			return err
		},
	}
	f := c.Flags()
	f.StringVar(&persona, "persona", "", e.t("persona que roda o teste (id ou slug)"))
	f.StringVar(&inputFile, "input", "", e.t("arquivo com a entrada (markdown); - lê a entrada padrão"))
	f.StringVar(&text, "text", "", e.t("entrada em texto livre"))
	f.IntVar(&revision, "revision", 0, e.t("versão da skill (padrão: a atual)"))
	f.BoolVar(&noWait, "no-wait", false, e.t("não espera o fim; imprime o id do teste"))
	f.DurationVar(&timeout, "timeout", 30*time.Minute, e.t("quanto esperar pelo fim do teste"))
	return c
}
