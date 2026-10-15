package service_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// O mascaramento pode alongar um título (a senha curta de uma URL de banco vira "[REDACTED]"): o export trunca
// para caber nas 500 runas que a importação aceita, e o arquivo continua importável.
func TestExportTituloAlongadoPeloMascaramentoCabeNoLimite(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// 481 runas (válido); depois de mascarar, 508
	title := strings.Repeat("ã", 430) + strings.Repeat(" postgres://u:p@h", 3)
	if n := utf8.RuneCountInString(title); n > 500 {
		t.Fatalf("pré-condição: título original com %d runas", n)
	}
	if _, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: title}); err != nil {
		t.Fatal(err)
	}

	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Tasks) != 1 {
		t.Fatalf("tasks = %d", len(doc.Tasks))
	}
	got := doc.Tasks[0].Title
	if n := utf8.RuneCountInString(got); n != 500 {
		t.Fatalf("título exportado com %d runas, esperava 500 (truncado)", n)
	}
	if !strings.Contains(got, "[REDACTED]") || strings.Contains(got, ":p@") {
		t.Fatalf("o segredo deveria estar mascarado: %q", got[len(got)-80:])
	}
	doc.Company.Slug = "copia"
	if _, err := e.svc.ImportCompany(ctx, domain.HumanActor, doc, service.ImportOptions{Slug: "copia"}); err != nil {
		t.Fatalf("o arquivo exportado deveria ser importável: %v", err)
	}
}

// Um título curto não é tocado.
func TestExportTituloCurtoIntacto(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.CreateTask(ctx, domain.HumanActor, e.co.ID, service.CreateTaskInput{Title: "Corrigir login"}); err != nil {
		t.Fatal(err)
	}
	doc, err := e.svc.ExportCompany(ctx, e.co.ID, service.ExportOptions{})
	if err != nil || doc.Tasks[0].Title != "Corrigir login" {
		t.Fatalf("%v %+v", err, doc.Tasks)
	}
}
