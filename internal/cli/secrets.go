package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
)

// BlobsMigrateArgs são os parâmetros de `hanko blobs migrate`.
type BlobsMigrateArgs struct {
	From, To   string
	Company    string // slug ou id; vazio = todas
	KeepSource bool
	DryRun     bool
	Batch      int
}

// Segredos (ADR-0050): hanko secret ...

func (e *env) secretLine(s api.Secret) []string {
	used := "-"
	if len(s.UsedBy) > 0 {
		parts := make([]string, len(s.UsedBy))
		for i, u := range s.UsedBy {
			parts[i] = u.Kind + ":" + u.Slug
		}
		used = strings.Join(parts, ",")
	}
	revealed := e.t("não")
	if s.Revealed {
		revealed = e.t("sim")
	}
	last := "-"
	if s.LastUsedAt != nil {
		last = e.fmtTime(*s.LastUsedAt)
	}
	return []string{s.Name, s.Provider, fmt.Sprint(s.Version), revealed, last, oneLine(used, 40)}
}

// showRevealed imprime o valor entregue pelo servidor (criação/rotação gerada ou revelar), que não pode ser lido de novo.
func (e *env) showRevealed(s api.Secret) {
	if s.Value == "" {
		return
	}
	fmt.Fprintf(e.out, e.t("Valor (mostrado só agora; guarde-o): %s\n"), s.Value)
}

