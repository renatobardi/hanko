package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

func TestExportOmitsIntegrationSecrets(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://m.example.com/mcp?key=qs_tok_777777", Headers: map[string]string{"Authorization": "Bearer exp_tok_555555"}, PersonaIDs: []domain.ID{p.ID}})
	h := e.webhook(t, service.CreateIntegrationInput{Name: "Hook", URL: srv.URL + "/t", Events: []string{domain.EventTaskCreated}})
	sk := mkSkill(t, e.env, "Alfa", "corpo alfa")
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}

	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	for _, secret := range []string{"exp_tok_555555", "qs_tok_777777", h.RevealedSecret, "/t\""} {
		if secret != "" && strings.Contains(string(b), secret) {
			t.Fatalf("export vaza %q: %s", secret, b)
		}
	}
	if len(doc.Skills) != 1 || doc.Skills[0].Slug != "alfa" || doc.Skills[0].Body != "corpo alfa" || len(doc.Integrations) != 2 {
		t.Fatalf("export: skills=%+v integrations=%+v", doc.Skills, doc.Integrations)
	}
	if len(doc.Personas) != 1 || len(doc.Personas[0].Skills) != 1 || doc.Personas[0].Skills[0] != "alfa" {
		t.Fatalf("skills da persona: %+v", doc.Personas)
	}
}

func TestSkillsIntegrationsRoundTrip(t *testing.T) {
	e, srv := webhookEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Ana", nil)
	e.mcp(t, service.CreateIntegrationInput{Name: "Docs", URL: "https://m.example.com/mcp", Headers: map[string]string{"Authorization": "Bearer rt_tok_999999"}, PersonaIDs: []domain.ID{p.ID}})
	e.webhook(t, service.CreateIntegrationInput{Name: "Hook", URL: srv.URL, Events: []string{domain.EventTaskCreated, domain.EventRunFailed}})
	sk := mkSkill(t, e.env, "Alfa", "corpo alfa")
	mkSkill(t, e.env, "Beta", "corpo beta")
	if err := e.svc.SetPersonaSkill(ctx, domain.HumanActor, e.co.ID, p.ID, sk.ID, true); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// o arquivo passa pelo JSON como passaria pela API
	raw, _ := json.Marshal(doc)
	var in export.Document
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, in, service.ImportOptions{Slug: "copia", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts["skills"] != 2 || res.Counts["integrations"] != 2 {
		t.Fatalf("counts: %v", res.Counts)
	}
	cid := res.Company.ID
	skills, err := e.svc.ListSkills(ctx, cid)
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills importadas: %d %v", len(skills), err)
	}
	pers, err := e.svc.ListPersonas(ctx, cid)
	if err != nil || len(pers) != 1 {
		t.Fatal(err, len(pers))
	}
	ps, err := e.svc.ListPersonaSkills(ctx, cid, pers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	on := 0
	for _, x := range ps {
		if x.Enabled {
			on++
			if x.Slug != "alfa" {
				t.Errorf("skill habilitada: %s", x.Slug)
			}
		}
	}
	if on != 1 {
		t.Errorf("skills habilitadas = %d, quer 1", on)
	}
	ints, err := e.svc.ListIntegrations(ctx, cid)
	if err != nil || len(ints) != 2 {
		t.Fatalf("integrações importadas: %d %v", len(ints), err)
	}
	for _, v := range ints {
		// mesmo com Active, integração importada entra desligada e sem segredo
		if v.Enabled || v.SecretSet {
			t.Errorf("%s: enabled=%v secret_set=%v", v.Slug, v.Enabled, v.SecretSet)
		}
	}
	links, err := e.svc.ListPersonaIntegrations(ctx, cid, pers[0].ID)
	if err != nil || len(links) != 1 || !links[0].Linked {
		t.Fatalf("ligação MCP: %+v %v", links, err)
	}
}

func TestImportRejectsBadSkillsAndIntegrations(t *testing.T) {
	e := newIntEnv(t, service.IntegrationsConfig{})
	ctx := context.Background()
	base := func() export.Document {
		d, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	cases := map[string]func(*export.Document){
		"skill duplicada": func(d *export.Document) {
			d.Skills = []export.Skill{{Slug: "a", Name: "A", Body: "x"}, {Slug: "a", Name: "B", Body: "y"}}
		},
		"slug reservado":  func(d *export.Document) { d.Skills = []export.Skill{{Slug: "nova", Name: "A", Body: "x"}} },
		"skill sem corpo": func(d *export.Document) { d.Skills = []export.Skill{{Slug: "a", Name: "A"}} },
		"webhook http": func(d *export.Document) {
			d.Integrations = []export.Integration{{Kind: "webhook", Slug: "w", Name: "W", URL: "http://x.example.com", Events: []string{"task.created"}}}
		},
		"webhook evento inválido": func(d *export.Document) {
			d.Integrations = []export.Integration{{Kind: "webhook", Slug: "w", Name: "W", URL: "https://x.example.com", Events: []string{"nope"}}}
		},
		"mcp com slug que o Hanko usa para si": func(d *export.Document) {
			d.Integrations = []export.Integration{{Kind: "mcp", Slug: "hanko", Name: "Falso", URL: "https://mcp.example.com/mcp"}}
		},
		"mcp stdio com o interruptor do operador desligado": func(d *export.Document) {
			d.Integrations = []export.Integration{{Kind: "mcp", Slug: "local", Name: "Local", Transport: "stdio", Command: "meu-servidor"}}
		},
		"tipo desconhecido": func(d *export.Document) { d.Integrations = []export.Integration{{Kind: "ftp", Slug: "w", Name: "W"}} },
		"persona de skill ausente": func(d *export.Document) {
			d.Skills = []export.Skill{{Slug: "a", Name: "A", Body: "x"}}
			d.Personas = []export.Persona{{Slug: "p", Name: "P", Agent: "fake", Status: "idle", Skills: []string{"zzz"}}}
		},
	}
	for name, mod := range cases {
		t.Run(name, func(t *testing.T) {
			d := base()
			mod(&d)
			if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, d, service.ImportOptions{Slug: "ruim"}); err == nil {
				t.Fatal("importou um arquivo inválido")
			}
		})
	}
}
