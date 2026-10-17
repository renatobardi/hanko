package ui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Ambientes de execução (ADR-0037): Configurações › Ambientes (o local, com as verificações reais deste servidor,
// e os remotos SSH e sandbox), a página de cada ambiente, o cartão na página da persona e o ambiente na
// página da execução. Os segredos (chave privada, valores das variáveis) só entram pelos formulários.

// maxEnvFormBytes é o teto do corpo dos formulários de ambiente (chave, known_hosts e variáveis).
const maxEnvFormBytes = 256 << 10

var environmentNotices = map[string]string{
	"criado":     i18n.N("Ambiente cadastrado. Teste a conexão antes de mandar personas para ele."),
	"salvo":      i18n.N("Ambiente atualizado."),
	"padrao":     i18n.N("Ambiente padrão atualizado."),
	"arquivado":  i18n.N("Ambiente arquivado; as personas dele foram movidas."),
	"reativado":  i18n.N("Ambiente reativado."),
	"testado":    i18n.N("Teste de conexão concluído; o relatório está abaixo."),
	"testefalho": i18n.N("O teste de conexão falhou; veja o relatório abaixo."),
}

func (s *server) environmentRoutes(mux langMux) {
	co := func(h companyHandler) http.HandlerFunc { return s.withCompany(h) }
	post := func(h http.HandlerFunc) http.HandlerFunc {
		return s.sameOrigin(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxEnvFormBytes)
			h(w, r)
		})
	}
	const b = "/c/{company}/configuracoes/ambientes"
	mux.HandleFunc("GET "+b+"/novo", co(s.handleNewEnvironment))
	mux.HandleFunc("POST "+b+"/novo", post(co(s.handleCreateEnvironment)))
	mux.HandleFunc("POST "+b+"/testar", post(co(s.handleProbeDraft)))
	mux.HandleFunc("POST "+b+"/padrao", post(co(s.handleSetDefaultEnvironment)))
	mux.HandleFunc("GET "+b+"/{env}", co(s.handleEnvironment))
	mux.HandleFunc("POST "+b+"/{env}", post(co(s.handleUpdateEnvironment)))
	mux.HandleFunc("POST "+b+"/{env}/testar", post(co(s.handleProbeEnvironment)))
	mux.HandleFunc("POST "+b+"/{env}/arquivar", post(co(s.handleArchiveEnvironment)))
	mux.HandleFunc("POST "+b+"/{env}/reativar", post(co(s.handleReactivateEnvironment)))
	mux.HandleFunc("GET /c/{company}/personas/{persona}/ambiente", co(s.handlePersonaEnvironment))
	mux.HandleFunc("POST /c/{company}/personas/{persona}/ambiente", post(co(s.handleSetPersonaEnvironment)))
	mux.HandleFunc("GET /c/{company}/execucoes/{run}/ambiente", co(s.handleRunEnvironment))
	s.readinessRoutes(mux, post) // prontidão da persona e ambiente do projeto (ADR-0055)
}

func envBase(co domain.Company) string { return "/c/" + co.Slug + "/configuracoes/ambientes" }

func driverLabel(ctx context.Context, d domain.EnvironmentDriver) string {
	switch d {
	case domain.EnvSSH:
		return i18n.T(ctx, "SSH · host remoto")
	case domain.EnvSandbox:
		return i18n.T(ctx, "Sandbox · contêiner por execução")
	case domain.EnvCloud:
		return i18n.T(ctx, "Nuvem · sandbox por execução")
	}
	return i18n.T(ctx, "Local · este servidor")
}

func envTarget(v service.EnvironmentView) string {
	switch {
	case v.Config.SSH != nil:
		c := v.Config.SSH
		return fmt.Sprintf("%s@%s:%d %s", c.User, c.Host, c.Port, c.Root)
	case v.Config.Sandbox != nil:
		return v.Config.Sandbox.Image
	case v.Config.Cloud != nil:
		c := v.Config.Cloud
		return fmt.Sprintf("%s · %dcpu/%dGiB · %s", c.Provider, c.CPUs, c.MemoryGiB, c.Network)
	case v.Driver == domain.EnvLocal:
		return i18n.T(context.Background(), "este servidor")
	}
	return ""
}

