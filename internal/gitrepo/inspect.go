package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// StatusEntry é um arquivo alterado no worktree.
type StatusEntry struct {
	XY   string `json:"xy"` // duas letras do porcelain: índice e árvore ("??" = não rastreado)
	Path string `json:"path"`
}

// Status é o retrato do worktree.
type Status struct {
	Branch  string        `json:"branch"` // "" = HEAD solto
	Head    string        `json:"head"`
	Entries []StatusEntry `json:"entries"`
}

// Dirty diz se há alterações não commitadas.
func (s Status) Dirty() bool { return len(s.Entries) > 0 }

// maxStatusEntries é o teto de arquivos listados.
const maxStatusEntries = 2000

// WorktreeStatus lê o status do worktree (alterações não commitadas, inclusive arquivos novos).
func (g *Git) WorktreeStatus(ctx context.Context, wt string) (Status, error) {
	var st Status
	br, err := g.CurrentBranch(ctx, wt)
	if err != nil {
		return st, err
	}
	st.Branch = br
	head, err := g.RevParse(ctx, wt, "HEAD")
	if err != nil {
		return st, err
	}
	st.Head = head
	out, err := g.run(ctx, call{dir: wt, args: []string{"status", "--porcelain=v1", "-z", "--untracked-files=normal"}})
	if err != nil {
		return st, err
	}
	st.Entries = []StatusEntry{}
	recs := strings.Split(string(out), "\x00")
	for i := 0; i < len(recs); i++ {
		r := strings.TrimLeft(recs[i], "\n")
		if len(r) < 4 {
			continue
		}
		e := StatusEntry{XY: r[:2], Path: r[3:]}
		if e.XY[0] == 'R' || e.XY[0] == 'C' { // renomeado: o próximo registro é a origem
			i++
		}
		st.Entries = append(st.Entries, e)
		if len(st.Entries) >= maxStatusEntries {
			break
		}
	}
	return st, nil
}

// Patch é um diff devolvido com o teto de tamanho.
type Patch struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	Stat      string `json:"stat,omitempty"`
}

// DiffWorktree compara a base com o conteúdo atual do worktree (commits e alterações ainda não commitadas dos
// arquivos rastreados).
func (g *Git) DiffWorktree(ctx context.Context, wt, base string, max int) (Patch, error) {
	return g.diff(ctx, wt, []string{base}, max)
}

// DiffCommits compara dois commits (worktree já removido: a branch mora no espelho).
func (g *Git) DiffCommits(ctx context.Context, mirror, from, to string, max int) (Patch, error) {
	return g.diff(ctx, mirror, []string{from, to}, max)
}

func (g *Git) diff(ctx context.Context, dir string, revs []string, max int) (Patch, error) {
	for _, r := range revs {
		if r == "" || strings.HasPrefix(r, "-") {
			return Patch{}, errors.New("revisão inválida")
		}
	}
	common := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--submodule=short"}
	stat, err := g.run(ctx, call{dir: dir, args: append(append(append([]string{}, common...), "--stat", "--stat-width=100"), append(revs, "--")...)})
	if err != nil {
		return Patch{}, err
	}
	out, err := g.run(ctx, call{dir: dir, args: append(append([]string{}, common...), append(revs, "--")...)})
	if err != nil {
		return Patch{}, err
	}
	p := Patch{Stat: string(stat)}
	if max > 0 && len(out) > max {
		out = out[:max]
		p.Truncated = true
	}
	p.Text = strings.ToValidUTF8(string(out), "�")
	return p, nil
}

// Commit é uma linha do log.
type Commit struct {
	SHA     string    `json:"sha"`
	Author  string    `json:"author"`
	At      time.Time `json:"at"`
	Subject string    `json:"subject"`
}

// Log lista até n commits de from..to (from vazio = todo o histórico de to), do mais novo para o mais antigo.
func (g *Git) Log(ctx context.Context, dir, from, to string, n int) ([]Commit, error) {
	if to == "" || strings.HasPrefix(to, "-") || strings.HasPrefix(from, "-") {
		return nil, errors.New("revisão inválida")
	}
	if n <= 0 || n > 500 {
		n = 100
	}
	rng := to
	if from != "" {
		rng = from + ".." + to
	}
	out, err := g.run(ctx, call{dir: dir, args: []string{"log", "--no-color", "--no-show-signature", fmt.Sprintf("--max-count=%d", n),
		"--format=%H%x1f%an%x1f%aI%x1f%s%x1e", rng, "--"}})
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, rec := range strings.Split(string(out), "\x1e") {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) != 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[2])
		commits = append(commits, Commit{SHA: f[0], Author: f[1], At: at, Subject: f[3]})
	}
	return commits, nil
}

// Entry é um item de uma pasta do repositório.
type Entry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
}

// File é o conteúdo de um arquivo mostrado no browser read-only.
type File struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

