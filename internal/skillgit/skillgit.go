// Package skillgit busca skills num repositório git público (ADR-0035): clona raso, sem checkout, num diretório
// temporário com o git do sistema (argv, sem shell), resolve o commit e a árvore e procura na árvore os diretórios
// com SKILL.md. Nenhum arquivo do repositório é extraído para o disco: só os SKILL.md são lidos, do banco de objetos
// do git. Scripts e executáveis nunca são copiados nem executados, só sinalizados. O diretório é apagado no fim.
package skillgit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/renatobardi/hanko/internal/adapter/procrun"
	"github.com/renatobardi/hanko/internal/netguard"
	"github.com/renatobardi/hanko/internal/skillmd"
)

// Padrões.
const (
	DefaultTimeout   = 60 * time.Second
	DefaultMaxBytes  = 50 << 20
	DefaultMaxSkills = 200
	// blobLimit é o maior blob que o clone parcial traz (o servidor que entende o filtro poupa o resto); cobre
	// com folga o maior SKILL.md aceito.
	blobLimit = "64k"
	// maxStderr é quanto da saída de erro do git entra na mensagem.
	maxStderr = 600
	// maxTreeEntries é o teto de arquivos listados na árvore.
	maxTreeEntries = 200_000
)

// Resolver resolve nomes para a checagem de destino.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Options é a política da busca.
type Options struct {
	GitBin    string        // padrão "git" (PATH)
	Timeout   time.Duration // teto da busca inteira; padrão 60 s
	MaxBytes  int64         // teto do clone em disco; padrão 50 MiB
	MaxSkills int           // máximo de skills lidas; padrão 200
	// AllowFile aceita URLs file:// (só testes; nunca exposto ao usuário).
	AllowFile bool
	// AllowPrivate libera hosts internos (HANKO_INTEGRATIONS_ALLOW_PRIVATE).
	AllowPrivate bool
	// Resolver resolve o host para a checagem de destino; nil = net.DefaultResolver.
	Resolver Resolver
}

func (o *Options) defaults() {
	if o.GitBin == "" {
		o.GitBin = "git"
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MaxSkills <= 0 {
		o.MaxSkills = DefaultMaxSkills
	}
}

// Scan diz onde procurar dentro do repositório.
type Scan struct {
	Prefix   string   // só diretórios dentro deste caminho ("" = o repositório todo)
	Excluded []string // caminhos ignorados (o próprio e tudo abaixo dele)
}

// Found é um diretório com SKILL.md.
type Found struct {
	Path        string // diretório relativo à raiz, com "/" ("." = a raiz)
	Raw         string // o SKILL.md (vazio quando Err)
	Name        string // do frontmatter
	Description string
	HasScripts  bool // o diretório tem scripts ou executáveis (ignorados)
	Err         string
}

// Snapshot é o resultado de uma busca.
type Snapshot struct {
	Commit    string
	Tree      string
	Skills    []Found
	Truncated bool // havia mais de MaxSkills
}

var (
	// ErrTooLarge: o clone passou do teto de tamanho.
	ErrTooLarge = errors.New("o repositório passa do tamanho máximo permitido")
	refRe       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	hexRe       = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidateRef confere um branch (ou HEAD): sem espaços, sem "..", sem começar com "-".
func ValidateRef(ref string) error {
	if ref == "HEAD" {
		return nil
	}
	if !refRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".lock") ||
		strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return errors.New("ref inválida: use o nome de um branch ou HEAD")
	}
	return nil
}

// ValidateURL confere a URL de uma fonte: https de host público, sem usuário e senha, sem query nem fragmento,
// na porta padrão. Com AllowFile (testes) também aceita file:// absoluto. Devolve a URL normalizada.
func ValidateURL(raw string, o Options) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2000 {
		return "", errors.New("URL longa demais")
	}
	if o.AllowFile && strings.HasPrefix(raw, "file://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "" || !filepath.IsAbs(u.Path) {
			return "", errors.New("URL file:// inválida")
		}
		return raw, nil
	}
	u, err := netguard.ValidateURL(raw, false, netguard.Policy{AllowPrivate: o.AllowPrivate})
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return "", errors.New("a URL não pode ter query nem fragmento")
	}
	if u.Port() != "" && u.Port() != "443" {
		return "", errors.New("use a porta padrão do https")
	}
	return u.String(), nil
}

