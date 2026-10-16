package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/i18n"
)

// Pipelines, cases, fila de revisão, aprendizados e exemplos de decisão (ADR-0034).

// maxPipelineFile é o teto do arquivo JSON de um pipeline.
const maxPipelineFile = 1 << 20

// maxExamplesExport é o teto do JSONL baixado pela CLI.
const maxExamplesExport int64 = 256 << 20

func (e *env) pipelinesCmds() []*cobra.Command {
	return []*cobra.Command{e.pipelineCmd(), e.caseCmd(), e.reviewCmd(), e.learningsCmd(), e.decisionExampleCmd()}
}

// compact tira os textos vazios do corpo (a API recusa enum vazio).
func compact(m map[string]any) map[string]any {
	for k, v := range m {
		if s, ok := v.(string); ok && s == "" {
			delete(m, k)
		}
	}
	return m
}

// parseFields lê pares chave=valor; números viram número, true/false viram booleano.
func parseFields(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, fmt.Errorf("campo %q: use chave=valor", p)
		}
		switch {
		case v == "true" || v == "false":
			out[k] = v == "true"
		case v == "":
			out[k] = nil
		default:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				out[k] = f
			} else {
				out[k] = v
			}
		}
	}
	return out, nil
}

func (e *env) caseLine(c api.Case) []string {
	where := dash(c.StageName)
	if c.PipelineKey != "" {
		where = c.PipelineKey + "/" + c.StageKey
	}
	return []string{c.Identifier, oneLine(c.Title, 60), dash(c.CaseType), c.Status, where, "v" + strconv.Itoa(c.Version), e.fmtTime(c.UpdatedAt)}
}

func (e *env) caseTable(cs []api.Case) {
	rows := make([][]string, 0, len(cs))
	for _, c := range cs {
		rows = append(rows, e.caseLine(c))
	}
	e.table([]string{"CASE", e.t("TÍTULO"), e.t("TIPO"), "STATUS", e.t("ETAPA"), e.t("VERSÃO"), e.t("ATUALIZADO")}, rows)
}

