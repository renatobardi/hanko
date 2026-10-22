package ops

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/syscmd"
)

func spec() ServiceSpec {
	return ServiceSpec{Name: "hanko", Exe: "/usr/local/bin/hanko", Args: []string{"serve"}, WorkDir: "/var/lib/hanko", EnvFile: "/etc/hanko/hanko.env", User: "hanko"}
}

func TestRenderSystemd(t *testing.T) {
	u := RenderSystemd(spec())
	for _, want := range []string{
		"ExecStart=/usr/local/bin/hanko serve\n", "WorkingDirectory=/var/lib/hanko", "EnvironmentFile=-/etc/hanko/hanko.env",
		"User=hanko", "Restart=on-failure", "NoNewPrivileges=true", "WantedBy=multi-user.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit sem %q:\n%s", want, u)
		}
	}
	s := spec()
	s.UserLevel, s.User = true, ""
	s.Exe = "/home/ana/meus apps/hanko"
	u = RenderSystemd(s)
	if !strings.Contains(u, `ExecStart="/home/ana/meus apps/hanko" serve`) || !strings.Contains(u, "WantedBy=default.target") || strings.Contains(u, "User=") {
		t.Errorf("unit de usuário:\n%s", u)
	}
	// o conteúdo do arquivo de ambiente jamais entra na unit
	if strings.Contains(u, "HANKO_DATABASE_URL") {
		t.Error("variável na unit")
	}
}

func TestSystemdQuoteEscapes(t *testing.T) {
	if got := systemdQuote(`a "b" $c %d`); got != `"a \"b\" $$c %%d"` {
		t.Fatalf("got %s", got)
	}
}

func TestRenderLaunchd(t *testing.T) {
	s := spec()
	s.UserLevel, s.User, s.LogDir = true, "", "/Users/ana/Library/Logs/hanko"
	p := RenderLaunchd(s)
	for _, want := range []string{"<string>com.renatobardi.hanko</string>", "<string>/bin/sh</string>", "exec &apos;/usr/local/bin/hanko&apos; &apos;serve&apos;", ". &apos;/etc/hanko/hanko.env&apos;",
		"<key>RunAtLoad</key>", "<key>SuccessfulExit</key>", "/Users/ana/Library/Logs/hanko/hanko.log", "<key>WorkingDirectory</key>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist sem %q:\n%s", want, p)
		}
	}
	s.EnvFile = ""
	p = RenderLaunchd(s)
	if !strings.Contains(p, "<string>/usr/local/bin/hanko</string>") || strings.Contains(p, "/bin/sh") {
		t.Errorf("plist sem env file:\n%s", p)
	}
	s.Exe = "/x/a&b<c>"
	if !strings.Contains(RenderLaunchd(s), "a&amp;b&lt;c&gt;") {
		t.Error("xml não escapado")
	}
}

