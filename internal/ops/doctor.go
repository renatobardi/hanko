// Package ops reúne a operação da instância (ADR-0062): o diagnóstico `hanko doctor`, a atualização do binário
// `hanko update` e a instalação como serviço do sistema `hanko service install`. Tudo que toca o mundo externo
// (banco, rede, systemd/launchd, GitHub) entra por interfaces; os testes usam fakes.
package ops

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/config"
	"github.com/renatobardi/hanko/internal/db"
	"github.com/renatobardi/hanko/internal/syscmd"
)

// Status de uma checagem.
type Status string

const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip" // não se aplica a esta instalação
)

// Check é o resultado de uma checagem.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
}

// Report é o conjunto de checagens.
type Report struct {
	Checks []Check `json:"checks"`
}

// Count devolve quantas checagens têm o status.
func (r Report) Count(s Status) int {
	n := 0
	for _, c := range r.Checks {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Healthy: nenhuma checagem falhou (avisos não contam).
func (r Report) Healthy() bool { return r.Count(Fail) == 0 }

// DBProbe é o que o doctor precisa do banco.
type DBProbe interface {
	Ping(ctx context.Context) error
	ServerVersion(ctx context.Context) (string, error)
	AppliedMigrations(ctx context.Context) ([]int64, error)
}

// DoctorEnv reúne o mundo externo do doctor.
type DoctorEnv struct {
	Cfg    config.Config
	CfgErr error // erro ao carregar a configuração; o doctor mostra e segue com o que dá
	Getenv func(string) string
	DB     DBProbe // nulo = configuração sem banco utilizável
	Cmd    syscmd.Runner
	HTTP   interface {
		Do(*http.Request) (*http.Response, error)
	}
	// Dial abre uma conexão TCP (OTel). Nulo usa net.Dialer.
	Dial func(ctx context.Context, addr string) error
	// DiskFree devolve bytes livres do sistema de arquivos de path.
	DiskFree func(path string) (uint64, error)
	// LastBackup devolve quando foi o backup mais recente (zero: nenhum); nulo pula a checagem.
	LastBackup func() (time.Time, error)
	Now        func() time.Time
}

// Limiares de disco.
const (
	diskWarnBytes = 5 << 30
	diskFailBytes = 512 << 20
	// backupStale: sem backup há mais que isso vira aviso.
	backupStale = 48 * time.Hour
)

// Doctor roda todas as checagens. Uma checagem que falha não impede as outras.
func Doctor(ctx context.Context, e DoctorEnv) Report {
	if e.Now == nil {
		e.Now = time.Now
	}
	var r Report
	add := func(c Check) { r.Checks = append(r.Checks, c) }

	add(checkConfig(e))
	add(checkPostgres(ctx, e))
	add(checkMigrations(ctx, e))
	add(checkOTel(ctx, e))
	add(checkMemory(ctx, e))
	add(checkHarnesses(e))
	add(checkSecrets(e))
	add(checkDisk(e))
	add(checkPGTools(e))
	if e.LastBackup != nil {
		add(checkBackup(e))
	}
	return r
}

func checkConfig(e DoctorEnv) Check {
	if e.CfgErr != nil {
		return Check{Name: "config", Status: Fail, Detail: strings.ReplaceAll(e.CfgErr.Error(), "\n", "; "), Hint: "corrija as variáveis HANKO_* (ou o hanko.yaml) e rode de novo"}
	}
	c := Check{Name: "config", Status: OK, Detail: "modo " + e.Cfg.Mode + ", escuta em " + e.Cfg.Addr}
	if len(e.Cfg.Warnings) > 0 {
		c.Status = Warn
		c.Detail += "; " + e.Cfg.Warnings[0]
		if len(e.Cfg.Warnings) > 1 {
			c.Detail += fmt.Sprintf(" (+%d aviso(s))", len(e.Cfg.Warnings)-1)
		}
	}
	return c
}

func checkPostgres(ctx context.Context, e DoctorEnv) Check {
	if e.DB == nil {
		return Check{Name: "postgres", Status: Fail, Detail: "banco indisponível", Hint: "defina HANKO_DATABASE_URL com a URL do Postgres"}
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := e.DB.Ping(pctx); err != nil {
		return Check{Name: "postgres", Status: Fail, Detail: "sem resposta: " + redact(err.Error(), e.Cfg.DatabaseURL), Hint: "confira se o Postgres está no ar e se HANKO_DATABASE_URL está certa"}
	}
	v, err := e.DB.ServerVersion(pctx)
	if err != nil {
		return Check{Name: "postgres", Status: Warn, Detail: "conectou, mas não leu a versão: " + err.Error()}
	}
	return Check{Name: "postgres", Status: OK, Detail: "conectado, versão " + v}
}

func checkMigrations(ctx context.Context, e DoctorEnv) Check {
	if e.DB == nil {
		return Check{Name: "migrations", Status: Skip, Detail: "sem banco"}
	}
	applied, err := e.DB.AppliedMigrations(ctx)
	if err != nil {
		return Check{Name: "migrations", Status: Fail, Detail: "não consegui ler o schema: " + err.Error()}
	}
	emb := db.EmbeddedVersions()
	pending := db.PendingVersions(emb, applied)
	known := make(map[int64]bool, len(emb))
	for _, v := range emb {
		known[v] = true
	}
	var unknown []int64
	for _, v := range applied {
		if !known[v] {
			unknown = append(unknown, v)
		}
	}
	switch {
	case len(unknown) > 0:
		return Check{Name: "migrations", Status: Warn, Detail: fmt.Sprintf("o banco tem %d migration(s) que este binário não conhece (a mais nova: %d)", len(unknown), unknown[len(unknown)-1]),
			Hint: "este binário é mais antigo que o schema: atualize o hanko"}
	case len(pending) > 0:
		c := Check{Name: "migrations", Status: Fail, Detail: fmt.Sprintf("%d migration(s) pendente(s), a primeira: %d", len(pending), pending[0]), Hint: "rode `hanko migrate`"}
		if e.Cfg.MigrateOnBoot {
			c.Status = Warn
			c.Hint = "serão aplicadas no próximo `hanko serve` (HANKO_MIGRATE_ON_BOOT) ou agora com `hanko migrate`"
		}
		return c
	}
	return Check{Name: "migrations", Status: OK, Detail: fmt.Sprintf("schema em dia (%d migrations, última %d)", len(applied), db.LatestEmbedded())}
}

// otelEndpoint resolve o destino OTLP gRPC como o SDK: variável específica, geral, ou o padrão local.
func otelEndpoint(getenv func(string) string) (addr, src string) {
	for _, k := range []string{"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			if strings.Contains(v, "://") {
				if u, err := url.Parse(v); err == nil && u.Host != "" {
					return hostPort(u.Host, u.Scheme), k
				}
			}
			return v, k
		}
	}
	return "localhost:4317", ""
}

func hostPort(host, scheme string) string {
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host
	}
	if scheme == "https" {
		return host + ":443"
	}
	return host + ":4317"
}

