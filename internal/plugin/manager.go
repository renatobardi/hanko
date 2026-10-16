package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Spec descreve como subir o processo de um plugin.
type Spec struct {
	// Key é o id do plugin (slug global).
	Key     string
	Version string
	// Dir é o diretório do plugin (já resolvido, dentro de HANKO_PLUGINS_DIR); Command é relativo a ele.
	Dir     string
	Command string
	Args    []string
	// WorkDir é o diretório de trabalho próprio do plugin (<dados>/plugins/<id>).
	WorkDir string
	// Env é o ambiente completo do processo (allowlist mínima montada pelo chamador).
	Env []string
}

func (s Spec) fingerprint() string {
	h := sha256.New()
	enc := json.NewEncoder(h)
	_ = enc.Encode(s)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Status é o retrato do processo de um plugin.
type Status struct {
	Running     bool       `json:"running"`
	Ready       bool       `json:"ready"`
	PID         int        `json:"pid,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	Restarts    int        `json:"restarts"`
	LastError   string     `json:"last_error,omitempty"`
	NextRestart *time.Time `json:"next_restart,omitempty"`
	Version     string     `json:"version,omitempty"`
}

// Options configura o gerenciador. Os zeros usam os padrões.
type Options struct {
	HostVersion string
	Logger      *slog.Logger
	// Handler atende as chamadas do plugin para o Hanko. Obrigatório.
	Handler func(ctx context.Context, key, method string, params json.RawMessage) (any, *RPCError)
	// OnLog recebe o stderr do processo e os avisos do host sobre o plugin.
	OnLog func(key, level, msg string)
	// OnState é chamado quando o processo fica pronto (initialize + health ok) ou falha ao subir/cai.
	OnState func(key string, ready bool, errText string)

	CallTimeout    time.Duration // padrão 30 s
	InitTimeout    time.Duration // padrão 30 s
	HealthInterval time.Duration // padrão 60 s
	HealthTimeout  time.Duration // padrão 10 s
	BackoffMin     time.Duration // padrão 1 s
	BackoffMax     time.Duration // teto do backoff de reinício, padrão 5 min
	ShutdownWait   time.Duration // espera depois do shutdown antes do SIGTERM, padrão 10 s
	KillGrace      time.Duration // entre SIGTERM e SIGKILL, padrão 5 s
	MaxMessage     int           // padrão 1 MiB
	MaxInflight    int           // requisições simultâneas do plugin, padrão 8
}

func (o *Options) defaults() {
	set := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	set(&o.CallTimeout, 30*time.Second)
	set(&o.InitTimeout, 30*time.Second)
	set(&o.HealthInterval, time.Minute)
	set(&o.HealthTimeout, 10*time.Second)
	set(&o.BackoffMin, time.Second)
	set(&o.BackoffMax, 5*time.Minute)
	set(&o.ShutdownWait, 10*time.Second)
	set(&o.KillGrace, 5*time.Second)
	if o.MaxMessage <= 0 {
		o.MaxMessage = DefaultMaxMessage
	}
	if o.MaxInflight <= 0 {
		o.MaxInflight = 8
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.OnLog == nil {
		o.OnLog = func(string, string, string) {}
	}
	if o.OnState == nil {
		o.OnState = func(string, bool, string) {}
	}
}

// ErrNotRunning: o plugin não tem processo de pé (desligado, sem empresa ligada, ou reiniciando).
var ErrNotRunning = errors.New("plugin: processo não está rodando")

// Manager mantém um processo por plugin, com reinício por backoff exponencial, saúde periódica, timeout por
// chamada e desligamento ordenado (shutdown, espera, SIGTERM, espera, SIGKILL).
type Manager struct {
	o     Options
	mu    sync.Mutex
	procs map[string]*proc
}

// NewManager cria o gerenciador.
func NewManager(o Options) *Manager {
	o.defaults()
	return &Manager{o: o, procs: map[string]*proc{}}
}

// Start garante o processo do plugin com esta spec. Se já roda com outra spec (versão nova, outro comando),
// para o antigo e sobe o novo. Não espera ficar pronto (use Ready).
func (m *Manager) Start(ctx context.Context, s Spec) {
	m.mu.Lock()
	p := m.procs[s.Key]
	if p != nil && p.spec.fingerprint() == s.fingerprint() {
		m.mu.Unlock()
		return
	}
	delete(m.procs, s.Key)
	m.mu.Unlock()
	if p != nil {
		p.stop(ctx)
	}
	np := newProc(m, s)
	m.mu.Lock()
	if old := m.procs[s.Key]; old != nil { // corrida com outro Start: fica o que chegou primeiro
		m.mu.Unlock()
		np.cancelAll()
		return
	}
	m.procs[s.Key] = np
	m.mu.Unlock()
	go np.supervise()
}

// Restart para e sobe de novo, mesmo com a mesma spec (aprovar capacidades, religar).
func (m *Manager) Restart(ctx context.Context, s Spec) {
	_ = m.Stop(ctx, s.Key)
	m.Start(ctx, s)
}

// Stop para o processo do plugin (se houver) e espera ele sair.
func (m *Manager) Stop(ctx context.Context, key string) error {
	m.mu.Lock()
	p := m.procs[key]
	delete(m.procs, key)
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	p.stop(ctx)
	return nil
}

// StopAll para todos (desligamento do servidor).
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	ps := make([]*proc, 0, len(m.procs))
	for k, p := range m.procs {
		ps = append(ps, p)
		delete(m.procs, k)
	}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func() { defer wg.Done(); p.stop(ctx) }()
	}
	wg.Wait()
}

// Running lista os plugins com processo gerenciado (de pé ou reiniciando).
func (m *Manager) Running() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.procs))
	for k := range m.procs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) get(key string) *proc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.procs[key]
}

// Ready espera a primeira tentativa de subida do processo atual terminar e devolve o erro dela (nil = pronto).
func (m *Manager) Ready(ctx context.Context, key string) error {
	p := m.get(key)
	if p == nil {
		return ErrNotRunning
	}
	select {
	case <-p.firstReady:
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.firstErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status devolve o estado do processo; zero quando não é gerenciado.
func (m *Manager) Status(key string) Status {
	p := m.get(key)
	if p == nil {
		return Status{}
	}
	return p.status()
}

// Call chama method no plugin com o timeout dado (0 = CallTimeout). Erro do plugin volta como *RPCError.
func (m *Manager) Call(ctx context.Context, key, method string, params any, timeout time.Duration) (json.RawMessage, error) {
	p := m.get(key)
	if p == nil {
		return nil, ErrNotRunning
	}
	c := p.current()
	if c == nil {
		return nil, ErrNotRunning
	}
	if timeout <= 0 {
		timeout = m.o.CallTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := c.Call(cctx, method, params)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, fmt.Errorf("plugin %s: %s: tempo esgotado (%s)", key, method, timeout)
	}
	return res, err
}

// proc é o processo gerenciado de um plugin, com o laço de reinício.
type proc struct {
	m    *Manager
	spec Spec

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu        sync.Mutex
	conn      *conn
	pid       int
	started   time.Time
	ready     bool
	restarts  int
	lastErr   string
	nextStart *time.Time
	kill      context.CancelFunc
	exited    chan struct{}

	firstReady chan struct{}
	firstOnce  sync.Once
	firstErr   error
}

func newProc(m *Manager, s Spec) *proc {
	return &proc{m: m, spec: s, stopCh: make(chan struct{}), doneCh: make(chan struct{}), firstReady: make(chan struct{})}
}

func (p *proc) current() *conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ready {
		return nil
	}
	return p.conn
}

func (p *proc) status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{Running: p.conn != nil, Ready: p.ready, PID: p.pid, Restarts: p.restarts, LastError: p.lastErr, NextRestart: p.nextStart, Version: p.spec.Version}
	if p.conn != nil {
		t := p.started
		st.StartedAt = &t
	}
	return st
}

func (p *proc) settleFirst(err error) {
	p.firstOnce.Do(func() {
		p.mu.Lock()
		p.firstErr = err
		p.mu.Unlock()
		close(p.firstReady)
	})
}

func (p *proc) cancelAll() {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.settleFirst(ErrNotRunning)
	close(p.doneCh)
}

// supervise sobe o processo e o reinicia quando cai, com backoff exponencial (BackoffMin dobrando até
// BackoffMax). Uma execução que durou mais que um minuto zera o backoff.
func (p *proc) supervise() {
	defer close(p.doneCh)
	o := p.m.o
	backoff := o.BackoffMin
	for {
		began := time.Now()
		err := p.runOnce()
		select {
		case <-p.stopCh:
			p.settleFirst(ErrNotRunning)
			return
		default:
		}
		msg := "o processo terminou"
		if err != nil {
			msg = err.Error()
		}
		p.settleFirst(errors.New(msg))
		o.OnState(p.spec.Key, false, msg)
		o.OnLog(p.spec.Key, "host", "processo caiu: "+msg+"; reinício em "+backoff.String())
		o.Logger.Warn("plugin: processo caiu", "plugin", p.spec.Key, "err", msg, "backoff", backoff)
		if time.Since(began) > time.Minute {
			backoff = o.BackoffMin
		}
		next := time.Now().Add(backoff)
		p.mu.Lock()
		p.lastErr, p.nextStart = msg, &next
		p.mu.Unlock()
		t := time.NewTimer(backoff)
		select {
		case <-p.stopCh:
			t.Stop()
			return
		case <-t.C:
		}
		p.mu.Lock()
		p.restarts++
		p.nextStart = nil
		p.mu.Unlock()
		backoff *= 2
		if backoff > o.BackoffMax {
			backoff = o.BackoffMax
		}
	}
}

// ResolveCommand devolve o caminho absoluto do executável, que precisa ficar dentro do diretório do plugin
// mesmo depois de resolver links simbólicos.
func ResolveCommand(dir, command string) (string, error) {
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("diretório do plugin: %w", err)
	}
	full, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(command)))
	if err != nil {
		return "", fmt.Errorf("comando do plugin: %w", err)
	}
	if !strings.HasPrefix(full, base+string(filepath.Separator)) {
		return "", errors.New("o comando do plugin aponta para fora do diretório do plugin")
	}
	st, err := os.Stat(full)
	if err != nil {
		return "", fmt.Errorf("comando do plugin: %w", err)
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o111 == 0 {
		return "", errors.New("o comando do plugin não é um arquivo executável")
	}
	return full, nil
}

// runOnce sobe o processo, faz initialize + health e bloqueia até ele sair.
func (p *proc) runOnce() error {
	o := p.m.o
	s := p.spec
	select {
	case <-p.stopCh:
		return nil
	default:
	}
	bin, err := ResolveCommand(s.Dir, s.Command)
	if err != nil {
		return err
	}
	if s.WorkDir != "" {
		if err := os.MkdirAll(filepath.Join(s.WorkDir, "tmp"), 0o700); err != nil {
			return fmt.Errorf("diretório de trabalho: %w", err)
		}
	}
	cmd := exec.Command(bin, s.Args...) //nolint:gosec // o operador instalou o plugin; argv sem shell, comando confinado ao diretório do plugin
	cmd.Dir = s.WorkDir
	cmd.Env = s.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin: %w", err)
	}
	// stdout e stderr por os.Pipe: o Wait não fica preso a um neto que herdou a pipe (o grupo é varrido com
	// SIGKILL depois que o líder sai, e então as leituras terminam).
	outR, outW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("stdout: %w", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return fmt.Errorf("stderr: %w", err)
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	startErr := cmd.Start()
	_ = outW.Close()
	_ = errW.Close()
	if startErr != nil {
		_ = outR.Close()
		_ = errR.Close()
		return fmt.Errorf("iniciar: %w", startErr)
	}
	stdout, stderr := outR, errR
	defer stdout.Close()
	defer stderr.Close()
	pid := cmd.Process.Pid
	key := s.Key
	c := newConn(stdin, o.MaxMessage, o.MaxInflight,
		func(ctx context.Context, method string, params json.RawMessage) (any, *RPCError) {
			return o.Handler(ctx, key, method, params)
		},
		func(reason string) { o.OnLog(key, "host", reason) })

	exited := make(chan struct{})
	killCtx, kill := context.WithCancel(context.Background())
	p.mu.Lock()
	p.conn, p.pid, p.started, p.ready, p.kill, p.exited = c, pid, time.Now(), false, kill, exited
	p.mu.Unlock()
	select {
	case <-p.stopCh: // stop chegou enquanto o processo subia
		kill()
	default:
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); c.readLoop(stdout) }()
	go func() { defer readers.Done(); p.readStderr(stderr) }()

	// SIGTERM ao grupo quando kill é chamado; SIGKILL depois de KillGrace.
	go func() {
		select {
		case <-exited:
			return
		case <-killCtx.Done():
		}
		_ = syscall.Kill(-pid, syscall.SIGTERM)
		t := time.NewTimer(o.KillGrace)
		defer t.Stop()
		select {
		case <-exited:
		case <-t.C:
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}()

	initDone := make(chan struct{})
	go func() {
		defer close(initDone)
		if err := p.handshake(killCtx, c); err != nil {
			p.mu.Lock()
			p.lastErr = err.Error()
			p.mu.Unlock()
			p.settleFirst(err)
			o.OnState(key, false, err.Error())
			kill()
			return
		}
		p.mu.Lock()
		p.ready, p.lastErr = true, ""
		p.mu.Unlock()
		p.settleFirst(nil)
		o.OnState(key, true, "")
		p.healthLoop(killCtx, c, kill)
	}()

	waitErr := cmd.Wait()
	close(exited)
	_ = syscall.Kill(-pid, syscall.SIGKILL) // varre filhos que sobraram no grupo
	readers.Wait()
	c.close(errors.New("o processo do plugin terminou"))
	kill()
	<-initDone
	c.wait()
	p.mu.Lock()
	p.conn, p.ready, p.pid = nil, false, 0
	p.mu.Unlock()
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			return fmt.Errorf("o processo saiu: %s", ee.ProcessState.String())
		}
		return fmt.Errorf("o processo saiu: %w", waitErr)
	}
	return nil
}

// handshake faz initialize e health.
func (p *proc) handshake(ctx context.Context, c *conn) error {
	o := p.m.o
	ictx, cancel := context.WithTimeout(ctx, o.InitTimeout)
	defer cancel()
	raw, err := c.Call(ictx, "initialize", map[string]any{"api_version": 1, "host_version": o.HostVersion, "plugin_id": p.spec.Key})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var ir struct {
		APIVersion int `json:"api_version"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &ir) == nil && ir.APIVersion != 0 && ir.APIVersion != 1 {
		return fmt.Errorf("initialize: o plugin fala a versão %d do protocolo; este Hanko fala a 1", ir.APIVersion)
	}
	return p.health(ctx, c)
}

