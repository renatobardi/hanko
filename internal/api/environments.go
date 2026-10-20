package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Ambientes de execução (ADR-0037): onde as runs das personas rodam (local, host SSH ou sandbox). Leitura de
// qualquer membro; escrita, teste e escolha do padrão de admin (o service confere o papel). A chave privada e
// os valores das variáveis só entram: nenhuma resposta os devolve. Run tokens não usam estes endpoints.

// environmentsBackend é o subconjunto do service para ambientes; opcional pelo mesmo motivo de skillsBackend.
type environmentsBackend interface {
	CreateEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateEnvironmentInput) (service.EnvironmentView, error)
	UpdateEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in service.UpdateEnvironmentInput) (service.EnvironmentView, error)
	ArchiveEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, moveTo *domain.ID) (service.EnvironmentView, error)
	SetDefaultEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, id *domain.ID) error
	SetPersonaEnvironment(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, envID *domain.ID) (domain.Persona, error)
	ListEnvironments(ctx context.Context, companyID domain.ID) ([]service.EnvironmentView, error)
	GetEnvironment(ctx context.Context, companyID domain.ID, ref string) (service.EnvironmentView, error)
	ListEnvironmentLeases(ctx context.Context, companyID, envID domain.ID, limit int) ([]service.EnvironmentLeaseView, error)
	ProbeEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (service.EnvironmentView, error)
	ProbeEnvironmentDraft(ctx context.Context, companyID domain.ID, in service.CreateEnvironmentInput) (domain.ProbeReport, error)
	SetProjectEnvironment(ctx context.Context, actor domain.Actor, companyID, projectID domain.ID, envID *domain.ID) (domain.Project, error)
}

func (s *server) environments() (environmentsBackend, error) {
	b, ok := s.svc.(environmentsBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "ambientes indisponíveis neste servidor")
	}
	return b, nil
}

// SSHConfig é a parte não secreta de um ambiente SSH.
type SSHConfig struct {
	Host                string `json:"host" example:"build.exemplo.com" doc:"Host ou IP; precisa estar na lista do operador (HANKO_ENV_SSH_ALLOW_HOSTS)."`
	Port                int    `json:"port,omitempty" minimum:"0" maximum:"65535" doc:"Porta (padrão 22)."`
	Username            string `json:"username" example:"hanko"`
	RemoteWorkspaceRoot string `json:"remote_workspace_root" example:"/srv/hanko" doc:"Pasta absoluta do host sob a qual cada execução ganha a sua cópia do workspace."`
	KnownHosts          string `json:"known_hosts" doc:"Linhas de known_hosts com a chave do host (obrigatório; chave desconhecida nunca é aceita). Ex.: saída de ssh-keyscan -H host."`
}

// SandboxConfig é a configuração de um ambiente sandbox (contêiner por execução).
type SandboxConfig struct {
	Image     string  `json:"image" example:"ghcr.io/acme/agente@sha256:..." doc:"Imagem; prefira fixar por digest (@sha256:), com tag há um aviso."`
	Network   string  `json:"network,omitempty" enum:"none,bridge" doc:"Rede do contêiner (padrão none: sem rede)."`
	MemoryMiB int     `json:"memory_mib,omitempty" minimum:"0" doc:"Memória em MiB (padrão 2048)."`
	CPUs      float64 `json:"cpus,omitempty" minimum:"0" doc:"CPUs (padrão 2)."`
	PIDsLimit int     `json:"pids_limit,omitempty" minimum:"0" doc:"Máximo de processos (padrão 512)."`
}

// CloudConfig é a configuração de um ambiente cloud (sandbox em nuvem por execução, ADR-0055).
type CloudConfig struct {
	Provider            string `json:"provider,omitempty" enum:"daytona" doc:"Provider (padrão daytona); o operador liga o driver com HANKO_ENV_CLOUD."`
	Snapshot            string `json:"snapshot,omitempty" doc:"Snapshot/imagem do provider; vazio = o padrão dele."`
	CPUs                int    `json:"cpus,omitempty" minimum:"0" doc:"CPUs do sandbox (padrão 2)."`
	MemoryGiB           int    `json:"memory_gib,omitempty" minimum:"0" doc:"Memória em GiB (padrão 4)."`
	DiskGiB             int    `json:"disk_gib,omitempty" minimum:"0" doc:"Disco em GiB (padrão 10)."`
	Network             string `json:"network,omitempty" enum:"none,full" doc:"Rede do sandbox (padrão full: o harness precisa alcançar o modelo e o MCP)."`
	RemoteWorkspaceRoot string `json:"remote_workspace_root,omitempty" doc:"Pasta absoluta do sandbox sob a qual cada execução ganha a sua cópia (padrão /home/daytona/hanko)."`
}

