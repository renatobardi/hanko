package ops

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Canais de atualização. stable = releases publicadas (tag vX.Y.Z); beta = também as pré-releases (vX.Y.Z-rc.N).
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// maxDownload limita o que `hanko update` baixa (o tar.gz do binário tem dezenas de MiB).
const maxDownload = 400 << 20

// Asset é um arquivo de uma release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // URL da API do asset (aceita Accept: application/octet-stream)
	Size int64  `json:"size"`
}

// Release é uma versão publicada.
type Release struct {
	Version    string  `json:"version"` // sem o "v"
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
	Notes      string  `json:"notes,omitempty"`
}

// Source é onde as releases moram (GitHub em produção, fake nos testes).
type Source interface {
	// Latest devolve a release mais nova do canal.
	Latest(ctx context.Context, channel string) (Release, error)
	// Download grava o asset em w.
	Download(ctx context.Context, a Asset, w io.Writer) error
}

// ---------- versões ----------

type semver struct {
	major, minor, patch int
	pre                 []string
}

func parseSemver(s string) (semver, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s, pre = s[:i], s[i+1:]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return semver{}, false
		}
		n[i] = v
	}
	v := semver{major: n[0], minor: n[1], patch: n[2]}
	if pre != "" {
		v.pre = strings.Split(pre, ".")
	}
	return v, true
}

// CompareVersions devolve -1, 0 ou 1 (a<b, a==b, a>b) pela precedência do semver. ok é falso se algum lado não é semver.
func CompareVersions(a, b string) (c int, ok bool) {
	x, ok1 := parseSemver(a)
	y, ok2 := parseSemver(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	cmp := func(p, q int) int {
		switch {
		case p < q:
			return -1
		case p > q:
			return 1
		}
		return 0
	}
	if c := cmp(x.major, y.major); c != 0 {
		return c, true
	}
	if c := cmp(x.minor, y.minor); c != 0 {
		return c, true
	}
	if c := cmp(x.patch, y.patch); c != 0 {
		return c, true
	}
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0, true
	case len(x.pre) == 0:
		return 1, true
	case len(y.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		xi, xe := strconv.Atoi(x.pre[i])
		yi, ye := strconv.Atoi(y.pre[i])
		switch {
		case xe == nil && ye == nil:
			if c := cmp(xi, yi); c != 0 {
				return c, true
			}
		case xe == nil: // identificador numérico < alfanumérico
			return -1, true
		case ye == nil:
			return 1, true
		default:
			if c := strings.Compare(x.pre[i], y.pre[i]); c != 0 {
				return c, true
			}
		}
	}
	return cmp(len(x.pre), len(y.pre)), true
}

// ---------- GitHub ----------

// GitHub lê releases da API do GitHub. BaseURL e Client existem para os testes apontarem para um httptest.Server.
type GitHub struct {
	BaseURL string // padrão https://api.github.com
	Repo    string // dono/repo
	Token   string // opcional; necessário em repositório privado
	Client  *http.Client
}

func (g GitHub) base() string {
	if g.BaseURL != "" {
		return strings.TrimRight(g.BaseURL, "/")
	}
	return "https://api.github.com"
}

func (g GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return http.DefaultClient
}

func (g GitHub) newReq(ctx context.Context, url, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "hanko-update")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	return req, nil
}

type ghRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Body       string `json:"body"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Latest lista as releases e escolhe a de maior versão semver do canal (rascunhos nunca; pré-releases só no beta).
func (g GitHub) Latest(ctx context.Context, channel string) (Release, error) {
	if g.Repo == "" {
		return Release{}, errors.New("repositório de releases não configurado")
	}
	req, err := g.newReq(ctx, g.base()+"/repos/"+g.Repo+"/releases?per_page=50", "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("consultar releases: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // leitura
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Release{}, fmt.Errorf("consultar releases: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return Release{}, fmt.Errorf("ler releases: %w", err)
	}
	var best *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || (r.Prerelease && channel != ChannelBeta) {
			continue
		}
		if _, ok := parseSemver(r.TagName); !ok {
			continue
		}
		if best == nil {
			best = r
			continue
		}
		if c, _ := CompareVersions(r.TagName, best.TagName); c > 0 {
			best = r
		}
	}
	if best == nil {
		return Release{}, fmt.Errorf("nenhuma release no canal %s", channel)
	}
	out := Release{Version: strings.TrimPrefix(best.TagName, "v"), Prerelease: best.Prerelease, Notes: best.Body}
	for _, a := range best.Assets {
		out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return out, nil
}

// Download baixa o asset (octet-stream) para w, no máximo maxDownload bytes.
func (g GitHub) Download(ctx context.Context, a Asset, w io.Writer) error {
	req, err := g.newReq(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return err
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return fmt.Errorf("baixar %s: %w", a.Name, err)
	}
	defer resp.Body.Close() //nolint:errcheck // leitura
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("baixar %s: HTTP %d", a.Name, resp.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return fmt.Errorf("baixar %s: %w", a.Name, err)
	}
	if n > maxDownload {
		return fmt.Errorf("baixar %s: arquivo maior que %s", a.Name, HumanBytes(maxDownload))
	}
	return nil
}

// ---------- atualização ----------

// Updater troca o binário em execução por uma release.
type Updater struct {
	Current string // versão deste binário (ex.: v0.3.0 ou dev)
	Exe     string // caminho do executável a trocar
	GOOS    string
	GOARCH  string
	Source  Source
	// Managed, se preenchido, é o motivo pelo qual este binário não se atualiza sozinho (contêiner, gerenciador de pacotes).
	Managed string
	// Backup roda antes de trocar o binário e devolve o nome do backup feito; erro aborta a atualização.
	Backup func(ctx context.Context) (string, error)
	// Restart, se não nulo, reinicia o serviço depois da troca.
	Restart func(ctx context.Context) error
}

// Plan é o que a atualização faria.
type Plan struct {
	Channel    string `json:"channel"`
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Newer      bool   `json:"newer"`
	Prerelease bool   `json:"prerelease"`
	Archive    Asset  `json:"archive"`
	Checksums  Asset  `json:"checksums"`
}

// ArchiveName é o nome do tar.gz da release para esta plataforma (o do goreleaser).
func ArchiveName(version, goos, goarch string) string {
	return fmt.Sprintf("hanko_%s_%s_%s.tar.gz", strings.TrimPrefix(version, "v"), goos, goarch)
}

var tracer = otel.Tracer("hanko/ops")

func traceAttrs(p Plan) trace.SpanStartOption {
	return trace.WithAttributes(attribute.String("hanko.update.current", p.Current), attribute.String("hanko.update.latest", p.Latest), attribute.String("hanko.update.channel", p.Channel))
}

// Check consulta o canal e diz se há versão mais nova. Não altera nada.
func (u *Updater) Check(ctx context.Context, channel string) (Plan, error) {
	if channel != ChannelStable && channel != ChannelBeta {
		return Plan{}, fmt.Errorf("canal %q inválido: use %s ou %s", channel, ChannelStable, ChannelBeta)
	}
	ctx, span := tracer.Start(ctx, "hanko.update.check")
	defer span.End()
	rel, err := u.Source.Latest(ctx, channel)
	if err != nil {
		span.RecordError(err)
		return Plan{}, err
	}
	p := Plan{Channel: channel, Current: u.Current, Latest: rel.Version, Prerelease: rel.Prerelease}
	span.SetAttributes(attribute.String("hanko.update.latest", rel.Version), attribute.String("hanko.update.channel", channel))
	if c, ok := CompareVersions(rel.Version, u.Current); ok {
		p.Newer = c > 0
	} else {
		p.Newer = rel.Version != strings.TrimPrefix(u.Current, "v") // binário de desenvolvimento: qualquer release é "outra"
	}
	want := ArchiveName(rel.Version, u.GOOS, u.GOARCH)
	for _, a := range rel.Assets {
		switch a.Name {
		case want:
			p.Archive = a
		case "checksums.txt":
			p.Checksums = a
		}
	}
	if p.Archive.URL == "" {
		return p, fmt.Errorf("a release %s não tem o arquivo %s para esta plataforma", rel.Version, want)
	}
	if p.Checksums.URL == "" {
		return p, fmt.Errorf("a release %s não tem checksums.txt: recuso instalar sem conferir a integridade", rel.Version)
	}
	return p, nil
}

// ApplyOptions personaliza Apply.
type ApplyOptions struct {
	SkipBackup bool
	// Force permite trocar um binário de desenvolvimento ou "atualizar" para a mesma versão ou uma mais antiga.
	Force bool
}

// ApplyResult é o resultado de Apply.
type ApplyResult struct {
	Backup   string // nome do backup feito antes (vazio se pulado)
	Previous string // caminho da cópia do binário anterior (para voltar)
	Restart  bool
}

// Apply faz a atualização: backup, download, conferência do sha256, troca atômica do binário e, se configurado,
// reinício do serviço. Qualquer falha antes da troca deixa o binário como estava.
func (u *Updater) Apply(ctx context.Context, p Plan, o ApplyOptions) (ApplyResult, error) {
	ctx, span := tracer.Start(ctx, "hanko.update.apply", traceAttrs(p))
	defer span.End()
	res, err := u.apply(ctx, p, o)
	if err != nil {
		span.RecordError(err)
	}
	return res, err
}

func (u *Updater) apply(ctx context.Context, p Plan, o ApplyOptions) (ApplyResult, error) {
	var res ApplyResult
	if u.Managed != "" {
		return res, fmt.Errorf("este binário não se atualiza sozinho: %s", u.Managed)
	}
	if _, ok := parseSemver(u.Current); !ok && !o.Force {
		return res, fmt.Errorf("a versão atual %q não é uma release (binário de desenvolvimento): use --force para trocá-lo", u.Current)
	}
	if !p.Newer && !o.Force {
		return res, errors.New("já está na versão mais nova do canal")
	}
	if u.Exe == "" {
		return res, errors.New("caminho do executável desconhecido")
	}
	if !o.SkipBackup {
		if u.Backup == nil {
			return res, errors.New("backup indisponível neste servidor; use --skip-backup se tiver outro backup recente")
		}
		name, err := u.Backup(ctx)
		if err != nil {
			return res, fmt.Errorf("backup antes de atualizar falhou (nada foi alterado): %w", err)
		}
		res.Backup = name
	}

	dir := filepath.Dir(u.Exe)
	tmp, err := os.MkdirTemp(dir, ".hanko-update-")
	if err != nil {
		return res, fmt.Errorf("diretório temporário em %s: %w", dir, err)
	}
	defer os.RemoveAll(tmp) //nolint:errcheck // limpeza best-effort

	// checksums
	var sums bytes.Buffer
	if err := u.Source.Download(ctx, p.Checksums, &sums); err != nil {
		return res, err
	}
	want, err := checksumFor(&sums, p.Archive.Name)
	if err != nil {
		return res, err
	}
	// arquivo
	arch := filepath.Join(tmp, p.Archive.Name)
	af, err := os.OpenFile(arch, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // dentro do diretório temporário
	if err != nil {
		return res, err
	}
	h := sha256.New()
	derr := u.Source.Download(ctx, p.Archive, io.MultiWriter(af, h))
	if cerr := af.Close(); derr == nil {
		derr = cerr
	}
	if derr != nil {
		return res, derr
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return res, fmt.Errorf("sha256 de %s não confere (esperado %s, obtido %s): download descartado", p.Archive.Name, want, got)
	}
	// binário novo
	newBin := filepath.Join(tmp, "hanko.new")
	if err := extractBinary(arch, newBin); err != nil {
		return res, err
	}
	// troca atômica, guardando o anterior
	prev := u.Exe + ".prev"
	if err := copyFile(u.Exe, prev, 0o755); err != nil {
		return res, fmt.Errorf("guardar o binário anterior: %w", err)
	}
	staged := u.Exe + ".new"
	if err := copyFile(newBin, staged, 0o755); err != nil {
		return res, err
	}
	if err := os.Rename(staged, u.Exe); err != nil {
		_ = os.Remove(staged)
		return res, fmt.Errorf("trocar o binário: %w", err)
	}
	res.Previous = prev
	if u.Restart != nil {
		if err := u.Restart(ctx); err != nil {
			return res, fmt.Errorf("binário trocado, mas o reinício do serviço falhou (reinicie à mão): %w", err)
		}
		res.Restart = true
	}
	return res, nil
}

// Rollback devolve o binário anterior (o .prev guardado por Apply).
func (u *Updater) Rollback() error {
	prev := u.Exe + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return fmt.Errorf("não há binário anterior em %s", prev)
	}
	return os.Rename(prev, u.Exe)
}

func checksumFor(r io.Reader, name string) (string, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt não lista %s", name)
}

// extractBinary tira o executável "hanko" do tar.gz (qualquer outro membro é ignorado) para dst.
func extractBinary(archive, dst string) error {
	f, err := os.Open(archive) //nolint:gosec // dentro do diretório temporário
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck // leitura
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("abrir %s: %w", filepath.Base(archive), err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("o arquivo da release não contém o executável hanko")
		}
		if err != nil {
			return fmt.Errorf("ler %s: %w", filepath.Base(archive), err)
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "hanko" {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755) //nolint:gosec // dentro do diretório temporário
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, io.LimitReader(tr, maxDownload+1))
		if e := out.Close(); cerr == nil {
			cerr = e
		}
		return cerr
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // caminhos controlados por Apply
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck // leitura
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm) //nolint:gosec // idem
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}