// checkHost resolve o host e recusa destinos internos. O git conecta por conta própria, então a checagem é
// feita antes do clone (há uma janela de rebinding de DNS entre ela e a conexão; o ADR-0035 a registra).
func checkHost(ctx context.Context, raw string, o Options) error {
	if o.AllowPrivate || strings.HasPrefix(raw, "file://") {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("URL inválida")
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if netguard.BlockedIP(ip) {
			return netguard.ErrBlocked
		}
		return nil
	}
	var res Resolver = net.DefaultResolver
	if o.Resolver != nil {
		res = o.Resolver
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ips, err := res.LookupNetIP(lctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolver %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolver %s: sem endereço", host)
	}
	for _, ip := range ips {
		if netguard.BlockedIP(ip) {
			return netguard.ErrBlocked
		}
	}
	return nil
}

// fetcher guarda o estado de uma busca.
type fetcher struct {
	o    Options
	tmp  string
	home string
	repo string
	base []string // -c da política, em toda chamada (inclusive a busca preguiçosa de blobs)
}

// Fetch clona url (ref = branch ou HEAD), lê o commit e a árvore e procura as skills. O diretório temporário é
// sempre apagado. O tempo e o tamanho valem para a busca inteira.
func Fetch(ctx context.Context, rawURL, ref string, sc Scan, o Options) (Snapshot, error) {
	o.defaults()
	u, err := ValidateURL(rawURL, o)
	if err != nil {
		return Snapshot{}, err
	}
	if err := ValidateRef(ref); err != nil {
		return Snapshot{}, err
	}
	if _, err := CleanPath(sc.Prefix); err != nil {
		return Snapshot{}, err
	}
	if err := checkHost(ctx, u, o); err != nil {
		return Snapshot{}, err
	}
	tmp, err := os.MkdirTemp("", "hanko-skillsrc-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("diretório temporário: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	f := &fetcher{o: o, tmp: tmp, home: filepath.Join(tmp, "home"), repo: filepath.Join(tmp, "repo")}
	if err := os.Mkdir(f.home, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("diretório temporário: %w", err)
	}
	f.base = []string{
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always",
		"-c", "http.followRedirects=false", "-c", "core.symlinks=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "credential.helper=", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false",
	}
	if o.AllowFile {
		f.base = append(f.base, "-c", "protocol.file.allow=always")
	}

	cctx, cancel := context.WithTimeoutCause(ctx, o.Timeout, errors.New("tempo limite da busca excedido"))
	defer cancel()
	wctx, stopWatch := context.WithCancelCause(cctx)
	defer stopWatch(nil)
	var tooBig atomic.Bool
	go watchSize(wctx, tmp, o.MaxBytes, func() { tooBig.Store(true); stopWatch(ErrTooLarge) })

	snap, err := f.fetch(wctx, u, ref, sc)
	if tooBig.Load() {
		return Snapshot{}, ErrTooLarge
	}
	if err != nil {
		if c := context.Cause(wctx); c != nil && wctx.Err() != nil {
			return Snapshot{}, c
		}
		return Snapshot{}, err
	}
	if n, _ := dirSize(tmp); n > o.MaxBytes {
		return Snapshot{}, ErrTooLarge
	}
	return snap, nil
}

func (f *fetcher) fetch(ctx context.Context, u, ref string, sc Scan) (Snapshot, error) {
	args := []string{"clone", "--quiet", "--no-checkout", "--depth", "1", "--single-branch", "--no-tags", "--filter=blob:limit=" + blobLimit}
	if ref != "HEAD" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", u, f.repo)
	if _, err := f.git(ctx, f.tmp, args...); err != nil {
		return Snapshot{}, err
	}
	return f.snapshot(ctx, "HEAD", sc)
}

// ScanLocal lê as skills de um repositório que já está no disco (o espelho de um workspace, ADR-0054): resolve rev
// (um branch remoto do espelho, como refs/remotes/origin/main) sem clonar e sem tocar na rede. Nada é extraído para
// o disco; scripts são só sinalizados, como em Fetch.
func ScanLocal(ctx context.Context, repoDir, rev string, sc Scan, o Options) (Snapshot, error) {
	o.defaults()
	if rev == "" || strings.HasPrefix(rev, "-") || strings.Contains(rev, "..") || strings.ContainsAny(rev, " \t\n:^~?*[\\") {
		return Snapshot{}, errors.New("revisão inválida")
	}
	if _, err := CleanPath(sc.Prefix); err != nil {
		return Snapshot{}, err
	}
	tmp, err := os.MkdirTemp("", "hanko-skillsrc-*")
	if err != nil {
		return Snapshot{}, fmt.Errorf("diretório temporário: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	f := &fetcher{o: o, tmp: tmp, home: filepath.Join(tmp, "home"), repo: repoDir}
	if err := os.Mkdir(f.home, 0o700); err != nil {
		return Snapshot{}, fmt.Errorf("diretório temporário: %w", err)
	}
	f.base = []string{
		"-c", "protocol.allow=never", "-c", "core.symlinks=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "credential.helper=", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false",
	}
	cctx, cancel := context.WithTimeoutCause(ctx, o.Timeout, errors.New("tempo limite da leitura excedido"))
	defer cancel()
	return f.snapshot(cctx, rev, sc)
}

// snapshot lê o commit, a árvore e as skills de rev no repositório f.repo.
func (f *fetcher) snapshot(ctx context.Context, rev string, sc Scan) (Snapshot, error) {
	cb, err := f.git(ctx, f.repo, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return Snapshot{}, err
	}
	commit := strings.TrimSpace(string(cb))
	if !hexRe.MatchString(commit) {
		return Snapshot{}, fmt.Errorf("commit inesperado: %q", commit)
	}
	tree, err := f.git(ctx, f.repo, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return Snapshot{}, err
	}
	lsOut, err := f.git(ctx, f.repo, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return Snapshot{}, err
	}
	entries, err := ParseTree(lsOut)
	if err != nil {
		return Snapshot{}, err
	}
	found, truncated, err := ScanTree(entries, sc, f.o.MaxSkills)
	if err != nil {
		return Snapshot{}, err
	}
	for i := range found {
		if found[i].Err != "" {
			continue
		}
		raw, err := f.git(ctx, f.repo, "cat-file", "blob", found[i].blob)
		if err != nil {
			if ctx.Err() != nil {
				return Snapshot{}, err
			}
			found[i].Err = "não foi possível ler o SKILL.md"
			continue
		}
		found[i].fill(string(raw))
	}
	out := make([]Found, len(found))
	for i, x := range found {
		out[i] = x.Found
	}
	return Snapshot{Commit: commit, Tree: strings.TrimSpace(string(tree)), Skills: out, Truncated: truncated}, nil
}

// gitEnv é o ambiente mínimo do git: sem configuração do sistema nem do usuário, sem prompt de credencial,
// sem proxy herdado (um proxy contornaria a checagem de destino) e sem LFS.
func gitEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "LANG=C", "LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_ASKPASS=",
		"SSH_ASKPASS=", "GIT_LFS_SKIP_SMUDGE=1", "GIT_PROTOCOL_FROM_USER=0",
	}
}

// maxGitOut é o teto da saída padrão guardada de uma chamada (a listagem da árvore é a maior).
const maxGitOut = 32 << 20

// git executa o git com a política e devolve a saída padrão; um código diferente de zero é erro com o fim da
// saída de erro.
func (f *fetcher) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var out, errb bytes.Buffer
	full := append(append([]string{}, f.base...), args...)
	res, err := procrun.Run(ctx, procrun.Options{Bin: f.o.GitBin, Args: full, Dir: dir, Env: gitEnv(f.home), Grace: time.Second,
		OnStdout: func(l []byte) {
			if out.Len() < maxGitOut {
				out.Write(l)
				out.WriteByte('\n')
			}
		},
		OnStderr: func(l []byte) {
			if errb.Len() < 4096 {
				errb.Write(l)
				errb.WriteByte('\n')
			}
		}})
	if err != nil {
		return nil, fmt.Errorf("git: %w", err)
	}
	if res.Cancelled {
		if c := context.Cause(ctx); c != nil {
			return nil, c
		}
		return nil, errors.New("git: interrompido")
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > maxStderr {
			msg = msg[len(msg)-maxStderr:]
		}
		if msg == "" {
			msg = fmt.Sprintf("código %d", res.ExitCode)
		}
		return nil, fmt.Errorf("git %s: %s", args[0], msg)
	}
	b := out.Bytes()
	// o leitor por linhas acrescenta um \n ao fim; o conteúdo original é preservado no resto
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

func watchSize(ctx context.Context, dir string, limit int64, over func()) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, _ := dirSize(dir); n > limit {
				over()
				return
			}
		}
	}
}

