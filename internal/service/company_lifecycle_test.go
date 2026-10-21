package service_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/service"
)

func strp(s string) *string { return &s }

func TestUpdateCompany(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	c, err := e.svc.UpdateCompany(ctx, domain.HumanActor, e.co.ID, service.UpdateCompanyInput{
		Name: strp("Acme Brasil"), Timezone: strp("UTC"), Description: strp("  time de produto  "), TaskPrefix: strp("ac")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Acme Brasil" || c.Timezone != "UTC" || c.Description != "time de produto" || c.TaskPrefix != "AC" || c.Slug != "acme-corp" {
		t.Fatalf("empresa: %+v", c)
	}

	for name, in := range map[string]service.UpdateCompanyInput{
		"nome vazio":   {Name: strp("  ")},
		"fuso ruim":    {Timezone: strp("Mars/Olympus")},
		"prefixo ruim": {TaskPrefix: strp("1x")},
		"descrição":    {Description: strp(strings.Repeat("a", 2001))},
	} {
		if _, err := e.svc.UpdateCompany(ctx, domain.HumanActor, e.co.ID, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// com tasks o prefixo trava, o resto continua editável
	e.task(t, service.CreateTaskInput{Title: "primeira"})
	if _, err := e.svc.UpdateCompany(ctx, domain.HumanActor, e.co.ID, service.UpdateCompanyInput{TaskPrefix: strp("ZZ")}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("prefixo com tasks: %v", err)
	}
	if _, err := e.svc.UpdateCompany(ctx, domain.HumanActor, e.co.ID, service.UpdateCompanyInput{TaskPrefix: strp("ac"), Name: strp("Acme 2")}); err != nil {
		t.Fatalf("mesmo prefixo é no-op: %v", err)
	}

	acts, err := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range acts {
		found = found || a.Verb == "company.updated"
	}
	if !found {
		t.Error("sem company.updated na atividade")
	}
}

func TestArchiveRestoreCompany(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Dev", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t1", PersonaID: &p.ID})
	fl, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "Diário", Cron: "0 9 * * *", PersonaID: p.ID})
	if err != nil {
		t.Fatal(err)
	}

	co, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !co.Archived() {
		t.Fatal("deveria estar arquivada")
	}
	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("arquivar duas vezes: %v", err)
	}
	got, err := e.svc.GetPersona(ctx, e.co.ID, p.ID.String())
	if err != nil || got.Status != domain.PersonaPaused {
		t.Fatalf("persona deveria estar pausada: %+v %v", got, err)
	}
	flows, _ := e.svc.ListFlows(ctx, e.co.ID)
	if len(flows) != 1 || flows[0].Enabled {
		t.Fatalf("fluxo deveria estar desligado: %+v", flows)
	}

	// somente leitura: as escritas principais falham com ErrCompanyArchived; as leituras seguem
	writes := map[string]func() error{
		"criar task": func() error {
			_, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "x"})
			return err
		},
		"comentar": func() error {
			_, err := e.svc.AddComment(ctx, domain.HumanActor, e.co.ID, tk.ID, service.AddCommentInput{Body: "oi"})
			return err
		},
		"contratar": func() error {
			_, err := e.svc.HirePersona(ctx, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Z", Agent: "fake", Model: "m"})
			return err
		},
		"criar fluxo": func() error {
			_, err := e.svc.CreateFlow(ctx, domain.HumanActor, e.co.ID, service.CreateFlowInput{Name: "F2", Cron: "0 9 * * *", PersonaID: p.ID})
			return err
		},
		"criar projeto": func() error {
			_, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "P"})
			return err
		},
		"editar empresa": func() error {
			_, err := e.svc.UpdateCompany(ctx, domain.HumanActor, e.co.ID, service.UpdateCompanyInput{Name: strp("Novo")})
			return err
		},
	}
	for name, w := range writes {
		if err := w(); !errors.Is(err, domain.ErrCompanyArchived) {
			t.Errorf("%s: esperava ErrCompanyArchived, veio %v", name, err)
		}
	}
	if _, err := e.svc.GetTask(ctx, e.co.ID, tk.ID.String()); err != nil {
		t.Errorf("leitura deveria seguir: %v", err)
	}
	if n := e.jobs.wakeCount(); n > 1 { // o wake da criação da task, antes de arquivar
		t.Errorf("wakes demais: %d", n)
	}

	// restaurar: continua pausado
	if _, err := e.svc.RestoreCompany(ctx, domain.HumanActor, e.co.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RestoreCompany(ctx, domain.HumanActor, e.co.ID, false); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("restaurar duas vezes: %v", err)
	}
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.Status != domain.PersonaPaused {
		t.Fatalf("restaurar não retoma sozinho: %s", got.Status)
	}
	if _, err := e.svc.CreateProject(ctx, domain.HumanActor, e.co.ID, service.CreateProjectInput{Name: "P"}); err != nil {
		t.Fatalf("escrita deveria voltar: %v", err)
	}

	// arquivar de novo e restaurar retomando o que o arquivamento mexeu
	if _, err := e.svc.UpdatePersona(ctx, domain.HumanActor, e.co.ID, p.ID, service.UpdatePersonaInput{Status: ptr(domain.PersonaIdle)}); err != nil {
		t.Fatal(err)
	}
	flOn := true
	if _, err := e.svc.UpdateFlow(ctx, domain.HumanActor, e.co.ID, fl.ID, service.UpdateFlowInput{Enabled: &flOn}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RestoreCompany(ctx, domain.HumanActor, e.co.ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.GetPersona(ctx, e.co.ID, p.ID.String()); got.Status != domain.PersonaIdle {
		t.Fatalf("resume deveria reativar a persona: %s", got.Status)
	}
	flows, _ = e.svc.ListFlows(ctx, e.co.ID)
	if len(flows) != 1 || !flows[0].Enabled || flows[0].NextFireAt == nil {
		t.Fatalf("resume deveria religar o fluxo: %+v", flows)
	}
}

func TestDeleteCompany(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.persona(t, "Dev", nil)
	e.task(t, service.CreateTaskInput{Title: "histórico", PersonaID: &p.ID})

	// ativa: não exclui
	if _, err := e.svc.DeleteCompany(ctx, domain.HumanActor, e.co.ID, "Acme Corp"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("excluir ativa: %v", err)
	}
	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DeleteCompany(ctx, domain.HumanActor, e.co.ID, "acme corp"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("nome errado: %v", err)
	}
	d, err := e.svc.DeleteCompany(ctx, domain.HumanActor, e.co.ID, "Acme Corp")
	if err != nil {
		t.Fatal(err)
	}
	if d.Slug != "acme-corp" || d.ExportBytes == 0 || d.SHA256 == "" {
		t.Fatalf("registro: %+v", d)
	}
	if _, err := e.svc.GetCompany(ctx, e.co.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("empresa deveria ter sumido: %v", err)
	}

	// o export guardado é um hanko.company.v1 válido e reimportável
	_, gz, err := e.svc.CompanyDeletionExport(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(raw)) != d.ExportBytes {
		t.Errorf("export_bytes %d != %d", d.ExportBytes, len(raw))
	}
	var doc export.Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Format != export.Format || len(doc.Tasks) != 1 || len(doc.Personas) != 1 {
		t.Fatalf("export: %s tasks=%d personas=%d", doc.Format, len(doc.Tasks), len(doc.Personas))
	}
	res, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "acme-de-volta"})
	if err != nil || res.Counts["tasks"] != 1 {
		t.Fatalf("reimportar: %+v %v", res, err)
	}

	list, err := e.svc.ListCompanyDeletions(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("lista: %v %v", list, err)
	}
	audit, err := e.svc.Pool.Exec(ctx, `SELECT 1 FROM admin_audit WHERE action = 'company.delete'`)
	if err != nil || audit.RowsAffected() != 1 {
		t.Fatalf("auditoria: %v %v", audit, err)
	}
}
