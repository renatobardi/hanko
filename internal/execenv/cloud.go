package execenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/domain"
)

// Sandbox em nuvem (ADR-0055): um sandbox descartável por run, criado por um provider via API e alcançado pelo
// gateway SSH dele. Depois de criado, é o mesmo caminho endurecido do driver ssh (tar por stdin/stdout, known_hosts
// obrigatório, comando remoto com aspas POSIX), só que sem chave: o acesso é um token de curta duração.

// CloudSpec é o pedido de um sandbox.
type CloudSpec struct {
	// Name identifica o sandbox para o operador (rótulo hanko-run-<run>).
	Name      string
	Labels    map[string]string
	Snapshot  string
	CPUs      int
	MemoryGiB int
	DiskGiB   int
	// BlockNetwork bloqueia a rede do sandbox.
	BlockNetwork bool
	// IdleStop e Reap são a rede de segurança contra vazamento (servidor caiu no meio): o provider para o sandbox
	// ocioso depois de IdleStop e o apaga Reap depois de parado.
	IdleStop, Reap time.Duration
}

// CloudSandbox é um sandbox criado.
type CloudSandbox struct{ ID string }

// CloudSSH é o acesso SSH ao sandbox.
type CloudSSH struct {
	Host string
	Port int
	// User é o token de acesso (o gateway autentica por ele); nunca vai para log nem para o argv de outro processo.
	User string
}

// CloudProvider é um provider de sandbox em nuvem já com a credencial da empresa. Cada chamada é uma requisição
// HTTP curta; o contexto limita a espera.
type CloudProvider interface {
	// Ping valida a credencial e a alcançabilidade da API sem criar nada.
	Ping(ctx context.Context) error
	// Create cria o sandbox e só devolve quando ele está pronto para receber SSH.
	Create(ctx context.Context, spec CloudSpec) (CloudSandbox, error)
	// SSH abre um acesso SSH temporário ao sandbox.
	SSH(ctx context.Context, id string, ttl time.Duration) (CloudSSH, error)
	// Delete apaga o sandbox; apagar um que não existe mais não é erro.
	Delete(ctx context.Context, id string) error
}

// CloudDriver é a escolha do operador (HANKO_ENV_CLOUD): o provider, a política do gateway e a fábrica do cliente
// com a chave de API da empresa.
type CloudDriver interface {
	// Name é o nome do provider ("daytona").
	Name() string
	// New devolve o cliente do provider com a chave de API do ambiente.
	New(apiKey string) CloudProvider
	// KnownHosts são as linhas de known_hosts do gateway SSH (obrigatórias: o ssh nunca aceita chave desconhecida).
	KnownHosts() string
}

// Tempos do ciclo de vida do sandbox cloud.
const (
	cloudIdleStop  = 15 * time.Minute
	cloudReap      = time.Hour
	cloudSSHTTL    = 2 * time.Hour
	cloudOpTimeout = 3 * time.Minute
)

var cloudTokenRe = regexp.MustCompile(`^[A-Za-z0-9_.=:+/-]{1,512}$`)

// cloudSession é a execução de uma run num sandbox cloud: o sandbox nasce no primeiro Prepare e morre no Finish.
type cloudSession struct {
	cfg   Config
	t     Target
	run   RunInfo
	drv   CloudDriver
	prov  CloudProvider
	inner *sshSession

	mu     sync.Mutex
	boxID  string
	closed bool
}

func cloudSpecOf(c domain.CloudConfig, name string, labels map[string]string) CloudSpec {
	return CloudSpec{Name: name, Labels: labels, Snapshot: c.Snapshot, CPUs: c.CPUs, MemoryGiB: c.MemoryGiB, DiskGiB: c.DiskGiB,
		BlockNetwork: c.Network == "none", IdleStop: cloudIdleStop, Reap: cloudReap}
}

// cloudParts valida o alvo e devolve o driver do operador, o cliente com a chave da empresa e a configuração.
func cloudParts(cfg Config, t Target) (CloudDriver, CloudProvider, domain.CloudConfig, error) {
	var c domain.CloudConfig
	if err := cfg.CloudStatus(); err != nil {
		return nil, nil, c, err
	}
	c = t.Cloud
	if err := c.Normalize(); err != nil {
		return nil, nil, c, fmt.Errorf("configuração cloud do ambiente %q: %w", t.Name, err)
	}
	drv := cfg.Cloud
	if drv.Name() != c.Provider {
		return nil, nil, c, fmt.Errorf("o provider %q do ambiente %q não é o que o operador liberou (%q)", c.Provider, t.Name, drv.Name())
	}
	if t.APIKey == "" {
		return nil, nil, c, fmt.Errorf("ambiente %q sem chave de API do provider", t.Name)
	}
	return drv, drv.New(t.APIKey), c, nil
}

