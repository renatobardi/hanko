package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/backup"
	"github.com/renatobardi/hanko/internal/ops"
	"github.com/renatobardi/hanko/internal/syscmd"
)

// Operação da instância (ADR-0060): doctor, update, service e backup. Tudo roda NO servidor (ou no computador onde o
// Hanko está instalado), sem passar pela API: precisa funcionar com o servidor parado.

// BackupAdmin é o acesso local aos backups (`hanko backup …`). A implementação do app conecta ao banco só quando um
// comando precisa (criar, restaurar); listar e conferir só leem o diretório.
type BackupAdmin interface {
	Create(ctx context.Context, o backup.CreateOptions) (backup.Entry, error)
	List() ([]backup.Entry, error)
	Verify(ctx context.Context, name string) (backup.Manifest, error)
	Restore(ctx context.Context, name string, o backup.RestoreOptions) (backup.RestoreReport, error)
	// OtherSessions conta as conexões de outros clientes ao banco (servidor ainda no ar).
	OtherSessions(ctx context.Context) (int, error)
	Close()
}

// OpsRuntime injeta o mundo externo de `hanko update` e `hanko service`; o zero value usa o computador de verdade.
type OpsRuntime struct {
	// UpdateSource é a fonte das releases; nil usa o GitHub (HANKO_UPDATE_REPO, HANKO_UPDATE_TOKEN).
	UpdateSource ops.Source
	// Exe devolve o caminho do binário em execução; nil usa os.Executable.
	Exe func() (string, error)
	// InContainer diz se o processo roda em contêiner; nil verifica /.dockerenv.
	InContainer func() bool
	// Installer instala o serviço; nil monta um sobre o sistema operacional atual.
	Installer *ops.Installer
	GOOS      string
	GOARCH    string
}

// EmbeddedOptions são as opções de `hanko serve --embedded-pg`.
type EmbeddedOptions struct {
	Dir    string // diretório do cluster (vazio = <HANKO_HOME>/pg)
	BinDir string // pasta com initdb e pg_ctl (vazio = procurar)
	Port   int    // 0 = reaproveitar/escolher
}

const defaultUpdateRepo = "renatobardi/hanko"

func (e *env) goos() string {
	if e.rt.Ops.GOOS != "" {
		return e.rt.Ops.GOOS
	}
	return runtime.GOOS
}

func (e *env) goarch() string {
	if e.rt.Ops.GOARCH != "" {
		return e.rt.Ops.GOARCH
	}
	return runtime.GOARCH
}

func (e *env) exePath() (string, error) {
	if e.rt.Ops.Exe != nil {
		return e.rt.Ops.Exe()
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func (e *env) inContainer() bool {
	if e.rt.Ops.InContainer != nil {
		return e.rt.Ops.InContainer()
	}
	if e.getenv("HANKO_IN_CONTAINER") == "1" {
		return true
	}
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

func (e *env) installer() (*ops.Installer, error) {
	if e.rt.Ops.Installer != nil {
		return e.rt.Ops.Installer, nil
	}
	p, err := ops.PlatformFor(e.goos())
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return &ops.Installer{Platform: p, Cmd: syscmd.OS{}, Home: home, UID: os.Geteuid()}, nil
}

// ---------- doctor ----------

func (e *env) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: e.t("Diagnostica a instalação (Postgres, migrations, OTel, memória, harnesses, segredos, disco)"),
		Long: e.t("Roda as checagens da instalação e mostra o que está ok, o que merece atenção e o que falhou, com a dica de correção. " +
			"Sai com código 1 se alguma checagem falhou (avisos não contam). Não altera nada e nunca imprime valores de segredos."),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.rt.Doctor == nil {
				return e.errNotWired()
			}
			rep, err := e.rt.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if e.asJSON {
				if err := e.printJSON(rep); err != nil {
					return err
				}
			} else {
				e.printDoctor(rep)
			}
			if !rep.Healthy() {
				return errors.New(e.t("o diagnóstico encontrou falhas"))
			}
			return nil
		},
	}
}

func (e *env) statusWord(s ops.Status) string {
	switch s {
	case ops.OK:
		return e.t("OK")
	case ops.Warn:
		return e.t("AVISO")
	case ops.Fail:
		return e.t("FALHA")
	}
	return e.t("PULADO")
}

