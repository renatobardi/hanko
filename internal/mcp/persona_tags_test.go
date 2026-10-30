package mcp

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// O agente vê as tags (ADR-0087 item 9): hanko_persona_list devolve e filtra por tag, hanko_whoami mostra as próprias.
// Nenhuma tool escreve tag.
func TestPersonaTagsNoMCP(t *testing.T) {
	e := setup(t, false)
	back := domain.PersonaTag{ID: domain.NewID(), Name: "Backend"}
	now := time.Now()
	arch := domain.PersonaTag{ID: domain.NewID(), Name: "Legado", ArchivedAt: &now}
	e.f.personas[1].Tags = []domain.PersonaTag{back, arch}
	cs, _ := e.connect(t, e.tok)
	defer cs.Close()

	var out personaListOut
	structured(t, call(t, cs, "hanko_persona_list", map[string]any{"tag": "BACKEND, outra"}), &out)
	if len(out.Personas) != 1 || out.Personas[0].Slug != e.f.personas[1].Slug || !slices.Equal(out.Personas[0].Tags, []string{"Backend", "Legado"}) {
		t.Fatalf("filtro por tag: %+v", out.Personas)
	}
	out = personaListOut{}
	structured(t, call(t, cs, "hanko_persona_list", map[string]any{}), &out)
	if len(out.Personas) != 2 || len(out.Personas[0].Tags) != 0 {
		t.Fatalf("sem filtro: %+v", out.Personas)
	}

	var who whoamiOut
	res := call(t, cs, "hanko_whoami", map[string]any{})
	structured(t, res, &who)
	if !slices.Equal(who.Tags, []string{"Backend", "Legado"}) || !strings.Contains(text(res), "suas tags: Backend, Legado") {
		t.Fatalf("whoami: %+v", who)
	}

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if strings.Contains(tl.Name, "tag") {
			t.Errorf("tool de tag %q: o agente só lê tags pelas tools de persona", tl.Name)
		}
	}
}