func TestRenderComposeUnitAndTimer(t *testing.T) {
	u, err := RenderComposeUnit("/opt/app")
	if err != nil || !strings.Contains(u, "WorkingDirectory=/opt/app") || !strings.Contains(u, "RemainAfterExit=yes") || !strings.Contains(u, "docker compose up -d") {
		t.Fatalf("%v\n%s", err, u)
	}
	if _, err := RenderComposeUnit("relativo"); err == nil {
		t.Fatal("esperava erro")
	}
	sv, tm, err := RenderTimer(TimerSpec{Name: "hanko-backup", Description: "Backup local do Hanko", Exec: "/opt/app/deploy/oute-server/backup.sh",
		OnCalendar: "*-*-* 01:30:00", RandomDelay: "10min", Timeout: "30min", After: "docker.service"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Type=oneshot", "ExecStart=/opt/app/deploy/oute-server/backup.sh", "TimeoutStartSec=30min", "Requires=docker.service"} {
		if !strings.Contains(sv, want) {
			t.Errorf("service sem %q:\n%s", want, sv)
		}
	}
	for _, want := range []string{"OnCalendar=*-*-* 01:30:00", "RandomizedDelaySec=10min", "Persistent=true", "WantedBy=timers.target"} {
		if !strings.Contains(tm, want) {
			t.Errorf("timer sem %q:\n%s", want, tm)
		}
	}
	for _, bad := range []TimerSpec{
		{Name: "X", Exec: "/a", OnCalendar: "daily"},
		{Name: "ok", Exec: "relativo", OnCalendar: "daily"},
		{Name: "ok", Exec: "/a", OnCalendar: "daily\nExecStart=/bin/evil"},
	} {
		if _, _, err := RenderTimer(bad); err == nil {
			t.Errorf("aceitou %+v", bad)
		}
	}
}

func TestValidateRejectsInjection(t *testing.T) {
	cases := []func(*ServiceSpec){
		func(s *ServiceSpec) { s.Name = "Hanko" },
		func(s *ServiceSpec) { s.Exe = "relativo/hanko" },
		func(s *ServiceSpec) { s.Exe = "/bin/hanko\nExecStartPost=/bin/evil" },
		func(s *ServiceSpec) { s.Args = []string{"serve\n[Service]"} },
		func(s *ServiceSpec) { s.User = "root; rm" },
		func(s *ServiceSpec) { s.UserLevel = true }, // User + UserLevel
		func(s *ServiceSpec) { s.Exe = "" },
	}
	for i, mod := range cases {
		s := spec()
		mod(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("caso %d aceito: %+v", i, s)
		}
	}
	if err := spec().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformFor(t *testing.T) {
	if p, _ := PlatformFor("linux"); p != PlatformSystemd {
		t.Fatal(p)
	}
	if p, _ := PlatformFor("darwin"); p != PlatformLaunchd {
		t.Fatal(p)
	}
	if _, err := PlatformFor("windows"); err == nil {
		t.Fatal("esperava erro")
	}
}

type memFS struct {
	files map[string]string
	modes map[string]os.FileMode
	dirs  []string
}

func newMemFS() *memFS { return &memFS{files: map[string]string{}, modes: map[string]os.FileMode{}} }

func (m *memFS) inst(platform string, cmd syscmd.Runner) *Installer {
	return &Installer{Platform: platform, Cmd: cmd, Home: "/home/ana", UID: 501,
		WriteFile: func(p string, d []byte, mode os.FileMode) error { m.files[p] = string(d); m.modes[p] = mode; return nil },
		MkdirAll:  func(p string, _ os.FileMode) error { m.dirs = append(m.dirs, p); return nil },
		Exists:    func(p string) bool { _, ok := m.files[p]; return ok },
		Remove: func(p string) error {
			if _, ok := m.files[p]; !ok {
				return os.ErrNotExist
			}
			delete(m.files, p)
			return nil
		}}
}

func cmdStrings(f *syscmd.Fake) []string {
	var out []string
	for _, c := range f.Calls() {
		out = append(out, c.Cmd.String())
	}
	return out
}

func TestInstallSystemd(t *testing.T) {
	fs, cmd := newMemFS(), &syscmd.Fake{}
	in := fs.inst(PlatformSystemd, cmd)
	files, err := in.Install(context.Background(), spec(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !strings.Contains(fs.files["/etc/systemd/system/hanko.service"], "ExecStart=") {
		t.Fatalf("arquivos = %v", fs.files)
	}
	if fs.modes["/etc/hanko/hanko.env"] != 0o600 || !strings.Contains(fs.files["/etc/hanko/hanko.env"], "HANKO_DATABASE_URL=") {
		t.Fatalf("env: modo %v", fs.modes["/etc/hanko/hanko.env"])
	}
	if got := strings.Join(cmdStrings(cmd), "|"); got != "systemctl daemon-reload|systemctl enable --now hanko.service" {
		t.Fatalf("comandos = %s", got)
	}
	// reinstalar não pisa no env do operador
	fs.files["/etc/hanko/hanko.env"] = "HANKO_DATABASE_URL=postgres://real"
	if _, err := in.Install(context.Background(), spec(), true); err != nil {
		t.Fatal(err)
	}
	if fs.files["/etc/hanko/hanko.env"] != "HANKO_DATABASE_URL=postgres://real" {
		t.Fatal("env sobrescrito")
	}
}

func TestInstallUserLevelPaths(t *testing.T) {
	fs, cmd := newMemFS(), &syscmd.Fake{}
	s := spec()
	s.UserLevel, s.User = true, ""
	s.EnvFile = (&Installer{Home: "/home/ana"}).DefaultEnvFile(s)
	if _, err := fs.inst(PlatformSystemd, cmd).Install(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files["/home/ana/.config/systemd/user/hanko.service"]; !ok {
		t.Fatalf("arquivos = %v", fs.files)
	}
	if got := strings.Join(cmdStrings(cmd), "|"); got != "systemctl --user daemon-reload|systemctl --user enable --now hanko.service" {
		t.Fatalf("comandos = %s", got)
	}
}

func TestInstallLaunchdBootsOutThenIn(t *testing.T) {
	fs, cmd := newMemFS(), &syscmd.Fake{}
	s := spec()
	s.UserLevel, s.User, s.EnvFile = true, "", "/Users/ana/.config/hanko/hanko.env"
	if _, err := fs.inst(PlatformLaunchd, cmd).Install(context.Background(), s, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files["/home/ana/Library/LaunchAgents/com.renatobardi.hanko.plist"]; !ok {
		t.Fatalf("arquivos = %v", fs.files)
	}
	want := "launchctl bootout gui/501/com.renatobardi.hanko|launchctl bootstrap gui/501 /home/ana/Library/LaunchAgents/com.renatobardi.hanko.plist"
	if got := strings.Join(cmdStrings(cmd), "|"); got != want {
		t.Fatalf("comandos = %s", got)
	}
}

func TestInstallWithoutEnableOnlyWrites(t *testing.T) {
	fs, cmd := newMemFS(), &syscmd.Fake{}
	if _, err := fs.inst(PlatformSystemd, cmd).Install(context.Background(), spec(), false); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Calls()) != 0 {
		t.Fatalf("não deveria rodar nada: %v", cmdStrings(cmd))
	}
}

func TestInstallSurfacesActivationFailure(t *testing.T) {
	fs := newMemFS()
	cmd := &syscmd.Fake{Handler: func(c syscmd.Cmd) error {
		if len(c.Args) > 0 && c.Args[0] == "enable" {
			return errors.New("Failed to connect to bus")
		}
		return nil
	}}
	if _, err := fs.inst(PlatformSystemd, cmd).Install(context.Background(), spec(), true); err == nil || !strings.Contains(err.Error(), "bus") {
		t.Fatalf("err = %v", err)
	}
}

func TestUninstallKeepsEnvFile(t *testing.T) {
	fs, cmd := newMemFS(), &syscmd.Fake{}
	in := fs.inst(PlatformSystemd, cmd)
	if _, err := in.Install(context.Background(), spec(), true); err != nil {
		t.Fatal(err)
	}
	if err := in.Uninstall(context.Background(), spec()); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files["/etc/systemd/system/hanko.service"]; ok {
		t.Fatal("unit não removida")
	}
	if _, ok := fs.files["/etc/hanko/hanko.env"]; !ok {
		t.Fatal("o env do operador não pode ser apagado")
	}
	// idempotente
	if err := in.Uninstall(context.Background(), spec()); err != nil {
		t.Fatal(err)
	}
}

func TestStatusAndRestart(t *testing.T) {
	fs := newMemFS()
	cmd := &syscmd.Fake{Handler: func(c syscmd.Cmd) error {
		if c.Args[0] == "is-active" {
			_, _ = c.Stdout.Write([]byte("inactive\n"))
			return errors.New("exit status 3")
		}
		return nil
	}}
	in := fs.inst(PlatformSystemd, cmd)
	if st, err := in.Status(context.Background(), spec()); err != nil || st != "inactive" {
		t.Fatalf("status = %q, %v", st, err)
	}
	if err := in.Restart(context.Background(), spec()); err != nil {
		t.Fatal(err)
	}
	if got := cmdStrings(cmd); got[len(got)-1] != "systemctl restart hanko.service" {
		t.Fatalf("%v", got)
	}
	ld := fs.inst(PlatformLaunchd, &syscmd.Fake{Handler: func(syscmd.Cmd) error { return errors.New("não carregado") }})
	if st, _ := ld.Status(context.Background(), ServiceSpec{Name: "hanko", UserLevel: true}); st != "inactive" {
		t.Fatalf("launchd status = %q", st)
	}
}