func envBadge(ctx context.Context, v service.EnvironmentView) views.Badge {
	switch {
	case v.Status == domain.EnvArchived:
		return views.Badge{Label: i18n.T(ctx, "Arquivado"), Class: "outline"}
	case v.Unavailable != "":
		return views.Badge{Label: i18n.T(ctx, "Indisponível"), Class: "danger"}
	case v.LastProbeOK != nil && !*v.LastProbeOK:
		return views.Badge{Label: i18n.T(ctx, "Último teste falhou"), Class: "warn"}
	case v.LastProbeOK != nil:
		return views.Badge{Label: i18n.T(ctx, "Pronto"), Class: "done"}
	}
	return views.Badge{Label: i18n.T(ctx, "Não testado"), Class: "soft"}
}

func (s *server) envRow(ctx context.Context, co domain.Company, v service.EnvironmentView) views.EnvRow {
	loc := loadLoc(co.Timezone)
	href := envBase(co) + "/" + url.PathEscape(v.Slug)
	row := views.EnvRow{Name: v.Name, Slug: v.Slug, Href: href, Description: v.Description, Driver: string(v.Driver), Target: envTarget(v),
		Badge: envBadge(ctx, v), Default: v.IsDefault, Personas: len(v.Personas), InUse: v.ActiveLeases, Unavailable: v.Unavailable,
		Status: string(v.Status), DriverIcon: "server"}
	switch v.Driver {
	case domain.EnvSandbox:
		row.DriverIcon = "terminal"
	case domain.EnvCloud:
		row.DriverIcon = "globe"
	}
	if v.LastProbeAt != nil {
		row.HasProbe, row.LastProbe = true, relTimeCtx(ctx, *v.LastProbeAt, s.now(), loc)
		row.LastProbeOK = v.LastProbeOK != nil && *v.LastProbeOK
	}
	if v.Status == domain.EnvActive {
		row.TestAction = href + "/testar"
	}
	return row
}

// defaultOptions são as escolhas do ambiente padrão: o local e os ambientes ativos.
func defaultOptions(ctx context.Context, list []service.EnvironmentView) []views.Option {
	opts := []views.Option{{Value: "local", Label: i18n.T(ctx, "Local · este servidor"), Selected: true}}
	for _, v := range list {
		if v.Status != domain.EnvActive {
			continue
		}
		opts = append(opts, views.Option{Value: v.Slug, Label: v.Name + " (" + string(v.Driver) + ")", Selected: v.IsDefault})
		if v.IsDefault {
			opts[0].Selected = false
		}
	}
	return opts
}

func (s *server) handleEnvironments(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	list, err := s.be.ListEnvironments(ctx, co.ID)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	drivers := s.be.EnvironmentDrivers(ctx)
	base := envBase(co)
	vm := views.EnvHubVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "ambientes"), Local: s.localEnvironment(ctx, co),
		DefaultAction: base + "/padrao", DefaultOptions: defaultOptions(ctx, list),
		NewSSHHref: base + "/novo?driver=ssh", NewSandboxHref: base + "/novo?driver=sandbox",
		NewLocalHref: base + "/novo?driver=local", NewCloudHref: base + "/novo?driver=cloud",
		SSHOff: drivers[domain.EnvSSH], SandboxOff: drivers[domain.EnvSandbox], CloudOff: drivers[domain.EnvCloud]}
	if n := environmentNotices[r.URL.Query().Get("ok")]; n != "" {
		vm.Notice = i18n.T(ctx, n)
	}
	vm.Local.IsDefault = vm.DefaultOptions[0].Selected
	for _, v := range list {
		row := s.envRow(ctx, co, v)
		if v.Status == domain.EnvArchived {
			vm.Archived = append(vm.Archived, row)
		} else {
			vm.Rows = append(vm.Rows, row)
		}
	}
	s.render(w, r, http.StatusOK, views.EnvironmentsHubPage(s.settingsShell(r, co, i18n.T(ctx, "Ambientes")), vm))
}

func driverFrom(v string) string {
	switch v {
	case string(domain.EnvSandbox), string(domain.EnvCloud), string(domain.EnvLocal):
		return v
	}
	return string(domain.EnvSSH)
}

func cloudNetworkOptions(cur string) []views.Option {
	if cur == "" {
		cur = "full"
	}
	return []views.Option{{Value: "full", Label: "full", Selected: cur == "full"}, {Value: "none", Label: "none", Selected: cur == "none"}}
}

func networkOptions(cur string) []views.Option {
	if cur == "" {
		cur = domain.SandboxNetworkNone
	}
	return []views.Option{{Value: domain.SandboxNetworkNone, Label: "none", Selected: cur == domain.SandboxNetworkNone},
		{Value: domain.SandboxNetworkBridge, Label: "bridge", Selected: cur == domain.SandboxNetworkBridge}}
}

