package heartbeat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

// contentBackend é o fakeBackend que também entrega o conteúdo dos anexos.
type contentBackend struct {
	*fakeBackend
	files map[domain.ID][]byte
}

func (c contentBackend) ArtifactContent(_ context.Context, _, id domain.ID) (domain.Artifact, []byte, error) {
	b, ok := c.files[id]
	if !ok {
		return domain.Artifact{}, nil, errors.New("sem conteúdo")
	}
	return domain.Artifact{}, b, nil
}

// Os anexos da run vão para .hanko-anexos/<run>/ no diretório de trabalho, o briefing dá o caminho de cada um e a
// limpeza apaga a pasta da run; um anexo ilegível segue só pelo MCP (ADR-0066).
func TestBuildSpecWritesAttachmentsToTheWorkDir(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "acme")
	if err := os.Mkdir(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	h, _ := newTestHB(t, &fakeBackend{}, okAdapter(), &mcpMem{}, Options{WorkspacesRoot: root})
	rc := sampleContext()
	rc.Workspace = &domain.Workspace{Path: ws}
	png := domain.NewID()
	dup := domain.NewID()
	missing := domain.NewID()
	rc.Attachments = []service.AttachmentBrief{
		{ID: png, Name: "foto.png", Mime: "image/png", SizeBytes: 4},
		{ID: dup, Name: "../../foto.png", Mime: "image/png", SizeBytes: 3},
		{ID: missing, Name: "sumiu.pdf", Mime: "application/pdf", SizeBytes: 9},
	}
	be := contentBackend{fakeBackend: &fakeBackend{}, files: map[domain.ID][]byte{png: []byte("\x89PNG"), dup: []byte("dup")}}
	h.attach(be, adapter.Registry{"fake": okAdapter()}, &mcpMem{}, events.NewBroker(), auth.RunTokens{Secret: []byte("0123456789abcdef0123")})
	var notes []string
	emit := func(_ domain.RunEventKind, s string) { notes = append(notes, s) }

	spec, tempCleanup, filesCleanup, err := h.buildRunSpec(context.Background(), rc, "", "", memory.Scope{Workspace: "acme"}, service.RunExtras{}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if tempCleanup != nil {
		t.Fatal("workspace utilizável não é diretório temporário (o ambiente remoto depende disso)")
	}
	if filesCleanup == nil {
		t.Fatal("sem limpeza dos anexos gravados")
	}
	runDir := filepath.Join(spec.Dir, attachmentsDirName, rc.Run.ID.String())
	if b, err := os.ReadFile(filepath.Join(runDir, "foto.png")); err != nil || string(b) != "\x89PNG" {
		t.Fatalf("foto.png: %q %v", b, err)
	}
	// mesmo nome (e com diretórios no nome): fica dentro da pasta, com o começo do id na frente
	if b, err := os.ReadFile(filepath.Join(runDir, dup.String()[:8]+"-foto.png")); err != nil || string(b) != "dup" {
		t.Fatalf("duplicado: %q %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(spec.Dir, attachmentsDirName, ".gitignore")); err != nil || string(b) != "*\n" {
		t.Fatalf(".gitignore: %q %v", b, err)
	}
	want := "`" + attachmentsDirName + "/" + rc.Run.ID.String() + "/foto.png`"
	if !strings.Contains(spec.Prompt, want) || !strings.Contains(spec.Prompt, "ferramenta de leitura de arquivos") {
		t.Fatalf("o briefing não dá o caminho %s:\n%s", want, spec.Prompt)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "sumiu.pdf") {
		t.Fatalf("o anexo ilegível deveria virar aviso: %v", notes)
	}

	filesCleanup()
	if _, err := os.Stat(filepath.Join(spec.Dir, attachmentsDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a pasta de anexos ficou depois da limpeza: %v", err)
	}
	if _, err := os.Stat(spec.Dir); err != nil {
		t.Fatalf("a limpeza não pode apagar o workspace: %v", err)
	}
}

func TestDiskName(t *testing.T) {
	id := domain.NewID()
	used := map[string]bool{}
	for in, want := range map[string]string{"relatório.pdf": "relatório.pdf", `C:\fotos\a.png`: "a.png", ".env": "env", "..": "anexo"} {
		if got := diskName(in, id, used); got != want {
			t.Errorf("diskName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := diskName("a.png", id, used); got != id.String()[:8]+"-a.png" {
		t.Errorf("repetido = %q", got)
	}
}