func openCloud(cfg Config, t Target, run RunInfo) (*cloudSession, error) {
	drv, prov, c, err := cloudParts(cfg, t)
	if err != nil {
		return nil, err
	}
	t.Cloud = c
	// A sessão SSH interna ainda não tem host nem token: entram em provision.
	inner, err := openSSHOpts(cfg, Target{Driver: domain.EnvSSH, Name: t.Name, Env: t.Env,
		SSH: domain.SSHConfig{Host: "pending", Port: domain.DefaultSSHPort, User: "pending", Root: c.Root, KnownHosts: drv.KnownHosts()}},
		run, sshOpts{gateway: true})
	if err != nil {
		return nil, err
	}
	return &cloudSession{cfg: cfg, t: t, run: run, drv: drv, prov: prov, inner: inner}, nil
}

func (s *cloudSession) WorkDir() string                { return s.inner.WorkDir() }
func (s *cloudSession) AuxDir() (local, remote string) { return s.inner.AuxDir() }
func (s *cloudSession) LocalWorkDir() string           { return s.run.LocalDir }

// RemoteDir identifica o sandbox e a pasta da run no registro de uso (sem o token).
func (s *cloudSession) RemoteDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.boxID == "" {
		return s.t.Cloud.Provider + ":(não criado):" + s.inner.RemoteDir()
	}
	return s.t.Cloud.Provider + ":" + s.boxID + ":" + s.inner.RemoteDir()
}

func (s *cloudSession) Describe() string {
	return fmt.Sprintf("%s (sandbox %s, rede %s)", s.t.Name, s.t.Cloud.Provider, s.t.Cloud.Network)
}

// provision cria o sandbox e aponta a sessão SSH interna para o gateway. Idempotente.
func (s *cloudSession) provision(ctx context.Context) error {
	if s.boxID != "" {
		return nil
	}
	return provisionCloud(ctx, s.prov, s.t.Cloud, "hanko-run-"+s.run.RunID, map[string]string{"hanko.run": s.run.RunID, "hanko.company": s.run.Company}, s.inner, &s.boxID)
}

// provisionCloud cria o sandbox, abre o SSH e configura inner. boxID recebe o id assim que o sandbox existe (para que
// uma falha depois disso ainda consiga apagá-lo).
func provisionCloud(ctx context.Context, prov CloudProvider, c domain.CloudConfig, name string, labels map[string]string, inner *sshSession, boxID *string) error {
	ctx, span := otel.Tracer("hanko/execenv").Start(ctx, "hanko.environment.cloud.provision", trace.WithAttributes(attribute.String("hanko.cloud.provider", c.Provider)))
	defer span.End()
	cctx, cancel := context.WithTimeout(ctx, cloudOpTimeout)
	defer cancel()
	box, err := prov.Create(cctx, cloudSpecOf(c, name, labels))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "create")
		return fmt.Errorf("criar o sandbox em nuvem: %w", err)
	}
	*boxID = box.ID
	acc, err := prov.SSH(cctx, box.ID, cloudSSHTTL)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "ssh access")
		return fmt.Errorf("abrir o acesso SSH ao sandbox %s: %w", box.ID, err)
	}
	if !ValidHostOrIP(acc.Host) || !cloudTokenRe.MatchString(acc.User) {
		return fmt.Errorf("o provider devolveu um acesso SSH inválido para o sandbox %s", box.ID)
	}
	if acc.Port == 0 {
		acc.Port = domain.DefaultSSHPort
	}
	if acc.Port < 1 || acc.Port > 65535 {
		return fmt.Errorf("o provider devolveu uma porta SSH inválida para o sandbox %s", box.ID)
	}
	inner.t.SSH.Host, inner.t.SSH.Port, inner.t.SSH.User = acc.Host, acc.Port, acc.User
	return nil
}

// ValidHostOrIP é domain.ValidSSHHost, exportada para os drivers e os testes.
func ValidHostOrIP(h string) bool { return domain.ValidSSHHost(h) }

func (s *cloudSession) Prepare(ctx context.Context, bin string, args []string, env map[string]string) (procrun.Invocation, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return procrun.Invocation{}, errors.New("sessão do ambiente encerrada")
	}
	err := s.provision(ctx)
	s.mu.Unlock()
	if err != nil {
		return procrun.Invocation{}, err
	}
	return s.inner.Prepare(ctx, bin, args, env)
}