// ListDir lista a pasta rel do worktree em root, só leitura. Links simbólicos aparecem como arquivo sem seguir o
// destino; ".git" nunca aparece.
func ListDir(root, rel string) ([]Entry, error) {
	dir, err := safeJoin(root, rel)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("ler a pasta: %w", err)
	}
	out := make([]Entry, 0, len(ents))
	for _, e := range ents {
		if e.Name() == ".git" {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		out = append(out, Entry{Name: e.Name(), Dir: e.IsDir() && info.Mode()&os.ModeSymlink == 0, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ReadFile lê o arquivo rel do worktree em root, só leitura, até max bytes. Link simbólico que sai do worktree
// é recusado; binário não devolve conteúdo.
func ReadFile(root, rel string, max int) (File, error) {
	p, err := safeJoin(root, rel)
	if err != nil {
		return File{}, err
	}
	f, err := os.Open(p) //nolint:gosec // p passou por safeJoin (dentro de root, symlinks resolvidos)
	if err != nil {
		return File{}, fmt.Errorf("abrir o arquivo: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	if st.IsDir() {
		return File{}, errors.New("é uma pasta")
	}
	buf, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return File{}, err
	}
	out := File{Path: rel, Size: st.Size()}
	if len(buf) > max {
		buf, out.Truncated = buf[:max], true
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		out.Binary = true
		return out, nil
	}
	for i := 0; out.Truncated && i < utf8.UTFMax && len(buf) > 0 && !utf8.Valid(buf); i++ { // não corta no meio de um caractere
		buf = buf[:len(buf)-1]
	}
	if !utf8.Valid(buf) {
		out.Binary = true
		return out, nil
	}
	out.Content = string(buf)
	return out, nil
}

// safeJoin junta rel a root garantindo que o resultado, com os symlinks resolvidos, continua dentro de root e fora
// de .git.
func safeJoin(root, rel string) (string, error) {
	rel = filepath.ToSlash(rel)
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || strings.EqualFold(seg, ".git") {
			return "", errors.New("caminho inválido")
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("worktree indisponível: %w", err)
	}
	full, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("caminho não encontrado: %w", err)
	}
	r, err := filepath.Rel(realRoot, full)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", errors.New("o caminho sai do repositório")
	}
	for _, seg := range strings.Split(filepath.ToSlash(r), "/") {
		if strings.EqualFold(seg, ".git") {
			return "", errors.New("caminho inválido")
		}
	}
	return full, nil
}

// ListTree lista a pasta rel de um commit (worktree removido: a branch mora no espelho).
func (g *Git) ListTree(ctx context.Context, mirror, rev, rel string) ([]Entry, error) {
	if rev == "" || strings.HasPrefix(rev, "-") {
		return nil, errors.New("revisão inválida")
	}
	spec := rev
	args := []string{"ls-tree", "-z", "--long", "--end-of-options", spec}
	if rel != "" {
		args = []string{"ls-tree", "-z", "--long", "--end-of-options", spec, rel + "/"}
	}
	out, err := g.run(ctx, call{dir: mirror, args: args})
	if err != nil {
		return nil, err
	}
	ents := []Entry{}
	for _, rec := range strings.Split(string(out), "\x00") {
		rec = strings.TrimLeft(rec, "\n")
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 4 {
			continue
		}
		var size int64
		_, _ = fmt.Sscanf(f[3], "%d", &size)
		base := name[strings.LastIndex(name, "/")+1:]
		if base == ".git" {
			continue
		}
		ents = append(ents, Entry{Name: base, Dir: f[1] == "tree", Size: size})
	}
	sort.Slice(ents, func(i, j int) bool {
		if ents[i].Dir != ents[j].Dir {
			return ents[i].Dir
		}
		return ents[i].Name < ents[j].Name
	})
	return ents, nil
}

// ShowFile lê o arquivo rel de um commit.
func (g *Git) ShowFile(ctx context.Context, mirror, rev, rel string, max int) (File, error) {
	if rev == "" || strings.HasPrefix(rev, "-") || rel == "" {
		return File{}, errors.New("revisão ou caminho inválido")
	}
	spec := rev + ":" + rel
	out, err := g.run(ctx, call{dir: mirror, args: []string{"cat-file", "blob", spec}})
	if err != nil {
		return File{}, err
	}
	sz, err := g.run(ctx, call{dir: mirror, args: []string{"cat-file", "-s", spec}})
	if err != nil {
		return File{}, err
	}
	var size int64
	_, _ = fmt.Sscanf(strings.TrimSpace(string(sz)), "%d", &size)
	if int64(len(out))+1 == size { // o leitor por linhas tira o último \n
		out = append(out, '\n')
	}
	f := File{Path: rel, Size: size}
	if len(out) > max {
		out, f.Truncated = out[:max], true
	}
	if bytes.IndexByte(out, 0) >= 0 || !utf8.Valid(out) && !f.Truncated {
		f.Binary = true
		return f, nil
	}
	f.Content = strings.ToValidUTF8(string(out), "")
	return f, nil
}