func (e *env) printDoctor(rep ops.Report) {
	for _, c := range rep.Checks {
		fmt.Fprintf(e.out, "%-7s %-11s %s\n", e.statusWord(c.Status), c.Name, e.t(c.Detail))
		if c.Hint != "" && (c.Status == ops.Warn || c.Status == ops.Fail) {
			fmt.Fprintf(e.out, "%-7s %-11s → %s\n", "", "", e.t(c.Hint))
		}
	}
	fmt.Fprintf(e.out, e.t("\n%d ok, %d aviso(s), %d falha(s), %d pulado(s)\n"), rep.Count(ops.OK), rep.Count(ops.Warn), rep.Count(ops.Fail), rep.Count(ops.Skip))
}

// ---------- update ----------

func (e *env) updateSource() ops.Source {
	if e.rt.Ops.UpdateSource != nil {
		return e.rt.Ops.UpdateSource
	}
	repo := e.getenv("HANKO_UPDATE_REPO")
	if repo == "" {
		repo = defaultUpdateRepo
	}
	tok := e.getenv("HANKO_UPDATE_TOKEN")
	if tok == "" {
		tok = e.getenv("GITHUB_TOKEN")
	}
	return ops.GitHub{Repo: repo, Token: tok}
}

func (e *env) updateCmd() *cobra.Command {
	var channel string
	var check, skipBackup, force, restart, rollback bool
	var svcName string
	var userLevel bool
	c := &cobra.Command{
		Use:   "update",
		Short: e.t("Atualiza o binário do hanko para a release mais nova do canal (com backup antes)"),
		Long: e.t("Consulta as releases do canal (stable ou beta), faz um backup do banco, baixa o binário, confere o sha256 e troca o executável de forma atômica. " +
			"O binário anterior fica ao lado (.prev) e `hanko update --rollback` o devolve. As migrations novas rodam no próximo start do servidor. " +
			"Em contêiner a atualização é da imagem, não do binário."),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			exe, err := e.exePath()
			if err != nil {
				return fmt.Errorf("%s: %w", e.t("localizar o executável"), err)
			}
			u := &ops.Updater{Current: e.version, Exe: exe, GOOS: e.goos(), GOARCH: e.goarch(), Source: e.updateSource()}
			if e.inContainer() {
				u.Managed = e.t("instalação em contêiner: atualize a imagem e recrie o contêiner")
			}
			if restart || rollback {
				ins, err := e.installer()
				if err != nil {
					return err
				}
				spec := ops.ServiceSpec{Name: svcName, UserLevel: userLevel}
				u.Restart = func(ctx context.Context) error { return ins.Restart(ctx, spec) }
			}
			if rollback {
				if err := u.Rollback(); err != nil {
					return err
				}
				fmt.Fprintln(e.out, e.t("Binário anterior restaurado."))
				if restart {
					return u.Restart(ctx)
				}
				fmt.Fprintln(e.out, e.t("Reinicie o servidor para usar a versão anterior."))
				return nil
			}
			if channel == "" {
				channel = e.getenv("HANKO_UPDATE_CHANNEL")
			}
			if channel == "" {
				channel = ops.ChannelStable
			}
			plan, err := u.Check(ctx, channel)
			if err != nil {
				return err
			}
			if check {
				if e.asJSON {
					return e.printJSON(plan)
				}
				if plan.Newer {
					fmt.Fprintf(e.out, e.t("Há versão nova no canal %s: %s (atual: %s). Rode `hanko update` para atualizar.\n"), plan.Channel, plan.Latest, plan.Current)
				} else {
					fmt.Fprintf(e.out, e.t("Nada a atualizar: %s é a mais nova do canal %s.\n"), plan.Current, plan.Channel)
				}
				return nil
			}
			if !plan.Newer && !force {
				fmt.Fprintf(e.out, e.t("Nada a atualizar: %s é a mais nova do canal %s.\n"), plan.Current, plan.Channel)
				return nil
			}
			if !skipBackup {
				u.Backup = e.preUpdateBackup(ctx, plan)
			}
			res, err := u.Apply(ctx, plan, ops.ApplyOptions{SkipBackup: skipBackup, Force: force})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(map[string]any{"plan": plan, "backup": res.Backup, "previous": res.Previous, "restarted": res.Restart})
			}
			if res.Backup != "" {
				fmt.Fprintf(e.out, e.t("Backup antes de atualizar: %s\n"), res.Backup)
			}
			fmt.Fprintf(e.out, e.t("Atualizado: %s → %s (canal %s).\n"), plan.Current, plan.Latest, plan.Channel)
			fmt.Fprintf(e.out, e.t("Binário anterior guardado em %s (`hanko update --rollback` volta).\n"), res.Previous)
			if res.Restart {
				fmt.Fprintln(e.out, e.t("Serviço reiniciado; as migrations novas rodam no start."))
			} else {
				fmt.Fprintln(e.out, e.t("Reinicie o servidor para usar a versão nova (as migrations rodam no start)."))
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&channel, "channel", "", e.t("canal: stable ou beta (HANKO_UPDATE_CHANNEL; padrão stable)"))
	f.BoolVar(&check, "check", false, e.t("só consulta: diz se há versão nova, sem alterar nada"))
	f.BoolVar(&skipBackup, "skip-backup", false, e.t("não faz o backup antes (use só se já tem um recente)"))
	f.BoolVar(&force, "force", false, e.t("troca mesmo na mesma versão ou em binário de desenvolvimento"))
	f.BoolVar(&restart, "restart", false, e.t("reinicia o serviço do sistema depois da troca"))
	f.BoolVar(&rollback, "rollback", false, e.t("devolve o binário anterior guardado pela última atualização"))
	f.StringVar(&svcName, "service-name", "hanko", e.t("nome do serviço do sistema (com --restart)"))
	f.BoolVar(&userLevel, "user", false, e.t("o serviço é do usuário (systemd --user / LaunchAgent)"))
	return c
}