func (p *proc) health(ctx context.Context, c *conn) error {
	hctx, cancel := context.WithTimeout(ctx, p.m.o.HealthTimeout)
	defer cancel()
	raw, err := c.Call(hctx, "health", nil)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	var hr struct {
		OK      *bool  `json:"ok"`
		Message string `json:"message"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &hr) == nil && hr.OK != nil && !*hr.OK {
		msg := hr.Message
		if msg == "" {
			msg = "o plugin se declarou sem saúde"
		}
		return fmt.Errorf("health: %s", msg)
	}
	return nil
}

func (p *proc) healthLoop(ctx context.Context, c *conn, kill context.CancelFunc) {
	t := time.NewTicker(p.m.o.HealthInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := p.health(ctx, c); err != nil {
			if ctx.Err() != nil {
				return
			}
			p.m.o.OnLog(p.spec.Key, "host", "verificação de saúde falhou: "+err.Error()+"; reiniciando")
			p.mu.Lock()
			p.lastErr = err.Error()
			p.mu.Unlock()
			kill()
			return
		}
	}
}

// readStderr manda o stderr do processo, linha a linha (cortadas em 4 KiB), para o log do plugin.
func (p *proc) readStderr(r io.Reader) {
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	flush := func() {
		if len(buf) > 0 {
			p.m.o.OnLog(p.spec.Key, "stderr", string(buf))
			buf = buf[:0]
		}
	}
	for {
		n, err := r.Read(tmp)
		for _, b := range tmp[:n] {
			if b == '\n' {
				flush()
				continue
			}
			if len(buf) < 4096 {
				buf = append(buf, b)
			}
		}
		if err != nil {
			flush()
			return
		}
	}
}

// stop encerra o processo: notificação shutdown, até ShutdownWait para sair sozinho, depois SIGTERM e, passado
// KillGrace, SIGKILL. Também interrompe o laço de reinício.
func (p *proc) stop(ctx context.Context) {
	o := p.m.o
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.mu.Lock()
	c, kill, exited := p.conn, p.kill, p.exited
	p.mu.Unlock()
	if c != nil && exited != nil {
		sctx, cancel := context.WithTimeout(ctx, o.ShutdownWait)
		_, _ = c.Call(sctx, "shutdown", nil)
		select {
		case <-exited:
		case <-sctx.Done():
		}
		cancel()
	}
	if kill != nil {
		kill()
	}
	select {
	case <-p.doneCh:
	case <-ctx.Done():
	}
}