func (s *server) blankEnvironmentForm(ctx context.Context, co domain.Company, driver string) views.EnvFormVM {
	base := envBase(co)
	vm := views.EnvFormVM{Driver: driver, Action: base + "/novo", ProbeAction: base + "/testar", CancelHref: base, FieldErr: map[string]string{},
		Port: "22", NetworkOptions: networkOptions(""), CloudNetworkOptions: cloudNetworkOptions("")}
	vm.DriverTabs = []views.TabLink{{Key: "local", Label: i18n.T(ctx, "Local"), Href: base + "/novo?driver=local", On: driver == "local"},
		{Key: "ssh", Label: "SSH", Href: base + "/novo?driver=ssh", On: driver == "ssh"},
		{Key: "sandbox", Label: "Sandbox", Href: base + "/novo?driver=sandbox", On: driver == "sandbox"},
		{Key: "cloud", Label: i18n.T(ctx, "Nuvem"), Href: base + "/novo?driver=cloud", On: driver == "cloud"}}
	return vm
}

func (s *server) renderNewEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.EnvFormVM) {
	ctx := r.Context()
	sh := s.shell(r, co, "configuracoes", i18n.T(ctx, "Novo ambiente"), views.Crumb{Label: i18n.T(ctx, "Configurações"), Href: "/c/" + co.Slug + "/configuracoes"},
		views.Crumb{Label: i18n.T(ctx, "Ambientes"), Href: envBase(co)}, views.Crumb{Label: i18n.T(ctx, "Novo")})
	s.render(w, r, status, views.NewEnvironmentPage(sh, vm))
}

func (s *server) handleNewEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	s.renderNewEnvironment(w, r, co, http.StatusOK, s.blankEnvironmentForm(r.Context(), co, driverFrom(r.URL.Query().Get("driver"))))
}

// fillEnvironmentForm devolve ao formulário o que foi enviado, menos os segredos (chave e valores).
func fillEnvironmentForm(r *http.Request, vm *views.EnvFormVM) {
	vm.Name, vm.Description = trimForm(r, "name"), trimForm(r, "description")
	vm.Host, vm.Port, vm.User, vm.Root = trimForm(r, "host"), trimForm(r, "port"), trimForm(r, "username"), trimForm(r, "root")
	vm.KnownHosts = r.PostFormValue("known_hosts")
	vm.Image, vm.Memory, vm.CPUs, vm.PIDs = trimForm(r, "image"), trimForm(r, "memory"), trimForm(r, "cpus"), trimForm(r, "pids")
	vm.NetworkOptions = networkOptions(trimForm(r, "network"))
	vm.Snapshot, vm.CloudCPUs, vm.CloudMemory, vm.CloudDisk, vm.CloudRoot = trimForm(r, "snapshot"), trimForm(r, "cloud_cpus"), trimForm(r, "cloud_memory"),
		trimForm(r, "cloud_disk"), trimForm(r, "cloud_root")
	vm.CloudNetworkOptions = cloudNetworkOptions(trimForm(r, "cloud_network"))
	vm.DropEnv = trimForm(r, "drop_env")
}

func atoiField(v, field string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, domain.Invalid(field, "must be a whole number")
	}
	return n, nil
}

// environmentFromFormCloud lê a parte do driver (SSH, sandbox ou nuvem, ADR-0055) e as variáveis do formulário.
func environmentFromFormCloud(r *http.Request, driver string) (ssh *domain.SSHConfig, sb *domain.SandboxConfig, cl *domain.CloudConfig, env map[string]string, err error) {
	env, perr := pairs(r.PostFormValue("env"), "=")
	if perr != nil {
		return nil, nil, nil, nil, domain.Invalid("env", perr.Error())
	}
	switch driver {
	case string(domain.EnvSSH):
		port, err := atoiField(trimForm(r, "port"), "port")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		ssh = &domain.SSHConfig{Host: trimForm(r, "host"), Port: port, User: trimForm(r, "username"), Root: trimForm(r, "root"),
			KnownHosts: r.PostFormValue("known_hosts")}
	case string(domain.EnvSandbox):
		mem, err := atoiField(trimForm(r, "memory"), "memory_mib")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		pids, err := atoiField(trimForm(r, "pids"), "pids_limit")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		cpus := 0.0
		if v := strings.ReplaceAll(trimForm(r, "cpus"), ",", "."); v != "" {
			if cpus, err = strconv.ParseFloat(v, 64); err != nil {
				return nil, nil, nil, nil, domain.Invalid("cpus", "must be a number")
			}
		}
		sb = &domain.SandboxConfig{Image: trimForm(r, "image"), Network: trimForm(r, "network"), MemoryMiB: mem, CPUs: cpus, PIDsLimit: pids}
	case string(domain.EnvCloud):
		cpus, err := atoiField(trimForm(r, "cloud_cpus"), "cpus")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		mem, err := atoiField(trimForm(r, "cloud_memory"), "memory_gib")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		disk, err := atoiField(trimForm(r, "cloud_disk"), "disk_gib")
		if err != nil {
			return nil, nil, nil, nil, err
		}
		cl = &domain.CloudConfig{Snapshot: trimForm(r, "snapshot"), CPUs: cpus, MemoryGiB: mem, DiskGiB: disk, Network: trimForm(r, "cloud_network"), Root: trimForm(r, "cloud_root")}
	}
	return ssh, sb, cl, env, nil
}