func (s *cloudSession) Abort(ctx context.Context) {
	s.mu.Lock()
	id := s.boxID
	s.mu.Unlock()
	if id != "" {
		s.inner.Abort(ctx)
	}
}

// Finish traz o trabalho de volta e apaga o sandbox. Run ok e sincronização ok: o sandbox é apagado. Qualquer falha
// mantém o sandbox (a rede de segurança do provider o para e apaga sozinho) para diagnóstico e recuperação do
// trabalho, e o id fica no motivo do uso.
func (s *cloudSession) Finish(ctx context.Context, ok bool) error {
	s.mu.Lock()
	id := s.boxID
	s.mu.Unlock()
	if id == "" {
		return nil // o sandbox nem foi criado: o harness nunca começou
	}
	syncErr := s.inner.Finish(ctx, ok)
	if !ok || syncErr != nil {
		if syncErr != nil {
			return fmt.Errorf("%w (sandbox %s mantido por %s para recuperar o trabalho)", syncErr, id, cloudReap)
		}
		return nil
	}
	if err := s.prov.Delete(ctx, id); err != nil {
		return fmt.Errorf("apagar o sandbox %s: %w", id, err)
	}
	s.mu.Lock()
	s.boxID = ""
	s.mu.Unlock()
	return nil
}

func (s *cloudSession) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.inner.Close()
}

// withCloudBox cria um sandbox, roda fn com a sessão SSH apontada para ele e apaga o sandbox no fim, sempre.
func withCloudBox(ctx context.Context, cfg Config, t Target, fn func(inner *sshSession) error) (err error) {
	drv, prov, c, err := cloudParts(cfg, t)
	if err != nil {
		return err
	}
	t.Cloud = c
	inner, err := openSSHOpts(cfg, Target{Driver: domain.EnvSSH, Name: t.Name, Env: t.Env,
		SSH: domain.SSHConfig{Host: "pending", Port: domain.DefaultSSHPort, User: "pending", Root: c.Root, KnownHosts: drv.KnownHosts()}},
		RunInfo{RunID: "probe", Company: "probe"}, sshOpts{gateway: true})
	if err != nil {
		return err
	}
	defer inner.Close()
	var id string
	defer func() {
		if id == "" {
			return
		}
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cloudOpTimeout)
		defer cancel()
		if derr := prov.Delete(dctx, id); derr != nil && err == nil {
			err = fmt.Errorf("apagar o sandbox %s: %w", id, derr)
		}
	}()
	if err := provisionCloud(ctx, prov, c, "hanko-probe", map[string]string{"hanko.purpose": "probe"}, inner, &id); err != nil {
		return err
	}
	return fn(inner)
}

// probeCloud testa o provider: credencial, criação, acesso SSH, os binários do sandbox e a limpeza.
func probeCloud(ctx context.Context, cfg Config, t Target) domain.ProbeReport {
	var rep domain.ProbeReport
	drv, prov, c, err := cloudParts(cfg, t)
	if err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "config", OK: false, Detail: err.Error()})
		return rep
	}
	pctx, cancel := context.WithTimeout(ctx, ProbeCloudTimeout)
	defer cancel()
	if err := prov.Ping(pctx); err != nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "api", OK: false, Detail: err.Error()})
		return rep
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "api", OK: true, Detail: drv.Name()})
	t.Cloud = c
	var out bytes.Buffer
	err = withCloudBox(pctx, cfg, t, func(inner *sshSession) error {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "create", OK: true, Detail: "sandbox criado"})
		return inner.exec(pctx, probeScriptURL(c.Root, urlFor(cfg, c.Network != "none")), nil, &limitedBuffer{b: &out, max: 16 << 10})
	})
	switch {
	case err != nil && out.Len() == 0:
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "connect", OK: false, Detail: err.Error()})
		return rep
	case err != nil:
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "cleanup", OK: false, Detail: err.Error()})
	}
	rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "connect", OK: true, Detail: "gateway SSH"})
	parseProbe(out.Bytes(), &rep, c.Root)
	if err == nil {
		rep.Steps = append(rep.Steps, domain.ProbeStep{Name: "cleanup", OK: true, Detail: "sandbox apagado"})
	} else {
		rep.OK = false
	}
	if c.Network == "none" {
		rep.Warnings = append(rep.Warnings, "rede none: o harness não alcança o MCP do Hanko nem a API do modelo")
	}
	return rep
}

// scriptOut é um io.Writer limitado usado pelos executores de script.
func scriptOut(b *bytes.Buffer) io.Writer { return &limitedBuffer{b: b, max: 64 << 10} }
