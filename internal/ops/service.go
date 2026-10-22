package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/renatobardi/hanko/internal/syscmd"
)

// Plataformas de serviço suportadas.
const (
	PlatformSystemd = "systemd" // linux
	PlatformLaunchd = "launchd" // macOS
)

// PlatformFor escolhe o gerenciador de serviços pelo GOOS.
func PlatformFor(goos string) (string, error) {
	switch goos {
	case "linux":
		return PlatformSystemd, nil
	case "darwin":
		return PlatformLaunchd, nil
	}
	return "", fmt.Errorf("sistema %q sem suporte a `hanko service` (suportados: linux/systemd e macOS/launchd)", goos)
}

// ServiceSpec descreve o serviço do servidor Hanko.
type ServiceSpec struct {
	Name    string // nome da unit (padrão hanko)
	Exe     string // caminho absoluto do binário
	Args    []string
	WorkDir string
	// EnvFile é o arquivo KEY=VALUE com as variáveis HANKO_* (modo 0600); nunca vai dentro da unit.
	EnvFile string
	// User é o usuário que roda o serviço (somente instalação do sistema, systemd).
	User string
	// UserLevel instala para o usuário atual (systemd --user, LaunchAgent) em vez de para o sistema.
	UserLevel bool
	LogDir    string // launchd: onde vão stdout/stderr
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,40}$`)

// Validate confere o spec. Caminhos precisam ser absolutos e sem quebra de linha (injeção em unit/plist).
func (s ServiceSpec) Validate() error {
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("nome de serviço %q inválido (minúsculas, dígitos, . _ -)", s.Name)
	}
	for label, p := range map[string]string{"binário": s.Exe, "diretório de trabalho": s.WorkDir, "arquivo de ambiente": s.EnvFile, "diretório de logs": s.LogDir} {
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("%s %q precisa ser caminho absoluto", label, p)
		}
		if strings.ContainsAny(p, "\n\r\x00") {
			return fmt.Errorf("%s contém caracteres inválidos", label)
		}
	}
	if s.Exe == "" {
		return errors.New("caminho do binário obrigatório")
	}
	for _, a := range s.Args {
		if strings.ContainsAny(a, "\n\r\x00") {
			return errors.New("argumento com caracteres inválidos")
		}
	}
	if s.User != "" && (s.UserLevel || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(s.User)) {
		return fmt.Errorf("usuário %q inválido (e só vale na instalação do sistema)", s.User)
	}
	return nil
}

// systemdQuote escapa um argumento para ExecStart (aspas duplas, \ e " escapados, % dobrado).
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	if s == "" || strings.ContainsAny(s, " \t'\"\\$%") {
		return `"` + r.Replace(s) + `"`
	}
	return s
}

// RenderSystemd gera a unit do servidor.
func RenderSystemd(s ServiceSpec) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=Hanko: control plane para times de agentes de IA\nAfter=network-online.target postgresql.service\nWants=network-online.target\n\n[Service]\nType=simple\n")
	exec := []string{systemdQuote(s.Exe)}
	for _, a := range s.Args {
		exec = append(exec, systemdQuote(a))
	}
	b.WriteString("ExecStart=" + strings.Join(exec, " ") + "\n")
	if s.WorkDir != "" {
		b.WriteString("WorkingDirectory=" + s.WorkDir + "\n")
	}
	if s.EnvFile != "" {
		b.WriteString("EnvironmentFile=-" + s.EnvFile + "\n")
	}
	if s.User != "" && !s.UserLevel {
		b.WriteString("User=" + s.User + "\n")
	}
	b.WriteString("Restart=on-failure\nRestartSec=5\nTimeoutStopSec=60\nKillSignal=SIGTERM\nNoNewPrivileges=true\nLimitNOFILE=65536\n\n[Install]\nWantedBy=")
	if s.UserLevel {
		b.WriteString("default.target\n")
	} else {
		b.WriteString("multi-user.target\n")
	}
	return b.String()
}

// RenderComposeUnit gera a unit que sobe e para uma stack docker compose junto com a máquina.
func RenderComposeUnit(dir string) (string, error) {
	if !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\n\r\x00") {
		return "", fmt.Errorf("diretório da stack %q precisa ser caminho absoluto", dir)
	}
	return "[Unit]\nDescription=Hanko: stack docker compose\nRequires=docker.service\nAfter=docker.service network-online.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\nWorkingDirectory=" + dir +
		"\nExecStart=/usr/bin/docker compose up -d\nExecStop=/usr/bin/docker compose stop\nTimeoutStartSec=300\n\n[Install]\nWantedBy=multi-user.target\n", nil
}

// TimerSpec descreve uma tarefa periódica (systemd timer).
type TimerSpec struct {
	Name        string
	Description string
	Exec        string // linha de comando completa (caminho absoluto)
	OnCalendar  string // ex.: *-*-* 01:30:00
	RandomDelay string // ex.: 10min
	Timeout     string // ex.: 30min
	After       string // ex.: docker.service
}

var calendarRe = regexp.MustCompile(`^[A-Za-z0-9*:,/.\- ~]+$`)

// RenderTimer gera o par service/timer de uma tarefa periódica.
func RenderTimer(t TimerSpec) (service, timer string, err error) {
	if !nameRe.MatchString(t.Name) {
		return "", "", fmt.Errorf("nome %q inválido", t.Name)
	}
	if t.Exec == "" || !filepath.IsAbs(strings.Fields(t.Exec)[0]) || strings.ContainsAny(t.Exec, "\n\r\x00") {
		return "", "", errors.New("o comando do timer precisa começar por um caminho absoluto")
	}
	if !calendarRe.MatchString(t.OnCalendar) {
		return "", "", fmt.Errorf("agenda %q inválida", t.OnCalendar)
	}
	desc := strings.ReplaceAll(t.Description, "\n", " ")
	if desc == "" {
		desc = t.Name
	}
	var sv strings.Builder
	sv.WriteString("[Unit]\nDescription=" + desc + "\n")
	if t.After != "" {
		sv.WriteString("After=" + t.After + "\nRequires=" + t.After + "\n")
	}
	sv.WriteString("\n[Service]\nType=oneshot\nExecStart=" + t.Exec + "\n")
	if t.Timeout != "" {
		sv.WriteString("TimeoutStartSec=" + t.Timeout + "\n")
	}
	sv.WriteString("Nice=10\n")
	var tm strings.Builder
	tm.WriteString("[Unit]\nDescription=" + desc + " (agenda)\n\n[Timer]\nOnCalendar=" + t.OnCalendar + "\n")
	if t.RandomDelay != "" {
		tm.WriteString("RandomizedDelaySec=" + t.RandomDelay + "\n")
	}
	tm.WriteString("Persistent=true\n\n[Install]\nWantedBy=timers.target\n")
	return sv.String(), tm.String(), nil
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// LaunchdLabel é o rótulo do job do launchd para o nome do serviço.
func LaunchdLabel(name string) string { return "com.renatobardi." + name }

// shellQuote protege um argumento para sh -c.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// RenderLaunchd gera o plist. O launchd não lê arquivo de ambiente: um sh carrega o EnvFile e faz exec do binário.
func RenderLaunchd(s ServiceSpec) string {
	var prog []string
	if s.EnvFile != "" {
		parts := []string{shellQuote(s.Exe)}
		for _, a := range s.Args {
			parts = append(parts, shellQuote(a))
		}
		prog = []string{"/bin/sh", "-c", "set -a; [ -f " + shellQuote(s.EnvFile) + " ] && . " + shellQuote(s.EnvFile) + "; set +a; exec " + strings.Join(parts, " ")}
	} else {
		prog = append([]string{s.Exe}, s.Args...)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(LaunchdLabel(s.Name)) + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range prog {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString("\t</array>\n")
	if s.WorkDir != "" {
		b.WriteString("\t<key>WorkingDirectory</key>\n\t<string>" + xmlEscape(s.WorkDir) + "</string>\n")
	}
	if s.LogDir != "" {
		b.WriteString("\t<key>StandardOutPath</key>\n\t<string>" + xmlEscape(filepath.Join(s.LogDir, s.Name+".log")) + "</string>\n")
		b.WriteString("\t<key>StandardErrorPath</key>\n\t<string>" + xmlEscape(filepath.Join(s.LogDir, s.Name+".err.log")) + "</string>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>\n\t<key>ThrottleInterval</key>\n\t<integer>10</integer>\n</dict>\n</plist>\n")
	return b.String()
}

// File é um arquivo que a instalação grava.
type File struct {
	Path    string
	Content string
	Mode    os.FileMode
	// KeepExisting: não sobrescreve se já existir (o arquivo de ambiente do operador).
	KeepExisting bool
}

// Installer instala, remove e consulta o serviço do servidor.
type Installer struct {
	Platform string
	Cmd      syscmd.Runner
	Home     string // $HOME, para a instalação de usuário
	UID      int    // uid efetivo (launchd: domínio gui/<uid>)
	// FS é o sistema de arquivos; nulo usa o disco de verdade.
	WriteFile func(path string, data []byte, perm os.FileMode) error
	MkdirAll  func(path string, perm os.FileMode) error
	Exists    func(path string) bool
	Remove    func(path string) error
}

func (i *Installer) remove(path string) error {
	if i.Remove != nil {
		return i.Remove(path)
	}
	return os.Remove(path)
}

func (i *Installer) write(path string, data []byte, perm os.FileMode) error {
	if i.WriteFile != nil {
		return i.WriteFile(path, data, perm)
	}
	return os.WriteFile(path, data, perm)
}

func (i *Installer) mkdir(path string, perm os.FileMode) error {
	if i.MkdirAll != nil {
		return i.MkdirAll(path, perm)
	}
	return os.MkdirAll(path, perm)
}

func (i *Installer) exists(path string) bool {
	if i.Exists != nil {
		return i.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// UnitPath é onde a unit/plist do serviço mora.
func (i *Installer) UnitPath(s ServiceSpec) string {
	switch i.Platform {
	case PlatformLaunchd:
		if s.UserLevel {
			return filepath.Join(i.Home, "Library", "LaunchAgents", LaunchdLabel(s.Name)+".plist")
		}
		return filepath.Join("/Library/LaunchDaemons", LaunchdLabel(s.Name)+".plist")
	}
	if s.UserLevel {
		return filepath.Join(i.Home, ".config", "systemd", "user", s.Name+".service")
	}
	return filepath.Join("/etc/systemd/system", s.Name+".service")
}

// DefaultEnvFile é o arquivo de ambiente padrão do serviço.
func (i *Installer) DefaultEnvFile(s ServiceSpec) string {
	if s.UserLevel {
		return filepath.Join(i.Home, ".config", "hanko", "hanko.env")
	}
	return "/etc/hanko/hanko.env"
}

const envTemplate = `# Ambiente do serviço Hanko. Formato KEY=VALUE, sem aspas e sem "export".
# Este arquivo guarda segredos: mantenha o modo 0600.
HANKO_DATABASE_URL=
HANKO_ADDR=127.0.0.1:7420
HANKO_MODE=authenticated
# HANKO_SECRET=        # openssl rand -hex 32
# HANKO_SECRET_KEY=    # openssl rand -hex 32
`

// Plan devolve os arquivos e os comandos da instalação, sem executar nada.
func (i *Installer) Plan(s ServiceSpec, enable bool) (files []File, cmds []syscmd.Cmd, err error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	switch i.Platform {
	case PlatformSystemd:
		files = append(files, File{Path: i.UnitPath(s), Content: RenderSystemd(s), Mode: 0o644})
	case PlatformLaunchd:
		files = append(files, File{Path: i.UnitPath(s), Content: RenderLaunchd(s), Mode: 0o644})
	default:
		return nil, nil, fmt.Errorf("plataforma %q desconhecida", i.Platform)
	}
	if s.EnvFile != "" {
		files = append(files, File{Path: s.EnvFile, Content: envTemplate, Mode: 0o600, KeepExisting: true})
	}
	if enable {
		cmds = i.enableCmds(s)
	}
	return files, cmds, nil
}

func (i *Installer) sysctl(user bool, args ...string) syscmd.Cmd {
	if user {
		args = append([]string{"--user"}, args...)
	}
	return syscmd.Cmd{Name: "systemctl", Args: args}
}

func (i *Installer) domain(s ServiceSpec) string {
	if s.UserLevel {
		return fmt.Sprintf("gui/%d", i.UID)
	}
	return "system"
}

func (i *Installer) enableCmds(s ServiceSpec) []syscmd.Cmd {
	if i.Platform == PlatformLaunchd {
		return []syscmd.Cmd{{Name: "launchctl", Args: []string{"bootstrap", i.domain(s), i.UnitPath(s)}}}
	}
	return []syscmd.Cmd{i.sysctl(s.UserLevel, "daemon-reload"), i.sysctl(s.UserLevel, "enable", "--now", s.Name+".service")}
}

// Install grava os arquivos e, com enable, ativa o serviço. Idempotente: reinstalar atualiza a unit e reativa.
func (i *Installer) Install(ctx context.Context, s ServiceSpec, enable bool) ([]File, error) {
	files, cmds, err := i.Plan(s, enable)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.KeepExisting && i.exists(f.Path) {
			continue
		}
		if err := i.mkdir(filepath.Dir(f.Path), 0o755); err != nil {
			return files, fmt.Errorf("criar %s: %w", filepath.Dir(f.Path), err)
		}
		if err := i.write(f.Path, []byte(f.Content), f.Mode); err != nil {
			return files, fmt.Errorf("gravar %s: %w", f.Path, err)
		}
	}
	if s.LogDir != "" {
		if err := i.mkdir(s.LogDir, 0o755); err != nil {
			return files, err
		}
	}
	if i.Platform == PlatformLaunchd && enable {
		// bootstrap falha se já carregado: descarrega antes (ignora erro de "não carregado")
		_ = i.Cmd.Run(ctx, syscmd.Cmd{Name: "launchctl", Args: []string{"bootout", i.domain(s) + "/" + LaunchdLabel(s.Name)}})
	}
	for _, c := range cmds {
		if err := i.Cmd.Run(ctx, c); err != nil {
			return files, fmt.Errorf("ativar o serviço: %w", err)
		}
	}
	return files, nil
}

// Uninstall desativa e remove a unit. O arquivo de ambiente (segredos do operador) nunca é apagado.
func (i *Installer) Uninstall(ctx context.Context, s ServiceSpec) error {
	if err := s.Validate(); err != nil {
		return err
	}
	var cmds []syscmd.Cmd
	if i.Platform == PlatformLaunchd {
		cmds = []syscmd.Cmd{{Name: "launchctl", Args: []string{"bootout", i.domain(s) + "/" + LaunchdLabel(s.Name)}}}
	} else {
		cmds = []syscmd.Cmd{i.sysctl(s.UserLevel, "disable", "--now", s.Name+".service")}
	}
	for _, c := range cmds {
		_ = i.Cmd.Run(ctx, c) // serviço já parado/ausente não impede a remoção
	}
	if err := i.remove(i.UnitPath(s)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remover %s: %w", i.UnitPath(s), err)
	}
	if i.Platform == PlatformSystemd {
		_ = i.Cmd.Run(ctx, i.sysctl(s.UserLevel, "daemon-reload"))
	}
	return nil
}

// Restart reinicia o serviço.
func (i *Installer) Restart(ctx context.Context, s ServiceSpec) error {
	if i.Platform == PlatformLaunchd {
		return i.Cmd.Run(ctx, syscmd.Cmd{Name: "launchctl", Args: []string{"kickstart", "-k", i.domain(s) + "/" + LaunchdLabel(s.Name)}})
	}
	return i.Cmd.Run(ctx, i.sysctl(s.UserLevel, "restart", s.Name+".service"))
}

// Status devolve o estado do serviço ("active", "inactive"...) pelo gerenciador.
func (i *Installer) Status(ctx context.Context, s ServiceSpec) (string, error) {
	var out strings.Builder
	var c syscmd.Cmd
	if i.Platform == PlatformLaunchd {
		c = syscmd.Cmd{Name: "launchctl", Args: []string{"print", i.domain(s) + "/" + LaunchdLabel(s.Name)}, Stdout: &out}
	} else {
		c = i.sysctl(s.UserLevel, "is-active", s.Name+".service")
		c.Stdout = &out
	}
	err := i.Cmd.Run(ctx, c)
	st := strings.TrimSpace(out.String())
	if i.Platform == PlatformLaunchd {
		if err != nil {
			return "inactive", nil //nolint:nilerr // launchctl print sai com erro quando o job não está carregado
		}
		return "loaded", nil
	}
	if st == "" && err != nil {
		return "", err
	}
	return st, nil // is-active sai com código != 0 para "inactive": o texto é a resposta
}
