package config

import (
	"io/fs"
	"strings"
	"testing"
	"time"
)

func envMap(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, fs.ErrNotExist
	}
}

func TestLoad(t *testing.T) {
	const db = "postgres://u@h:5432/d"
	tests := []struct {
		name    string
		env     map[string]string
		files   map[string]string
		wantErr string
		check   func(t *testing.T, c Config)
	}{
		{
			name: "defaults",
			env:  map[string]string{"HANKO_DATABASE_URL": db},
			check: func(t *testing.T, c Config) {
				if c.Addr != "127.0.0.1:7420" || c.PublicURL != "http://127.0.0.1:7420" || c.Mode != "trusted" ||
					!c.MigrateOnBoot || c.Memory.Provider != "ai-memory" || c.Memory.URL != "http://127.0.0.1:49374" ||
					c.Workers != 4 || c.RunTimeout != 30*time.Minute || c.HeartbeatInterval != time.Minute ||
					c.LogLevel != "info" || c.LogFormat != "json" {
					t.Fatalf("defaults inesperados: %+v", c)
				}
				if !c.SecretGenerated || len(c.Secret) < 32 {
					t.Fatalf("secret deveria ser gerado: %+v", c)
				}
				if !hasWarn(c, "HANKO_SECRET") {
					t.Fatalf("faltou aviso de secret: %v", c.Warnings)
				}
			},
		},
		{
			name:    "sem database url",
			wantErr: "HANKO_DATABASE_URL é obrigatório",
		},
		{
			name: "yaml e env sobrescreve",
			env:  map[string]string{"HANKO_CONFIG": "c.yaml", "HANKO_WORKERS": "9", "HANKO_MIGRATE_ON_BOOT": "false"},
			files: map[string]string{"c.yaml": `
database_url: ` + db + `
workers: 2
run_timeout: 10m
memory:
  provider: none
secret: 0123456789abcdef0123456789abcdef
`},
			check: func(t *testing.T, c Config) {
				if c.Workers != 9 || c.RunTimeout != 10*time.Minute || c.Memory.Provider != "none" ||
					c.MigrateOnBoot || c.Secret != "0123456789abcdef0123456789abcdef" || c.SecretGenerated {
					t.Fatalf("merge incorreto: %+v", c)
				}
			},
		},
		{
			name:  "hanko.yaml padrão",
			files: map[string]string{"hanko.yaml": "database_url: " + db + "\naddr: 0.0.0.0:8080\n"},
			check: func(t *testing.T, c Config) {
				if c.Addr != "0.0.0.0:8080" || c.PublicURL != "http://0.0.0.0:8080" {
					t.Fatalf("%+v", c)
				}
				if !hasWarn(c, "trusted com bind fora de loopback") {
					t.Fatalf("faltou aviso: %v", c.Warnings)
				}
			},
		},
		{
			name:    "config explícito inexistente",
			env:     map[string]string{"HANKO_CONFIG": "nao.yaml", "HANKO_DATABASE_URL": db},
			wantErr: "não foi possível ler nao.yaml",
		},
		{
			name:    "yaml campo desconhecido",
			files:   map[string]string{"hanko.yaml": "database_url: " + db + "\nfoo: 1\n"},
			wantErr: "inválido",
		},
		{
			name:    "mode inválido",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "x"},
			wantErr: "HANKO_MODE",
		},
		{
			name: "authenticated válido (loopback http)",
			env:  map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef"},
			check: func(t *testing.T, c Config) {
				if !c.Authenticated() || c.Auth.Provider != AuthLocal || c.SecureCookies() {
					t.Fatalf("cfg inesperada: %+v", c.Auth)
				}
			},
		},
		{
			name:    "authenticated exige secret",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated"},
			wantErr: "HANKO_SECRET",
		},
		{
			name:    "authenticated exige https fora de loopback",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef", "HANKO_PUBLIC_URL": "http://hanko.exemplo.com"},
			wantErr: "https",
		},
		{
			name: "authenticated https ok",
			env:  map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef", "HANKO_PUBLIC_URL": "https://hanko.exemplo.com/"},
			check: func(t *testing.T, c Config) {
				if !c.SecureCookies() || c.PublicURL != "https://hanko.exemplo.com" {
					t.Fatalf("cfg inesperada: %q", c.PublicURL)
				}
			},
		},
		{
			name:    "authenticated provider inválido",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef", "HANKO_AUTH_PROVIDER": "ldap"},
			wantErr: "HANKO_AUTH_PROVIDER",
		},
		{
			name:    "firebase exige projeto, chave e domínio",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef", "HANKO_AUTH_PROVIDER": "firebase"},
			wantErr: "HANKO_FIREBASE_PROJECT_ID",
		},
		{
			name: "firebase completo avisa sem lista de admissão",
			env: map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef",
				"HANKO_AUTH_PROVIDER": "firebase", "HANKO_FIREBASE_PROJECT_ID": "p", "HANKO_FIREBASE_API_KEY": "k", "HANKO_FIREBASE_AUTH_DOMAIN": "p.firebaseapp.com"},
			check: func(t *testing.T, c Config) {
				if !hasWarn(c, "primeiro usuário") {
					t.Fatalf("faltou aviso de admissão: %v", c.Warnings)
				}
			},
		},
		{
			name: "firebase com lista de admissão normaliza e não avisa",
			env: map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MODE": "authenticated", "HANKO_SECRET": "0123456789abcdef0123456789abcdef",
				"HANKO_AUTH_PROVIDER": "Firebase", "HANKO_FIREBASE_PROJECT_ID": "p", "HANKO_FIREBASE_API_KEY": "k", "HANKO_FIREBASE_AUTH_DOMAIN": "p.firebaseapp.com",
				"HANKO_AUTH_ALLOWED_EMAILS": " Ana@Exemplo.com , ", "HANKO_AUTH_ALLOWED_DOMAINS": "Exemplo.com"},
			check: func(t *testing.T, c Config) {
				if hasWarn(c, "primeiro usuário") || len(c.Auth.AllowedEmails) != 1 || c.Auth.AllowedEmails[0] != "ana@exemplo.com" || c.Auth.AllowedDomains[0] != "exemplo.com" {
					t.Fatalf("cfg inesperada: %+v %v", c.Auth, c.Warnings)
				}
			},
		},
		{
			name:    "memory provider inválido",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_MEMORY_PROVIDER": "redis"},
			wantErr: "HANKO_MEMORY_PROVIDER",
		},
		{
			name:    "vários erros agregados",
			env:     map[string]string{"HANKO_DATABASE_URL": "mysql://x", "HANKO_WORKERS": "0", "HANKO_LOG_LEVEL": "trace", "HANKO_LOG_FORMAT": "xml"},
			wantErr: "HANKO_WORKERS",
		},
		{
			name:    "duração inválida",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_RUN_TIMEOUT": "abc"},
			wantErr: "variável de ambiente inválida",
		},
		{
			name:    "addr inválido",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_ADDR": "7420"},
			wantErr: "HANKO_ADDR",
		},
		{
			name:    "secret curto",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_SECRET": "curto-demais"},
			wantErr: "HANKO_SECRET tem 12 bytes",
		},
		{
			name: "listas e raiz de workspaces",
			env: map[string]string{"HANKO_DATABASE_URL": db, "HANKO_AGENT_ENV_ALLOW": "FOO, BAR_*,", "HANKO_ALLOWED_HOSTS": "hanko.local, ,x.test:8443",
				"HANKO_WORKSPACES_ROOT": "/srv/ws"},
			check: func(t *testing.T, c Config) {
				if len(c.AgentEnvAllow) != 2 || c.AgentEnvAllow[1] != "BAR_*" || len(c.AllowedHosts) != 2 || c.AllowedHosts[1] != "x.test:8443" || c.WorkspacesRoot != "/srv/ws" {
					t.Fatalf("%+v", c)
				}
			},
		},
		{
			name: "raiz de workspaces padrão",
			env:  map[string]string{"HANKO_DATABASE_URL": db},
			check: func(t *testing.T, c Config) {
				if !strings.HasSuffix(c.WorkspacesRoot, "hanko-workspaces") {
					t.Fatal(c.WorkspacesRoot)
				}
			},
		},
		{
			name:    "raiz relativa",
			env:     map[string]string{"HANKO_DATABASE_URL": db, "HANKO_WORKSPACES_ROOT": "rel/x"},
			wantErr: "HANKO_WORKSPACES_ROOT",
		},
		{
			name: "public url explícita",
			env:  map[string]string{"HANKO_DATABASE_URL": db, "HANKO_PUBLIC_URL": "https://hanko.oute.pro/"},
			check: func(t *testing.T, c Config) {
				if c.PublicURL != "https://hanko.oute.pro" {
					t.Fatal(c.PublicURL)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := LoadFrom(envMap(tc.env), files(tc.files))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("erro = %v, quer contendo %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

func TestLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:1": true, "localhost:1": true, "[::1]:1": true, "0.0.0.0:1": false, ":1": false, "10.0.0.2:1": false,
	} {
		if got := (Config{Addr: addr}).IsLoopback(); got != want {
			t.Errorf("IsLoopback(%q) = %v", addr, got)
		}
	}
	if !(Config{Addr: "127.0.0.1:1", Mode: ModeTrusted}).TrustedMCP() || (Config{Addr: "0.0.0.0:1", Mode: ModeTrusted}).TrustedMCP() {
		t.Error("TrustedMCP")
	}
}

func hasWarn(c Config, sub string) bool {
	for _, w := range c.Warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
