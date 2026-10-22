// Package embeddedpg sobe um Postgres local descartável para `hanko serve --embedded-pg` (ADR-0061). É só para
// desenvolvimento e trial: o Hanko NÃO empacota o Postgres nem baixa nada da rede; usa initdb e pg_ctl de uma
// instalação local do PostgreSQL e gerencia um cluster próprio (dados, senha e porta em um diretório do usuário).
// Produção continua com o Postgres externo, única dependência de runtime.
package embeddedpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/renatobardi/hanko/internal/syscmd"
)

// MinMajor é a versão mínima aceita do PostgreSQL (a produção e o CI usam 16).
const MinMajor = 14

// Options configura o cluster.
type Options struct {
	// Dir guarda dados, senha, porta e log (padrão <HANKO_HOME>/pg).
	Dir string
	// BinDir é onde ficam initdb e pg_ctl; vazio procura no PATH e nos lugares comuns de instalação.
	BinDir string
	// Port fixa a porta; 0 reaproveita a última (arquivo port) ou escolhe uma livre.
	Port   int
	DBName string // padrão hanko
	User   string // padrão hanko
	Locale string // padrão C.UTF-8 (en_US.UTF-8 no macOS)

	Cmd syscmd.Runner
	Log *slog.Logger

	// Pontos de injeção (testes). Nulos usam o ambiente real.
	Euid     func() int
	Glob     func(pattern string) ([]string, error)
	FreePort func() (int, error)
	EnsureDB func(ctx context.Context, adminDSN, name string) error
	GOOS     string
}

// Instance é um cluster em execução.
type Instance struct {
	DSN  string
	Port int
	// Reused: o cluster já estava no ar (aberto por outro processo); Stop não o derruba.
	Reused bool
	opts   Options
	data   string
}

