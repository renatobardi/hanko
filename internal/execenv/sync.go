package execenv

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Tetos da sincronização do workspace com o alvo (nos dois sentidos).
const (
	MaxSyncBytes   int64 = 2 << 30
	MaxSyncEntries       = 200_000
)

// ErrSyncTooBig: o workspace (ou o que voltou do alvo) passou dos tetos.
var ErrSyncTooBig = fmt.Errorf("a cópia do workspace passou do limite (%d GiB ou %d arquivos)", MaxSyncBytes>>30, MaxSyncEntries)

// tree é um diretório local que vai para o alvo sob um prefixo do arquivo tar.
type tree struct {
	dir    string
	prefix string // "work" ou ".hanko"
}

// writeTar escreve as árvores no tar (formato PAX) e devolve o manifesto das entradas da árvore "work"
// (caminhos relativos com "/"). Só diretórios, arquivos regulares e links simbólicos entram; o .git vai junto.
func writeTar(w io.Writer, trees []tree) (map[string]bool, error) {
	tw := tar.NewWriter(w)
	manifest := map[string]bool{}
	var total int64
	entries := 0
	for _, t := range trees {
		if t.dir == "" {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: t.prefix + "/", Mode: 0o755, ModTime: time.Now(), Format: tar.FormatPAX}); err != nil {
			return nil, err
		}
		err := filepath.WalkDir(t.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(t.dir, p)
			if err != nil || rel == "." {
				return err
			}
			rel = filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := info.Mode()
			link := ""
			switch {
			case mode.IsDir(), mode.IsRegular():
			case mode&fs.ModeSymlink != 0:
				if link, err = os.Readlink(p); err != nil {
					return err
				}
			default:
				return nil // socket, fifo, dispositivo: não viajam
			}
			entries++
			if entries > MaxSyncEntries {
				return ErrSyncTooBig
			}
			h, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			h.Name = t.prefix + "/" + rel
			if mode.IsDir() {
				h.Name += "/"
			}
			h.Uid, h.Gid, h.Uname, h.Gname = 0, 0, "", ""
			h.Format = tar.FormatPAX
			if t.prefix == "work" {
				manifest[rel] = true
			}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if mode.IsRegular() {
				total += info.Size()
				if total > MaxSyncBytes {
					return ErrSyncTooBig
				}
				f, err := os.Open(p) //nolint:gosec // caminho vem do WalkDir do diretório da run
				if err != nil {
					return err
				}
				_, cerr := io.CopyN(tw, f, info.Size())
				_ = f.Close()
				if cerr != nil {
					return cerr
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return manifest, tw.Close()
}

// extractTar lê o tar que veio do alvo (o conteúdo de work/) para dentro de dir, confinado por os.Root: nenhum
// caminho absoluto, "..", nem link simbólico sai de dir. Depois apaga o que estava no manifesto da subida e
// não voltou (o agente apagou no alvo). Arquivos criados localmente durante a run (fora do manifesto) ficam.
func extractTar(r io.Reader, dir string, manifest map[string]bool) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("abrir diretório local: %w", err)
	}
	defer root.Close()
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	var total int64
	entries := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("ler o tar do ambiente: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(filepath.ToSlash(h.Name), "./"))
		if name == "." || name == "" {
			continue
		}
		if !fs.ValidPath(name) {
			return fmt.Errorf("caminho recusado no tar do ambiente: %q", h.Name)
		}
		entries++
		if entries > MaxSyncEntries {
			return ErrSyncTooBig
		}
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil && path.Dir(name) != "." {
			return fmt.Errorf("criar %s: %w", path.Dir(name), err)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := replaceNonDir(root, name); err != nil {
				return err
			}
			if err := root.MkdirAll(name, 0o755); err != nil {
				return fmt.Errorf("criar %s: %w", name, err)
			}
			_ = root.Chmod(name, modeOf(h)|0o700)
		case tar.TypeReg:
			total += h.Size
			if total > MaxSyncBytes {
				return ErrSyncTooBig
			}
			if err := removeIfNotRegular(root, name); err != nil {
				return err
			}
			perm := modeOf(h) | 0o600
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
			if err != nil {
				return fmt.Errorf("gravar %s: %w", name, err)
			}
			_, cerr := io.CopyN(f, tr, h.Size)
			if err := f.Close(); cerr == nil {
				cerr = err
			}
			if cerr != nil {
				return fmt.Errorf("gravar %s: %w", name, cerr)
			}
			_ = root.Chmod(name, perm)
			_ = root.Chtimes(name, h.ModTime, h.ModTime)
		case tar.TypeSymlink:
			if err := root.RemoveAll(name); err != nil {
				return fmt.Errorf("trocar %s: %w", name, err)
			}
			if err := root.Symlink(h.Linkname, name); err != nil {
				return fmt.Errorf("criar link %s: %w", name, err)
			}
		default:
			continue // hard links e especiais não voltam
		}
		seen[name] = true
	}
	// Apaga, dos mais fundos para os mais rasos, o que subiu e não voltou.
	var gone []string
	for p := range manifest {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return strings.Count(gone[i], "/") > strings.Count(gone[j], "/") })
	for _, p := range gone {
		if err := root.RemoveAll(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("apagar %s: %w", p, err)
		}
	}
	return nil
}

// replaceNonDir apaga o que existir em name e não for diretório (arquivo ou link virou pasta no alvo).
func replaceNonDir(root *os.Root, name string) error {
	st, err := root.Lstat(name)
	if err != nil || st.IsDir() {
		return nil
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("trocar %s: %w", name, err)
	}
	return nil
}

// removeIfNotRegular apaga um diretório ou link que ocupe o lugar de um arquivo regular.
func removeIfNotRegular(root *os.Root, name string) error {
	st, err := root.Lstat(name)
	if err != nil || st.Mode().IsRegular() {
		return nil
	}
	if err := root.RemoveAll(name); err != nil {
		return fmt.Errorf("trocar %s: %w", name, err)
	}
	return nil
}

// modeOf são os bits de permissão do cabeçalho (sem setuid, setgid nem sticky).
func modeOf(h *tar.Header) fs.FileMode {
	return fs.FileMode(uint32(h.Mode & 0o777)) //nolint:gosec // mascarado em 9 bits antes da conversão
}
