package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func hireACP(e *env, cfg string) (domain.Persona, error) {
	return e.svc.HirePersona(context.Background(), domain.HumanActor, e.co.ID,
		service.HirePersonaInput{Name: "Externo", Agent: "acp", AdapterConfig: json.RawMessage(cfg)})
}

func TestHireACPPersonaValidatesAdapterConfig(t *testing.T) {
	e := newEnv(t)

	p, err := hireACP(e, `{"command":"meu-agente","args":["--stdio"],"env":{"MY_HOME":"/x"},"permission":"allow"}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.GetPersona(context.Background(), e.co.ID, p.Slug)
	if err != nil || got.Agent != "acp" {
		t.Fatalf("persona = %+v, err = %v", got, err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.AdapterConfig, &cfg); err != nil || cfg["command"] != "meu-agente" || cfg["permission"] != "allow" {
		t.Fatalf("adapter_config = %s (%v)", got.AdapterConfig, err)
	}

	bad := map[string]string{
		"sem config":              ``,
		"sem command":             `{"args":["x"]}`,
		"command com argumentos":  `{"command":"agente --stdio"}`,
		"chave desconhecida":      `{"command":"a","permision":"allow"}`,
		"permissão inválida":      `{"command":"a","permission":"tudo"}`,
		"env reservado":           `{"command":"a","env":{"HANKO_SECRET":"x"}}`,
		"timeout fora da faixa":   `{"command":"a","init_timeout_sec":9999}`,
		"args com tipo errado":    `{"command":"a","args":"x"}`,
		"config de outro harness": `{"permission_mode":"plan"}`,
	}
	for name, cfg := range bad {
		_, err := hireACP(e, cfg)
		var ve *domain.ValidationError
		if !errors.As(err, &ve) || ve.Field != "adapter_config" || !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: err = %v, quer erro de validação em adapter_config", name, err)
		}
	}
	if _, err := hireACP(e, `{}`); err == nil || !strings.Contains(err.Error(), `"command" is required`) {
		t.Errorf("mensagem amigável: %v", err)
	}

	// outros harnesses não são afetados: o adapter_config deles continua livre
	if _, err := e.svc.HirePersona(context.Background(), domain.HumanActor, e.co.ID,
		service.HirePersonaInput{Name: "Claudia", Agent: "claude", AdapterConfig: json.RawMessage(`{"permission_mode":"plan"}`)}); err != nil {
		t.Errorf("claude com config própria: %v", err)
	}
}

func TestACPCannotBeAnAlternateReviewerHarness(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Autora", Agent: "claude", ReviewerAgent: "acp"})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Field != "reviewer_agent" || !strings.Contains(ve.Reason, "reviewer persona") {
		t.Fatalf("contratação: %v", err)
	}
	a := e.persona(t, "Ana", nil)
	acp := "acp"
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdatePersonaInput{ReviewerAgent: &acp}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("edição: %v", err)
	}
	// um revisor persona que roda em ACP é o caminho
	rev, err := hireACP(e, `{"command":"revisor-acp"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, a.ID, service.UpdatePersonaInput{ReviewerPersona: &rev.ID}); err != nil {
		t.Fatalf("persona revisora em acp: %v", err)
	}
}

func TestACPPersonaExportImport(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.persona(t, "Ana", nil)
	if _, err := hireACP(e, `{"command":"meu-agente","args":["--stdio","--api-key","sk-ant-api03-abcdefghijklmnop","--verbose"],`+
		`"env":{"MY_API_KEY":"s3cr3t","MY_HOME":"/data"},"permission":"read_only"}`); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var exported json.RawMessage
	for _, p := range doc.Personas {
		if p.Agent == "acp" {
			exported, _ = json.Marshal(p.AdapterConfig)
		}
	}
	if exported == nil {
		t.Fatal("persona acp não exportada")
	}
	if s := string(exported); strings.Contains(s, "s3cr3t") || strings.Contains(s, "sk-ant") || strings.Contains(s, "api-key") ||
		!strings.Contains(s, `"command":"meu-agente"`) || !strings.Contains(s, `"MY_HOME":"/data"`) || !strings.Contains(s, `"--verbose"`) {
		t.Fatalf("export sem segredos e com o resto: %s", s)
	}
	adm := mkUser(t, e, "adm@x.com", domain.RoleAdmin)
	res, err := e.svc.ImportCompany(as(adm), domain.HumanActor, doc, service.ImportOptions{Slug: "copia"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	ps, err := e.svc.ListPersonas(as(adm), res.Company.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range ps {
		if p.Agent == "acp" {
			found = true
			var c map[string]any
			if err := json.Unmarshal(p.AdapterConfig, &c); err != nil || c["command"] != "meu-agente" || c["permission"] != "read_only" {
				t.Errorf("adapter_config importado: %s (%v)", p.AdapterConfig, err)
			}
		}
	}
	if !found {
		t.Fatal("persona acp não importada")
	}

	// arquivo editado à mão sem command: a recusa aponta o campo
	for i := range doc.Personas {
		if doc.Personas[i].Agent == "acp" {
			doc.Personas[i].AdapterConfig = map[string]any{"permission": "allow"}
		}
	}
	_, err = e.svc.ImportCompany(as(adm), domain.HumanActor, doc, service.ImportOptions{Slug: "copia2"})
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || !strings.HasSuffix(ve.Field, "adapter_config") || !strings.Contains(ve.Reason, `"command" is required`) {
		t.Fatalf("import sem command: %v", err)
	}
	if _, err := e.svc.GetCompany(ctx, "copia2"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a recusa não pode deixar empresa: %v", err)
	}

	// reviewer_agent acp no arquivo também é recusado
	for i := range doc.Personas {
		if doc.Personas[i].Agent == "acp" {
			doc.Personas[i].AdapterConfig = map[string]any{"command": "x"}
		} else {
			doc.Personas[i].ReviewerAgent = "acp"
		}
	}
	if _, err := e.svc.ImportCompany(as(adm), domain.HumanActor, doc, service.ImportOptions{Slug: "copia3"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("reviewer_agent acp no arquivo: %v", err)
	}
}