func (e *env) pipelineCmd() *cobra.Command {
	c := &cobra.Command{Use: "pipeline", Aliases: []string{"pipelines"}, Short: e.t("Pipelines: etapas com aprovação e automação por persona")}

	var archived bool
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os pipelines"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ps, err := e.client().Pipelines(cmd.Context(), co, archived)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ps)
			}
			rows := make([][]string, 0, len(ps))
			for _, p := range ps {
				st := e.t("ativo")
				if p.Archived {
					st = e.t("arquivado")
				}
				rows = append(rows, []string{p.Key, p.Name, strconv.Itoa(len(p.Stages)), strconv.Itoa(p.OpenCases), strconv.Itoa(p.InReview), st})
			}
			e.table([]string{"KEY", e.t("NOME"), e.t("ETAPAS"), e.t("ABERTOS"), e.t("EM REVISÃO"), "STATUS"}, rows)
			return nil
		},
	}
	ls.Flags().BoolVar(&archived, "archived", false, e.t("inclui os arquivados"))

	show := &cobra.Command{
		Use: "show " + e.t("<pipeline>"), Short: e.t("Mostra as etapas e as transições de um pipeline"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().Pipeline(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			e.kv([2]string{e.t("Pipeline"), p.Name + " (" + p.Key + ")"}, [2]string{e.t("Modo estrito"), e.yesNo(p.EnforceTransitions)},
				[2]string{e.t("Cases abertos"), strconv.Itoa(p.OpenCases)}, [2]string{e.t("Em revisão"), strconv.Itoa(p.InReview)})
			rows := make([][]string, 0, len(p.Stages))
			for _, st := range p.Stages {
				var notes []string
				if st.Config.Approval != nil && st.Config.Approval.Required {
					notes = append(notes, e.t("aprovação"))
				}
				if st.Config.Automation != nil && st.Config.Automation.PersonaID != "" {
					notes = append(notes, e.t("automação"))
				}
				if st.Config.Disabled {
					notes = append(notes, e.t("desligada"))
				}
				if r := st.Config.Review; r != nil {
					notes = append(notes, "→ "+r.ApproveTo+" | "+r.RejectTo)
				}
				rows = append(rows, []string{strconv.Itoa(st.Position + 1), st.Key, st.Name, st.Kind, dash(strings.Join(notes, ", "))})
			}
			fmt.Fprintln(e.out)
			e.table([]string{"#", "KEY", e.t("NOME"), e.t("TIPO"), e.t("REGRAS")}, rows)
			if len(p.Transitions) > 0 {
				fmt.Fprintln(e.out)
				for _, t := range p.Transitions {
					fmt.Fprintf(e.out, "%s → %s %s\n", t.From, t.To, t.Label)
				}
			}
			return nil
		},
	}

	var name, key, desc, file string
	var stages []string
	var enforce bool
	create := &cobra.Command{
		Use: "create", Short: e.t("Cria um pipeline (--stage key=Nome:tipo, na ordem, ou --file com o JSON da API)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var body map[string]any
			if file != "" {
				b, err := readLimited(file, maxPipelineFile)
				if err != nil {
					return fmt.Errorf(e.t("ler %s: %w"), file, err)
				}
				if err := json.Unmarshal(b, &body); err != nil {
					return usageError{fmt.Errorf("%s: %w", file, err)}
				}
			} else {
				if name == "" {
					return usageError{errors.New(e.t("informe --name ou --file"))}
				}
				body = map[string]any{"name": name, "key": key, "description": desc, "enforce_transitions": enforce}
				var ss []map[string]any
				for _, s := range stages {
					k, rest, ok := strings.Cut(s, "=")
					if !ok {
						return usageError{fmt.Errorf(e.t("etapa %q: use key=Nome:tipo"), s)}
					}
					n, kind, _ := strings.Cut(rest, ":")
					ss = append(ss, compact(map[string]any{"key": k, "name": n, "kind": kind}))
				}
				body["stages"] = ss
			}
			p, err := e.client().CreatePipeline(cmd.Context(), co, compact(body))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Pipeline %s criado com %d etapa(s).\n"), p.Key, len(p.Stages))
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&name, "name", "", e.t("nome do pipeline"))
	cf.StringVar(&key, "key", "", e.t("key (padrão: derivada do nome)"))
	cf.StringVar(&desc, "description", "", e.t("descrição"))
	cf.StringArrayVar(&stages, "stage", nil, e.t("etapa key=Nome:tipo (working, review, done, cancelled); repita na ordem"))
	cf.BoolVar(&enforce, "strict", false, e.t("liga o modo estrito"))
	cf.StringVar(&file, "file", "", e.t("arquivo JSON com o corpo da API (etapas com configuração completa)"))

	archive := func(use string, v bool, msg string) *cobra.Command {
		return &cobra.Command{
			Use: use + " " + e.t("<pipeline>"), Short: e.t(msg), Args: cobra.ExactArgs(1), // i18n:dyn
			RunE: func(cmd *cobra.Command, args []string) error {
				co, err := e.needCompany()
				if err != nil {
					return err
				}
				p, err := e.client().UpdatePipeline(cmd.Context(), co, args[0], map[string]any{"archived": v})
				if err != nil {
					return err
				}
				if e.asJSON {
					return e.printJSON(p)
				}
				fmt.Fprintln(e.out, e.t(msg)+": "+p.Key) // i18n:dyn
				return nil
			},
		}
	}

	stage := &cobra.Command{Use: "stage", Short: e.t("Etapas de um pipeline")}
	var sName, sKind, sKey, sConfig string
	var sPos int
	add := &cobra.Command{
		Use: "add " + e.t("<pipeline>"), Short: e.t("Acrescenta uma etapa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			body := map[string]any{"name": sName, "key": sKey, "kind": sKind}
			if sPos > 0 {
				body["position"] = sPos - 1
			}
			if sConfig != "" {
				b, err := readLimited(sConfig, maxPipelineFile)
				if err != nil {
					return fmt.Errorf(e.t("ler %s: %w"), sConfig, err)
				}
				var cfg map[string]any
				if err := json.Unmarshal(b, &cfg); err != nil {
					return usageError{fmt.Errorf("%s: %w", sConfig, err)}
				}
				body["config"] = cfg
			}
			p, err := e.client().AddStage(cmd.Context(), co, args[0], compact(body))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Pipeline %s: %d etapa(s).\n"), p.Key, len(p.Stages))
			return nil
		},
	}
	af := add.Flags()
	af.StringVar(&sName, "name", "", e.t("nome da etapa"))
	af.StringVar(&sKey, "key", "", e.t("key (padrão: derivada do nome)"))
	af.StringVar(&sKind, "kind", "working", e.t("tipo: working, review, done ou cancelled"))
	af.IntVar(&sPos, "position", 0, e.t("posição (1 = primeira; padrão: no fim)"))
	af.StringVar(&sConfig, "config", "", e.t("arquivo JSON com a configuração da etapa"))
	_ = add.MarkFlagRequired("name")
	rm := &cobra.Command{
		Use: "rm " + e.t("<pipeline> <etapa>"), Short: e.t("Remove uma etapa sem cases"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().DeleteStage(cmd.Context(), co, args[0], args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Pipeline %s: %d etapa(s).\n"), p.Key, len(p.Stages))
			return nil
		},
	}
	order := &cobra.Command{
		Use: "order " + e.t("<pipeline> <etapa>..."), Short: e.t("Reordena as etapas (todas as keys, na ordem nova)"), Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			p, err := e.client().ReorderStages(cmd.Context(), co, args[0], args[1:])
			if err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Pipeline %s: %d etapa(s).\n"), p.Key, len(p.Stages))
			return nil
		},
	}
	stage.AddCommand(add, rm, order)

	var edges []string
	var strict bool
	trans := &cobra.Command{
		Use: "transitions " + e.t("<pipeline>"), Short: e.t("Troca as transições permitidas (--edge de:para) e o modo estrito"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var ts []api.PipelineTransition
			for _, ed := range edges {
				f, t, ok := strings.Cut(ed, ":")
				if !ok {
					return usageError{fmt.Errorf(e.t("transição %q: use de:para"), ed)}
				}
				ts = append(ts, api.PipelineTransition{From: f, To: t})
			}
			p, err := e.client().SetTransitions(cmd.Context(), co, args[0], ts, strict)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(p)
			}
			fmt.Fprintf(e.out, e.t("Pipeline %s: %d transição(ões), modo estrito %s.\n"), p.Key, len(p.Transitions), e.yesNo(p.EnforceTransitions))
			return nil
		},
	}
	trans.Flags().StringArrayVar(&edges, "edge", nil, e.t("transição de:para (keys); repita"))
	trans.Flags().BoolVar(&strict, "strict", false, e.t("liga o modo estrito"))

	c.AddCommand(ls, show, create, archive("archive", true, i18n.N("Pipeline arquivado")), archive("restore", false, i18n.N("Pipeline reativado")), stage, trans)
	return c
}

