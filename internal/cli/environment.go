package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/renatobardi/hanko/internal/api"
	"github.com/renatobardi/hanko/internal/client"
)

// Ambientes de execução (ADR-0037): hanko env list|show|create|update|probe|leases|default|rm.

// readSecretFile lê uma chave ou known_hosts de um arquivo ("-" = stdin), com teto de 64 KiB; segredo não vai
// por flag para não ficar no histórico do shell.
func (e *env) readSecretFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	var r io.Reader
	if path == "-" {
		r = e.in
	} else {
		f, err := os.Open(path) //nolint:gosec // o operador escolhe o arquivo
		if err != nil {
			return "", err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, 64<<10+1))
	if err != nil {
		return "", err
	}
	if len(b) > 64<<10 {
		return "", usageError{errors.New(e.t("arquivo grande demais (máximo de 64 KiB)"))}
	}
	return string(b), nil
}

func (e *env) envTarget(v api.Environment) string {
	switch {
	case v.SSH != nil:
		return fmt.Sprintf("%s@%s:%d %s", v.SSH.Username, v.SSH.Host, v.SSH.Port, v.SSH.RemoteWorkspaceRoot)
	case v.Sandbox != nil:
		return fmt.Sprintf("%s (%s)", v.Sandbox.Image, v.Sandbox.Network)
	case v.Cloud != nil:
		return fmt.Sprintf("%s (%s, %dcpu/%dGiB, %s)", v.Cloud.Provider, e.t("nuvem"), v.Cloud.CPUs, v.Cloud.MemoryGiB, v.Cloud.Network)
	case v.Driver == "local":
		return e.t("este servidor")
	}
	return "-"
}

func (e *env) envState(v api.Environment) string {
	switch {
	case v.Status == "archived":
		return e.t("arquivado")
	case !v.Available:
		return e.t("indisponível")
	}
	return e.t("ativo")
}

func (e *env) printProbe(r api.ProbeReport) {
	status := e.t("FALHOU")
	if r.OK {
		status = e.t("OK")
	}
	pairs := [][2]string{{e.t("Resultado"), status}}
	for _, st := range r.Steps {
		mark := "ok"
		if !st.OK {
			mark = e.t("falhou")
		}
		pairs = append(pairs, [2]string{st.Name, mark + " — " + st.Detail})
	}
	if len(r.Capabilities) > 0 {
		pairs = append(pairs, [2]string{e.t("Capacidades"), strings.Join(r.Capabilities, ", ")})
	}
	for _, w := range r.Warnings {
		pairs = append(pairs, [2]string{e.t("Aviso"), w})
	}
	e.kv(pairs...)
}

