// Package backup cria, lista, confere e restaura backups da instalação (ADR-0062). Um backup é um arquivo tar com
// o dump do Postgres (pg_dump -Fc, que já leva o conteúdo dos artefatos do driver postgres) e, à parte, o conteúdo
// dos artefatos guardados em drivers externos (S3), mais um manifest com tamanho e sha256 de cada membro. O pg_dump
// e o pg_restore rodam por syscmd.Runner e o conteúdo externo por Blobs, então tudo é testável sem Postgres nem S3.
package backup

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/syscmd"
)

// FormatVersion é a versão do layout do arquivo.
const FormatVersion = 1

// Tipos de backup (quem o originou).
const (
	KindManual    = "manual"
	KindScheduled = "scheduled"
	KindPreUpdate = "pre_update"
	KindPreDeploy = "pre_deploy"
)

// Nomes dos membros do tar.
const (
	memberManifest = "manifest.json"
	memberDump     = "db.pgdump"
	memberBlobs    = "blobs/"
)

// DefaultKeep é quantos backups automáticos (agendados e pré-atualização) ficam por padrão.
const DefaultKeep = 7

// ErrNotFound: não há backup com esse nome.
var ErrNotFound = errors.New("backup: não encontrado")

// ErrCorrupt: o arquivo não bate com o manifest (ou está incompleto).
var ErrCorrupt = errors.New("backup: arquivo corrompido")