// preUpdateBackup devolve a função de backup do Updater: usa o BackupAdmin local e marca o backup como pré-atualização.
func (e *env) preUpdateBackup(_ context.Context, p ops.Plan) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if e.rt.Backup == nil {
			return "", e.errNotWired()
		}
		adm, err := e.rt.Backup(ctx)
		if err != nil {
			return "", err
		}
		defer adm.Close()
		ent, err := adm.Create(ctx, backup.CreateOptions{Kind: backup.KindPreUpdate, Label: fmt.Sprintf("%s -> %s", p.Current, p.Latest), Prune: true})
		if err != nil {
			return "", err
		}
		return ent.Name, nil
	}
}

// ---------- service ----------

func (e *env) serviceCmd() *cobra.Command {
	c := &cobra.Command{Use: "service", Short: e.t("Instala o hanko como serviço do sistema (systemd no Linux, launchd no macOS)")}
	var name, runAs, envFile, workDir, execPath string
	var userLevel, dryRun, noEnable bool
	spec := func() (ops.ServiceSpec, *ops.Installer, error) {
		ins, err := e.installer()
		if err != nil {
			return ops.ServiceSpec{}, nil, err
		}
		s := ops.ServiceSpec{Name: name, Args: []string{"serve"}, User: runAs, UserLevel: userLevel, EnvFile: envFile, WorkDir: workDir, Exe: execPath}
		if s.Exe == "" {
			if s.Exe, err = e.exePath(); err != nil {
				return s, ins, fmt.Errorf("%s: %w", e.t("localizar o executável"), err)
			}
		}
		if s.EnvFile == "" {
			s.EnvFile = ins.DefaultEnvFile(s)
		}
		if s.WorkDir == "" {
			if userLevel {
				s.WorkDir = filepath.Join(ins.Home, ".hanko")
			} else {
				s.WorkDir = "/var/lib/hanko"
			}
		}
		if userLevel && ins.Platform == ops.PlatformLaunchd {
			s.LogDir = filepath.Join(ins.Home, "Library", "Logs", "hanko")
		}
		return s, ins, nil
	}
	common := func(f interface {
		StringVar(*string, string, string, string)
		BoolVar(*bool, string, bool, string)
	}) {
		f.StringVar(&name, "name", "hanko", e.t("nome do serviço"))
		f.BoolVar(&userLevel, "user", false, e.t("serviço do usuário atual (systemd --user / LaunchAgent), sem root"))
	}

	install := &cobra.Command{
		Use: "install", Short: e.t("Grava a unit/plist do servidor e ativa o serviço"), Args: cobra.NoArgs,
		Long: e.t("Cria a unit do systemd (ou o plist do launchd) que roda `hanko serve`, um arquivo de ambiente com modo 0600 para HANKO_DATABASE_URL e os segredos " +
			"(nunca sobrescrito se já existir) e ativa o serviço. Sem --user precisa de root (use sudo). Reinstalar é seguro: atualiza a unit e reativa."),
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, ins, err := spec()
			if err != nil {
				return err
			}
			if dryRun {
				files, cmds, err := ins.Plan(s, !noEnable)
				if err != nil {
					return err
				}
				for _, f := range files {
					fmt.Fprintf(e.out, "# %s\n%s\n", f.Path, f.Content)
				}
				for _, c := range cmds {
					fmt.Fprintf(e.out, "$ %s\n", c)
				}
				return nil
			}
			files, err := ins.Install(cmd.Context(), s, !noEnable)
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return fmt.Errorf("%w\n%s", err, e.t("dica: instalação do sistema precisa de root (sudo) ou use --user"))
				}
				return err
			}
			for _, f := range files {
				fmt.Fprintf(e.out, e.t("Gravado: %s\n"), f.Path)
			}
			fmt.Fprintf(e.out, e.t("Edite %s (HANKO_DATABASE_URL, HANKO_SECRET, HANKO_SECRET_KEY) e reinicie o serviço: `hanko service restart`.\n"), s.EnvFile)
			if !noEnable {
				fmt.Fprintln(e.out, e.t("Serviço ativado."))
			}
			return nil
		},
	}
	fi := install.Flags()
	common(fi)
	fi.StringVar(&runAs, "run-as", "", e.t("usuário que roda o serviço (instalação do sistema)"))
	fi.StringVar(&envFile, "env-file", "", e.t("arquivo de ambiente (padrão /etc/hanko/hanko.env ou ~/.config/hanko/hanko.env)"))
	fi.StringVar(&workDir, "workdir", "", e.t("diretório de trabalho do serviço"))
	fi.StringVar(&execPath, "exec", "", e.t("caminho do binário (padrão: o que está rodando agora)"))
	fi.BoolVar(&dryRun, "dry-run", false, e.t("só mostra os arquivos e comandos, sem gravar nem executar"))
	fi.BoolVar(&noEnable, "no-enable", false, e.t("grava a unit mas não ativa o serviço"))

	uninstall := &cobra.Command{
		Use: "uninstall", Short: e.t("Desativa e remove a unit/plist (o arquivo de ambiente fica)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, ins, err := spec()
			if err != nil {
				return err
			}
			if err := ins.Uninstall(cmd.Context(), s); err != nil {
				return err
			}
			fmt.Fprintln(e.out, e.t("Serviço removido. O arquivo de ambiente foi mantido."))
			return nil
		},
	}
	common(uninstall.Flags())

	status := &cobra.Command{
		Use: "status", Short: e.t("Mostra o estado do serviço"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, ins, err := spec()
			if err != nil {
				return err
			}
			st, err := ins.Status(cmd.Context(), s)
			if err != nil {
				return err
			}
			fmt.Fprintln(e.out, st)
			return nil
		},
	}
	common(status.Flags())

	restart := &cobra.Command{
		Use: "restart", Short: e.t("Reinicia o serviço"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, ins, err := spec()
			if err != nil {
				return err
			}
			return ins.Restart(cmd.Context(), s)
		},
	}
	common(restart.Flags())

	c.AddCommand(install, uninstall, status, restart, e.servicePrintCmd())
	return c
}