// secretValue lê o valor de --value, --value-stdin ou --value-file-like pelo stdin; stdin evita o histórico do shell.
func (e *env) secretValue(value string, fromStdin bool) (string, error) {
	switch {
	case value != "" && fromStdin:
		return "", usageError{errors.New(e.t("use --value ou --value-stdin, não os dois"))}
	case fromStdin:
		b, err := io.ReadAll(io.LimitReader(e.in, 16<<10))
		if err != nil {
			return "", fmt.Errorf(e.t("ler stdin: %w"), err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return value, nil
}

func (e *env) secretCmd() *cobra.Command {
	c := &cobra.Command{Use: "secret", Aliases: []string{"secrets"},
		Short: e.t("Segredos da empresa (cifrados; referenciados por ${secret:NOME})")}
	var user bool
	c.PersistentFlags().BoolVar(&user, "user", false, e.t("operar nos segredos do seu usuário (só você vê e usa)"))

	var (
		value, desc, vaultRef string
		fromStdin, generate   bool
	)
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cria um segredo (--value-stdin, --generate ou --vault-ref)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.secretValue(value, fromStdin)
			if err != nil {
				return err
			}
			req := client.CreateSecretReq{Name: a[0], Description: desc, Value: v, Generate: generate}
			if vaultRef != "" {
				req.Provider, req.ExternalRef = "vault", vaultRef
			}
			s, err := e.client().CreateSecret(cmd.Context(), co, user, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("Segredo criado: %s (use %s)\n"), s.Name, s.Reference)
			e.showRevealed(s)
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&value, "value", "", e.t("valor (prefira --value-stdin: o valor não fica no histórico do shell)"))
	cf.BoolVar(&fromStdin, "value-stdin", false, e.t("lê o valor do stdin"))
	cf.BoolVar(&generate, "generate", false, e.t("o servidor gera o valor e o mostra uma vez"))
	cf.StringVar(&vaultRef, "vault-ref", "", e.t("segredo no cofre externo: caminho#campo (o valor não passa pelo Hanko)"))
	cf.StringVar(&desc, "description", "", e.t("descrição"))

	ls := &cobra.Command{
		Use: "ls", Aliases: []string{"list"}, Short: e.t("Lista os segredos (nomes e metadados; nunca valores)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			l, err := e.client().ListSecrets(cmd.Context(), co, user)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum segredo."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, s := range l {
				rows[i] = e.secretLine(s)
			}
			e.table([]string{e.t("NOME"), e.t("PROVIDER"), e.t("VERSÃO"), e.t("REVELADO"), e.t("ÚLTIMO USO"), e.t("USADO POR")}, rows)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<nome>"), Short: e.t("Mostra os metadados de um segredo (nunca o valor)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			s, err := e.client().GetSecret(cmd.Context(), co, user, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			e.table([]string{e.t("NOME"), e.t("PROVIDER"), e.t("VERSÃO"), e.t("REVELADO"), e.t("ÚLTIMO USO"), e.t("USADO POR")}, [][]string{e.secretLine(s)})
			fmt.Fprintf(e.out, e.t("Referência: %s\n"), s.Reference)
			if s.ExternalRef != "" {
				fmt.Fprintf(e.out, e.t("Cofre: %s\n"), s.ExternalRef)
			}
			return nil
		},
	}

	var rValue, rVault, rDesc string
	var rStdin, rGen bool
	rotate := &cobra.Command{
		Use: "rotate " + e.t("<nome>"), Short: e.t("Troca o valor de um segredo (a versão sobe)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.secretValue(rValue, rStdin)
			if err != nil {
				return err
			}
			req := client.RotateSecretReq{Value: v, Generate: rGen}
			if rVault != "" {
				req.Provider, req.ExternalRef = "vault", rVault
			}
			if cmd.Flags().Changed("description") {
				req.Description = &rDesc
			}
			s, err := e.client().RotateSecret(cmd.Context(), co, user, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("Segredo %s rotacionado (versão %d).\n"), s.Name, s.Version)
			e.showRevealed(s)
			return nil
		},
	}
	rf := rotate.Flags()
	rf.StringVar(&rValue, "value", "", e.t("novo valor (prefira --value-stdin)"))
	rf.BoolVar(&rStdin, "value-stdin", false, e.t("lê o novo valor do stdin"))
	rf.BoolVar(&rGen, "generate", false, e.t("o servidor gera o novo valor e o mostra uma vez"))
	rf.StringVar(&rVault, "vault-ref", "", e.t("nova referência no cofre externo: caminho#campo"))
	rf.StringVar(&rDesc, "description", "", e.t("nova descrição"))

	reveal := &cobra.Command{
		Use: "reveal " + e.t("<nome>"), Short: e.t("Mostra o valor UMA vez por versão (as próximas chamadas falham até rotacionar)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			s, err := e.client().RevealSecret(cmd.Context(), co, user, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			e.showRevealed(s)
			return nil
		},
	}

	var force bool
	rm := &cobra.Command{
		Use: "rm " + e.t("<nome>"), Aliases: []string{"delete"}, Short: e.t("Apaga um segredo (recusado se estiver em uso, a menos que --force)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().DeleteSecret(cmd.Context(), co, user, a[0], force); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Segredo %s apagado.\n"), a[0])
			return nil
		},
	}
	rm.Flags().BoolVar(&force, "force", false, e.t("apaga mesmo em uso por personas ou integrações"))

	migrate := &cobra.Command{
		Use: "migrate-env", Short: e.t("Move o env em claro das personas para segredos cifrados (dono da instalação; idempotente)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r, err := e.client().MigratePersonaEnv(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(r)
			}
			fmt.Fprintf(e.out, e.t("%d persona(s) com o env movido para segredos cifrados.\n"), r.Personas)
			return nil
		},
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

	c.AddCommand(create, ls, show, rotate, reveal, rm, migrate, rekey)
	return c
}

// ---------- settings da instância (ADR-0050) ----------

func (e *env) settingsCmd() *cobra.Command {
	c := &cobra.Command{Use: "settings", Short: e.t("Configurações da instalação (dono da instalação); env vence e trava a chave")}

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista as configurações com o valor efetivo e a origem"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := e.client().ListInstanceSettings(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			rows := make([][]string, len(l))
			for i, s := range l {
				lock := ""
				if s.Locked {
					lock = e.t("travada por") + " " + s.Env
				}
				rows[i] = []string{s.Key, settingText(s.Value), e.sourceLabel(s.Source), lock}
			}
			e.table([]string{e.t("CHAVE"), e.t("VALOR"), e.t("ORIGEM"), e.t("TRAVA")}, rows)
			return nil
		},
	}

	get := &cobra.Command{
		Use: "get " + e.t("<chave>"), Short: e.t("Mostra uma configuração"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			l, err := e.client().ListInstanceSettings(cmd.Context())
			if err != nil {
				return err
			}
			for _, s := range l {
				if s.Key == a[0] {
					if e.asJSON {
						return e.printJSON(s)
					}
					fmt.Fprintln(e.out, settingText(s.Value))
					return nil
				}
			}
			return usageError{fmt.Errorf(e.t("chave desconhecida: %s"), a[0])}
		},
	}

	set := &cobra.Command{
		Use: "set " + e.t("<chave> <valor>"), Short: e.t("Define uma configuração (true/false, número, duração como 15m, ou lista a,b)"), Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, a []string) error {
			def, ok := settings.Lookup(a[0])
			if !ok {
				return usageError{fmt.Errorf(e.t("chave desconhecida: %s"), a[0])}
			}
			raw, err := settings.FromText(def, a[1])
			if err != nil {
				return usageError{fmt.Errorf("%s: %w", a[0], err)}
			}
			s, err := e.client().SetInstanceSetting(cmd.Context(), a[0], raw)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("%s = %s\n"), s.Key, settingText(s.Value))
			return nil
		},
	}

	unset := &cobra.Command{
		Use: "unset " + e.t("<chave>"), Short: e.t("Remove o valor do banco (volta ao padrão)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			s, err := e.client().UnsetInstanceSetting(cmd.Context(), a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(s)
			}
			fmt.Fprintf(e.out, e.t("%s = %s (%s)\n"), s.Key, settingText(s.Value), e.sourceLabel(s.Source))
			return nil
		},
	}

	var limit int
	audit := &cobra.Command{
		Use: "audit", Short: e.t("Histórico das mudanças"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := e.client().InstanceSettingsAudit(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			rows := make([][]string, len(l))
			for i, a := range l {
				rows[i] = []string{e.fmtTime(a.OccurredAt), a.Key, dashRaw(a.Old), dashRaw(a.New), a.ActorType}
			}
			e.table([]string{e.t("QUANDO"), e.t("CHAVE"), e.t("ANTES"), e.t("DEPOIS"), e.t("AUTOR")}, rows)
			return nil
		},
	}
	audit.Flags().IntVar(&limit, "limit", 50, e.t("máximo de linhas"))

	c.AddCommand(ls, get, set, unset, audit)
	return c
}