// environmentFormError põe o erro do service no campo do formulário.
func environmentFormError(ctx context.Context, vm *views.EnvFormVM, err error) {
	var ve *domain.ValidationError
	if errors.As(err, &ve) {
		field := ve.Field
		msg := map[string]string{
			"name":                  i18n.T(ctx, "Dê um nome ao ambiente."),
			"slug":                  i18n.T(ctx, "Identificador reservado ou inválido: troque o nome."),
			"host":                  i18n.T(ctx, "Host inválido ou fora da lista de hosts permitidos pelo operador (HANKO_ENV_SSH_ALLOW_HOSTS)."),
			"port":                  i18n.T(ctx, "Porta inválida: use um número de 1 a 65535."),
			"username":              i18n.T(ctx, "Usuário inválido: letras, números, ponto, hífen e sublinhado."),
			"remote_workspace_root": i18n.T(ctx, "Use uma pasta absoluta e limpa, diferente de /."),
			"known_hosts":           i18n.T(ctx, "Cole as linhas de known_hosts do host (ssh-keyscan -H host); sem elas a conexão não é aceita."),
			"private_key":           i18n.T(ctx, "Cole uma chave privada sem senha, no formato PEM ou OpenSSH."),
			"image":                 i18n.T(ctx, "Imagem inválida: use registro/nome:tag ou nome@sha256:<digest>."),
			"api_key":               i18n.T(ctx, "Informe a chave de API do provider (ou ${secret:NOME}), sem espaços."),
			"provider":              i18n.T(ctx, "Provider desconhecido: use daytona."),
			"snapshot":              i18n.T(ctx, "Snapshot inválido: use um nome como meu-snapshot ou registro/nome:tag."),
			"env":                   i18n.T(ctx, "Variável inválida: use NOME=valor; HANKO_*, PATH e HOME são reservados."),
			"secrets":               i18n.T(ctx, "Este servidor não tem HANKO_SECRET_KEY: sem ela não há como guardar a chave nem as variáveis."),
		}[field]
		if msg == "" {
			msg = ve.Field + ": " + ve.Reason + "."
		}
		if field == "driver" {
			msg = ve.Reason
		}
		vm.FieldErr[field] = msg
		vm.Error = msg
		return
	}
	_, title, msg := friendly(ctx, err)
	vm.Error = title + ". " + msg
}

func (s *server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	driver := driverFrom(trimForm(r, "driver"))
	vm := s.blankEnvironmentForm(ctx, co, driver)
	fillEnvironmentForm(r, &vm)
	ssh, sb, cl, env, err := environmentFromFormCloud(r, driver)
	if err == nil {
		var v service.EnvironmentView
		v, err = s.be.CreateEnvironment(ctx, actorFrom(ctx), co.ID, service.CreateEnvironmentInput{Name: vm.Name, Description: vm.Description,
			Driver: domain.EnvironmentDriver(driver), SSH: ssh, PrivateKey: r.PostFormValue("private_key"), Sandbox: sb, Cloud: cl,
			APIKey: strings.TrimSpace(r.PostFormValue("api_key")), Env: env})
		if err == nil {
			redirect(w, r, envBase(co)+"/"+url.PathEscape(v.Slug)+"?ok=criado")
			return
		}
	}
	environmentFormError(ctx, &vm, err)
	s.renderNewEnvironment(w, r, co, statusFor(err), vm)
}