// servicePrintCmd imprime units sem instalar nada: o deploy em contêiner as grava no host.
func (e *env) servicePrintCmd() *cobra.Command {
	c := &cobra.Command{Use: "print", Short: e.t("Imprime units do systemd sem instalar nada (usado pelo deploy)")}
	var dir string
	compose := &cobra.Command{
		Use: "compose", Short: e.t("Unit que sobe e para uma stack docker compose junto com a máquina"), Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			u, err := ops.RenderComposeUnit(dir)
			if err != nil {
				return usageError{err}
			}
			_, err = fmt.Fprint(e.out, u)
			return err
		},
	}
	compose.Flags().StringVar(&dir, "dir", "", e.t("diretório absoluto da stack (docker-compose.yml)"))
	_ = compose.MarkFlagRequired("dir")

	var t ops.TimerSpec
	var part string
	timer := &cobra.Command{
		Use: "timer", Short: e.t("Par service/timer de uma tarefa periódica"), Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			sv, tm, err := ops.RenderTimer(t)
			if err != nil {
				return usageError{err}
			}
			switch part {
			case "service":
				_, err = fmt.Fprint(e.out, sv)
			case "timer":
				_, err = fmt.Fprint(e.out, tm)
			default:
				return usageError{errors.New(e.t("--part deve ser service ou timer"))}
			}
			return err
		},
	}
	tf := timer.Flags()
	tf.StringVar(&t.Name, "name", "", e.t("nome da tarefa"))
	tf.StringVar(&t.Description, "description", "", e.t("descrição"))
	tf.StringVar(&t.Exec, "exec", "", e.t("comando completo, começando por caminho absoluto"))
	tf.StringVar(&t.OnCalendar, "calendar", "", e.t("agenda do systemd, ex.: *-*-* 01:30:00"))
	tf.StringVar(&t.RandomDelay, "random-delay", "", e.t("atraso aleatório máximo, ex.: 10min"))
	tf.StringVar(&t.Timeout, "timeout", "", e.t("tempo máximo da execução, ex.: 30min"))
	tf.StringVar(&t.After, "after", "", e.t("unit da qual depende, ex.: docker.service"))
	tf.StringVar(&part, "part", "service", e.t("qual arquivo imprimir: service ou timer"))
	for _, f := range []string{"name", "exec", "calendar"} {
		_ = timer.MarkFlagRequired(f)
	}
	c.AddCommand(compose, timer)
	return c
}