func (e *env) sourceLabel(src string) string {
	switch src {
	case "env":
		return e.t("ambiente")
	case "db":
		return e.t("banco")
	}
	return e.t("padrão")
}

func settingText(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case []any:
		parts := make([]string, len(x))
		for i, p := range x {
			parts[i] = fmt.Sprint(p)
		}
		if len(parts) == 0 {
			return "[]"
		}
		return strings.Join(parts, ",")
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprint(x)
	}
	return fmt.Sprint(v)
}

func dashRaw(b []byte) string {
	if len(b) == 0 || string(b) == "null" {
		return "-"
	}
	return string(b)
}

// ---------- blobs (ADR-0051) ----------

func (e *env) blobsCmd() *cobra.Command {
	c := &cobra.Command{Use: "blobs", Short: e.t("Onde o conteúdo dos artefatos fica guardado (postgres ou s3)")}

	status := &cobra.Command{
		Use: "status", Short: e.t("Mostra o driver ativo e quanto há em cada um (via API; dono da instalação)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := e.client().BlobStatus(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(st)
			}
			fmt.Fprintf(e.out, e.t("Driver ativo: %s (configurados: %s)\n"), st.Active, strings.Join(st.Available, ", "))
			rows := make([][]string, len(st.Usage))
			for i, u := range st.Usage {
				rows[i] = []string{u.Driver, fmt.Sprint(u.Versions), fmt.Sprint(u.Bytes)}
			}
			e.table([]string{e.t("DRIVER"), e.t("VERSÕES"), e.t("BYTES")}, rows)
			return nil
		},
	}

	var a BlobsMigrateArgs
	migrate := &cobra.Command{
		Use: "migrate", Short: e.t("Move o conteúdo dos artefatos de um driver para outro (retomável; usa o banco e o env do servidor)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.rt.BlobsMigrate == nil {
				return e.errNotWired()
			}
			if a.From == "" || a.To == "" {
				return usageError{errors.New(e.t("informe --from e --to (postgres ou s3)"))}
			}
			a.Company = e.company // --company restringe a uma empresa; sem ele, todas
			rep, err := e.rt.BlobsMigrate(cmd.Context(), a)
			if e.asJSON {
				if perr := e.printJSON(rep); perr != nil {
					return perr
				}
			} else {
				e.printMigrateReport(a, rep)
			}
			if err != nil {
				return err
			}
			if rep.Failed > 0 {
				return fmt.Errorf(e.t("%d artefato(s) não puderam ser movidos"), rep.Failed)
			}
			return nil
		},
	}
	mf := migrate.Flags()
	mf.StringVar(&a.From, "from", "", e.t("driver de origem: postgres ou s3"))
	mf.StringVar(&a.To, "to", "", e.t("driver de destino: postgres ou s3"))
	mf.BoolVar(&a.KeepSource, "keep-source", false, e.t("não apaga o conteúdo da origem depois de copiar"))
	mf.BoolVar(&a.DryRun, "dry-run", false, e.t("só conta o que seria movido"))
	mf.IntVar(&a.Batch, "batch", 100, e.t("tamanho do lote"))

	c.AddCommand(status, migrate)
	return c
}

func (e *env) printMigrateReport(a BlobsMigrateArgs, r storage.MigrateReport) {
	verb := e.t("movidos")
	if a.DryRun {
		verb = e.t("seriam movidos")
	}
	fmt.Fprintf(e.out, e.t("%s → %s: %d encontrado(s), %d %s (%d bytes), %d com falha, %d com sobra na origem.\n"),
		a.From, a.To, r.Scanned, r.Moved, verb, r.Bytes, r.Failed, r.Leftover)
	for _, m := range r.Errors {
		fmt.Fprintln(e.errOut, "  "+m)
	}
}
