//go:build integration

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hanko attach/attachment, persona history/restore e /new contra o servidor de verdade (ADR-0052).
func TestCLIAnexos(t *testing.T) {
	_, _, env := realServer(t)
	dir := t.TempDir()
	spec := filepath.Join(dir, "spec.txt")
	if err := os.WriteFile(spec, []byte("conteúdo do anexo"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOK(t, env, "task", "create", "Com anexo")

	out := mustOK(t, env, "attach", spec, "--task", "HNK-1")
	if !strings.Contains(out, "Anexo spec.txt anexado") {
		t.Fatalf("attach: %q", out)
	}
	// o mesmo nome ganha sufixo
	if out = mustOK(t, env, "attach", spec, "--task", "HNK-1"); !strings.Contains(out, "Anexo spec (2).txt anexado") {
		t.Fatalf("attach repetido: %q", out)
	}
	// entrada padrão exige --name; alvo é obrigatório e exclusivo
	mustFail(t, env, 2, "informe --name", "attach", "-", "--task", "HNK-1")
	mustFail(t, env, 2, "informe --task OU --conversation", "attach", spec)
	mustFail(t, env, 2, "informe --task OU --conversation", "attach", spec, "--task", "HNK-1", "--conversation", "x")

	var as []struct {
		ID, Name, Class string
	}
	if err := json.Unmarshal([]byte(mustOK(t, env, "--json", "attachment", "list", "--task", "HNK-1")), &as); err != nil || len(as) != 2 {
		t.Fatalf("list --json: %+v %v", as, err)
	}
	if as[0].Class != "attachment" {
		t.Errorf("class: %+v", as[0])
	}
	out = mustOK(t, env, "attachment", "list", "--task", "HNK-1")
	if !strings.Contains(out, "spec.txt") || !strings.Contains(out, "spec (2).txt") {
		t.Errorf("list:\n%s", out)
	}
	mustFail(t, env, 2, "informe --task", "attachment", "list")
	// os artefatos não listam anexos
	if out = mustOK(t, env, "artifact", "list", "--task", "HNK-1"); strings.Contains(out, "spec") {
		t.Errorf("artifact list não deveria mostrar anexos:\n%s", out)
	}

	if out = mustOK(t, env, "attachment", "get", as[0].ID); out != "conteúdo do anexo" {
		t.Errorf("get: %q", out)
	}
	dest := filepath.Join(dir, "baixado.txt")
	mustOK(t, env, "attachment", "get", as[0].ID, "-o", dest)
	if b, err := os.ReadFile(dest); err != nil || string(b) != "conteúdo do anexo" {
		t.Errorf("baixado: %q %v", b, err)
	}
	if out = mustOK(t, env, "attachment", "rm", as[0].ID); !strings.Contains(out, "apagado") {
		t.Errorf("rm: %q", out)
	}
	if err := json.Unmarshal([]byte(mustOK(t, env, "--json", "attachment", "list", "--task", "HNK-1")), &as); err != nil || len(as) != 1 {
		t.Fatalf("depois de apagar: %+v %v", as, err)
	}
}

func TestCLIRevisoesDeInstrucoes(t *testing.T) {
	_, _, env := realServer(t)
	dir := t.TempDir()
	mustOK(t, env, "persona", "hire", "Ada", "--agent", "fake", "--instructions", "Seja breve.")
	novo := filepath.Join(dir, "novo.md")
	if err := os.WriteFile(novo, []byte("Seja detalhista."), 0o600); err != nil {
		t.Fatal(err)
	}
	mustOK(t, env, "persona", "update", "ada", "--instructions-file", novo, "--reason", "ficou curto")
	mustFail(t, env, 2, "não combinam", "persona", "update", "ada", "--instructions", "x", "--instructions-file", novo)

	out := mustOK(t, env, "persona", "history", "ada")
	if !strings.Contains(out, "2*") || !strings.Contains(out, "edit") || !strings.Contains(out, "hire") || !strings.Contains(out, "ficou curto") {
		t.Fatalf("history:\n%s", out)
	}
	out = mustOK(t, env, "persona", "history", "ada", "--show", "2", "--against", "1")
	if !strings.Contains(out, "+ Seja detalhista.") || !strings.Contains(out, "- Seja breve.") {
		t.Fatalf("diff:\n%s", out)
	}
	out = mustOK(t, env, "persona", "history", "ada", "--show", "1")
	if !strings.Contains(out, "Seja breve.") {
		t.Fatalf("show:\n%s", out)
	}
	out = mustOK(t, env, "persona", "restore", "ada", "1", "--reason", "voltar")
	if !strings.Contains(out, "restauradas da revisão 1: agora na revisão 3") {
		t.Fatalf("restore: %q", out)
	}
	out = mustOK(t, env, "persona", "show", "ada")
	if !strings.Contains(out, "Seja breve.") {
		t.Fatalf("show persona:\n%s", out)
	}
	mustFail(t, env, 2, "revisão inválida", "persona", "restore", "ada", "zero")
}

func TestCLIChatNew(t *testing.T) {
	_, _, env := realServer(t)
	mustOK(t, env, "persona", "hire", "Ada", "--agent", "fake")
	var first struct {
		ConversationID string `json:"conversation_id"`
	}
	if err := json.Unmarshal([]byte(mustOK(t, env, "--json", "chat", "ada", "oi", "--no-wait")), &first); err != nil || first.ConversationID == "" {
		t.Fatalf("abrir: %+v %v", first, err)
	}
	out := mustOK(t, env, "chat", "ada", "/new", "--conversation", first.ConversationID)
	if !strings.Contains(out, "Contexto reiniciado") {
		t.Fatalf("/new: %q", out)
	}
}
