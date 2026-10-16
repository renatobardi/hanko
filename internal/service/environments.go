package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/execenv"
	"github.com/renatobardi/hanko/internal/store"
)

// Ambientes de execução (ADR-0037). Leitura é de qualquer membro; criar, editar, testar, arquivar, escolher o
// padrão e o ambiente de uma persona é de admin. A chave privada e os valores das variáveis ficam cifrados
// (o mesmo cofre das integrações, AAD = id do ambiente) e não voltam por nenhuma leitura. Se cada driver pode
// rodar é decisão do operador (Service.Environments), não de quem administra a empresa.

// reservedEnvironmentSlugs são segmentos fixos das rotas (API e UI) e o nome do ambiente implícito.
var reservedEnvironmentSlugs = []string{"local", "probe", "default", "padrao", "novo", "nova", "testar"}

// environmentSecrets é o conteúdo cifrado de um ambiente.
type environmentSecrets struct {
	PrivateKey string            `json:"private_key,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

func (v environmentSecrets) empty() bool { return v.PrivateKey == "" && len(v.Env) == 0 }

// EnvironmentView é um ambiente com o que as telas mostram.
type EnvironmentView struct {
	domain.Environment
	// Unavailable é o motivo de o driver não poder rodar neste servidor (interruptor do operador desligado,
	// binário ausente, host fora da lista); vazio = disponível.
	Unavailable string
	// Warnings são avisos que não impedem a execução (imagem sem digest, sem URL pública).
	Warnings []string
	// Personas são as personas que escolheram este ambiente.
	Personas []PersonaRef
	// ActiveLeases é quantas runs estão usando o ambiente agora.
	ActiveLeases int
}

// PersonaRef identifica uma persona numa lista.
type PersonaRef struct {
	ID   domain.ID
	Slug string
	Name string
}

// EnvironmentLeaseView é um uso do ambiente com a run.
type EnvironmentLeaseView struct {
	domain.EnvironmentLease
	RunStatus   domain.RunStatus
	RunName     string
	PersonaName string
	// Orphan: o uso ficou ativo mas a run já terminou (o servidor caiu no meio); o diretório remoto pode
	// ter ficado para trás.
	Orphan bool
}

// CreateEnvironmentInput define um ambiente novo.
type CreateEnvironmentInput struct {
	Name, Slug, Description string
	Driver                  domain.EnvironmentDriver
	SSH                     *domain.SSHConfig
	PrivateKey              string
	Sandbox                 *domain.SandboxConfig
	Env                     map[string]string
}

// UpdateEnvironmentInput muda um ambiente; nil fica como está. O driver é fixo.
type UpdateEnvironmentInput struct {
	Name, Description *string
	SSH               *domain.SSHConfig // substitui a configuração SSH inteira (a chave fica, salvo PrivateKey)
	PrivateKey        *string
	Sandbox           *domain.SandboxConfig
	SetEnv            map[string]string
	DropEnv           []string
	// Reactivate tira o ambiente do arquivo.
	Reactivate bool
}

// RunEnvironment é onde uma run vai rodar, já resolvido (persona → padrão da empresa → local) e com os
// segredos abertos. Só o executor o usa.
type RunEnvironment struct {
	// Environment é nil para o local.
	Environment *domain.Environment
	// Source: "persona", "default" ou "local".
	Source string
	Target execenv.Target
	Config execenv.Config
	// Warnings vão para o log da run.
	Warnings []string
}

func validateEnvironmentText(name, desc string) (string, string, error) {
	n := strings.TrimSpace(name)
	d := strings.TrimSpace(desc)
	switch {
	case n == "":
		return "", "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(n) > domain.MaxEnvironmentNameRunes:
		return "", "", domain.Invalid("name", fmt.Sprintf("at most %d characters", domain.MaxEnvironmentNameRunes))
	case utf8.RuneCountInString(d) > domain.MaxEnvironmentDescRunes:
		return "", "", domain.Invalid("description", fmt.Sprintf("at most %d characters", domain.MaxEnvironmentDescRunes))
	}
	return n, d, nil
}

// checkDriverAllowed aplica os interruptores do operador na criação e na edição.
func (s *Service) checkDriverAllowed(d domain.EnvironmentDriver, ssh *domain.SSHConfig) error {
	switch d {
	case domain.EnvSSH:
		if !s.Environments.SSHEnabled() {
			return domain.Invalid("driver", execenv.ErrSSHOff.Error())
		}
		if ssh != nil {
			if err := s.Environments.CheckSSHHost(ssh.Host); err != nil {
				return domain.Invalid("host", err.Error())
			}
		}
	case domain.EnvSandbox:
		if !strings.EqualFold(strings.TrimSpace(s.Environments.Sandbox), execenv.SandboxDocker) {
			return domain.Invalid("driver", execenv.ErrSandboxOff.Error())
		}
	default:
		return domain.Invalid("driver", "must be ssh or sandbox (local is implicit)")
	}
	return nil
}

func (s *Service) sealEnvironment(id uuid.UUID, v environmentSecrets) (string, error) {
	if v.empty() {
		return "", nil
	}
	if !s.Integrations.Secrets.Enabled() {
		return "", domain.Invalid("secrets", "HANKO_SECRET_KEY não está configurada neste servidor; sem ela não há como guardar a chave nem as variáveis do ambiente")
	}
	raw, err := json.Marshal(v) //nolint:gosec // serializa os segredos só para cifrar logo abaixo (secretbox); nunca sai em claro
	if err != nil {
		return "", fmt.Errorf("marshal environment secrets: %w", err)
	}
	enc, err := s.Integrations.Secrets.Seal(raw, id.String())
	if err != nil {
		return "", fmt.Errorf("seal environment secrets: %w", err)
	}
	return enc, nil
}

func (s *Service) openEnvironment(id uuid.UUID, enc string) (environmentSecrets, error) {
	var v environmentSecrets
	if enc == "" {
		return v, nil
	}
	raw, err := s.Integrations.Secrets.Open(enc, id.String())
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("environment secrets: %w", err)
	}
	return v, nil
}

func environmentFromRow(r store.Environment) domain.Environment {
	var cfg domain.EnvironmentConfig
	_ = json.Unmarshal(r.Config, &cfg)
	e := domain.Environment{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, Description: r.Description,
		Driver: domain.EnvironmentDriver(r.Driver), Status: domain.EnvironmentStatus(r.Status), Config: cfg, IsDefault: r.IsDefault,
		LastProbeAt: r.LastProbeAt, CreatedBy: actorOf(r.CreatedByType, r.CreatedByID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	// o segredo de um ambiente SSH é a chave (as variáveis são opcionais): key_set diz se há chave
	e.KeySet = r.SecretEnc != "" && e.Driver == domain.EnvSSH
	if r.LastProbeOk.Valid {
		ok := r.LastProbeOk.Bool
		e.LastProbeOK = &ok
	}
	if len(r.LastProbeReport) > 0 {
		var rep domain.ProbeReport
		if json.Unmarshal(r.LastProbeReport, &rep) == nil {
			e.LastProbeReport = &rep
		}
	}
	if e.Config.EnvNames == nil {
		e.Config.EnvNames = []string{}
	}
	return e
}

// view completa o ambiente com a disponibilidade e os avisos do operador.
func (s *Service) environmentView(e domain.Environment) EnvironmentView {
	v := EnvironmentView{Environment: e}
	if err := s.Environments.DriverStatus(e.Driver); err != nil {
		v.Unavailable = err.Error()
	} else if e.Driver == domain.EnvSSH && e.Config.SSH != nil {
		if err := s.Environments.CheckSSHHost(e.Config.SSH.Host); err != nil {
			v.Unavailable = err.Error()
		}
	}
	if e.Driver.Remote() {
		offline := e.Driver == domain.EnvSandbox && e.Config.Sandbox != nil && e.Config.Sandbox.Network == domain.SandboxNetworkNone
		if err := s.Environments.RemoteURLStatus(); err != nil && !offline {
			v.Warnings = append(v.Warnings, err.Error())
		}
	}
	if e.Driver == domain.EnvSandbox && e.Config.Sandbox != nil {
		if !e.Config.Sandbox.Pinned() {
			v.Warnings = append(v.Warnings, "imagem sem digest (@sha256:…): o conteúdo pode mudar entre execuções")
		}
		if e.Config.Sandbox.Network == domain.SandboxNetworkNone {
			v.Warnings = append(v.Warnings, "rede none: o harness não alcança o MCP do Hanko nem a API do modelo; use bridge para harnesses que precisam de rede")
		}
	}
	return v
}

// buildEnvironmentConfig valida a parte do driver e devolve a configuração e o segredo.
func buildEnvironmentConfig(d domain.EnvironmentDriver, ssh *domain.SSHConfig, key string, sb *domain.SandboxConfig, env map[string]string) (domain.EnvironmentConfig, environmentSecrets, error) {
	var cfg domain.EnvironmentConfig
	var sec environmentSecrets
	if err := domain.ValidateEnvironmentEnv(env); err != nil {
		return cfg, sec, err
	}
	switch d {
	case domain.EnvSSH:
		if ssh == nil {
			return cfg, sec, domain.Invalid("ssh", "required for the ssh driver")
		}
		c := *ssh
		if err := c.Normalize(); err != nil {
			return cfg, sec, err
		}
		if sb != nil {
			return cfg, sec, domain.Invalid("sandbox", "only applies to the sandbox driver")
		}
		if err := domain.ValidPrivateKey(strings.TrimSpace(key)); err != nil {
			return cfg, sec, err
		}
		cfg.SSH, sec.PrivateKey = &c, strings.TrimSpace(key)
	case domain.EnvSandbox:
		if sb == nil {
			return cfg, sec, domain.Invalid("sandbox", "required for the sandbox driver")
		}
		c := *sb
		if err := c.Normalize(); err != nil {
			return cfg, sec, err
		}
		if ssh != nil || key != "" {
			return cfg, sec, domain.Invalid("ssh", "only applies to the ssh driver")
		}
		cfg.Sandbox = &c
	}
	sec.Env = env
	cfg.EnvNames = sortedKeys(env)
	return cfg, sec, nil
}

func checkEnvironmentSlug(slug string) error {
	if slices.Contains(reservedEnvironmentSlugs, slug) {
		return domain.Invalid("slug", fmt.Sprintf("%q is reserved", slug))
	}
	return nil
}

// CreateEnvironment cadastra um ambiente SSH ou sandbox.
func (s *Service) CreateEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, in CreateEnvironmentInput) (EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return EnvironmentView{}, err
	}
	name, desc, err := validateEnvironmentText(in.Name, in.Description)
	if err != nil {
		return EnvironmentView{}, err
	}
	slug, err := resolveSlug(in.Slug, name)
	if err != nil {
		return EnvironmentView{}, err
	}
	if err := checkEnvironmentSlug(slug); err != nil {
		return EnvironmentView{}, err
	}
	if err := s.checkDriverAllowed(in.Driver, in.SSH); err != nil {
		return EnvironmentView{}, err
	}
	cfg, sec, err := buildEnvironmentConfig(in.Driver, in.SSH, in.PrivateKey, in.Sandbox, in.Env)
	if err != nil {
		return EnvironmentView{}, err
	}
	id := domain.NewID()
	enc, err := s.sealEnvironment(id, sec)
	if err != nil {
		return EnvironmentView{}, err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return EnvironmentView{}, fmt.Errorf("marshal environment config: %w", err)
	}
	var out EnvironmentView
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.CreateEnvironment(ctx, store.CreateEnvironmentParams{ID: id, CompanyID: companyID, Slug: slug, Name: name,
			Description: desc, Driver: string(in.Driver), Config: raw, SecretEnc: enc, ActorType: string(actor.Type), ActorID: actor.ID, Now: t.now})
		if err != nil {
			return mapErr(err, "create environment")
		}
		out = s.environmentView(environmentFromRow(row))
		return t.activity(actor, companyID, "environment.created", "environment", id,
			map[string]any{"slug": slug, "name": name, "driver": in.Driver}, "")
	})
	return out, err
}

// UpdateEnvironment muda um ambiente. A chave e os valores das variáveis só entram, nunca saem.
func (s *Service) UpdateEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, in UpdateEnvironmentInput) (EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return EnvironmentView{}, err
	}
	var out EnvironmentView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetEnvironmentByIDForUpdate(ctx, store.GetEnvironmentByIDForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get environment")
		}
		e := environmentFromRow(cur)
		sec, err := s.openEnvironment(cur.ID, cur.SecretEnc)
		if err != nil {
			return domain.Invalid("secrets", "os segredos guardados deste ambiente não abrem com a chave atual: "+err.Error())
		}
		name, desc := e.Name, e.Description
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			desc = *in.Description
		}
		if name, desc, err = validateEnvironmentText(name, desc); err != nil {
			return err
		}
		ssh, sb := e.Config.SSH, e.Config.Sandbox
		if in.SSH != nil {
			ssh = in.SSH
		}
		if in.Sandbox != nil {
			sb = in.Sandbox
		}
		key := sec.PrivateKey
		if in.PrivateKey != nil && strings.TrimSpace(*in.PrivateKey) != "" {
			key = *in.PrivateKey
		}
		env := map[string]string{}
		for k, v := range sec.Env {
			env[k] = v
		}
		for k, v := range in.SetEnv {
			env[k] = v
		}
		for _, k := range in.DropEnv {
			delete(env, k)
		}
		if in.SSH != nil || in.Sandbox != nil || in.Reactivate {
			if err := s.checkDriverAllowed(e.Driver, ssh); err != nil {
				return err
			}
		}
		cfg, nsec, err := buildEnvironmentConfig(e.Driver, ssh, key, sb, env)
		if err != nil {
			return err
		}
		enc, err := s.sealEnvironment(cur.ID, nsec)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal environment config: %w", err)
		}
		status := cur.Status
		if in.Reactivate {
			status = string(domain.EnvActive)
		}
		row, err := t.q.UpdateEnvironment(ctx, store.UpdateEnvironmentParams{CompanyID: companyID, ID: id, Name: name, Description: desc,
			Config: raw, SecretEnc: enc, Status: status, Now: t.now})
		if err != nil {
			return mapErr(err, "update environment")
		}
		out = s.environmentView(environmentFromRow(row))
		changed := []string{}
		if in.Name != nil {
			changed = append(changed, "name")
		}
		if in.Description != nil {
			changed = append(changed, "description")
		}
		if in.SSH != nil || in.Sandbox != nil {
			changed = append(changed, "config")
		}
		if in.PrivateKey != nil {
			changed = append(changed, "private_key")
		}
		if len(in.SetEnv) > 0 || len(in.DropEnv) > 0 {
			changed = append(changed, "env")
		}
		verb := "environment.updated"
		if in.Reactivate && cur.Status != string(domain.EnvActive) {
			verb = "environment.reactivated"
		}
		return t.activity(actor, companyID, verb, "environment", id, map[string]any{"slug": row.Slug, "changed": changed}, "")
	})
	return out, err
}

// ArchiveEnvironment arquiva o ambiente e leva as personas dele para moveTo (nil = o padrão da empresa ou o
// local), na mesma transação. Um ambiente padrão deixa de ser o padrão. Runs em andamento seguem até o fim.
func (s *Service) ArchiveEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID, moveTo *domain.ID) (EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return EnvironmentView{}, err
	}
	if moveTo != nil && *moveTo == id {
		return EnvironmentView{}, domain.Invalid("move_personas_to", "choose another environment")
	}
	var out EnvironmentView
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetEnvironmentByIDForUpdate(ctx, store.GetEnvironmentByIDForUpdateParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get environment")
		}
		if moveTo != nil {
			dst, err := t.q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *moveTo})
			if err != nil {
				return domain.Invalid("move_personas_to", "environment not found in company")
			}
			if dst.Status != string(domain.EnvActive) {
				return domain.Invalid("move_personas_to", "the destination environment is archived")
			}
		}
		from := id
		moved, err := t.q.MovePersonasEnvironment(ctx, store.MovePersonasEnvironmentParams{ToID: moveTo, Now: t.now, CompanyID: companyID, FromID: &from})
		if err != nil {
			return fmt.Errorf("move personas: %w", err)
		}
		row, err := t.q.UpdateEnvironment(ctx, store.UpdateEnvironmentParams{CompanyID: companyID, ID: id, Name: cur.Name, Description: cur.Description,
			Config: cur.Config, SecretEnc: cur.SecretEnc, Status: string(domain.EnvArchived), Now: t.now})
		if err != nil {
			return mapErr(err, "archive environment")
		}
		out = s.environmentView(environmentFromRow(row))
		data := map[string]any{"slug": cur.Slug, "personas_moved": moved, "was_default": cur.IsDefault}
		if moveTo != nil {
			data["moved_to"] = moveTo.String()
		}
		return t.activity(actor, companyID, "environment.archived", "environment", id, data, "")
	})
	return out, err
}

// SetDefaultEnvironment escolhe o ambiente padrão da empresa (nil = o local).
func (s *Service) SetDefaultEnvironment(ctx context.Context, actor domain.Actor, companyID domain.ID, id *domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		if err := t.q.LockEnvironmentDefault(ctx, companyID); err != nil {
			return fmt.Errorf("lock environment default: %w", err)
		}
		if err := t.q.ClearDefaultEnvironment(ctx, store.ClearDefaultEnvironmentParams{CompanyID: companyID, Now: t.now}); err != nil {
			return fmt.Errorf("clear default environment: %w", err)
		}
		data := map[string]any{"environment": "local"}
		subject := companyID
		subjectType := "company"
		if id != nil {
			row, err := t.q.SetDefaultEnvironment(ctx, store.SetDefaultEnvironmentParams{CompanyID: companyID, ID: *id, Now: t.now})
			if err != nil {
				if isNoRows(err) {
					return domain.Invalid("environment", "environment not found or archived")
				}
				return mapErr(err, "set default environment")
			}
			data["environment"] = row.Slug
			subject, subjectType = row.ID, "environment"
		}
		return t.activity(actor, companyID, "environment.default_changed", subjectType, subject, data, "")
	})
}

// SetPersonaEnvironment escolhe o ambiente das runs da persona (nil = o padrão da empresa).
func (s *Service) SetPersonaEnvironment(ctx context.Context, actor domain.Actor, companyID, personaID domain.ID, envID *domain.ID) (domain.Persona, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Persona{}, err
	}
	var out domain.Persona
	err := s.withTx(ctx, func(t *txc) error {
		data := map[string]any{"environment": "default"}
		if envID != nil {
			e, err := t.q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *envID})
			if err != nil {
				return domain.Invalid("environment", "environment not found in company")
			}
			if e.Status != string(domain.EnvActive) {
				return domain.Invalid("environment", "the environment is archived")
			}
			data["environment"] = e.Slug
		}
		row, err := t.q.SetPersonaEnvironment(ctx, store.SetPersonaEnvironmentParams{EnvironmentID: envID, Now: t.now, CompanyID: companyID, ID: personaID})
		if err != nil {
			return mapErr(err, "set persona environment")
		}
		out = personaFromRow(row)
		data["persona"] = row.Slug
		return t.activity(actor, companyID, "persona.environment_changed", "persona", personaID, data, "")
	})
	return out, err
}

// ListEnvironments devolve os ambientes cadastrados da empresa (o local é implícito e não entra).
func (s *Service) ListEnvironments(ctx context.Context, companyID domain.ID) ([]EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	rows, err := q.ListEnvironments(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list environments: %w", err)
	}
	personas, err := q.ListEnvironmentPersonas(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list environment personas: %w", err)
	}
	active, err := q.CountActiveEnvironmentLeases(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("count leases: %w", err)
	}
	byEnv := map[uuid.UUID][]PersonaRef{}
	for _, p := range personas {
		if p.EnvironmentID != nil {
			byEnv[*p.EnvironmentID] = append(byEnv[*p.EnvironmentID], PersonaRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
		}
	}
	act := map[uuid.UUID]int{}
	for _, a := range active {
		act[a.EnvironmentID] = int(a.Active)
	}
	out := make([]EnvironmentView, len(rows))
	for i, r := range rows {
		out[i] = s.environmentView(environmentFromRow(r))
		out[i].Personas = byEnv[r.ID]
		out[i].ActiveLeases = act[r.ID]
	}
	return out, nil
}

// GetEnvironment resolve ref como UUID ou slug dentro da empresa.
func (s *Service) GetEnvironment(ctx context.Context, companyID domain.ID, ref string) (EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return EnvironmentView{}, err
	}
	q := s.read()
	var r store.Environment
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		r, err = q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: id})
	} else {
		r, err = q.GetEnvironmentBySlug(ctx, store.GetEnvironmentBySlugParams{CompanyID: companyID, Slug: strings.ToLower(strings.TrimSpace(ref))})
	}
	if err != nil {
		return EnvironmentView{}, mapErr(err, "get environment")
	}
	v := s.environmentView(environmentFromRow(r))
	personas, err := q.ListEnvironmentPersonas(ctx, companyID)
	if err != nil {
		return EnvironmentView{}, fmt.Errorf("list environment personas: %w", err)
	}
	for _, p := range personas {
		if p.EnvironmentID != nil && *p.EnvironmentID == r.ID {
			v.Personas = append(v.Personas, PersonaRef{ID: p.ID, Slug: p.Slug, Name: p.Name})
		}
	}
	return v, nil
}

// ListEnvironmentLeases devolve os usos mais recentes do ambiente.
func (s *Service) ListEnvironmentLeases(ctx context.Context, companyID, envID domain.ID, limit int) ([]EnvironmentLeaseView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListEnvironmentLeases(ctx, store.ListEnvironmentLeasesParams{CompanyID: companyID, EnvironmentID: envID, Lim: i32(clamp(limit, 20, 200))})
	if err != nil {
		return nil, fmt.Errorf("list environment leases: %w", err)
	}
	out := make([]EnvironmentLeaseView, len(rows))
	for i, r := range rows {
		st := domain.RunStatus(r.RunStatus)
		out[i] = EnvironmentLeaseView{EnvironmentLease: domain.EnvironmentLease{ID: r.ID, CompanyID: r.CompanyID, EnvironmentID: r.EnvironmentID,
			RunID: r.RunID, Status: r.Status, RemoteDir: r.RemoteDir, AcquiredAt: r.AcquiredAt, ReleasedAt: r.ReleasedAt, FailureReason: r.FailureReason},
			RunStatus: st, RunName: r.RunName, PersonaName: r.PersonaName, Orphan: r.Status == domain.LeaseActive && st.Terminal()}
	}
	return out, nil
}

// RunEnvironmentInfo é o ambiente em que uma run rodou, para a página da execução.
type RunEnvironmentInfo struct {
	EnvironmentID domain.ID
	Slug, Name    string
	Driver        domain.EnvironmentDriver
	Lease         domain.EnvironmentLease
}

// RunEnvironmentOf devolve o ambiente remoto de uma run; ErrNotFound = a run rodou no local.
func (s *Service) RunEnvironmentOf(ctx context.Context, companyID, runID domain.ID) (RunEnvironmentInfo, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return RunEnvironmentInfo{}, err
	}
	r, err := s.read().GetRunEnvironmentLease(ctx, store.GetRunEnvironmentLeaseParams{CompanyID: companyID, RunID: runID})
	if err != nil {
		return RunEnvironmentInfo{}, mapErr(err, "run environment")
	}
	return RunEnvironmentInfo{EnvironmentID: r.EnvironmentID, Slug: r.EnvironmentSlug, Name: r.EnvironmentName, Driver: domain.EnvironmentDriver(r.EnvironmentDriver),
		Lease: domain.EnvironmentLease{ID: r.ID, CompanyID: r.CompanyID, EnvironmentID: r.EnvironmentID, RunID: r.RunID, Status: r.Status,
			RemoteDir: r.RemoteDir, AcquiredAt: r.AcquiredAt, ReleasedAt: r.ReleasedAt, FailureReason: r.FailureReason}}, nil
}

// target monta o alvo de execução de um ambiente com os segredos abertos.
func (s *Service) target(r store.Environment) (execenv.Target, error) {
	e := environmentFromRow(r)
	sec, err := s.openEnvironment(r.ID, r.SecretEnc)
	if err != nil {
		return execenv.Target{}, fmt.Errorf("os segredos do ambiente %q não abrem: %w", r.Name, err)
	}
	t := execenv.Target{Driver: e.Driver, Name: e.Name, PrivateKey: sec.PrivateKey, Env: sec.Env}
	if e.Config.SSH != nil {
		t.SSH = *e.Config.SSH
	}
	if e.Config.Sandbox != nil {
		t.Sandbox = *e.Config.Sandbox
	}
	return t, nil
}

// probeTimeout é o teto de um teste de conexão pedido pela API ou pela UI.
const probeTimeout = execenv.ProbeSandboxTimeout + 10*time.Second

// ProbeEnvironment testa a conexão de um ambiente cadastrado e grava o relatório.
func (s *Service) ProbeEnvironment(ctx context.Context, actor domain.Actor, companyID, id domain.ID) (EnvironmentView, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return EnvironmentView{}, err
	}
	r, err := s.read().GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return EnvironmentView{}, mapErr(err, "get environment")
	}
	tg, err := s.target(r)
	if err != nil {
		return EnvironmentView{}, domain.Invalid("secrets", err.Error())
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	rep := execenv.Probe(pctx, s.Environments, tg)
	cancel()
	raw, err := json.Marshal(rep)
	if err != nil {
		return EnvironmentView{}, fmt.Errorf("marshal probe: %w", err)
	}
	var out EnvironmentView
	err = s.withTx(ctx, func(t *txc) error {
		row, err := t.q.SetEnvironmentProbe(ctx, store.SetEnvironmentProbeParams{Now: t.now, Ok: pgtype.Bool{Bool: rep.OK, Valid: true}, Report: raw, CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "save probe")
		}
		out = s.environmentView(environmentFromRow(row))
		return t.activity(actor, companyID, "environment.probed", "environment", id, map[string]any{"slug": row.Slug, "ok": rep.OK}, "")
	})
	return out, err
}

// ProbeEnvironmentDraft testa um rascunho de ambiente sem gravar nada.
func (s *Service) ProbeEnvironmentDraft(ctx context.Context, companyID domain.ID, in CreateEnvironmentInput) (domain.ProbeReport, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.ProbeReport{}, err
	}
	if err := s.checkDriverAllowed(in.Driver, in.SSH); err != nil {
		return domain.ProbeReport{}, err
	}
	cfg, sec, err := buildEnvironmentConfig(in.Driver, in.SSH, in.PrivateKey, in.Sandbox, in.Env)
	if err != nil {
		return domain.ProbeReport{}, err
	}
	tg := execenv.Target{Driver: in.Driver, Name: strings.TrimSpace(in.Name), PrivateKey: sec.PrivateKey, Env: sec.Env}
	if cfg.SSH != nil {
		tg.SSH = *cfg.SSH
	}
	if cfg.Sandbox != nil {
		tg.Sandbox = *cfg.Sandbox
	}
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return execenv.Probe(pctx, s.Environments, tg), nil
}

// RunEnvironment resolve o ambiente de uma run da persona: o dela, senão o padrão da empresa, senão o local.
// É chamado pelo executor (sem identidade de usuário): não aplica papéis.
func (s *Service) RunEnvironment(ctx context.Context, companyID, personaID domain.ID) (RunEnvironment, error) {
	q := s.read()
	out := RunEnvironment{Source: "local", Config: s.Environments}
	p, err := q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: personaID})
	if err != nil {
		return out, mapErr(err, "get persona")
	}
	var row *store.Environment
	if p.EnvironmentID != nil {
		r, err := q.GetEnvironmentByID(ctx, store.GetEnvironmentByIDParams{CompanyID: companyID, ID: *p.EnvironmentID})
		switch {
		case err == nil && r.Status == string(domain.EnvActive):
			row, out.Source = &r, "persona"
		case err == nil:
			out.Warnings = append(out.Warnings, fmt.Sprintf("o ambiente %q da persona está arquivado; usando o padrão da empresa", r.Name))
		case !isNoRows(err):
			return out, fmt.Errorf("get environment: %w", err)
		}
	}
	if row == nil {
		r, err := q.GetDefaultEnvironment(ctx, companyID)
		switch {
		case err == nil:
			row, out.Source = &r, "default"
		case !isNoRows(err):
			return out, fmt.Errorf("get default environment: %w", err)
		}
	}
	if row == nil {
		return out, nil
	}
	e := environmentFromRow(*row)
	out.Environment = &e
	if out.Target, err = s.target(*row); err != nil {
		return out, err
	}
	return out, nil
}

// AcquireEnvironmentLease registra que a run começou a usar o ambiente (executor).
func (s *Service) AcquireEnvironmentLease(ctx context.Context, companyID, envID, runID domain.ID, remoteDir string) error {
	return s.withTx(ctx, func(t *txc) error {
		if _, err := t.q.CreateEnvironmentLease(ctx, store.CreateEnvironmentLeaseParams{ID: domain.NewID(), CompanyID: companyID, EnvironmentID: envID,
			RunID: runID, RemoteDir: remoteDir, Now: t.now}); err != nil {
			return mapErr(err, "create environment lease")
		}
		return t.activity(domain.SystemActor, companyID, "environment.leased", "environment", envID, map[string]any{"run_id": runID.String()}, "")
	})
}

// ReleaseEnvironmentLease fecha o uso da run: released quando tudo voltou, failed com o motivo (o diretório
// remoto fica para diagnóstico).
func (s *Service) ReleaseEnvironmentLease(ctx context.Context, companyID, runID domain.ID, failure string) error {
	status := domain.LeaseReleased
	if failure != "" {
		status = domain.LeaseFailed
	}
	if len(failure) > 2000 {
		failure = failure[:2000]
	}
	return s.withTx(ctx, func(t *txc) error {
		row, err := t.q.ReleaseEnvironmentLease(ctx, store.ReleaseEnvironmentLeaseParams{Status: status, Now: t.now, FailureReason: failure, CompanyID: companyID, RunID: runID})
		if err != nil {
			return mapErr(err, "release environment lease")
		}
		return t.activity(domain.SystemActor, companyID, "environment.released", "environment", row.EnvironmentID,
			map[string]any{"run_id": runID.String(), "status": status}, "")
	})
}

// rekeyEnvironmentSecrets é a parte dos ambientes da rotação de HANKO_SECRET_KEY.
func (s *Service) rekeyEnvironmentSecrets(ctx context.Context) (int, error) {
	rows, err := s.read().ListAllSecretEnvironments(ctx)
	if err != nil {
		return 0, fmt.Errorf("list secret environments: %w", err)
	}
	n := 0
	for _, r := range rows {
		if !s.Integrations.Secrets.NeedsReseal(r.SecretEnc) {
			continue
		}
		plain, err := s.Integrations.Secrets.Open(r.SecretEnc, r.ID.String())
		if err != nil {
			return n, fmt.Errorf("ambiente %s: %w", r.ID, err)
		}
		enc, err := s.Integrations.Secrets.Seal(plain, r.ID.String())
		if err != nil {
			return n, fmt.Errorf("ambiente %s: %w", r.ID, err)
		}
		if err := store.New(s.Pool).SetEnvironmentSecretEnc(ctx, store.SetEnvironmentSecretEncParams{ID: r.ID, SecretEnc: enc}); err != nil {
			return n, fmt.Errorf("ambiente %s: %w", r.ID, err)
		}
		n++
	}
	return n, nil
}

// EnvironmentDrivers diz, para cada driver remoto, por que ele não pode rodar neste servidor ("" = pode).
// É a decisão do operador (HANKO_ENV_SSH_ALLOW_HOSTS, HANKO_ENV_SANDBOX) e não depende da empresa.
func (s *Service) EnvironmentDrivers(context.Context) map[domain.EnvironmentDriver]string {
	out := map[domain.EnvironmentDriver]string{}
	for _, d := range []domain.EnvironmentDriver{domain.EnvSSH, domain.EnvSandbox} {
		out[d] = ""
		if err := s.Environments.DriverStatus(d); err != nil {
			out[d] = err.Error()
		}
	}
	return out
}

// EnvironmentSecretValues lista os valores secretos de um ambiente para a redação do log da run.
func (r RunEnvironment) EnvironmentSecretValues() []string {
	var out []string
	if r.Target.PrivateKey != "" {
		out = append(out, r.Target.PrivateKey)
	}
	for _, v := range r.Target.Env {
		out = append(out, v)
	}
	return out
}