func dirSize(dir string) (int64, error) {
	var n int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // arquivo sumiu no meio do clone: ignora
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n, err
}

// TreeEntry é um arquivo da árvore do commit.
type TreeEntry struct {
	Mode string // 100644, 100755, 120000 (link), 160000 (submódulo)
	Type string // blob, tree, commit
	Hash string
	Path string
}

// ParseTree lê a saída de `git ls-tree -r -z`.
func ParseTree(b []byte) ([]TreeEntry, error) {
	var out []TreeEntry
	for _, rec := range bytes.Split(b, []byte{0}) {
		rec = bytes.TrimLeft(rec, "\n")
		if len(rec) == 0 {
			continue
		}
		meta, p, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			return nil, errors.New("listagem da árvore inesperada")
		}
		fields := strings.Fields(string(meta))
		if len(fields) != 3 {
			return nil, errors.New("listagem da árvore inesperada")
		}
		out = append(out, TreeEntry{Mode: fields[0], Type: fields[1], Hash: fields[2], Path: string(p)})
		if len(out) > maxTreeEntries {
			return nil, errors.New("o repositório tem arquivos demais")
		}
	}
	return out, nil
}

// scriptExt são extensões tratadas como script (nunca importadas; só sinalizadas).
var scriptExt = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true,
	".ts": true, ".rb": true, ".pl": true, ".php": true, ".ps1": true, ".bat": true, ".cmd": true, ".exe": true,
	".bin": true, ".jar": true, ".go": true, ".lua": true, ".so": true, ".dll": true, ".dylib": true,
}