// ProbeStep é uma verificação do teste de conexão.
type ProbeStep struct {
	Name   string `json:"name" doc:"connect, system, workspace_root, docker, image ou bin:<nome>."`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// ProbeReport é o relatório de um teste de conexão.
type ProbeReport struct {
	OK       bool              `json:"ok"`
	Steps    []ProbeStep       `json:"steps"`
	System   string            `json:"system,omitempty" doc:"uname -sm do alvo."`
	Binaries map[string]string `json:"binaries,omitempty" doc:"Caminho de git, claude e codex no alvo (vazio = não encontrado)."`
	// Capabilities: o que o alvo oferece, medido pelo teste (sync, git, claude, codex, hanko-reachable) ou garantido pelo driver (network, isolated, persistent).
	Capabilities []string `json:"capabilities,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	DurationMs   int64    `json:"duration_ms"`
}

// EnvironmentPersona é uma persona que escolheu o ambiente.
type EnvironmentPersona struct {
	ID   string `json:"id" format:"uuid"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Environment é um ambiente de execução cadastrado. O local é implícito e não aparece na lista.
type Environment struct {
	ID           string               `json:"id" format:"uuid"`
	CompanyID    string               `json:"company_id" format:"uuid"`
	Slug         string               `json:"slug" example:"build"`
	Name         string               `json:"name" example:"Servidor de build"`
	Description  string               `json:"description,omitempty"`
	Driver       string               `json:"driver" enum:"local,ssh,sandbox,cloud" doc:"local = ambiente local explícito; sandbox = contêiner docker; cloud = sandbox em nuvem."`
	Status       string               `json:"status" enum:"active,archived"`
	SSH          *SSHConfig           `json:"ssh,omitempty"`
	Sandbox      *SandboxConfig       `json:"sandbox,omitempty"`
	Cloud        *CloudConfig         `json:"cloud,omitempty"`
	EnvNames     []string             `json:"env_names" doc:"Nomes das variáveis do ambiente (os valores nunca são devolvidos)."`
	IsDefault    bool                 `json:"is_default" doc:"Padrão da empresa para personas sem ambiente próprio."`
	KeySet       bool                 `json:"key_set" doc:"SSH: há chave privada guardada; cloud: há chave de API do provider (nunca devolvidas)."`
	Available    bool                 `json:"available" doc:"O driver pode rodar neste servidor (decisão do operador)."`
	Unavailable  string               `json:"unavailable,omitempty" doc:"Por que o driver não pode rodar agora."`
	Warnings     []string             `json:"warnings,omitempty"`
	Personas     []EnvironmentPersona `json:"personas" doc:"Personas que escolheram este ambiente."`
	Projects     []EnvironmentPersona `json:"projects" doc:"Projetos que escolheram este ambiente."`
	ActiveLeases int                  `json:"active_leases" doc:"Execuções usando o ambiente agora."`
	LastProbeAt  *time.Time           `json:"last_probe_at,omitempty"`
	LastProbeOK  *bool                `json:"last_probe_ok,omitempty"`
	LastProbe    *ProbeReport         `json:"last_probe,omitempty" doc:"Relatório do último teste de conexão."`
	CreatedBy    ActorRef             `json:"created_by"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

// EnvironmentLease é o uso do ambiente por uma execução.
type EnvironmentLease struct {
	ID            string     `json:"id" format:"uuid"`
	RunID         string     `json:"run_id" format:"uuid"`
	RunName       string     `json:"run_name,omitempty"`
	RunStatus     string     `json:"run_status"`
	PersonaName   string     `json:"persona_name,omitempty"`
	Status        string     `json:"status" enum:"active,released,failed"`
	RemoteDir     string     `json:"remote_dir,omitempty" doc:"Diretório da execução no alvo (mantido quando o uso falha)."`
	AcquiredAt    time.Time  `json:"acquired_at"`
	ReleasedAt    *time.Time `json:"released_at,omitempty"`
	FailureReason string     `json:"failure_reason,omitempty"`
	Orphan        bool       `json:"orphan" doc:"O uso ficou ativo mas a execução já terminou (o servidor caiu no meio)."`
}

func probeOut(r domain.ProbeReport) ProbeReport {
	steps := make([]ProbeStep, len(r.Steps))
	for i, st := range r.Steps {
		steps[i] = ProbeStep{Name: st.Name, OK: st.OK, Detail: st.Detail}
	}
	return ProbeReport{OK: r.OK, Steps: steps, System: r.System, Binaries: r.Binaries, Capabilities: r.Capabilities, Warnings: r.Warnings, DurationMs: r.DurationMs}
}

func environmentOut(v service.EnvironmentView) Environment {
	o := Environment{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Slug: v.Slug, Name: v.Name, Description: v.Description,
		Driver: string(v.Driver), Status: string(v.Status), EnvNames: v.Config.EnvNames, IsDefault: v.IsDefault, KeySet: v.KeySet,
		Available: v.Unavailable == "", Unavailable: v.Unavailable, Warnings: v.Warnings, Personas: []EnvironmentPersona{}, Projects: []EnvironmentPersona{},
		ActiveLeases: v.ActiveLeases, LastProbeAt: v.LastProbeAt, LastProbeOK: v.LastProbeOK,
		CreatedBy: actorRef(v.CreatedBy), CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	if o.EnvNames == nil {
		o.EnvNames = []string{}
	}
	if c := v.Config.SSH; c != nil {
		o.SSH = &SSHConfig{Host: c.Host, Port: c.Port, Username: c.User, RemoteWorkspaceRoot: c.Root, KnownHosts: c.KnownHosts}
	}
	if c := v.Config.Sandbox; c != nil {
		o.Sandbox = &SandboxConfig{Image: c.Image, Network: c.Network, MemoryMiB: c.MemoryMiB, CPUs: c.CPUs, PIDsLimit: c.PIDsLimit}
	}
	if c := v.Config.Cloud; c != nil {
		o.Cloud = &CloudConfig{Provider: c.Provider, Snapshot: c.Snapshot, CPUs: c.CPUs, MemoryGiB: c.MemoryGiB, DiskGiB: c.DiskGiB, Network: c.Network, RemoteWorkspaceRoot: c.Root}
	}
	for _, p := range v.Projects {
		o.Projects = append(o.Projects, EnvironmentPersona{ID: idStr(p.ID), Slug: p.Slug, Name: p.Name})
	}
	if v.LastProbeReport != nil {
		r := probeOut(*v.LastProbeReport)
		o.LastProbe = &r
	}
	for _, p := range v.Personas {
		o.Personas = append(o.Personas, EnvironmentPersona{ID: idStr(p.ID), Slug: p.Slug, Name: p.Name})
	}
	return o
}

func leaseOut(l service.EnvironmentLeaseView) EnvironmentLease {
	return EnvironmentLease{ID: idStr(l.ID), RunID: idStr(l.RunID), RunName: l.RunName, RunStatus: string(l.RunStatus), PersonaName: l.PersonaName,
		Status: l.Status, RemoteDir: l.RemoteDir, AcquiredAt: l.AcquiredAt, ReleasedAt: l.ReleasedAt, FailureReason: l.FailureReason, Orphan: l.Orphan}
}

func sshIn(c *SSHConfig) *domain.SSHConfig {
	if c == nil {
		return nil
	}
	return &domain.SSHConfig{Host: c.Host, Port: c.Port, User: c.Username, Root: c.RemoteWorkspaceRoot, KnownHosts: c.KnownHosts}
}

func sandboxIn(c *SandboxConfig) *domain.SandboxConfig {
	if c == nil {
		return nil
	}
	return &domain.SandboxConfig{Image: c.Image, Network: c.Network, MemoryMiB: c.MemoryMiB, CPUs: c.CPUs, PIDsLimit: c.PIDsLimit}
}

func cloudIn(c *CloudConfig) *domain.CloudConfig {
	if c == nil {
		return nil
	}
	return &domain.CloudConfig{Provider: c.Provider, Snapshot: c.Snapshot, CPUs: c.CPUs, MemoryGiB: c.MemoryGiB, DiskGiB: c.DiskGiB, Network: c.Network, Root: c.RemoteWorkspaceRoot}
}

type EnvironmentPath struct {
	CompanyPath
	Environment string `path:"environment" doc:"Id ou slug do ambiente." example:"build"`
}

// environmentBody é o corpo de criação e de teste de rascunho.
type environmentBody struct {
	Name        string            `json:"name" minLength:"1" maxLength:"120" example:"Servidor de build"`
	Slug        string            `json:"slug,omitempty" pattern:"^[a-z0-9][a-z0-9-]{0,62}$" doc:"Derivado do nome quando omitido."`
	Description string            `json:"description,omitempty" maxLength:"500"`
	Driver      string            `json:"driver" enum:"local,ssh,sandbox,cloud" doc:"local = ambiente local explícito (só variáveis); o local implícito não precisa de cadastro."`
	SSH         *SSHConfig        `json:"ssh,omitempty"`
	PrivateKey  string            `json:"private_key,omitempty" doc:"SSH: chave privada (PEM/OpenSSH, sem senha). Guardada cifrada; nunca devolvida."`
	Sandbox     *SandboxConfig    `json:"sandbox,omitempty"`
	Cloud       *CloudConfig      `json:"cloud,omitempty"`
	APIKey      string            `json:"api_key,omitempty" doc:"Cloud: chave de API do provider (ou ${secret:NOME}). Guardada cifrada; nunca devolvida."`
	Env         map[string]string `json:"env,omitempty" doc:"Variáveis do harness no ambiente (segredos; HANKO_*, PATH e HOME são reservados). Guardadas cifradas; nunca devolvidas."`
}

func (b environmentBody) input() service.CreateEnvironmentInput {
	return service.CreateEnvironmentInput{Name: b.Name, Slug: b.Slug, Description: b.Description, Driver: domain.EnvironmentDriver(b.Driver),
		SSH: sshIn(b.SSH), PrivateKey: b.PrivateKey, Sandbox: sandboxIn(b.Sandbox), Cloud: cloudIn(b.Cloud), APIKey: b.APIKey, Env: b.Env}
}

type createEnvironmentIn struct {
	CompanyPath
	Body environmentBody
}

type probeDraftIn struct {
	CompanyPath
	Body environmentBody
}

type updateEnvironmentIn struct {
	EnvironmentPath
	Body struct {
		Name        *string           `json:"name,omitempty" minLength:"1" maxLength:"120"`
		Description *string           `json:"description,omitempty" maxLength:"500"`
		SSH         *SSHConfig        `json:"ssh,omitempty" doc:"Substitui a configuração SSH (a chave fica, salvo private_key)."`
		PrivateKey  *string           `json:"private_key,omitempty" doc:"Troca a chave privada."`
		Sandbox     *SandboxConfig    `json:"sandbox,omitempty" doc:"Substitui a configuração do sandbox."`
		Cloud       *CloudConfig      `json:"cloud,omitempty" doc:"Substitui a configuração do sandbox em nuvem."`
		APIKey      *string           `json:"api_key,omitempty" doc:"Troca a chave de API do provider cloud."`
		SetEnv      map[string]string `json:"set_env,omitempty" doc:"Define ou troca estas variáveis; as demais ficam."`
		DropEnv     []string          `json:"drop_env,omitempty"`
		Reactivate  bool              `json:"reactivate,omitempty" doc:"Tira o ambiente do arquivo."`
	}
}

type archiveEnvironmentIn struct {
	EnvironmentPath
	MovePersonasTo string `query:"move_personas_to" doc:"Ambiente (id ou slug) para onde vão as personas e os projetos deste; vazio ou local = o padrão da empresa."`
}

type listEnvironmentsIn struct {
	CompanyPath
	Page
}

type leasesIn struct {
	EnvironmentPath
	Limit int `query:"limit" minimum:"1" maximum:"200" default:"20" doc:"Máximo de usos (mais recentes primeiro)."`
}

type setDefaultEnvironmentIn struct {
	CompanyPath
	Body struct {
		Environment string `json:"environment" doc:"Id ou slug do ambiente padrão, ou local." example:"local"`
	}
}

// environmentRef resolve uma referência de ambiente; "", "local" e "default" são o padrão/local (nil).
func (s *server) environmentRef(ctx context.Context, eb environmentsBackend, companyID domain.ID, ref string) (*domain.ID, error) {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "", "local", "default":
		return nil, nil
	}
	v, err := eb.GetEnvironment(ctx, companyID, ref)
	if err != nil {
		return nil, s.fail(ctx, err)
	}
	return &v.ID, nil
}

// setPersonaEnvironment aplica o campo environment do PATCH de persona ("" = sem mudança).
func (s *server) setPersonaEnvironment(ctx context.Context, sc scope, personaID domain.ID, ref string) error {
	if ref == "" {
		return nil
	}
	eb, err := s.environments()
	if err != nil {
		return err
	}
	id, err := s.environmentRef(ctx, eb, sc.Company.ID, ref) // local/default = o padrão da empresa
	if err != nil {
		return err
	}
	if _, err := eb.SetPersonaEnvironment(ctx, sc.Actor, sc.Company.ID, personaID, id); err != nil {
		return s.fail(ctx, err)
	}
	return nil
}

func (s *server) registerEnvironments(api huma.API) {
	// pre resolve a empresa e o backend das rotas de escrita (sem run token).
	pre := func(ctx context.Context, company string, write bool) (environmentsBackend, scope, error) {
		if write {
			if err := forbidPersona(ctx); err != nil {
				return nil, scope{}, err
			}
		}
		eb, err := s.environments()
		if err != nil {
			return nil, scope{}, err
		}
		sc, err := s.scope(ctx, company)
		return eb, sc, err
	}

	huma.Register(api, created(op("create-environment", http.MethodPost, "/companies/{company}/environments", "environments", "Cadastra um ambiente de execução (SSH ou sandbox)")),
		func(ctx context.Context, in *createEnvironmentIn) (*ItemOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := eb.CreateEnvironment(ctx, sc.Actor, sc.Company.ID, in.Body.input())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Environment]{Body: environmentOut(v)}, nil
		})

	huma.Register(api, op("list-environments", http.MethodGet, "/companies/{company}/environments", "environments",
		"Lista os ambientes cadastrados (o local é implícito: é o padrão quando nenhum tem is_default)"),
		func(ctx context.Context, in *listEnvironmentsIn) (*ListOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			list, err := eb.ListEnvironments(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(list, in.Page, environmentOut), nil
		})

	huma.Register(api, op("probe-environment-draft", http.MethodPost, "/companies/{company}/environments/probe", "environments",
		"Testa a conexão de um rascunho de ambiente, sem gravar"),
		func(ctx context.Context, in *probeDraftIn) (*ItemOut[ProbeReport], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			r, err := eb.ProbeEnvironmentDraft(ctx, sc.Company.ID, in.Body.input())
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ProbeReport]{Body: probeOut(r)}, nil
		})

	huma.Register(api, noContent(op("set-default-environment", http.MethodPut, "/companies/{company}/environments/default", "environments",
		"Escolhe o ambiente padrão da empresa (local = processos filhos neste servidor)")),
		func(ctx context.Context, in *setDefaultEnvironmentIn) (*struct{}, error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			id, err := s.environmentRef(ctx, eb, sc.Company.ID, in.Body.Environment)
			if err != nil {
				return nil, err
			}
			if err := eb.SetDefaultEnvironment(ctx, sc.Actor, sc.Company.ID, id); err != nil {
				return nil, s.fail(ctx, err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, op("get-environment", http.MethodGet, "/companies/{company}/environments/{environment}", "environments", "Obtém um ambiente"),
		func(ctx context.Context, in *EnvironmentPath) (*ItemOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			v, err := eb.GetEnvironment(ctx, sc.Company.ID, in.Environment)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Environment]{Body: environmentOut(v)}, nil
		})

	huma.Register(api, op("update-environment", http.MethodPatch, "/companies/{company}/environments/{environment}", "environments", "Atualiza um ambiente"),
		func(ctx context.Context, in *updateEnvironmentIn) (*ItemOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := eb.GetEnvironment(ctx, sc.Company.ID, in.Environment)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			b := in.Body
			v, err := eb.UpdateEnvironment(ctx, sc.Actor, sc.Company.ID, cur.ID, service.UpdateEnvironmentInput{Name: b.Name, Description: b.Description,
				SSH: sshIn(b.SSH), PrivateKey: b.PrivateKey, Sandbox: sandboxIn(b.Sandbox), Cloud: cloudIn(b.Cloud), APIKey: b.APIKey,
				SetEnv: b.SetEnv, DropEnv: b.DropEnv, Reactivate: b.Reactivate})
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Environment]{Body: environmentOut(v)}, nil
		})

	huma.Register(api, op("archive-environment", http.MethodDelete, "/companies/{company}/environments/{environment}", "environments",
		"Arquiva um ambiente e move as personas dele (as execuções em andamento seguem até o fim)"),
		func(ctx context.Context, in *archiveEnvironmentIn) (*ItemOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := eb.GetEnvironment(ctx, sc.Company.ID, in.Environment)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			to, err := s.environmentRef(ctx, eb, sc.Company.ID, in.MovePersonasTo)
			if err != nil {
				return nil, err
			}
			v, err := eb.ArchiveEnvironment(ctx, sc.Actor, sc.Company.ID, cur.ID, to)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Environment]{Body: environmentOut(v)}, nil
		})

	huma.Register(api, op("probe-environment", http.MethodPost, "/companies/{company}/environments/{environment}/probe", "environments",
		"Testa a conexão do ambiente (sistema, pasta gravável, git/claude/codex) e grava o relatório"),
		func(ctx context.Context, in *EnvironmentPath) (*ItemOut[Environment], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := eb.GetEnvironment(ctx, sc.Company.ID, in.Environment)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := eb.ProbeEnvironment(ctx, sc.Actor, sc.Company.ID, cur.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Environment]{Body: environmentOut(v)}, nil
		})

	huma.Register(api, op("list-environment-leases", http.MethodGet, "/companies/{company}/environments/{environment}/leases", "environments",
		"Lista os usos recentes do ambiente pelas execuções"),
		func(ctx context.Context, in *leasesIn) (*ListOut[EnvironmentLease], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			cur, err := eb.GetEnvironment(ctx, sc.Company.ID, in.Environment)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			list, err := eb.ListEnvironmentLeases(ctx, sc.Company.ID, cur.ID, in.Limit)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(list, Page{Limit: in.Limit}, leaseOut), nil
		})

	huma.Register(api, op("set-project-environment", http.MethodPut, "/companies/{company}/projects/{project}/environment", "environments",
		"Escolhe o ambiente de execução do projeto (admin); a persona tem precedência sobre o projeto, e o projeto sobre o padrão da empresa"),
		func(ctx context.Context, in *setProjectEnvironmentIn) (*ItemOut[Project], error) {
			eb, sc, err := pre(ctx, in.Company, true)
			if err != nil {
				return nil, err
			}
			pid, err := s.projectRef(ctx, sc.Company.ID, in.Project)
			if err != nil || pid == nil {
				return nil, s.fail(ctx, orNotFound(err))
			}
			id, err := s.environmentRef(ctx, eb, sc.Company.ID, in.Body.Environment)
			if err != nil {
				return nil, err
			}
			p, err := eb.SetProjectEnvironment(ctx, sc.Actor, sc.Company.ID, *pid, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Project]{Body: projectOut(p)}, nil
		})
}

type setProjectEnvironmentIn struct {
	ProjectPath
	Body struct {
		Environment string `json:"environment" doc:"Id ou slug do ambiente; vazio, local ou default = o padrão da empresa." example:"build"`
	}
}