// ---------- backup ----------

func (e *env) backupAdmin(ctx context.Context) (BackupAdmin, error) {
	if e.rt.Backup == nil {
		return nil, e.errNotWired()
	}
	return e.rt.Backup(ctx)
}

func (e *env) entryRow(b backup.Entry) []string {
	return []string{b.Name, e.fmtTime(b.Manifest.CreatedAt), b.Manifest.Kind, ops.HumanBytes(b.Size), fmt.Sprint(b.Manifest.SchemaVersion),
		fmt.Sprint(len(b.Manifest.Blobs)), oneLine(b.Manifest.Label, 40)}
}

func (e *env) backupCmd() *cobra.Command {
	c := &cobra.Command{
		Use: "backup", Short: e.t("Cria, lista, confere e restaura backups da instalação (pg_dump + conteúdo externo)"),
		Long: e.t("Um backup é um arquivo tar em HANKO_BACKUP_DIR com o dump do Postgres (pg_dump -Fc, que já leva os artefatos do driver postgres), " +
			"o conteúdo dos artefatos guardados em S3 e um manifest com sha256. Roda no servidor, sem a API. " +
			"O backup do host (restic) continua valendo: este comando é o ponto de volta rápido e o que `hanko update` usa antes de trocar o binário."),
	}

	var label string
	create := &cobra.Command{
		Use: "create", Short: e.t("Cria um backup agora"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			adm, err := e.backupAdmin(cmd.Context())
			if err != nil {
				return err
			}
			defer adm.Close()
			ent, err := adm.Create(cmd.Context(), backup.CreateOptions{Kind: backup.KindManual, Label: label})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ent)
			}
			fmt.Fprintf(e.out, e.t("Backup criado: %s (%s, schema %d, %d conteúdo(s) externo(s))\n"), ent.Path, ops.HumanBytes(ent.Size), ent.Manifest.SchemaVersion, len(ent.Manifest.Blobs))
			return nil
		},
	}
	create.Flags().StringVar(&label, "label", "", e.t("rótulo livre, ex.: antes do upgrade"))

	list := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os backups do diretório (não precisa do banco)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			adm, err := e.backupAdmin(cmd.Context())
			if err != nil {
				return err
			}
			defer adm.Close()
			l, err := adm.List()
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(l)
			}
			if len(l) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhum backup encontrado."))
				return nil
			}
			rows := make([][]string, len(l))
			for i, b := range l {
				rows[i] = e.entryRow(b)
			}
			e.table([]string{e.t("ARQUIVO"), e.t("CRIADO"), e.t("TIPO"), e.t("TAMANHO"), e.t("SCHEMA"), e.t("EXTERNOS"), e.t("RÓTULO")}, rows)
			return nil
		},
	}

	verify := &cobra.Command{
		Use: "verify " + e.t("<arquivo|latest>"), Short: e.t("Confere o arquivo inteiro contra o manifest (sha256), sem tocar no banco"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			adm, err := e.backupAdmin(cmd.Context())
			if err != nil {
				return err
			}
			defer adm.Close()
			man, err := adm.Verify(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(man)
			}
			fmt.Fprintf(e.out, e.t("Íntegro: %s (criado em %s, schema %d, dump %s, %d conteúdo(s) externo(s))\n"), args[0], e.fmtTime(man.CreatedAt), man.SchemaVersion, ops.HumanBytes(man.DumpBytes), len(man.Blobs))
			return nil
		},
	}

	var yes, force, skipBlobs bool
	restore := &cobra.Command{
		Use: "restore " + e.t("<arquivo|latest>"), Short: e.t("Restaura o banco (e o conteúdo externo) a partir de um backup: DESTRUTIVO"), Args: cobra.ExactArgs(1),
		Long: e.t("Substitui o conteúdo do banco apontado por HANKO_DATABASE_URL pelo do backup (pg_restore --clean em uma transação). " +
			"Pare o servidor antes: o comando recusa se há outras conexões ao banco (--force ignora). Confere o sha256 do arquivo antes de tocar no banco. " +
			"Exige --yes."),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			adm, err := e.backupAdmin(ctx)
			if err != nil {
				return err
			}
			defer adm.Close()
			man, err := adm.Verify(ctx, args[0])
			if err != nil {
				return err
			}
			if !yes {
				return usageError{fmt.Errorf(e.t("restaurar %s (criado em %s) SUBSTITUI o banco atual; repita com --yes para confirmar"), args[0], e.fmtTime(man.CreatedAt))}
			}
			if !force {
				n, err := adm.OtherSessions(ctx)
				if err != nil {
					return fmt.Errorf("%s: %w", e.t("conferir conexões ao banco"), err)
				}
				if n > 0 {
					return fmt.Errorf(e.t("há %d outra(s) conexão(ões) ao banco: pare o servidor antes de restaurar (ou use --force)"), n)
				}
			}
			rep, err := adm.Restore(ctx, args[0], backup.RestoreOptions{Force: force, SkipBlobs: skipBlobs})
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(rep)
			}
			fmt.Fprintf(e.out, e.t("Restaurado: %s (schema %d, %d conteúdo(s) externo(s)).\n"), rep.Name, rep.SchemaVersion, rep.BlobsRestored)
			if rep.NeedsMigrate {
				fmt.Fprintln(e.out, e.t("O schema do backup é mais antigo que o deste binário: rode `hanko migrate` (ou suba o servidor) antes de usar."))
			}
			fmt.Fprintln(e.out, e.t("Suba o servidor e rode `hanko doctor` para conferir."))
			return nil
		},
	}
	rf := restore.Flags()
	rf.BoolVar(&yes, "yes", false, e.t("confirma que o banco atual será substituído"))
	rf.BoolVar(&force, "force", false, e.t("restaura mesmo com outras conexões ao banco ou schema do backup mais novo que o binário"))
	rf.BoolVar(&skipBlobs, "skip-blobs", false, e.t("restaura só o banco, sem devolver o conteúdo externo (S3)"))

	c.AddCommand(create, list, verify, restore, e.backupScheduleCmd())
	return c
}