func checkOTel(ctx context.Context, e DoctorEnv) Check {
	if e.Getenv == nil {
		return Check{Name: "otel", Status: Skip, Detail: "ambiente indisponível"}
	}
	if strings.EqualFold(strings.TrimSpace(e.Getenv("OTEL_SDK_DISABLED")), "true") {
		return Check{Name: "otel", Status: Skip, Detail: "desligado (OTEL_SDK_DISABLED=true)"}
	}
	addr, src := otelEndpoint(e.Getenv)
	dial := e.Dial
	if dial == nil {
		dial = func(ctx context.Context, a string) error {
			var d net.Dialer
			c, err := d.DialContext(ctx, "tcp", a)
			if err != nil {
				return err
			}
			return c.Close()
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := dial(dctx, addr); err != nil {
		hint := "suba um coletor OTLP em " + addr + " ou aponte OTEL_EXPORTER_OTLP_ENDPOINT; o Hanko funciona sem, mas perde traces e métricas"
		return Check{Name: "otel", Status: Warn, Detail: "coletor OTLP " + addr + " inacessível", Hint: hint}
	}
	if src == "" {
		return Check{Name: "otel", Status: OK, Detail: "coletor OTLP padrão " + addr + " acessível"}
	}
	return Check{Name: "otel", Status: OK, Detail: "coletor OTLP " + addr + " acessível (" + src + ")"}
}

func checkMemory(ctx context.Context, e DoctorEnv) Check {
	if e.Cfg.Memory.Provider != config.MemoryAIMemory {
		return Check{Name: "ai-memory", Status: Skip, Detail: "memória desligada (HANKO_MEMORY_PROVIDER=" + e.Cfg.Memory.Provider + ")"}
	}
	if e.HTTP == nil || e.Cfg.Memory.URL == "" {
		return Check{Name: "ai-memory", Status: Skip, Detail: "sem URL configurada"}
	}
	hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(hctx, http.MethodGet, strings.TrimRight(e.Cfg.Memory.URL, "/")+"/health", nil)
	if err != nil {
		return Check{Name: "ai-memory", Status: Warn, Detail: "URL inválida: " + redact(err.Error(), e.Cfg.Memory.Token)}
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return Check{Name: "ai-memory", Status: Warn, Detail: "sem resposta em " + e.Cfg.Memory.URL, Hint: "o Hanko segue sem memória (os fatos esperam na outbox); suba o ai-memory ou use HANKO_MEMORY_PROVIDER=none"}
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Check{Name: "ai-memory", Status: Warn, Detail: fmt.Sprintf("/health respondeu HTTP %d", resp.StatusCode)}
	}
	return Check{Name: "ai-memory", Status: OK, Detail: "saudável em " + e.Cfg.Memory.URL}
}

func checkHarnesses(e DoctorEnv) Check {
	if e.Cmd == nil {
		return Check{Name: "harnesses", Status: Skip, Detail: "sem executor"}
	}
	bins := []struct{ name, bin string }{
		{"claude", e.Cfg.ClaudeBin}, {"codex", e.Cfg.CodexBin}, {"devin", e.Cfg.DevinBin}, {"pi", e.Cfg.PiBin}, {"cursor", e.Cfg.CursorBin},
	}
	var found, missing []string
	for _, b := range bins {
		if b.bin == "" {
			continue
		}
		if _, err := e.Cmd.LookPath(b.bin); err == nil {
			found = append(found, b.name)
		} else {
			missing = append(missing, b.name)
		}
	}
	if len(found) == 0 {
		return Check{Name: "harnesses", Status: Warn, Detail: "nenhum harness de CLI no PATH (" + strings.Join(missing, ", ") + ")",
			Hint: "instale ao menos um (claude, codex...) ou aponte HANKO_CLAUDE_BIN etc.; sem eles só o harness http funciona"}
	}
	d := "disponíveis: " + strings.Join(found, ", ")
	if len(missing) > 0 {
		d += "; ausentes: " + strings.Join(missing, ", ")
	}
	return Check{Name: "harnesses", Status: OK, Detail: d}
}

func checkSecrets(e DoctorEnv) Check {
	if e.CfgErr != nil {
		return Check{Name: "secrets", Status: Skip, Detail: "config inválida"}
	}
	var problems, notes []string
	if e.Cfg.SecretGenerated {
		problems = append(problems, "HANKO_SECRET não definido (gerado a cada boot: run tokens não sobrevivem a restart)")
	}
	if e.Cfg.SecretKey == "" {
		notes = append(notes, "HANKO_SECRET_KEY ausente (segredos da empresa cifrados ficam indisponíveis)")
	}
	if (e.Cfg.Vault.Addr == "") != (e.Cfg.Vault.Token == "") {
		problems = append(problems, "HANKO_VAULT_ADDR e HANKO_VAULT_TOKEN precisam andar juntos")
	}
	if e.Cfg.Mode == config.ModeAuthenticated && e.Cfg.Auth.Provider == config.AuthLocal && e.Cfg.Secret == "" {
		problems = append(problems, "modo authenticated sem HANKO_SECRET")
	}
	switch {
	case len(problems) > 0:
		return Check{Name: "secrets", Status: Warn, Detail: strings.Join(append(problems, notes...), "; "), Hint: "gere com `openssl rand -hex 32` e guarde no ambiente do servidor"}
	case len(notes) > 0:
		return Check{Name: "secrets", Status: Warn, Detail: strings.Join(notes, "; "), Hint: "gere com `openssl rand -hex 32` e guarde no ambiente do servidor"}
	}
	return Check{Name: "secrets", Status: OK, Detail: "HANKO_SECRET e HANKO_SECRET_KEY definidos"}
}

// nearestExisting sobe até o primeiro diretório existente (o disco de um diretório que ainda não foi criado é o do pai).
func nearestExisting(p string) string {
	for p != "" && p != "/" && p != "." {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		p = filepath.Dir(p)
	}
	return "/"
}

func checkDisk(e DoctorEnv) Check {
	if e.DiskFree == nil {
		return Check{Name: "disco", Status: Skip, Detail: "medição indisponível"}
	}
	paths := map[string]string{}
	if e.Cfg.WorkspacesRoot != "" {
		paths["workspaces"] = e.Cfg.WorkspacesRoot
	}
	if e.Cfg.Ops.BackupDir != "" {
		paths["backups"] = e.Cfg.Ops.BackupDir
	}
	if len(paths) == 0 {
		return Check{Name: "disco", Status: Skip, Detail: "sem diretórios configurados"}
	}
	names := make([]string, 0, len(paths))
	for n := range paths {
		names = append(names, n)
	}
	sort.Strings(names)
	worst, parts := OK, []string{}
	var hint string
	for _, n := range names {
		free, err := e.DiskFree(nearestExisting(paths[n]))
		if err != nil {
			parts = append(parts, n+": não medi ("+err.Error()+")")
			if worst == OK {
				worst = Warn
			}
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s livres", n, HumanBytes(int64(free)))) //nolint:gosec // espaço livre cabe em int64
		switch {
		case free < diskFailBytes:
			worst, hint = Fail, "libere espaço: workspaces de runs antigas e backups ocupam o disco"
		case free < diskWarnBytes && worst != Fail:
			worst, hint = Warn, "menos de 5 GiB livres: libere espaço antes que um backup ou uma run falhe"
		}
	}
	return Check{Name: "disco", Status: worst, Detail: strings.Join(parts, "; "), Hint: hint}
}

func checkPGTools(e DoctorEnv) Check {
	if e.Cmd == nil {
		return Check{Name: "pg_dump", Status: Skip, Detail: "sem executor"}
	}
	dump, restore, psql := e.Cfg.Ops.PGDumpBin, e.Cfg.Ops.PGRestoreBin, e.Cfg.Ops.PSQLBin
	if psql == "" {
		psql = "psql"
	}
	if dump == "" {
		dump = "pg_dump"
	}
	if restore == "" {
		restore = "pg_restore"
	}
	var miss []string
	for _, b := range []string{dump, restore, psql} {
		if _, err := e.Cmd.LookPath(b); err != nil {
			miss = append(miss, b)
		}
	}
	if len(miss) > 0 {
		return Check{Name: "pg_dump", Status: Warn, Detail: strings.Join(miss, ", ") + " não encontrado(s) no PATH", Hint: "`hanko backup` precisa do cliente do PostgreSQL (postgresql-client); em contêiner, use o backup do host"}
	}
	return Check{Name: "pg_dump", Status: OK, Detail: dump + ", " + restore + " e " + psql + " disponíveis"}
}

func checkBackup(e DoctorEnv) Check {
	t, err := e.LastBackup()
	if err != nil {
		return Check{Name: "backup", Status: Warn, Detail: "não consegui ler o diretório de backups: " + err.Error()}
	}
	if t.IsZero() {
		return Check{Name: "backup", Status: Warn, Detail: "nenhum backup feito ainda", Hint: "rode `hanko backup create` ou agende em Configurações › Backups"}
	}
	age := e.Now().Sub(t)
	if age > backupStale {
		return Check{Name: "backup", Status: Warn, Detail: fmt.Sprintf("o último backup tem %s", HumanAge(age)), Hint: "confira o agendamento em Configurações › Backups"}
	}
	return Check{Name: "backup", Status: OK, Detail: fmt.Sprintf("último backup há %s", HumanAge(age))}
}

// HumanBytes formata bytes em unidades binárias.
func HumanBytes(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(k), 0
	for m := n / k; m >= k; m /= k {
		div *= k
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanAge formata uma duração como "3h" ou "2d".
func HumanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "menos de 1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()))
	}
	return fmt.Sprintf("%d d", int(d.Hours()/24))
}

// redact tira o segredo (se houver) de uma mensagem de erro.
func redact(msg string, secrets ...string) string {
	for _, s := range secrets {
		if s == "" {
			continue
		}
		msg = strings.ReplaceAll(msg, s, "***")
		if u, err := url.Parse(s); err == nil && u.User != nil {
			if pw, ok := u.User.Password(); ok && pw != "" {
				msg = strings.ReplaceAll(msg, pw, "***")
			}
		}
	}
	return msg
}

// ErrDoctorFailed é devolvido pelo CLI quando alguma checagem falhou (código de saída 1).
var ErrDoctorFailed = errors.New("o diagnóstico encontrou falhas")
