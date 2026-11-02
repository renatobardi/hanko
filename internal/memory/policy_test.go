package memory

import (
	"reflect"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want Class
	}{
		{"memory_briefing", nil, ClassRead},
		{"memory_status", nil, ClassRead},
		{"memory_search_pages", nil, ClassRead},
		{"memory_list_pages", nil, ClassRead},
		{"memory_get_page", nil, ClassRead},
		{"memory_add_note", nil, ClassAdditive},
		{"memory_append_page", nil, ClassAdditive},
		{"memory_delete_page", nil, ClassHeld},
		{"memory_merge_pages", nil, ClassHeld},
		{"memory_rename_page", nil, ClassHeld},
		{"memory_update_page", nil, ClassHeld},
		{"memory_frobnicate", nil, ClassHeld}, // desconhecida: retém
		{"", nil, ClassHeld},
		{"memory_handoff_accept", nil, ClassDeny},
		// catálogo real do ai-memory (2026-10-15)
		{"memory_query", nil, ClassRead},
		{"memory_read_page", nil, ClassRead},
		{"memory_recent", nil, ClassRead},
		{"memory_explore", nil, ClassRead},
		{"memory_lint", nil, ClassRead},
		{"memory_read_session_observations", nil, ClassRead},
		{"memory_forget_sweep", nil, ClassHeld},
		{"memory_consolidate", nil, ClassHeld},
		{"memory_auto_improve", nil, ClassHeld},
		{"memory_handoff_begin", nil, ClassDeny},
		{"memory_handoff_list", nil, ClassDeny},
		{"memory_session_finalize", nil, ClassDeny},
		// write_page: aditivo só na pasta de relatórios da curadoria.
		{"memory_write_page", map[string]any{"path": "hanko/curadoria/2026-01-01"}, ClassAdditive},
		{"memory_write_page", map[string]any{"path": "wiki/arquitetura"}, ClassHeld},
		{"memory_write_page", map[string]any{"path": "hanko/curadoria/../decisions/x"}, ClassHeld},
		{"memory_write_page", map[string]any{"path": "hanko/curadoria/a", "paths": []any{"wiki/x"}}, ClassHeld},
		{"memory_write_page", nil, ClassHeld},
	}
	for _, tc := range cases {
		if got := Classify(tc.tool, tc.args); got != tc.want {
			t.Errorf("Classify(%q, %v) = %s; esperava %s", tc.tool, tc.args, got, tc.want)
		}
	}
}

func TestInferKind(t *testing.T) {
	for tool, want := range map[string]string{
		"memory_delete_page": "delete", "memory_forget": "delete", "memory_merge_pages": "merge",
		"memory_dedupe": "merge", "memory_update_page": "rewrite", "memory_write_page": "rewrite",
	} {
		if got := InferKind(tool); got != want {
			t.Errorf("InferKind(%q) = %q; esperava %q", tool, got, want)
		}
	}
}

func TestTargetsAndProposed(t *testing.T) {
	args := map[string]any{
		"path": "a", "paths": []any{"b", "a", " c "}, "into": "d", "body": "texto novo", "x": 1,
	}
	if got, want := TargetsOf(args), []string{"a", "b", "c", "d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("TargetsOf = %v; esperava %v", got, want)
	}
	if got := ProposedOf(args); got != "texto novo" {
		t.Errorf("ProposedOf = %q", got)
	}
	if ProposedOf(map[string]any{"path": "a"}) != "" {
		t.Error("sem texto novo, ProposedOf deveria ser vazio")
	}
	if got := SortedTargets([]string{"b", "a"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("SortedTargets = %v", got)
	}
}

func TestUnifiedDiff(t *testing.T) {
	if d := UnifiedDiff("a\nb\n", "a\nb\n"); d != "" {
		t.Errorf("textos iguais: diff = %q", d)
	}
	d := UnifiedDiff("um\ndois\ntrês\n", "um\n2\ntrês\n")
	for _, want := range []string{" um\n", "-dois\n", "+2\n", " três\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff sem %q:\n%s", want, d)
		}
	}
	// remoção total
	if d := UnifiedDiff("x\ny\n", ""); !strings.Contains(d, "-x\n") || !strings.Contains(d, "-y\n") {
		t.Errorf("remoção: %q", d)
	}
	// longas sequências iguais são recolhidas
	var old, nw []string
	for i := 0; i < 40; i++ {
		old = append(old, "igual")
		nw = append(nw, "igual")
	}
	nw[20] = "mudou"
	d = UnifiedDiff(strings.Join(old, "\n"), strings.Join(nw, "\n"))
	if !strings.Contains(d, "linhas iguais") || strings.Count(d, "\n") > 20 {
		t.Errorf("diff não recolheu o contexto:\n%s", d)
	}
}

func TestUnifiedDiffEntradaEnormeNaoExplode(t *testing.T) {
	big := strings.Repeat("linha\n", 3000)
	other := strings.Repeat("outra\n", 3000)
	d := UnifiedDiff(big, other)
	if d == "" || len(d) > maxDiffBytes+100 {
		t.Errorf("diff enorme: %d bytes", len(d))
	}
}

func TestToolsDaVersao33SaoRetidas(t *testing.T) {
	for _, tool := range []string{"memory_patch_page", "memory_restore_page"} {
		if c := Classify(tool, map[string]any{"path": "a.md"}); c != ClassHeld {
			t.Errorf("%s = %s; esperava held", tool, c)
		}
		if _, ok := confirmedClass[tool]; !ok {
			t.Errorf("%s deveria estar em confirmedClass", tool)
		}
	}
}

func TestProposedOfMostraAsEdicoesDeUmPatch(t *testing.T) {
	got := ProposedOf(map[string]any{"path": "a.md", "edits": []any{
		map[string]any{"op": "replace_section", "anchor": "## Riscos", "section_sha256": "x", "content": "texto novo"},
		map[string]any{"op": "append", "target": "end_of_page", "content": "fim"},
	}})
	for _, want := range []string{"replace_section", "## Riscos", "texto novo", "append", "fim"} {
		if !strings.Contains(got, want) {
			t.Errorf("ProposedOf = %q; faltou %q", got, want)
		}
	}
}