// currentVersion lê a versão do case quando --version não foi informada.
func (e *env) currentVersion(cmd *cobra.Command, co, ref string, v int) (int, error) {
	if v > 0 {
		return v, nil
	}
	c, err := e.client().Case(cmd.Context(), co, ref)
	if err != nil {
		return 0, err
	}
	return c.Version, nil
}

func (e *env) caseCmd() *cobra.Command {
	c := &cobra.Command{Use: "case", Aliases: []string{"cases"}, Short: e.t("Cases: registros estruturados de trabalho")}

	var typ, status, pipeline, stage, project, q, parent string
	var roots bool
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista cases"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v := url.Values{}
			for k, val := range map[string]string{"type": typ, "status": status, "pipeline": pipeline, "stage": stage, "project": project, "q": q, "parent": parent} {
				if val != "" {
					v.Set(k, val)
				}
			}
			if roots {
				v.Set("roots", "true")
			}
			cs, err := e.client().Cases(cmd.Context(), co, v)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			e.caseTable(cs)
			return nil
		},
	}
	lf := ls.Flags()
	lf.StringVar(&typ, "type", "", e.t("filtra pelo tipo"))
	lf.StringVar(&status, "status", "", e.t("status separados por vírgula"))
	lf.StringVar(&pipeline, "pipeline", "", e.t("filtra pelo pipeline"))
	lf.StringVar(&stage, "stage", "", e.t("filtra pela etapa (com --pipeline)"))
	lf.StringVar(&project, "project", "", e.t("filtra por projeto (id ou slug)"))
	lf.StringVar(&parent, "parent", "", e.t("só os filhos deste case"))
	lf.StringVarP(&q, "query", "q", "", e.t("busca no título, no resumo e na key"))
	lf.BoolVar(&roots, "roots", false, e.t("só os cases sem pai"))

	show := &cobra.Command{
		Use: "show " + e.t("<case>"), Short: e.t("Mostra um case com campos, tasks e histórico"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			cs, err := e.client().Case(cmd.Context(), co, args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			where := e.t("solto")
			if cs.PipelineKey != "" {
				where = cs.PipelineName + " › " + cs.StageName
			}
			e.kv([2]string{"Case", cs.Identifier + " — " + cs.Title}, [2]string{"Status", cs.Status}, [2]string{e.t("Etapa"), where},
				[2]string{e.t("Tipo"), dash(cs.CaseType)}, [2]string{"Key", dash(cs.Key)}, [2]string{e.t("Versão"), strconv.Itoa(cs.Version)})
			if len(cs.Fields) > 0 {
				fmt.Fprintln(e.out)
				keys := make([]string, 0, len(cs.Fields))
				for k := range cs.Fields {
					keys = append(keys, k)
				}
				sortStrings(keys)
				for _, k := range keys {
					fmt.Fprintf(e.out, "%s: %v\n", k, cs.Fields[k])
				}
			}
			if cs.Summary != "" {
				fmt.Fprintln(e.out)
				fmt.Fprintln(e.out, cs.Summary)
			}
			for _, l := range cs.Links {
				fmt.Fprintf(e.out, "task %s (%s) %s — %s\n", l.TaskKey, l.Role, l.TaskStatus, oneLine(l.TaskTitle, 60))
			}
			if len(cs.Events) > 0 {
				fmt.Fprintln(e.out)
				for _, ev := range cs.Events {
					line := e.fmtTime(ev.CreatedAt) + "  " + ev.Kind
					if r, _ := ev.Payload["reason"].(string); r != "" {
						line += " — " + oneLine(r, 80)
					}
					fmt.Fprintln(e.out, line)
				}
			}
			return nil
		},
	}

	var title, ctype, key, summary, cStatus, cPipeline, cStage, cParent, cProject string
	var fields []string
	create := &cobra.Command{
		Use: "create", Short: e.t("Cria um case solto ou num pipeline (com --key, repete = atualiza)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			fs, err := parseFields(fields)
			if err != nil {
				return usageError{err}
			}
			body := map[string]any{"title": title, "case_type": ctype, "key": key, "summary": summary, "status": cStatus, "pipeline": cPipeline,
				"stage": cStage, "parent": cParent, "project": cProject, "fields": fs}
			cs, err := e.client().CreateCase(cmd.Context(), co, compact(body))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			if cs.Created != nil && !*cs.Created {
				fmt.Fprintf(e.out, e.t("Case %s atualizado (versão %d).\n"), cs.Identifier, cs.Version)
				return nil
			}
			fmt.Fprintf(e.out, e.t("Case %s criado.\n"), cs.Identifier)
			return nil
		},
	}
	crf := create.Flags()
	crf.StringVar(&title, "title", "", e.t("título"))
	crf.StringVar(&ctype, "type", "", e.t("tipo (ex. lead)"))
	crf.StringVar(&key, "key", "", e.t("chave externa (única por tipo)"))
	crf.StringVar(&summary, "summary", "", e.t("resumo em markdown"))
	crf.StringVar(&cStatus, "status", "", e.t("status (só case solto)"))
	crf.StringVar(&cPipeline, "pipeline", "", e.t("pipeline (id ou key)"))
	crf.StringVar(&cStage, "stage", "", e.t("etapa inicial (key)"))
	crf.StringVar(&cParent, "parent", "", e.t("case pai"))
	crf.StringVar(&cProject, "project", "", e.t("projeto (id ou slug)"))
	crf.StringArrayVar(&fields, "field", nil, e.t("campo chave=valor; repita"))
	_ = create.MarkFlagRequired("title")

	var uTitle, uSummary, uStatus string
	var uFields []string
	var uVersion int
	update := &cobra.Command{
		Use: "update " + e.t("<case>"), Short: e.t("Atualiza título, resumo, campos ou status (case solto)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			body := map[string]any{}
			if cmd.Flags().Changed("title") {
				body["title"] = uTitle
			}
			if cmd.Flags().Changed("summary") {
				body["summary"] = uSummary
			}
			if uStatus != "" {
				body["status"] = uStatus
			}
			if len(uFields) > 0 {
				fs, err := parseFields(uFields)
				if err != nil {
					return usageError{err}
				}
				body["fields"] = fs
			}
			if len(body) == 0 {
				return usageError{errors.New(e.t("informe o que alterar"))}
			}
			v, err := e.currentVersion(cmd, co, args[0], uVersion)
			if err != nil {
				return err
			}
			body["expected_version"] = v
			cs, err := e.client().UpdateCase(cmd.Context(), co, args[0], body)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			fmt.Fprintf(e.out, e.t("Case %s atualizado (versão %d).\n"), cs.Identifier, cs.Version)
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&uTitle, "title", "", e.t("título"))
	uf.StringVar(&uSummary, "summary", "", e.t("resumo em markdown"))
	uf.StringVar(&uStatus, "status", "", e.t("status (só case solto)"))
	uf.StringArrayVar(&uFields, "field", nil, e.t("campo chave=valor (valor vazio apaga); repita"))
	uf.IntVar(&uVersion, "version", 0, e.t("versão que você leu (padrão: a atual)"))

	var force bool
	var reason string
	var mVersion int
	move := &cobra.Command{
		Use: "move " + e.t("<case> <etapa>"), Short: e.t("Move o case para outra etapa"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.currentVersion(cmd, co, args[0], mVersion)
			if err != nil {
				return err
			}
			cs, err := e.client().TransitionCase(cmd.Context(), co, args[0], compact(map[string]any{"expected_version": v, "to_stage": args[1], "force": force, "reason": reason}))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			fmt.Fprintf(e.out, e.t("Case %s em %s.\n"), cs.Identifier, cs.StageName)
			return nil
		},
	}
	move.Flags().BoolVar(&force, "force", false, e.t("passa por cima do modo estrito (exige --reason)"))
	move.Flags().StringVar(&reason, "reason", "", e.t("motivo"))
	move.Flags().IntVar(&mVersion, "version", 0, e.t("versão que você leu (padrão: a atual)"))

	c.AddCommand(ls, show, create, update, move, e.reviewDecisionCmd("review "+e.t("<case> <approve|reject|changes>"), ""))
	return c
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// reviewDecisionCmd decide a revisão de um case. Com decision vazio, a decisão é o segundo argumento.
func (e *env) reviewDecisionCmd(use, decision string) *cobra.Command {
	var reason string
	var version int
	nargs := 1
	if decision == "" {
		nargs = 2
	}
	cmd := &cobra.Command{
		Use: use, Short: e.t("Decide a revisão de um case"), Args: cobra.ExactArgs(nargs),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			d := decision
			if d == "" {
				d = args[1]
			}
			switch d {
			case "approve", "reject":
			case "changes", "request_changes":
				d = "request_changes"
			default:
				return usageError{errors.New(e.t("decisão: approve, reject ou changes"))}
			}
			v, err := e.currentVersion(cmd, co, args[0], version)
			if err != nil {
				return err
			}
			cs, err := e.client().ReviewCase(cmd.Context(), co, args[0], compact(map[string]any{"expected_version": v, "decision": d, "reason": reason}))
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(cs)
			}
			fmt.Fprintf(e.out, e.t("Case %s em %s.\n"), cs.Identifier, cs.StageName)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", e.t("motivo (obrigatório para recusar e pedir mudanças)"))
	cmd.Flags().IntVar(&version, "version", 0, e.t("versão que você leu (padrão: a atual)"))
	return cmd
}

func (e *env) reviewCmd() *cobra.Command {
	c := &cobra.Command{Use: "review", Short: e.t("Fila de revisão: o que espera decisão humana")}
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista a fila de revisão"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			items, err := e.client().ReviewQueue(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(items)
			}
			if len(items) == 0 {
				fmt.Fprintln(e.out, e.t("Nada precisa de você agora."))
				return nil
			}
			rows := make([][]string, 0, len(items))
			for _, it := range items {
				rows = append(rows, []string{it.Kind, it.Case.Identifier, oneLine(it.Case.Title, 50), dash(it.Case.PipelineKey), "v" + strconv.Itoa(it.Case.Version),
					oneLine(it.Reason, 50)})
			}
			e.table([]string{e.t("TIPO"), "CASE", e.t("TÍTULO"), "PIPELINE", e.t("VERSÃO"), e.t("MOTIVO")}, rows)
			return nil
		},
	}
	var reason string
	approve := &cobra.Command{
		Use: "approve " + e.t("<case>..."), Short: e.t("Aprova um ou mais cases (cada um na versão atual)"), Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			var items []map[string]any
			for _, ref := range args {
				cs, err := e.client().Case(cmd.Context(), co, ref)
				if err != nil {
					return err
				}
				items = append(items, compact(map[string]any{"case": cs.ID, "expected_version": cs.Version, "reason": reason}))
			}
			res, err := e.client().BulkApprove(cmd.Context(), co, items)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(res)
			}
			for i, r := range res.Results {
				if r.OK {
					fmt.Fprintf(e.out, e.t("%s aprovado.\n"), args[i])
				} else {
					fmt.Fprintf(e.out, e.t("%s não aprovado: %s\n"), args[i], r.Error)
				}
			}
			if res.Failed > 0 {
				return errors.New(e.t("Alguns itens não puderam ser aprovados."))
			}
			return nil
		},
	}
	approve.Flags().StringVar(&reason, "reason", "", e.t("motivo"))
	c.AddCommand(ls, approve, e.reviewDecisionCmd("reject "+e.t("<case>"), "reject"), e.reviewDecisionCmd("changes "+e.t("<case>"), "request_changes"))
	return c
}