// BlobEntry é o conteúdo de um artefato guardado fora do Postgres, incluído no backup.
type BlobEntry struct {
	CompanyID  string `json:"company_id"`
	ArtifactID string `json:"artifact_id"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Mime       string `json:"mime,omitempty"`
	Driver     string `json:"driver"`
}

func (b BlobEntry) member() string { return memberBlobs + b.CompanyID + "/" + b.ArtifactID }

// Manifest descreve o conteúdo do arquivo e o que é preciso para restaurá-lo com segurança.
type Manifest struct {
	Format        int         `json:"format"`
	HankoVersion  string      `json:"hanko_version"`
	CreatedAt     time.Time   `json:"created_at"`
	Kind          string      `json:"kind"`
	Label         string      `json:"label,omitempty"`
	SchemaVersion int64       `json:"schema_version"`
	DumpBytes     int64       `json:"dump_bytes"`
	DumpSHA256    string      `json:"dump_sha256"`
	Blobs         []BlobEntry `json:"blobs,omitempty"`
}

// Entry é um backup no diretório.
type Entry struct {
	Name     string   `json:"name"` // nome do arquivo (sem diretório)
	Path     string   `json:"path"`
	Size     int64    `json:"size"`
	Manifest Manifest `json:"manifest"`
}

// Blobs dá acesso ao conteúdo dos artefatos que NÃO estão no dump (drivers externos).
type Blobs interface {
	// List devolve o que precisa entrar no backup.
	List(ctx context.Context) ([]BlobEntry, error)
	// Get lê o conteúdo de um item.
	Get(ctx context.Context, b BlobEntry) ([]byte, error)
	// Put grava o conteúdo de volta (idempotente).
	Put(ctx context.Context, b BlobEntry, data []byte) error
}

// Manager cria e restaura backups em Dir.
type Manager struct {
	Dir     string
	Keep    int // backups automáticos retidos; <=0 usa DefaultKeep
	Version string
	// DSN é a URL do banco; usada só para montar o ambiente (PG*) do pg_dump/pg_restore, nunca vai em argumentos.
	DSN         string
	DumpBin     string // padrão pg_dump
	RestoreBin  string // padrão pg_restore
	PSQLBin     string // padrão psql
	Cmd         syscmd.Runner
	Blobs       Blobs // nulo = sem conteúdo externo
	SchemaFn    func(ctx context.Context) (int64, error)
	KnownSchema int64 // maior migration que este binário conhece; um backup mais novo é recusado sem Force
	Now         func() time.Time
	Log         *slog.Logger
}

// CreateOptions personaliza Create.
type CreateOptions struct {
	Kind  string // padrão manual
	Label string
	// Prune apaga os automáticos além de Keep depois de criar.
	Prune bool
}

// RestoreOptions personaliza Restore.
type RestoreOptions struct {
	// Force restaura mesmo com schema do backup mais novo que o binário.
	Force bool
	// SkipBlobs não devolve o conteúdo externo.
	SkipBlobs bool
}

// RestoreReport é o resultado de um restore.
type RestoreReport struct {
	Name          string
	SchemaVersion int64
	BlobsRestored int
	// NeedsMigrate: o schema do backup é mais antigo que o do binário; rode `hanko migrate`.
	NeedsMigrate bool
}

var nameRe = regexp.MustCompile(`^hanko-\d{8}T\d{6}Z(-\d+)?\.tar$`)

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (m *Manager) dumpBin() string {
	if m.DumpBin != "" {
		return m.DumpBin
	}
	return "pg_dump"
}

func (m *Manager) psqlBin() string {
	if m.PSQLBin != "" {
		return m.PSQLBin
	}
	return "psql"
}

func (m *Manager) restoreBin() string {
	if m.RestoreBin != "" {
		return m.RestoreBin
	}
	return "pg_restore"
}

func (m *Manager) keep() int {
	if m.Keep > 0 {
		return m.Keep
	}
	return DefaultKeep
}

var (
	tracer = otel.Tracer("hanko/backup")
	meter  = otel.Meter("hanko/backup")
	opsCtr metric.Int64Counter
	bytCtr metric.Int64Counter
)

func init() {
	opsCtr, _ = meter.Int64Counter("hanko.backup.ops", metric.WithDescription("operações de backup, por operação e resultado"))
	bytCtr, _ = meter.Int64Counter("hanko.backup.bytes", metric.WithUnit("By"), metric.WithDescription("bytes gravados em backups"))
}

func record(ctx context.Context, op string, err error) {
	res := "ok"
	if err != nil {
		res = "error"
	}
	opsCtr.Add(ctx, 1, metric.WithAttributes(attribute.String("op", op), attribute.String("result", res)))
}

// pgEnv monta as variáveis PG* a partir da URL do banco, para a senha nunca aparecer em argv.
func pgEnv(dsn string) ([]string, error) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("backup: URL do banco inválida")
	}
	env := []string{"PGHOST=" + cfg.Host, fmt.Sprintf("PGPORT=%d", cfg.Port), "PGUSER=" + cfg.User, "PGDATABASE=" + cfg.Database}
	if cfg.Password != "" {
		env = append(env, "PGPASSWORD="+cfg.Password)
	}
	mode := "prefer"
	if u, err := url.Parse(dsn); err == nil {
		if sm := u.Query().Get("sslmode"); sm != "" {
			mode = sm
		}
	}
	env = append(env, "PGSSLMODE="+mode, "PGCONNECT_TIMEOUT=15")
	return env, nil
}

func sumHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (m *Manager) ensureDir() error {
	if m.Dir == "" {
		return errors.New("backup: diretório de backups não configurado (HANKO_BACKUP_DIR)")
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("backup: criar %s: %w", m.Dir, err)
	}
	return nil
}

// Create gera um backup novo. Nada fica visível no diretório até o arquivo estar completo (rename atômico).
func (m *Manager) Create(ctx context.Context, o CreateOptions) (Entry, error) {
	ctx, span := tracer.Start(ctx, "hanko.backup.create", trace.WithAttributes(attribute.String("hanko.backup.kind", o.Kind)))
	defer span.End()
	e, err := m.create(ctx, o)
	record(ctx, "create", err)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "backup falhou")
		return Entry{}, err
	}
	bytCtr.Add(ctx, e.Size)
	return e, nil
}

func (m *Manager) create(ctx context.Context, o CreateOptions) (Entry, error) {
	if m.Cmd == nil {
		return Entry{}, errors.New("backup: executor não configurado")
	}
	if err := m.ensureDir(); err != nil {
		return Entry{}, err
	}
	kind := o.Kind
	if kind == "" {
		kind = KindManual
	}
	env, err := pgEnv(m.DSN)
	if err != nil {
		return Entry{}, err
	}
	if _, err := m.Cmd.LookPath(m.dumpBin()); err != nil {
		return Entry{}, fmt.Errorf("backup: %s não encontrado no PATH (instale o cliente do PostgreSQL ou aponte HANKO_PG_DUMP_BIN): %w", m.dumpBin(), err)
	}
	var schema int64
	if m.SchemaFn != nil {
		if schema, err = m.SchemaFn(ctx); err != nil {
			return Entry{}, fmt.Errorf("backup: versão do schema: %w", err)
		}
	}
	tmp, err := os.MkdirTemp(m.Dir, ".tmp-")
	if err != nil {
		return Entry{}, fmt.Errorf("backup: diretório temporário: %w", err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // limpeza best-effort

	// 1) dump do banco para um arquivo temporário (o tar precisa do tamanho antes do conteúdo)
	dumpPath := filepath.Join(tmp, memberDump)
	df, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // dentro do diretório temporário criado acima
	if err != nil {
		return Entry{}, fmt.Errorf("backup: %w", err)
	}
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(df, h)}
	runErr := m.Cmd.Run(ctx, syscmd.Cmd{Name: m.dumpBin(), Env: env, Stdout: cw,
		Args: []string{"--format=custom", "--no-owner", "--no-acl", "--lock-wait-timeout=60000"}})
	if cerr := df.Close(); runErr == nil && cerr != nil {
		runErr = cerr
	}
	if runErr != nil {
		return Entry{}, fmt.Errorf("backup: pg_dump: %w", runErr)
	}
	if cw.n == 0 {
		return Entry{}, errors.New("backup: pg_dump não produziu saída")
	}
	man := Manifest{Format: FormatVersion, HankoVersion: m.Version, CreatedAt: m.now(), Kind: kind, Label: o.Label,
		SchemaVersion: schema, DumpBytes: cw.n, DumpSHA256: hex.EncodeToString(h.Sum(nil))}

	// 2) conteúdo fora do dump
	var blobs []BlobEntry
	if m.Blobs != nil {
		if blobs, err = m.Blobs.List(ctx); err != nil {
			return Entry{}, fmt.Errorf("backup: listar conteúdo externo: %w", err)
		}
	}
	man.Blobs = blobs

	// 3) arquivo final
	name := m.freshName(man.CreatedAt)
	tarTmp := filepath.Join(tmp, "out.tar")
	if err := m.writeArchive(ctx, tarTmp, dumpPath, man); err != nil {
		return Entry{}, err
	}
	final := filepath.Join(m.Dir, name)
	if err := os.Rename(tarTmp, final); err != nil {
		return Entry{}, fmt.Errorf("backup: publicar arquivo: %w", err)
	}
	ent, err := m.entryFor(name, man)
	if err != nil {
		return Entry{}, err
	}
	if err := writeSidecar(final, man); err != nil {
		m.log().Warn("backup: sidecar não gravado (o manifest segue dentro do arquivo)", "name", name, "err", err)
	}
	m.log().Info("backup criado", "name", name, "bytes", ent.Size, "kind", kind, "blobs", len(blobs))
	if o.Prune {
		if _, err := m.Prune(); err != nil {
			m.log().Warn("backup: poda falhou", "err", err)
		}
	}
	return ent, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (m *Manager) freshName(t time.Time) string {
	base := "hanko-" + t.Format("20060102T150405Z")
	name := base + ".tar"
	for i := 2; ; i++ {
		if _, err := os.Stat(filepath.Join(m.Dir, name)); os.IsNotExist(err) {
			return name
		}
		name = fmt.Sprintf("%s-%d.tar", base, i)
	}
}

func (m *Manager) writeArchive(ctx context.Context, out, dumpPath string, man Manifest) error {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // dentro do diretório temporário
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	tw := tar.NewWriter(f)
	fail := func(err error) error {
		_ = f.Close()
		return err
	}
	put := func(name string, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: man.CreatedAt, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
	df, err := os.Open(dumpPath) //nolint:gosec // dentro do diretório temporário
	if err != nil {
		return fail(err)
	}
	err = put(memberDump, man.DumpBytes, df)
	_ = df.Close()
	if err != nil {
		return fail(fmt.Errorf("backup: gravar dump: %w", err))
	}
	for _, b := range man.Blobs {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		data, err := m.Blobs.Get(ctx, b)
		if err != nil {
			return fail(fmt.Errorf("backup: ler conteúdo %s/%s: %w", b.CompanyID, b.ArtifactID, err))
		}
		if int64(len(data)) != b.Size || sumHex(data) != b.SHA256 {
			return fail(fmt.Errorf("backup: o conteúdo %s/%s não bate com o tamanho/sha256 registrados", b.CompanyID, b.ArtifactID))
		}
		if err := put(b.member(), b.Size, strings.NewReader(string(data))); err != nil {
			return fail(fmt.Errorf("backup: gravar conteúdo externo: %w", err))
		}
	}
	mb, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := put(memberManifest, int64(len(mb)), strings.NewReader(string(mb))); err != nil {
		return fail(err)
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	return f.Close()
}

func sidecarPath(archive string) string { return strings.TrimSuffix(archive, ".tar") + ".json" }

func writeSidecar(archive string, man Manifest) error {
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	tmp := sidecarPath(archive) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, sidecarPath(archive))
}

func (m *Manager) entryFor(name string, man Manifest) (Entry, error) {
	p := filepath.Join(m.Dir, name)
	st, err := os.Stat(p)
	if err != nil {
		return Entry{}, fmt.Errorf("backup: %w", err)
	}
	return Entry{Name: name, Path: p, Size: st.Size(), Manifest: man}, nil
}

// List devolve os backups do diretório, do mais novo para o mais antigo. Lê o manifest do arquivo .json ao lado;
// sem ele, do próprio tar. Arquivos ilegíveis são ignorados (e logados).
func (m *Manager) List() ([]Entry, error) {
	if m.Dir == "" {
		return nil, errors.New("backup: diretório de backups não configurado (HANKO_BACKUP_DIR)")
	}
	des, err := os.ReadDir(m.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	var out []Entry
	for _, de := range des {
		if de.IsDir() || !nameRe.MatchString(de.Name()) {
			continue
		}
		p := filepath.Join(m.Dir, de.Name())
		man, err := readSidecar(p)
		if err != nil {
			man, err = readManifestFromTar(p)
		}
		if err != nil {
			m.log().Warn("backup: arquivo ignorado (manifest ilegível)", "name", de.Name(), "err", err)
			continue
		}
		e, err := m.entryFor(de.Name(), man)
		if err != nil {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Manifest.CreatedAt.Equal(out[j].Manifest.CreatedAt) {
			return out[i].Manifest.CreatedAt.After(out[j].Manifest.CreatedAt)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

func readSidecar(archive string) (Manifest, error) {
	b, err := os.ReadFile(sidecarPath(archive)) //nolint:gosec // caminho montado a partir do diretório de backups
	if err != nil {
		return Manifest{}, err
	}
	var man Manifest
	if err := json.Unmarshal(b, &man); err != nil {
		return Manifest{}, err
	}
	if man.Format != FormatVersion {
		return Manifest{}, fmt.Errorf("formato %d desconhecido", man.Format)
	}
	return man, nil
}

func readManifestFromTar(path string) (Manifest, error) {
	f, err := os.Open(path) //nolint:gosec // caminho montado a partir do diretório de backups
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close() //nolint:errcheck // leitura
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return Manifest{}, fmt.Errorf("%w: sem manifest", ErrCorrupt)
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if h.Name != memberManifest {
			continue
		}
		var man Manifest
		if err := json.NewDecoder(io.LimitReader(tr, 64<<20)).Decode(&man); err != nil {
			return Manifest{}, fmt.Errorf("%w: manifest: %v", ErrCorrupt, err)
		}
		return man, nil
	}
}

// resolve valida o nome (só o nome do arquivo, sem diretório) e devolve o caminho. "latest" é o mais novo.
func (m *Manager) resolve(name string) (string, error) {
	if name == "latest" {
		l, err := m.List()
		if err != nil {
			return "", err
		}
		if len(l) == 0 {
			return "", ErrNotFound
		}
		return l[0].Path, nil
	}
	if !nameRe.MatchString(name) {
		return "", fmt.Errorf("%w: %q não é um nome de backup válido", ErrNotFound, name)
	}
	p := filepath.Join(m.Dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// walk percorre o tar entregando cada membro (nome, tamanho, leitor); a leitura de cada um é entregue ao callback.
func walk(path string, fn func(h *tar.Header, r io.Reader) error) error {
	f, err := os.Open(path) //nolint:gosec // caminho validado por resolve
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // leitura
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if h.Typeflag != tar.TypeReg {
			return fmt.Errorf("%w: membro %q não é arquivo regular", ErrCorrupt, h.Name)
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

// Verify confere o arquivo inteiro contra o manifest (tamanho e sha256 de cada membro) sem tocar no banco.
func (m *Manager) Verify(ctx context.Context, name string) (Manifest, error) {
	_, span := tracer.Start(ctx, "hanko.backup.verify")
	defer span.End()
	p, err := m.resolve(name)
	if err != nil {
		return Manifest{}, err
	}
	man, err := verifyFile(p)
	record(ctx, "verify", err)
	if err != nil {
		span.RecordError(err)
	}
	return man, err
}

func verifyFile(path string) (Manifest, error) {
	man, err := readManifestFromTar(path)
	if err != nil {
		return Manifest{}, err
	}
	if man.Format != FormatVersion {
		return Manifest{}, fmt.Errorf("%w: formato %d desconhecido", ErrCorrupt, man.Format)
	}
	want := map[string][2]any{memberDump: {man.DumpBytes, man.DumpSHA256}}
	for _, b := range man.Blobs {
		want[b.member()] = [2]any{b.Size, b.SHA256}
	}
	seen := map[string]bool{}
	err = walk(path, func(h *tar.Header, r io.Reader) error {
		if h.Name == memberManifest {
			return nil
		}
		w, ok := want[h.Name]
		if !ok {
			return fmt.Errorf("%w: membro inesperado %q", ErrCorrupt, h.Name)
		}
		if seen[h.Name] {
			return fmt.Errorf("%w: membro repetido %q", ErrCorrupt, h.Name)
		}
		seen[h.Name] = true
		hh := sha256.New()
		n, err := io.Copy(hh, r)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if n != w[0].(int64) || hex.EncodeToString(hh.Sum(nil)) != w[1].(string) {
			return fmt.Errorf("%w: %q não bate com o manifest", ErrCorrupt, h.Name)
		}
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	for k := range want {
		if !seen[k] {
			return Manifest{}, fmt.Errorf("%w: falta o membro %q", ErrCorrupt, k)
		}
	}
	return man, nil
}

// Restore devolve o banco (e o conteúdo externo) ao estado do backup. É destrutivo: o pg_restore roda com --clean
// dentro de uma transação. O chamador deve ter parado o servidor e confirmado com o operador.
func (m *Manager) Restore(ctx context.Context, name string, o RestoreOptions) (RestoreReport, error) {
	ctx, span := tracer.Start(ctx, "hanko.backup.restore")
	defer span.End()
	rep, err := m.restore(ctx, name, o)
	record(ctx, "restore", err)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "restore falhou")
	}
	return rep, err
}

func (m *Manager) restore(ctx context.Context, name string, o RestoreOptions) (RestoreReport, error) {
	if m.Cmd == nil {
		return RestoreReport{}, errors.New("backup: executor não configurado")
	}
	p, err := m.resolve(name)
	if err != nil {
		return RestoreReport{}, err
	}
	man, err := verifyFile(p)
	if err != nil {
		return RestoreReport{}, err
	}
	if man.SchemaVersion > m.KnownSchema && m.KnownSchema > 0 && !o.Force {
		return RestoreReport{}, fmt.Errorf("backup: o schema do backup (%d) é mais novo que o deste binário (%d); atualize o hanko antes ou use --force", man.SchemaVersion, m.KnownSchema)
	}
	if len(man.Blobs) > 0 && m.Blobs == nil && !o.SkipBlobs {
		return RestoreReport{}, errors.New("backup: o arquivo tem conteúdo de artefatos em driver externo, mas o driver não está configurado neste servidor (use --skip-blobs para restaurar só o banco)")
	}
	for _, bin := range []string{m.restoreBin(), m.psqlBin()} {
		if _, err := m.Cmd.LookPath(bin); err != nil {
			return RestoreReport{}, fmt.Errorf("backup: %s não encontrado no PATH (instale o cliente do PostgreSQL; HANKO_PG_RESTORE_BIN e HANKO_PSQL_BIN apontam outro caminho): %w", bin, err)
		}
	}
	env, err := pgEnv(m.DSN)
	if err != nil {
		return RestoreReport{}, err
	}
	if err := m.ensureDir(); err != nil {
		return RestoreReport{}, err
	}
	tmp, err := os.MkdirTemp(m.Dir, ".tmp-")
	if err != nil {
		return RestoreReport{}, err
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // limpeza best-effort
	dumpPath := filepath.Join(tmp, memberDump)
	blobData := map[string][]byte{}
	err = walk(p, func(h *tar.Header, r io.Reader) error {
		switch {
		case h.Name == memberDump:
			f, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // diretório temporário
			if err != nil {
				return err
			}
			_, cerr := io.Copy(f, r)
			if e := f.Close(); cerr == nil {
				cerr = e
			}
			return cerr
		case strings.HasPrefix(h.Name, memberBlobs) && !o.SkipBlobs:
			data, err := io.ReadAll(io.LimitReader(r, h.Size+1))
			if err != nil {
				return err
			}
			blobData[h.Name] = data
		}
		return nil
	})
	if err != nil {
		return RestoreReport{}, err
	}
	// O banco é SUBSTITUÍDO, não mesclado: um --clean só apagaria o que o dump conhece e deixaria tabelas de migrations
	// mais novas (o goose voltaria atrás e a próxima migration falharia com "já existe"). Por isso o script é
	// "apaga o schema public + recria tudo", gerado pelo pg_restore e aplicado pelo psql em UMA transação: se o
	// restore falhar no meio, nada muda.
	script := filepath.Join(tmp, "restore.sql")
	sf, err := os.OpenFile(script, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // diretório temporário
	if err != nil {
		return RestoreReport{}, err
	}
	_, werr := io.WriteString(sf, "DROP SCHEMA IF EXISTS public CASCADE;\nCREATE SCHEMA public;\n")
	if werr == nil {
		werr = m.Cmd.Run(ctx, syscmd.Cmd{Name: m.restoreBin(), Env: env, Stdout: sf,
			Args: []string{"--no-owner", "--no-acl", "--file=-", dumpPath}})
	}
	if cerr := sf.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return RestoreReport{}, fmt.Errorf("backup: pg_restore: %w", werr)
	}
	if err := m.Cmd.Run(ctx, syscmd.Cmd{Name: m.psqlBin(), Env: env,
		Args: []string{"-X", "-q", "--single-transaction", "-v", "ON_ERROR_STOP=1", "--dbname=" + dbName(env), "--file=" + script}}); err != nil {
		return RestoreReport{}, fmt.Errorf("backup: aplicar o restore (psql; o banco não foi alterado): %w", err)
	}
	rep := RestoreReport{Name: filepath.Base(p), SchemaVersion: man.SchemaVersion, NeedsMigrate: m.KnownSchema > man.SchemaVersion}
	if !o.SkipBlobs {
		var errs []error
		for _, b := range man.Blobs {
			if err := m.Blobs.Put(ctx, b, blobData[b.member()]); err != nil {
				errs = append(errs, fmt.Errorf("%s/%s: %w", b.CompanyID, b.ArtifactID, err))
				continue
			}
			rep.BlobsRestored++
		}
		if len(errs) > 0 {
			return rep, fmt.Errorf("backup: o banco foi restaurado, mas %d conteúdo(s) externo(s) falharam: %w", len(errs), errors.Join(errs...))
		}
	}
	m.log().Info("backup restaurado", "name", rep.Name, "schema", rep.SchemaVersion, "blobs", rep.BlobsRestored)
	return rep, nil
}

func dbName(env []string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PGDATABASE="); ok {
			return v
		}
	}
	return ""
}

// Prune apaga os backups automáticos (agendados e pré-atualização/deploy) além de Keep, dos mais antigos. Os
// manuais nunca são podados. Devolve os nomes removidos.
func (m *Manager) Prune() ([]string, error) {
	l, err := m.List()
	if err != nil {
		return nil, err
	}
	var auto []Entry
	for _, e := range l {
		if e.Manifest.Kind != KindManual {
			auto = append(auto, e)
		}
	}
	var gone []string
	for i := m.keep(); i < len(auto); i++ {
		e := auto[i]
		if err := os.Remove(e.Path); err != nil && !os.IsNotExist(err) {
			return gone, fmt.Errorf("backup: apagar %s: %w", e.Name, err)
		}
		_ = os.Remove(sidecarPath(e.Path))
		gone = append(gone, e.Name)
	}
	return gone, nil
}