func (e *env) environmentCmd() *cobra.Command {
	c := &cobra.Command{Use: "env", Aliases: []string{"environment"},
		Short: e.t("Ambientes de execução: onde as personas rodam (local, SSH, sandbox docker, nuvem)")}

	var (
		driver, slug, desc, host, user, root, khFile, khText, keyFile, image, network string
		provider, snapshot, cloudNet, apiKeyFile, cloudRoot                           string
		cloudCPUs, cloudMem, cloudDisk                                                int
		port, memory, pids                                                            int
		cpus                                                                          float64
		envs                                                                          []string
		probeFirst                                                                    bool
	)
	buildReq := func(name string) (client.EnvironmentReq, error) {
		ev, err := e.kvMap(envs)
		if err != nil {
			return client.EnvironmentReq{}, err
		}
		req := client.EnvironmentReq{Name: name, Slug: slug, Description: desc, Driver: driver, Env: ev}
		switch driver {
		case "ssh":
			kh := khText
			if khFile != "" {
				if kh, err = e.readSecretFile(khFile); err != nil {
					return req, err
				}
			}
			if req.PrivateKey, err = e.readSecretFile(keyFile); err != nil {
				return req, err
			}
			req.SSH = &api.SSHConfig{Host: host, Port: port, Username: user, RemoteWorkspaceRoot: root, KnownHosts: kh}
		case "sandbox":
			req.Sandbox = &api.SandboxConfig{Image: image, Network: network, MemoryMiB: memory, CPUs: cpus, PIDsLimit: pids}
		case "cloud":
			if req.APIKey, err = e.readSecretFile(apiKeyFile); err != nil {
				return req, err
			}
			req.APIKey = strings.TrimSpace(req.APIKey)
			req.Cloud = &api.CloudConfig{Provider: provider, Snapshot: snapshot, CPUs: cloudCPUs, MemoryGiB: cloudMem, DiskGiB: cloudDisk,
				Network: cloudNet, RemoteWorkspaceRoot: cloudRoot}
		case "local":
			// só nome e variáveis
		default:
			return req, usageError{errors.New(e.t("--driver aceita local, ssh, sandbox ou cloud"))}
		}
		return req, nil
	}
	create := &cobra.Command{
		Use: "create " + e.t("<nome>"), Short: e.t("Cadastra um ambiente (local explícito, SSH, sandbox docker ou nuvem)"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			req, err := buildReq(a[0])
			if err != nil {
				return err
			}
			if probeFirst {
				r, err := e.client().ProbeEnvironmentDraft(cmd.Context(), co, req)
				if err != nil {
					return err
				}
				if !r.OK {
					e.printProbe(r)
					return fmt.Errorf("%s", e.t("o teste de conexão falhou; o ambiente não foi cadastrado"))
				}
			}
			v, err := e.client().CreateEnvironment(cmd.Context(), co, req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			fmt.Fprintf(e.out, e.t("Ambiente cadastrado: %s (%s, %s)\n"), v.Name, v.Driver, v.Slug)
			if v.Unavailable != "" {
				fmt.Fprintf(e.out, e.t("Atenção: %s\n"), v.Unavailable)
			}
			return nil
		},
	}
	cf := create.Flags()
	cf.StringVar(&driver, "driver", "", e.t("local, ssh, sandbox (docker) ou cloud (obrigatório)"))
	cf.StringVar(&slug, "slug", "", e.t("identificador (padrão: derivado do nome)"))
	cf.StringVar(&desc, "description", "", e.t("descrição"))
	cf.StringVar(&host, "host", "", e.t("SSH: host ou IP (precisa estar em HANKO_ENV_SSH_ALLOW_HOSTS)"))
	cf.IntVar(&port, "port", 22, e.t("SSH: porta"))
	cf.StringVar(&user, "user", "", e.t("SSH: usuário"))
	cf.StringVar(&root, "root", "", e.t("SSH: pasta absoluta no host para as cópias do workspace"))
	cf.StringVar(&khFile, "known-hosts-file", "", e.t("SSH: arquivo com as linhas de known_hosts do host (ssh-keyscan -H host)"))
	cf.StringVar(&khText, "known-hosts", "", e.t("SSH: linhas de known_hosts (alternativa a --known-hosts-file)"))
	cf.StringVar(&keyFile, "key-file", "", e.t("SSH: arquivo da chave privada, sem senha (- lê da entrada padrão)"))
	cf.StringVar(&image, "image", "", e.t("sandbox: imagem (prefira fixar com @sha256:)"))
	cf.StringVar(&network, "network", "none", e.t("sandbox: rede none ou bridge"))
	cf.IntVar(&memory, "memory", 0, e.t("sandbox: memória em MiB (padrão 2048)"))
	cf.Float64Var(&cpus, "cpus", 0, e.t("sandbox: CPUs (padrão 2)"))
	cf.IntVar(&pids, "pids", 0, e.t("sandbox: máximo de processos (padrão 512)"))
	cf.StringVar(&provider, "provider", "daytona", e.t("cloud: provider do sandbox em nuvem"))
	cf.StringVar(&snapshot, "snapshot", "", e.t("cloud: snapshot/imagem do provider (vazio = o padrão)"))
	cf.IntVar(&cloudCPUs, "cloud-cpus", 0, e.t("cloud: CPUs (padrão 2)"))
	cf.IntVar(&cloudMem, "cloud-memory", 0, e.t("cloud: memória em GiB (padrão 4)"))
	cf.IntVar(&cloudDisk, "cloud-disk", 0, e.t("cloud: disco em GiB (padrão 10)"))
	cf.StringVar(&cloudNet, "cloud-network", "full", e.t("cloud: rede none ou full"))
	cf.StringVar(&cloudRoot, "cloud-root", "", e.t("cloud: pasta absoluta no sandbox para as cópias do workspace"))
	cf.StringVar(&apiKeyFile, "api-key-file", "", e.t("cloud: arquivo com a chave de API do provider ou ${secret:NOME} (- lê da entrada padrão)"))
	cf.StringArrayVar(&envs, "env", nil, e.t("variável NOME=VALOR do harness no ambiente (segredo; repita a flag)"))
	cf.BoolVar(&probeFirst, "probe", false, e.t("testa a conexão antes de cadastrar"))
	_ = create.MarkFlagRequired("driver")

	ls := &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: e.t("Lista os ambientes (o local é implícito)"), Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			vs, err := e.client().Environments(cmd.Context(), co)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(vs)
			}
			def := "local"
			rows := [][]string{}
			for _, v := range vs {
				mark := ""
				if v.IsDefault {
					mark, def = "*", v.Slug
				}
				rows = append(rows, []string{v.Slug + mark, v.Driver, e.envState(v), oneLine(e.envTarget(v), 60), fmt.Sprint(len(v.Personas)), fmt.Sprint(v.ActiveLeases)})
			}
			rows = append([][]string{{"local", "local", e.t("ativo"), e.t("este servidor"), "-", "-"}}, rows...)
			e.table([]string{e.t("SLUG"), e.t("DRIVER"), e.t("ESTADO"), e.t("ALVO"), e.t("PERSONAS"), e.t("EM USO")}, rows)
			fmt.Fprintf(e.out, e.t("Padrão da empresa: %s\n"), def)
			return nil
		},
	}

	show := &cobra.Command{
		Use: "show " + e.t("<ambiente>"), Short: e.t("Mostra um ambiente (sem segredos) e o último teste"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().Environment(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			names := make([]string, len(v.Personas))
			for i, p := range v.Personas {
				names[i] = p.Slug
			}
			pairs := [][2]string{{e.t("Ambiente"), v.Name}, {e.t("Slug"), v.Slug}, {e.t("ID"), v.ID}, {e.t("Driver"), v.Driver},
				{e.t("Estado"), e.envState(v)}, {e.t("Alvo"), e.envTarget(v)}, {e.t("Padrão"), e.yesNo(v.IsDefault)},
				{e.t("Personas"), dash(strings.Join(names, ", "))}}
			if v.SSH != nil {
				pairs = append(pairs, [2]string{e.t("Chave privada"), e.yesNo(v.KeySet)})
			}
			if v.Cloud != nil {
				pairs = append(pairs, [2]string{e.t("Chave de API"), e.yesNo(v.KeySet)})
			}
			if len(v.Projects) > 0 {
				ps := make([]string, len(v.Projects))
				for i, p := range v.Projects {
					ps[i] = p.Slug
				}
				pairs = append(pairs, [2]string{e.t("Projetos"), strings.Join(ps, ", ")})
			}
			if len(v.EnvNames) > 0 {
				pairs = append(pairs, [2]string{e.t("Variáveis"), strings.Join(v.EnvNames, ", ") + " " + e.t("(valores guardados cifrados)")})
			}
			if v.Unavailable != "" {
				pairs = append(pairs, [2]string{e.t("Indisponível"), v.Unavailable})
			}
			for _, w := range v.Warnings {
				pairs = append(pairs, [2]string{e.t("Aviso"), w})
			}
			if v.LastProbeAt != nil {
				pairs = append(pairs, [2]string{e.t("Último teste"), e.fmtTimePtr(v.LastProbeAt)})
			}
			if v.LastProbe != nil && len(v.LastProbe.Capabilities) > 0 {
				pairs = append(pairs, [2]string{e.t("Capacidades"), strings.Join(v.LastProbe.Capabilities, ", ")})
			}
			e.kv(pairs...)
			return nil
		},
	}

	var upName, upDesc, upKey, upImage, upNet, upAPIKeyFile, upCloudNet, upSnapshot string
	var upSetEnv, upDropEnv []string
	var upReactivate bool
	update := &cobra.Command{
		Use: "update " + e.t("<ambiente>"), Short: e.t("Altera um ambiente"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			se, err := e.kvMap(upSetEnv)
			if err != nil {
				return err
			}
			req := client.UpdateEnvironmentReq{SetEnv: se, DropEnv: upDropEnv, Reactivate: upReactivate}
			changed := se != nil || len(upDropEnv) > 0 || upReactivate
			if f.Changed("name") {
				req.Name, changed = &upName, true
			}
			if f.Changed("description") {
				req.Description, changed = &upDesc, true
			}
			if f.Changed("key-file") {
				k, err := e.readSecretFile(upKey)
				if err != nil {
					return err
				}
				req.PrivateKey, changed = &k, true
			}
			if f.Changed("api-key-file") {
				k, err := e.readSecretFile(upAPIKeyFile)
				if err != nil {
					return err
				}
				k = strings.TrimSpace(k)
				req.APIKey, changed = &k, true
			}
			if f.Changed("cloud-network") || f.Changed("snapshot") {
				cur, err := e.client().Environment(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				if cur.Cloud == nil {
					return usageError{errors.New(e.t("--cloud-network e --snapshot valem só para cloud"))}
				}
				cl := *cur.Cloud
				if f.Changed("cloud-network") {
					cl.Network = upCloudNet
				}
				if f.Changed("snapshot") {
					cl.Snapshot = upSnapshot
				}
				req.Cloud, changed = &cl, true
			}
			if f.Changed("image") || f.Changed("network") {
				cur, err := e.client().Environment(cmd.Context(), co, a[0])
				if err != nil {
					return err
				}
				if cur.Sandbox == nil {
					return usageError{errors.New(e.t("--image e --network valem só para sandbox"))}
				}
				sb := *cur.Sandbox
				if f.Changed("image") {
					sb.Image = upImage
				}
				if f.Changed("network") {
					sb.Network = upNet
				}
				req.Sandbox, changed = &sb, true
			}
			if !changed {
				return usageError{errors.New(e.t("nada a alterar: veja hanko env update --help"))}
			}
			v, err := e.client().UpdateEnvironment(cmd.Context(), co, a[0], req)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			fmt.Fprintf(e.out, e.t("Ambiente atualizado: %s (%s)\n"), v.Name, e.envState(v))
			return nil
		},
	}
	uf := update.Flags()
	uf.StringVar(&upName, "name", "", e.t("novo nome"))
	uf.StringVar(&upDesc, "description", "", e.t("descrição"))
	uf.StringVar(&upKey, "key-file", "", e.t("SSH: troca a chave privada (- lê da entrada padrão)"))
	uf.StringVar(&upImage, "image", "", e.t("sandbox: nova imagem"))
	uf.StringVar(&upNet, "network", "", e.t("sandbox: rede none ou bridge"))
	uf.StringVar(&upAPIKeyFile, "api-key-file", "", e.t("cloud: troca a chave de API (- lê da entrada padrão)"))
	uf.StringVar(&upCloudNet, "cloud-network", "", e.t("cloud: rede none ou full"))
	uf.StringVar(&upSnapshot, "snapshot", "", e.t("cloud: snapshot/imagem do provider"))
	uf.StringArrayVar(&upSetEnv, "env", nil, e.t("define ou troca uma variável NOME=VALOR (repita a flag)"))
	uf.StringArrayVar(&upDropEnv, "drop-env", nil, e.t("remove uma variável pelo nome (repita a flag)"))
	uf.BoolVar(&upReactivate, "reactivate", false, e.t("tira o ambiente do arquivo"))

	probe := &cobra.Command{
		Use: "probe " + e.t("<ambiente>"), Short: e.t("Testa a conexão: sistema, pasta gravável, binários, acesso ao Hanko e capacidades"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().ProbeEnvironment(cmd.Context(), co, a[0])
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			if v.LastProbe == nil {
				return fmt.Errorf("%s", e.t("o servidor não devolveu o relatório do teste"))
			}
			e.printProbe(*v.LastProbe)
			if !v.LastProbe.OK {
				return fmt.Errorf("%s", e.t("o teste de conexão falhou"))
			}
			return nil
		},
	}

	var leaseLimit int
	leases := &cobra.Command{
		Use: "leases " + e.t("<ambiente>"), Short: e.t("Mostra as execuções que usaram o ambiente"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			ls, err := e.client().EnvironmentLeases(cmd.Context(), co, a[0], leaseLimit)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(ls)
			}
			if len(ls) == 0 {
				fmt.Fprintln(e.out, e.t("Nenhuma execução usou este ambiente ainda."))
				return nil
			}
			rows := make([][]string, 0, len(ls))
			for _, l := range ls {
				st := l.Status
				if l.Orphan {
					st += " (" + e.t("órfão") + ")"
				}
				rows = append(rows, []string{e.fmtTime(l.AcquiredAt), dash(l.RunName), dash(l.PersonaName), st, oneLine(dash(l.FailureReason), 50)})
			}
			e.table([]string{e.t("QUANDO"), e.t("EXECUÇÃO"), e.t("PERSONA"), e.t("ESTADO"), e.t("MOTIVO")}, rows)
			return nil
		},
	}
	leases.Flags().IntVar(&leaseLimit, "limit", 20, e.t("máximo de usos"))

	def := &cobra.Command{
		Use: "default " + e.t("<ambiente|local>"), Short: e.t("Escolhe o ambiente padrão da empresa"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			if err := e.client().SetDefaultEnvironment(cmd.Context(), co, a[0]); err != nil {
				return err
			}
			fmt.Fprintf(e.out, e.t("Ambiente padrão da empresa: %s\n"), a[0])
			return nil
		},
	}

	var moveTo string
	rm := &cobra.Command{
		Use: "rm " + e.t("<ambiente>"), Aliases: []string{"archive"}, Short: e.t("Arquiva um ambiente e move as personas dele"), Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			co, err := e.needCompany()
			if err != nil {
				return err
			}
			v, err := e.client().ArchiveEnvironment(cmd.Context(), co, a[0], moveTo)
			if err != nil {
				return err
			}
			if e.asJSON {
				return e.printJSON(v)
			}
			fmt.Fprintf(e.out, e.t("Ambiente %s arquivado; as personas dele foram para %s.\n"), v.Slug, dash(moveTo))
			return nil
		},
	}
	rm.Flags().StringVar(&moveTo, "move-to", "local", e.t("ambiente que recebe as personas (local = o padrão da empresa)"))

	c.AddCommand(ls, show, create, update, probe, leases, def, rm)
	return c
}