func (e *env) backupScheduleCmd() *cobra.Command {
	var enable, disable bool
	var cronExpr, tz string
	c := &cobra.Command{
		Use: "schedule", Short: e.t("Mostra ou muda o agendamento do backup no servidor (fluxo de sistema)"), Args: cobra.NoArgs,
		Long: e.t("Fala com o servidor pela API (dono da instalação). Sem flags mostra o agendamento; com --enable/--disable/--cron/--timezone muda."),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if enable && disable {
				return usageError{errors.New(e.t("use --enable ou --disable, não os dois"))}
			}
			req := map[string]any{}
			if enable {
				req["enabled"] = true
			}
			if disable {
				req["enabled"] = false
			}
			if cronExpr != "" {
				req["cron"] = cronExpr
			}
			if tz != "" {
				req["timezone"] = tz
			}
			var sch api.BackupSchedule
			var err error
			if len(req) == 0 {
				sch, err = e.client().GetBackupSchedule(cmd.Context())
			} else {
				sch, err = e.client().SetBackupSchedule(cmd.Context(), req)
			}
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(sch)
			}
			e.kv([2]string{e.t("Agendamento"), e.onOff(sch.Enabled)}, [2]string{"Cron", sch.Cron}, [2]string{e.t("Fuso"), sch.Timezone},
				[2]string{e.t("Próximo"), e.fmtTimePtr(sch.NextFireAt)}, [2]string{e.t("Último"), e.fmtTimePtr(sch.LastFiredAt)})
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&enable, "enable", false, e.t("liga o agendamento"))
	f.BoolVar(&disable, "disable", false, e.t("desliga o agendamento"))
	f.StringVar(&cronExpr, "cron", "", e.t("cron de 5 campos, ex.: \"0 3 * * *\""))
	f.StringVar(&tz, "timezone", "", e.t("fuso IANA, ex.: America/Sao_Paulo"))
	return c
}