func probeVM(ctx context.Context, rep domain.ProbeReport, when string) *views.ProbeVM {
	p := &views.ProbeVM{OK: rep.OK, System: rep.System, When: when, Warnings: rep.Warnings, Capabilities: rep.Capabilities}
	if rep.DurationMs > 0 {
		p.Duration = fmt.Sprintf("%d ms", rep.DurationMs)
	}
	for _, st := range rep.Steps {
		p.Steps = append(p.Steps, views.ProbeStepVM{Label: probeStepLabel(ctx, st.Name), OK: st.OK, Detail: st.Detail})
	}
	return p
}

func probeStepLabel(ctx context.Context, name string) string {
	switch name {
	case "config":
		return i18n.T(ctx, "Configuração")
	case "connect":
		return i18n.T(ctx, "Conexão")
	case "system":
		return i18n.T(ctx, "Sistema")
	case "workspace_root":
		return i18n.T(ctx, "Pasta remota gravável")
	case "docker":
		return "Docker"
	case "image":
		return i18n.T(ctx, "Imagem")
	case "api":
		return i18n.T(ctx, "API do provider")
	case "create":
		return i18n.T(ctx, "Criação do sandbox")
	case "cleanup":
		return i18n.T(ctx, "Limpeza")
	case "shell":
		return i18n.T(ctx, "Shell")
	case "hanko_url":
		return i18n.T(ctx, "Alcança o Hanko pela URL pública")
	}
	if b, ok := strings.CutPrefix(name, "bin:"); ok {
		return b
	}
	return name
}

// handleProbeDraft testa o rascunho do formulário (htmx) sem gravar nada.
func (s *server) handleProbeDraft(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	driver := driverFrom(trimForm(r, "driver"))
	ssh, sb, cl, env, err := environmentFromFormCloud(r, driver)
	var rep domain.ProbeReport
	if err == nil {
		rep, err = s.be.ProbeEnvironmentDraft(ctx, co.ID, service.CreateEnvironmentInput{Name: trimForm(r, "name"), Driver: domain.EnvironmentDriver(driver),
			SSH: ssh, PrivateKey: r.PostFormValue("private_key"), Sandbox: sb, Cloud: cl, APIKey: strings.TrimSpace(r.PostFormValue("api_key")), Env: env})
	}
	if err != nil {
		if errors.Is(err, domain.ErrNotAllowed) {
			s.fail(w, r, &co, err)
			return
		}
		vm := views.EnvFormVM{FieldErr: map[string]string{}}
		environmentFormError(ctx, &vm, err)
		s.render(w, r, http.StatusOK, views.EnvProbeReport(&views.ProbeVM{Error: vm.Error}))
		return
	}
	s.render(w, r, http.StatusOK, views.EnvProbeReport(probeVM(ctx, rep, "")))
}

func (s *server) handleSetDefaultEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	var id *domain.ID
	if ref := trimForm(r, "environment"); ref != "" && ref != "local" {
		v, err := s.be.GetEnvironment(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		id = &v.ID
	}
	if err := s.be.SetDefaultEnvironment(ctx, actorFrom(ctx), co.ID, id); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, envBase(co)+"?ok=padrao")
}

