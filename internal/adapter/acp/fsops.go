package acp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/renatobardi/hanko/internal/domain"
)

// Limits of the fs/* methods.
const (
	maxReadBytes  = 2 << 20
	maxWriteBytes = 8 << 20
)

// errOutside is returned for any path that is not inside the run directory (lexically or
// through a symlink).
var errOutside = errors.New("path is outside the run directory")

// fileRoot confines the agent's file access to the run directory. The kernel-enforced os.Root
// is the actual barrier (it refuses ".." and symlinks that leave the tree, including races);
// the lexical check in rel only turns the common mistakes into a clear message.
type fileRoot struct {
	abs, real string
	root      *os.Root
}

func newFileRoot(dir string) (*fileRoot, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve run directory: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve run directory: %w", err)
	}
	r, err := os.OpenRoot(real)
	if err != nil {
		return nil, fmt.Errorf("open run directory: %w", err)
	}
	return &fileRoot{abs: abs, real: real, root: r}, nil
}

func (f *fileRoot) Close() { _ = f.root.Close() }

// rel maps an absolute path from the agent to a path relative to the root.
func (f *fileRoot) rel(path string) (string, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return "", errors.New("path is empty or invalid")
	}
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	p := filepath.Clean(path)
	for _, base := range []string{f.abs, f.real} {
		r, err := filepath.Rel(base, p)
		if err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return r, nil
		}
	}
	return "", errOutside
}

// Read returns the text of a file, optionally a window of lines (1-based line, limit lines).
func (f *fileRoot) Read(path string, line, limit int) (string, error) {
	rel, err := f.rel(path)
	if err != nil {
		return "", err
	}
	// A FIFO or device would block the open (and the agent's read loop with it): look before opening, and
	// open non-blocking so that a swap between the two cannot hang either.
	if err := f.requireRegular(rel); err != nil {
		return "", err
	}
	file, err := f.root.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck // read-only handle
	st, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errNotRegular
	}
	data, err := io.ReadAll(io.LimitReader(file, maxReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxReadBytes {
		return "", fmt.Errorf("file is larger than the %d byte limit", maxReadBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", errors.New("file is binary, not text")
	}
	text := string(data)
	if line > 1 || limit > 0 {
		lines := strings.SplitAfter(text, "\n")
		start := 0
		if line > 1 {
			start = line - 1
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if limit > 0 && start+limit < end {
			end = start + limit
		}
		text = strings.Join(lines[start:end], "")
	}
	return text, nil
}

// Write replaces (or creates) a file, creating missing directories under the root.
func (f *fileRoot) Write(path, content string) (string, error) {
	rel, err := f.rel(path)
	if err != nil {
		return "", err
	}
	if rel == "." {
		return "", errors.New("path is the run directory itself")
	}
	if len(content) > maxWriteBytes {
		return "", fmt.Errorf("content is larger than the %d byte limit", maxWriteBytes)
	}
	if d := filepath.Dir(rel); d != "." {
		if err := f.root.MkdirAll(d, 0o755); err != nil { //nolint:gosec // workspace directories follow the umask like any tool that edits the tree
			return "", err
		}
	}
	if err := f.requireRegular(rel); err != nil {
		return "", err
	}
	// Non-blocking and no O_TRUNC at open: truncating happens only after fstat says it is a regular file.
	file, err := f.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|syscall.O_NONBLOCK, 0o644) //nolint:gosec // source files are world-readable like anywhere else in the workspace
	if err != nil {
		return "", err
	}
	defer file.Close() //nolint:errcheck // the write error below is the one that matters
	st, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errNotRegular
	}
	if err := file.Truncate(0); err != nil {
		return "", err
	}
	if _, err := file.WriteString(content); err != nil {
		return "", err
	}
	return rel, nil
}

var errNotRegular = errors.New("not a regular file")

// requireRegular refuses an existing path that is not a regular file (FIFO, device, socket, directory).
// A missing path is fine (Write creates it; Read reports not-found on open). Symlinks are followed by
// os.Root only inside the tree.
func (f *fileRoot) requireRegular(rel string) error {
	st, err := f.root.Stat(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !st.Mode().IsRegular() {
		return errNotRegular
	}
	return nil
}

// ---- fs/* handlers --------------------------------------------------------------------------

type readParams struct {
	Path  string `json:"path"`
	Line  *int   `json:"line"`
	Limit *int   `json:"limit"`
}

type writeParams struct {
	Path    string  `json:"path"`
	Content *string `json:"content"`
}

func (s *session) readTextFile(raw json.RawMessage) (any, *rpcError) {
	var p readParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid fs/read_text_file params: " + err.Error()}
	}
	line, limit := 0, 0
	if p.Line != nil {
		line = *p.Line
	}
	if p.Limit != nil {
		limit = *p.Limit
	}
	text, err := s.files.Read(p.Path, line, limit)
	if err != nil {
		return nil, s.fsError("leitura", p.Path, err)
	}
	return map[string]string{"content": text}, nil
}

func (s *session) writeTextFile(raw json.RawMessage) (any, *rpcError) {
	var p writeParams
	if err := json.Unmarshal(raw, &p); err != nil || p.Content == nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid fs/write_text_file params: path and content are required"}
	}
	switch {
	case s.readOnly:
		s.emit(domain.EventSystem, "escrita recusada (execução somente leitura): "+p.Path, nil)
		return nil, &rpcError{Code: codeForbidden, Message: "this run is read-only: writing files is not allowed"}
	case s.policy != PermAllow:
		s.emit(domain.EventSystem, fmt.Sprintf("escrita recusada (política %s): %s", s.policy, p.Path), nil)
		return nil, &rpcError{Code: codeForbidden, Message: fmt.Sprintf("writing files is not allowed by the permission policy %q", s.policy)}
	}
	rel, err := s.files.Write(p.Path, *p.Content)
	if err != nil {
		return nil, s.fsError("escrita", p.Path, err)
	}
	s.emit(domain.EventSystem, fmt.Sprintf("arquivo escrito pelo agente: %s (%d bytes)", rel, len(*p.Content)), nil)
	return struct{}{}, nil
}

// codeForbidden is the implementation-defined server error used for refused operations.
const codeForbidden = -32000

// codeNotFound is the ACP "resource not found" error.
const codeNotFound = -32002

func (s *session) fsError(op, path string, err error) *rpcError {
	switch {
	case errors.Is(err, errOutside) || strings.Contains(err.Error(), "escapes from parent"):
		s.emit(domain.EventSystem, fmt.Sprintf("acesso negado (%s fora do diretório da execução): %s", op, path), nil)
		return &rpcError{Code: codeInvalidParams, Message: "path is outside the run directory"}
	case errors.Is(err, fs.ErrNotExist):
		return &rpcError{Code: codeNotFound, Message: "file not found"}
	}
	return &rpcError{Code: codeInvalidParams, Message: err.Error()}
}