func (e *env) learningsCmd() *cobra.Command {
	var pipeline, stage, decision string
	c := &cobra.Command{
		Use: "learnings", Short: e.t("O que as decisões de revisão ensinaram (mais recentes primeiro)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v := url.Values{"limit": {"500"}}
			for k, val := range map[string]string{"pipeline": pipeline, "stage": stage, "decision": decision} {
				if val != "" {
					v.Set(k, val)
				}
			}
			ls, err := e.client().Learnings(cmd.Context(), co, v)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ls)
			}
			rows := make([][]string, 0, len(ls))
			for _, l := range ls {
				rows = append(rows, []string{e.fmtTime(l.At), l.Decision, l.CaseIdentifier, oneLine(l.CaseTitle, 40), l.FromName + " → " + l.ToName,
					dash(l.ActorName), oneLine(l.Reason, 60)})
			}
			e.table([]string{e.t("QUANDO"), e.t("DECISÃO"), "CASE", e.t("TÍTULO"), e.t("ETAPAS"), e.t("QUEM"), e.t("MOTIVO")}, rows)
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&pipeline, "pipeline", "", e.t("filtra pelo pipeline"))
	f.StringVar(&stage, "stage", "", e.t("filtra pela etapa (com --pipeline)"))
	f.StringVar(&decision, "decision", "", e.t("approve, reject, request_changes ou forced"))
	return c
}

func (e *env) decisionExampleCmd() *cobra.Command {
	c := &cobra.Command{Use: "decision-example", Aliases: []string{"decision-examples"}, Short: e.t("Exemplos de decisão para avaliação e treino")}
	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os exemplos de decisão"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			xs, err := e.client().DecisionExamples(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(xs)
			}
			rows := make([][]string, 0, len(xs))
			for _, x := range xs {
				rows = append(rows, []string{short(x.ID), x.SourceKind, x.DecisionOutcome, e.fmtTime(x.CutoffAt), oneLine(x.Notes, 60)})
			}
			e.table([]string{"ID", e.t("ORIGEM"), e.t("DECISÃO"), e.t("CORTE"), e.t("NOTAS")}, rows)
			return nil
		},
	}
	var out string
	exp := &cobra.Command{
		Use: "export", Short: e.t("Exporta os exemplos em JSONL (admin; saída padrão ou -o arquivo)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			b, err := e.client().ExportDecisionExamples(cmd.Context(), co, maxExamplesExport)
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
	exp.Flags().StringVarP(&out, "output", "o", "", e.t("grava neste arquivo (permissão 0600) em vez da saída padrão"))
	c.AddCommand(ls, exp)
	return c
}