func (s *server) environmentPageVM(r *http.Request, co domain.Company, v service.EnvironmentView) (views.EnvPageVM, error) {
	ctx := r.Context()
	loc := loadLoc(co.Timezone)
	href := envBase(co) + "/" + url.PathEscape(v.Slug)
	vm := views.EnvPageVM{Sections: settingsSections(ctx, "/c/"+co.Slug, "ambientes"), Name: v.Name, Slug: v.Slug, Description: v.Description,
		Badge: envBadge(ctx, v), DriverLabel: driverLabel(ctx, v.Driver), Target: envTarget(v), Unavailable: v.Unavailable, Warnings: v.Warnings,
		TestAction: href + "/testar", Archived: v.Status == domain.EnvArchived, ArchiveAction: href + "/arquivar", ReactivateAction: href + "/reativar"}
	if n := environmentNotices[r.URL.Query().Get("ok")]; n != "" {
		vm.Notice = i18n.T(ctx, n)
	}
	def := i18n.T(ctx, "não")
	if v.IsDefault {
		def = i18n.T(ctx, "sim")
	}
	for _, p := range v.Personas {
		vm.PersonaNames = append(vm.PersonaNames, p.Name)
	}
	vm.Facts = []views.KV{{K: i18n.T(ctx, "Tipo"), V: driverLabel(ctx, v.Driver)}, {K: i18n.T(ctx, "Alvo"), V: dashIfEmpty(envTarget(v))},
		{K: i18n.T(ctx, "Padrão da empresa"), V: def}, {K: "Personas", V: dashIfEmpty(strings.Join(vm.PersonaNames, ", "))},
		{K: i18n.T(ctx, "Em uso agora"), V: strconv.Itoa(v.ActiveLeases)}}
	if v.Driver == domain.EnvSSH || v.Driver == domain.EnvCloud {
		key := i18n.T(ctx, "guardada (cifrada)")
		if !v.KeySet {
			key = i18n.T(ctx, "ausente")
		}
		label := i18n.T(ctx, "Chave privada")
		if v.Driver == domain.EnvCloud {
			label = i18n.T(ctx, "Chave de API")
		}
		vm.Facts = append(vm.Facts, views.KV{K: label, V: key})
	}
	for _, p := range v.Projects {
		vm.ProjectNames = append(vm.ProjectNames, p.Name)
	}
	if len(vm.ProjectNames) > 0 {
		vm.Facts = append(vm.Facts, views.KV{K: i18n.T(ctx, "Projetos"), V: strings.Join(vm.ProjectNames, ", ")})
	}
	if len(v.Config.EnvNames) > 0 {
		vm.Facts = append(vm.Facts, views.KV{K: i18n.T(ctx, "Variáveis"), V: strings.Join(v.Config.EnvNames, ", ")})
	}
	if v.LastProbeReport != nil {
		when := ""
		if v.LastProbeAt != nil {
			when = relTimeCtx(ctx, *v.LastProbeAt, s.now(), loc)
		}
		vm.Probe = probeVM(ctx, *v.LastProbeReport, when)
	}
	f := views.EnvFormVM{Driver: string(v.Driver), Edit: true, Action: href, CancelHref: envBase(co), FieldErr: map[string]string{},
		Name: v.Name, Description: v.Description, KeySet: v.KeySet, EnvNames: v.Config.EnvNames, NetworkOptions: networkOptions(""),
		CloudNetworkOptions: cloudNetworkOptions("")}
	if c := v.Config.Cloud; c != nil {
		f.Snapshot, f.CloudCPUs, f.CloudMemory, f.CloudDisk, f.CloudRoot = c.Snapshot, strconv.Itoa(c.CPUs), strconv.Itoa(c.MemoryGiB), strconv.Itoa(c.DiskGiB), c.Root
		f.CloudNetworkOptions = cloudNetworkOptions(c.Network)
	}
	if c := v.Config.SSH; c != nil {
		f.Host, f.Port, f.User, f.Root, f.KnownHosts = c.Host, strconv.Itoa(c.Port), c.User, c.Root, c.KnownHosts
	}
	if c := v.Config.Sandbox; c != nil {
		f.Image, f.Memory, f.PIDs = c.Image, strconv.Itoa(c.MemoryMiB), strconv.Itoa(c.PIDsLimit)
		f.CPUs = strconv.FormatFloat(c.CPUs, 'f', -1, 64)
		f.NetworkOptions = networkOptions(c.Network)
	}
	vm.Form = f
	leases, err := s.be.ListEnvironmentLeases(ctx, co.ID, v.ID, 20)
	if err != nil {
		return vm, err
	}
	for _, l := range leases {
		b := views.Badge{Label: i18n.T(ctx, "Liberado"), Class: "done"}
		switch {
		case l.Orphan:
			b = views.Badge{Label: i18n.T(ctx, "Órfão"), Class: "warn"}
		case l.Status == domain.LeaseActive:
			b = views.Badge{Label: i18n.T(ctx, "Em uso"), Class: "soft", Live: true}
		case l.Status == domain.LeaseFailed:
			b = views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
		}
		run := l.RunName
		if run == "" {
			run = shortID(l.RunID)
		}
		vm.Leases = append(vm.Leases, views.LeaseRow{When: relTimeCtx(ctx, l.AcquiredAt, s.now(), loc), Run: run,
			RunHref: "/c/" + co.Slug + "/execucoes/" + l.RunID.String(), Persona: dashIfEmpty(l.PersonaName), Badge: b,
			RemoteDir: l.RemoteDir, Reason: dashIfEmpty(l.FailureReason)})
	}
	list, err := s.be.ListEnvironments(ctx, co.ID)
	if err != nil {
		return vm, err
	}
	vm.MoveOptions = []views.Option{{Value: "local", Label: i18n.T(ctx, "Padrão da empresa"), Selected: true}}
	for _, o := range list {
		if o.ID != v.ID && o.Status == domain.EnvActive {
			vm.MoveOptions = append(vm.MoveOptions, views.Option{Value: o.Slug, Label: o.Name})
		}
	}
	return vm, nil
}