// CleanPath normaliza um caminho relativo do repositório ("" para a raiz). Caminhos absolutos ou que saem da
// raiz são recusados.
func CleanPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if p == "" || p == "." || p == "/" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") {
		return "", errors.New("use um caminho relativo à raiz do repositório")
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") || len(c) > 500 {
		return "", errors.New("caminho inválido")
	}
	return c, nil
}

func under(p, dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// candidate é um Found com o blob do SKILL.md a ler.
type candidate struct {
	Found
	blob string
}

func (c *candidate) fill(raw string) {
	if len(raw) > skillmd.MaxSourceBytes {
		c.Err = fmt.Sprintf("SKILL.md maior que %d KiB", skillmd.MaxSourceBytes>>10)
		return
	}
	doc, err := skillmd.Parse(raw)
	if err != nil {
		c.Err = err.Error()
		return
	}
	c.Raw, c.Name, c.Description = raw, doc.Name, doc.Description
}

// ScanTree acha na árvore os diretórios com SKILL.md (nome sem diferenciar caixa), em ordem de caminho. Links
// simbólicos e submódulos são ignorados; um SKILL.md num deles vira erro na entrada.
func ScanTree(entries []TreeEntry, sc Scan, maxSkills int) ([]candidate, bool, error) {
	prefix, err := CleanPath(sc.Prefix)
	if err != nil {
		return nil, false, err
	}
	var excluded []string
	for _, e := range sc.Excluded {
		if c, err := CleanPath(e); err == nil && c != "" {
			excluded = append(excluded, c)
		}
	}
	isExcluded := func(rel string) bool {
		for _, e := range excluded {
			if under(rel, e) {
				return true
			}
		}
		return false
	}
	type skillFile struct{ hash, mode string }
	skills := map[string]skillFile{} // diretório ("" = raiz) → SKILL.md
	var scripts []string             // diretórios de arquivos que são scripts
	for _, e := range entries {
		dir := path.Dir(e.Path)
		if dir == "." {
			dir = ""
		}
		base := path.Base(e.Path)
		if strings.EqualFold(base, "SKILL.md") && e.Type == "blob" {
			if _, dup := skills[dir]; !dup || base == "SKILL.md" {
				skills[dir] = skillFile{hash: e.Hash, mode: e.Mode}
			}
			continue
		}
		if e.Type != "blob" {
			continue
		}
		if e.Mode == "100755" || scriptExt[strings.ToLower(path.Ext(base))] {
			scripts = append(scripts, dir)
		}
	}
	dirs := make([]string, 0, len(skills))
	for dir := range skills {
		if !under(dir, prefix) || isExcluded(dir) {
			continue
		}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	truncated := false
	if len(dirs) > maxSkills {
		dirs, truncated = dirs[:maxSkills], true
	}
	// o script pertence ao diretório de skill mais fundo que o contém
	hasScripts := map[string]bool{}
	for _, sdir := range scripts {
		best, found := "", false
		for _, dir := range dirs {
			if under(sdir, dir) && (!found || len(dir) > len(best)) {
				best, found = dir, true
			}
		}
		if found {
			hasScripts[best] = true
		}
	}
	out := make([]candidate, 0, len(dirs))
	for _, dir := range dirs {
		sf := skills[dir]
		c := candidate{Found: Found{Path: dir, HasScripts: hasScripts[dir]}, blob: sf.hash}
		if c.Path == "" {
			c.Path = "."
		}
		if sf.mode != "100644" && sf.mode != "100755" {
			c.Err = "SKILL.md não é um arquivo comum (link simbólico?): ignorado"
		}
		out = append(out, c)
	}
	return out, truncated, nil
}
