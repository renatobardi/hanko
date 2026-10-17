package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEnvironmentEnums(t *testing.T) {
	for d, remote := range map[EnvironmentDriver]bool{EnvLocal: false, EnvSSH: true, EnvSandbox: true} {
		if !d.Valid() || d.Remote() != remote {
			t.Errorf("driver %s", d)
		}
	}
	if EnvironmentDriver("x").Valid() || EnvironmentDriver("x").Remote() {
		t.Error("driver inválido")
	}
	if !EnvActive.Valid() || !EnvArchived.Valid() || EnvironmentStatus("x").Valid() {
		t.Error("status de ambiente")
	}
}

func TestValidSSHHost(t *testing.T) {
	for h, want := range map[string]bool{
		"build.exemplo.io": true, "10.0.0.5": true, "::1": true, "a": true,
		"": false, "-oProxyCommand=x": false, "a..b": false, "a b": false, "host;rm": false,
	} {
		if ValidSSHHost(h) != want {
			t.Errorf("ValidSSHHost(%q) = %v", h, !want)
		}
	}
}

const khLine = "|1|abc= ssh-ed25519 AAAAC3Nza"

func TestSSHConfigNormalize(t *testing.T) {
	ok := func() SSHConfig {
		return SSHConfig{Host: " build.exemplo.io ", User: "deploy", Root: "/srv/hanko", KnownHosts: "\r\n" + khLine + "\r\n"}
	}
	c := ok()
	if err := c.Normalize(); err != nil {
		t.Fatalf("config válida: %v", err)
	}
	if c.Port != DefaultSSHPort || c.Host != "build.exemplo.io" || c.KnownHosts != khLine {
		t.Errorf("normalização: %+v", c)
	}
	tests := []struct {
		name string
		mod  func(*SSHConfig)
		want string
	}{
		{"sem host", func(c *SSHConfig) { c.Host = "" }, "host"},
		{"host ruim", func(c *SSHConfig) { c.Host = "-x" }, "host"},
		{"porta alta", func(c *SSHConfig) { c.Port = 70000 }, "port"},
		{"porta negativa", func(c *SSHConfig) { c.Port = -1 }, "port"},
		{"usuário ruim", func(c *SSHConfig) { c.User = "a b" }, "username"},
		{"raiz relativa", func(c *SSHConfig) { c.Root = "srv" }, "remote_workspace_root"},
		{"raiz /", func(c *SSHConfig) { c.Root = "/" }, "remote_workspace_root"},
		{"raiz suja", func(c *SSHConfig) { c.Root = "/srv/../x" }, "remote_workspace_root"},
		{"raiz com aspas", func(c *SSHConfig) { c.Root = "/srv/a'b" }, "remote_workspace_root"},
		{"raiz enorme", func(c *SSHConfig) { c.Root = "/" + strings.Repeat("a", MaxRemoteRootBytes) }, "remote_workspace_root"},
		{"sem known_hosts", func(c *SSHConfig) { c.KnownHosts = "" }, "known_hosts"},
	}
	for _, tt := range tests {
		c := ok()
		tt.mod(&c)
		err := c.Normalize()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestValidKnownHosts(t *testing.T) {
	good := []string{khLine, "# comentário\n" + khLine, "@cert-authority *.x ssh-rsa AAAA", "h1,h2 ssh-rsa AAAA extra"}
	for _, kh := range good {
		if err := ValidKnownHosts(kh); err != nil {
			t.Errorf("ValidKnownHosts(%q) = %v", kh, err)
		}
	}
	long := strings.Repeat("a", 100) + " b"
	bad := []string{"", "# só comentário\n\n", "host ssh-rsa", "x\x00y z w", strings.Repeat("a ", MaxKnownHostsBytes), long}
	for i, kh := range bad {
		err := ValidKnownHosts(kh)
		if i == len(bad)-1 { // linha curta demais: malformada e recortada na mensagem
			if err == nil || !strings.Contains(err.Error(), "…") {
				t.Errorf("linha longa malformada: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidKnownHosts(%.20q) deveria falhar", kh)
		}
	}
}

func TestValidPrivateKey(t *testing.T) {
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"
	if err := ValidPrivateKey(key); err != nil {
		t.Errorf("chave válida: %v", err)
	}
	for _, k := range []string{"", "abc", strings.Repeat("a", MaxPrivateKeyBytes+1), "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\n-----END RSA PRIVATE KEY-----", key + "\x00"} {
		if err := ValidPrivateKey(k); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidPrivateKey(%.25q) deveria falhar", k)
		}
	}
}

func TestSandboxConfigNormalize(t *testing.T) {
	c := SandboxConfig{Image: " ghcr.io/acme/runner:1.2 ", Network: " BRIDGE "}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.Network != SandboxNetworkBridge || c.MemoryMiB != DefaultSandboxMemoryMiB || c.CPUs != DefaultSandboxCPUs || c.PIDsLimit != DefaultSandboxPIDs {
		t.Errorf("padrões: %+v", c)
	}
	if c.Pinned() {
		t.Error("imagem por tag não está fixada")
	}
	digest := "@sha256:" + strings.Repeat("a", 64)
	p := SandboxConfig{Image: "runner" + digest}
	if err := p.Normalize(); err != nil || !p.Pinned() || p.Network != SandboxNetworkNone {
		t.Errorf("imagem por digest: %+v, %v", p, err)
	}
	tests := []struct {
		name string
		c    SandboxConfig
		want string
	}{
		{"imagem vazia", SandboxConfig{}, "image"},
		{"imagem maiúscula", SandboxConfig{Image: "Runner"}, "image"},
		{"imagem com ..", SandboxConfig{Image: "a/../b"}, "image"},
		{"rede ruim", SandboxConfig{Image: "r", Network: "host"}, "network"},
		{"memória baixa", SandboxConfig{Image: "r", MemoryMiB: 10}, "memory_mib"},
		{"memória alta", SandboxConfig{Image: "r", MemoryMiB: MaxSandboxMemoryMiB + 1}, "memory_mib"},
		{"cpu baixa", SandboxConfig{Image: "r", CPUs: 0.01}, "cpus"},
		{"cpu alta", SandboxConfig{Image: "r", CPUs: MaxSandboxCPUs + 1}, "cpus"},
		{"pids baixo", SandboxConfig{Image: "r", PIDsLimit: 4}, "pids_limit"},
		{"pids alto", SandboxConfig{Image: "r", PIDsLimit: MaxSandboxPIDs + 1}, "pids_limit"},
	}
	for _, tt := range tests {
		err := tt.c.Normalize()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v", tt.name, err)
		}
	}
}

func TestValidateEnvironmentEnv(t *testing.T) {
	if err := ValidateEnvironmentEnv(map[string]string{"API_URL": "https://x", "_a1": ""}); err != nil {
		t.Errorf("env válido: %v", err)
	}
	many := map[string]string{}
	for i := 0; i <= MaxEnvironmentEnvVars; i++ {
		many["V"+strings.Repeat("a", i)] = "x"
	}
	bad := []map[string]string{
		many,
		{"1ABC": "x"}, {"A-B": "x"},
		{"HANKO_TOKEN": "x"}, {"hanko_x": "x"}, {"PATH": "x"}, {"HOME": "x"}, {"LD_PRELOAD": "x"},
		{"OK": strings.Repeat("v", MaxEnvironmentEnvValue+1)}, {"OK": "a\x00b"},
	}
	for i, env := range bad {
		if err := ValidateEnvironmentEnv(env); !errors.Is(err, ErrInvalid) {
			t.Errorf("caso %d deveria falhar", i)
		}
	}
	if clipText("abc", 5) != "abc" || clipText("abcdef", 3) != "abc…" {
		t.Error("clipText")
	}
}

func TestSkillSources(t *testing.T) {
	for _, e := range []EntrySelection{EntrySelected, EntryExcluded, EntryNew} {
		if !e.Valid() {
			t.Errorf("seleção %s", e)
		}
	}
	if EntrySelection("x").Valid() {
		t.Error("seleção inválida")
	}
	for n, want := range map[int]time.Duration{
		-1: 5 * time.Minute, 0: 5 * time.Minute, 1: 5 * time.Minute, 2: 10 * time.Minute, 3: 20 * time.Minute,
		4: 40 * time.Minute, 5: 60 * time.Minute, 50: 60 * time.Minute,
	} {
		if got := SkillSourceBackoff(n); got != want {
			t.Errorf("SkillSourceBackoff(%d) = %v, quer %v", n, got, want)
		}
	}
	for s, want := range map[SkillTestStatus]bool{
		SkillTestQueued: false, SkillTestRunning: false, SkillTestDone: true, SkillTestFailed: true, SkillTestCancelled: true,
	} {
		if s.Terminal() != want {
			t.Errorf("Terminal(%s)", s)
		}
	}
}