func (s *server) handleEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	v, err := s.be.GetEnvironment(r.Context(), co.ID, r.PathValue("env"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.environmentPageVM(r, co, v)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.EnvironmentPage(s.envShell(r, co, vm.Name), vm))
}

func (s *server) envShell(r *http.Request, co domain.Company, name string) views.Shell {
	ctx := r.Context()
	return s.shell(r, co, "configuracoes", name, views.Crumb{Label: i18n.T(ctx, "Configurações"), Href: "/c/" + co.Slug + "/configuracoes"},
		views.Crumb{Label: i18n.T(ctx, "Ambientes"), Href: envBase(co)}, views.Crumb{Label: name})
}

func (s *server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur, err := s.be.GetEnvironment(ctx, co.ID, r.PathValue("env"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	ssh, sb, cl, env, err := environmentFromFormCloud(r, string(cur.Driver))
	if err == nil {
		name, desc := trimForm(r, "name"), trimForm(r, "description")
		in := service.UpdateEnvironmentInput{Name: &name, Description: &desc, SSH: ssh, Sandbox: sb, Cloud: cl, SetEnv: env, DropEnv: csv(r.PostFormValue("drop_env"))}
		if k := r.PostFormValue("private_key"); strings.TrimSpace(k) != "" {
			in.PrivateKey = &k
		}
		if k := strings.TrimSpace(r.PostFormValue("api_key")); k != "" {
			in.APIKey = &k
		}
		_, err = s.be.UpdateEnvironment(ctx, actorFrom(ctx), co.ID, cur.ID, in)
		if err == nil {
			redirect(w, r, envBase(co)+"/"+url.PathEscape(cur.Slug)+"?ok=salvo")
			return
		}
	}
	vm, verr := s.environmentPageVM(r, co, cur)
	if verr != nil {
		s.fail(w, r, &co, verr)
		return
	}
	fillEnvironmentForm(r, &vm.Form)
	environmentFormError(ctx, &vm.Form, err)
	s.render(w, r, statusFor(err), views.EnvironmentPage(s.envShell(r, co, vm.Name), vm))
}

func (s *server) handleProbeEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur, err := s.be.GetEnvironment(ctx, co.ID, r.PathValue("env"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	v, err := s.be.ProbeEnvironment(ctx, actorFrom(ctx), co.ID, cur.ID)
	if err != nil {
		if isHX(r) && !errors.Is(err, domain.ErrNotAllowed) {
			vm := views.EnvFormVM{FieldErr: map[string]string{}}
			environmentFormError(ctx, &vm, err)
			s.render(w, r, http.StatusOK, views.EnvProbeReport(&views.ProbeVM{Error: vm.Error}))
			return
		}
		s.fail(w, r, &co, err)
		return
	}
	if isHX(r) && v.LastProbeReport != nil {
		s.render(w, r, http.StatusOK, views.EnvProbeReport(probeVM(ctx, *v.LastProbeReport, i18n.T(ctx, "agora"))))
		return
	}
	ok := "testado"
	if v.LastProbeOK == nil || !*v.LastProbeOK {
		ok = "testefalho"
	}
	redirect(w, r, envBase(co)+"/"+url.PathEscape(v.Slug)+"?ok="+ok+"#teste")
}

func (s *server) handleArchiveEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur, err := s.be.GetEnvironment(ctx, co.ID, r.PathValue("env"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var to *domain.ID
	if ref := trimForm(r, "move_to"); ref != "" && ref != "local" {
		dst, err := s.be.GetEnvironment(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		to = &dst.ID
	}
	if _, err := s.be.ArchiveEnvironment(ctx, actorFrom(ctx), co.ID, cur.ID, to); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, envBase(co)+"?ok=arquivado")
}

func (s *server) handleReactivateEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	cur, err := s.be.GetEnvironment(ctx, co.ID, r.PathValue("env"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	if _, err := s.be.UpdateEnvironment(ctx, actorFrom(ctx), co.ID, cur.ID, service.UpdateEnvironmentInput{Reactivate: true}); err != nil {
		s.fail(w, r, &co, err)
		return
	}
	redirect(w, r, envBase(co)+"/"+url.PathEscape(cur.Slug)+"?ok=reativado")
}

// personaEnvVM monta o cartão de ambiente da persona.
func (s *server) personaEnvVM(ctx context.Context, co domain.Company, p domain.Persona) (views.PersonaEnvVM, error) {
	list, err := s.be.ListEnvironments(ctx, co.ID)
	if err != nil {
		return views.PersonaEnvVM{}, err
	}
	defName := i18n.T(ctx, "Local · este servidor")
	for _, v := range list {
		if v.IsDefault {
			defName = v.Name
		}
	}
	vm := views.PersonaEnvVM{Action: "/c/" + co.Slug + "/personas/" + url.PathEscape(p.Slug) + "/ambiente", SettingsHref: envBase(co),
		Current: defName, Source: i18n.T(ctx, "(padrão da empresa)")}
	vm.Options = []views.Option{{Value: "default", Label: i18n.Tf(ctx, "Padrão da empresa (%s)", defName), Selected: p.EnvironmentID == nil}}
	for _, v := range list {
		mine := p.EnvironmentID != nil && *p.EnvironmentID == v.ID
		if mine {
			vm.Current, vm.Source = v.Name, "("+string(v.Driver)+")"
			if v.Status == domain.EnvArchived {
				vm.Source = i18n.T(ctx, "(arquivado: as execuções usam o padrão da empresa)")
			}
		}
		if v.Status == domain.EnvActive {
			vm.Options = append(vm.Options, views.Option{Value: v.Slug, Label: v.Name + " (" + string(v.Driver) + ")", Selected: mine})
		}
	}
	return vm, nil
}

func (s *server) handlePersonaEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm, err := s.personaEnvVM(ctx, co, p)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	s.render(w, r, http.StatusOK, views.PersonaEnvironmentCard(vm))
}

func (s *server) handleSetPersonaEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	p, err := s.be.GetPersona(ctx, co.ID, r.PathValue("persona"))
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	var id *domain.ID
	if ref := trimForm(r, "environment"); ref != "" && ref != "default" && ref != "local" {
		v, err := s.be.GetEnvironment(ctx, co.ID, ref)
		if err != nil {
			s.fail(w, r, &co, err)
			return
		}
		id = &v.ID
	}
	np, err := s.be.SetPersonaEnvironment(ctx, actorFrom(ctx), co.ID, p.ID, id)
	if err != nil {
		if !isHX(r) || errors.Is(err, domain.ErrNotAllowed) {
			s.fail(w, r, &co, err)
			return
		}
		vm, verr := s.personaEnvVM(ctx, co, p)
		if verr != nil {
			s.fail(w, r, &co, verr)
			return
		}
		_, title, msg := friendly(ctx, err)
		vm.Error = title + ". " + msg
		s.render(w, r, http.StatusOK, views.PersonaEnvironmentCard(vm))
		return
	}
	if !isHX(r) {
		redirect(w, r, "/c/"+co.Slug+"/personas/"+url.PathEscape(p.Slug)+"#ambiente")
		return
	}
	vm, err := s.personaEnvVM(ctx, co, np)
	if err != nil {
		s.fail(w, r, &co, err)
		return
	}
	vm.Notice = i18n.T(ctx, "Ambiente salvo: vale a partir da próxima execução.")
	s.render(w, r, http.StatusOK, views.PersonaEnvironmentCard(vm))
}

// handleRunEnvironment é o fragmento com o ambiente em que a execução rodou (o local não deixa registro).
func (s *server) handleRunEnvironment(w http.ResponseWriter, r *http.Request, co domain.Company) {
	ctx := r.Context()
	runID, err := domain.ParseID(r.PathValue("run"))
	if err != nil {
		s.fail(w, r, &co, domain.ErrNotFound)
		return
	}
	vm := views.RunEnvVM{Label: i18n.T(ctx, "Local · este servidor")}
	info, err := s.be.RunEnvironmentOf(ctx, co.ID, runID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if run, rerr := s.be.GetRun(ctx, co.ID, runID); rerr == nil && run.EnvironmentSource != "" {
			vm.Source = envSourceText(ctx, run.EnvironmentSource)
		}
	case err != nil:
		s.fail(w, r, &co, err)
		return
	default:
		vm.Label = info.Name + " (" + string(info.Driver) + ")"
		if info.Source != "" {
			vm.Source = envSourceText(ctx, info.Source)
		}
		vm.Href = envBase(co) + "/" + url.PathEscape(info.Slug)
		vm.RemoteDir = info.Lease.RemoteDir
		if info.Lease.Status == domain.LeaseFailed {
			b := views.Badge{Label: i18n.T(ctx, "Falhou"), Class: "danger"}
			vm.Badge = &b
		}
	}
	s.render(w, r, http.StatusOK, views.RunEnvironmentValue(vm))
}