func (o *Options) defaults() {
	if o.DBName == "" {
		o.DBName = "hanko"
	}
	if o.User == "" {
		o.User = "hanko"
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.Locale == "" {
		o.Locale = "C.UTF-8"
		if o.GOOS == "darwin" {
			o.Locale = "en_US.UTF-8"
		}
	}
	if o.Euid == nil {
		o.Euid = os.Geteuid
	}
	if o.Glob == nil {
		o.Glob = filepath.Glob
	}
	if o.FreePort == nil {
		o.FreePort = freePort
	}
	if o.EnsureDB == nil {
		o.EnsureDB = ensureDB
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck // só reserva a porta
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ensureDB cria o banco se não existir (conexão ao banco de manutenção).
func ensureDB(ctx context.Context, adminDSN, name string) error {
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close(ctx) //nolint:errcheck // fim
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()+``) // herda encoding e locale do initdb
	return err
}

// Bins são os executáveis do Postgres usados.
type Bins struct {
	Initdb, PgCtl string
	Major         int
}

var versionRe = regexp.MustCompile(`(\d+)(?:\.\d+)*`)

// candidateDirs são os lugares comuns onde os binários do servidor ficam fora do PATH.
func candidateDirs(o Options) []string {
	var dirs []string
	if o.BinDir != "" {
		return []string{o.BinDir}
	}
	for _, pat := range []string{"/usr/lib/postgresql/*/bin", "/opt/homebrew/opt/postgresql@*/bin", "/usr/local/opt/postgresql@*/bin", "/usr/pgsql-*/bin", "/Applications/Postgres.app/Contents/Versions/*/bin"} {
		m, _ := o.Glob(pat)
		sort.Sort(sort.Reverse(sort.StringSlice(m))) // versão mais nova primeiro (ordem lexical basta para 14..99)
		dirs = append(dirs, m...)
	}
	return dirs
}

// FindBinaries localiza initdb e pg_ctl e confere a versão.
func FindBinaries(ctx context.Context, o Options) (Bins, error) {
	o.defaults()
	find := func(name string) string {
		for _, d := range candidateDirs(o) {
			p := filepath.Join(d, name)
			if _, err := o.Cmd.LookPath(p); err == nil {
				return p
			}
		}
		if o.BinDir == "" {
			if p, err := o.Cmd.LookPath(name); err == nil {
				return p
			}
		}
		return ""
	}
	b := Bins{Initdb: find("initdb"), PgCtl: find("pg_ctl")}
	if b.Initdb == "" || b.PgCtl == "" {
		return Bins{}, errors.New("não encontrei initdb/pg_ctl do PostgreSQL neste computador. O modo embutido usa uma instalação local do Postgres (sem baixar nada): " +
			"instale-a (macOS: brew install postgresql@17; Debian/Ubuntu: apt install postgresql; Fedora: dnf install postgresql-server) ou aponte --pg-bin-dir para a pasta com os binários")
	}
	var out strings.Builder
	if err := o.Cmd.Run(ctx, syscmd.Cmd{Name: b.PgCtl, Args: []string{"--version"}, Stdout: &out}); err != nil {
		return Bins{}, fmt.Errorf("pg_ctl --version: %w", err)
	}
	m := versionRe.FindStringSubmatch(out.String())
	if m == nil {
		return Bins{}, fmt.Errorf("não entendi a versão do pg_ctl: %q", strings.TrimSpace(out.String()))
	}
	b.Major, _ = strconv.Atoi(m[1])
	if b.Major < MinMajor {
		return Bins{}, fmt.Errorf("PostgreSQL %d é antigo demais para o Hanko (mínimo %d)", b.Major, MinMajor)
	}
	return b, nil
}

func readTrim(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // dentro do diretório do cluster
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Start prepara (initdb na primeira vez) e sobe o cluster, devolvendo a URL de conexão. Reaproveita um cluster já no
// ar. O banco e a senha são criados uma vez e reaproveitados nas próximas execuções.
func Start(ctx context.Context, o Options) (*Instance, error) {
	o.defaults()
	if o.Cmd == nil {
		return nil, errors.New("embeddedpg: executor não configurado")
	}
	if o.Dir == "" {
		return nil, errors.New("embeddedpg: diretório do cluster não configurado")
	}
	if o.Euid() == 0 {
		return nil, errors.New("o PostgreSQL se recusa a rodar como root: execute `hanko serve --embedded-pg` com um usuário comum")
	}
	bins, err := FindBinaries(ctx, o)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("criar %s: %w", o.Dir, err)
	}
	data := filepath.Join(o.Dir, "data")
	pwFile := filepath.Join(o.Dir, "password")
	portFile := filepath.Join(o.Dir, "port")
	logFile := filepath.Join(o.Dir, "postgres.log")

	// 1) initdb, uma vez
	if _, err := os.Stat(filepath.Join(data, "PG_VERSION")); errors.Is(err, os.ErrNotExist) {
		pw, err := randomHex(18)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(pwFile, []byte(pw+"\n"), 0o600); err != nil {
			return nil, err
		}
		o.Log.Info("embedded-pg: inicializando o cluster (initdb)", "dir", data, "pg", bins.Major)
		if err := o.Cmd.Run(ctx, syscmd.Cmd{Name: bins.Initdb, Args: []string{"-D", data, "-U", o.User, "--pwfile=" + pwFile,
			"--auth=scram-sha-256", "--encoding=UTF8", "--locale=" + o.Locale}}); err != nil {
			return nil, fmt.Errorf("initdb: %w", err)
		}
	}
	pw := readTrim(pwFile)
	if pw == "" {
		return nil, fmt.Errorf("o cluster em %s existe, mas a senha (%s) sumiu: apague o diretório para recomeçar", data, pwFile)
	}

	// 2) porta estável entre execuções
	port := o.Port
	if port == 0 {
		port, _ = strconv.Atoi(readTrim(portFile))
	}
	running := o.Cmd.Run(ctx, syscmd.Cmd{Name: bins.PgCtl, Args: []string{"-D", data, "status"}}) == nil
	if port == 0 || (!running && !portFree(port)) {
		if port, err = o.FreePort(); err != nil {
			return nil, fmt.Errorf("escolher porta: %w", err)
		}
	}
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return nil, err
	}

	inst := &Instance{Port: port, opts: o, data: data, Reused: running}
	inst.DSN = fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", o.User, pw, port, o.DBName)
	admin := fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/postgres?sslmode=disable", o.User, pw, port)

	// 3) sobe
	if running {
		o.Log.Info("embedded-pg: cluster já estava no ar; reaproveitando", "port", port)
	} else {
		opts := fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories= -c fsync=off -c synchronous_commit=off", port)
		o.Log.Info("embedded-pg: subindo o cluster", "port", port, "log", logFile)
		if err := o.Cmd.Run(ctx, syscmd.Cmd{Name: bins.PgCtl, Args: []string{"-D", data, "-l", logFile, "-w", "-t", "60", "-o", opts, "start"}}); err != nil {
			return nil, fmt.Errorf("pg_ctl start (veja %s): %w", logFile, err)
		}
	}
	if err := o.EnsureDB(ctx, admin, o.DBName); err != nil {
		if !running {
			_ = inst.Stop(context.WithoutCancel(ctx))
		}
		return nil, fmt.Errorf("criar o banco %s: %w", o.DBName, err)
	}
	return inst, nil
}

func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// Stop derruba o cluster (modo fast). Um cluster reaproveitado de outro processo fica no ar.
func (i *Instance) Stop(ctx context.Context) error {
	if i == nil || i.Reused {
		return nil
	}
	bins, err := FindBinaries(ctx, i.opts)
	if err != nil {
		return err
	}
	if err := i.opts.Cmd.Run(ctx, syscmd.Cmd{Name: bins.PgCtl, Args: []string{"-D", i.data, "-m", "fast", "-w", "-t", "60", "stop"}}); err != nil {
		return fmt.Errorf("pg_ctl stop: %w", err)
	}
	i.opts.Log.Info("embedded-pg: cluster parado")
	return nil
}
