package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestExportImportPapeis(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	e.persona(t, "Ana", nil)

	for role, want := range map[domain.MemberRole]bool{
		domain.MemberViewer: false, domain.MemberMember: false, domain.MemberAdmin: true, domain.MemberOwner: true} {
		u := mkUser(t, e, string(role)+"@x.com", domain.RoleMember)
		join(t, e, u, role)
		_, err := e.svc.ExportCompany(as(u), e.co.ID, service.ExportOptions{})
		if want && err != nil || !want && !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("export como %s: %v (permitido=%v)", role, err, want)
		}
	}

	doc, err := e.svc.ExportCompany(bg, e.co.ID, service.ExportOptions{}) // modo confiável: sem restrição
	if err != nil {
		t.Fatal(err)
	}
	// importar cria empresa: usuário de instalação "member" não pode; "admin" pode e vira owner
	plain := mkUser(t, e, "plain@x.com", domain.RoleMember)
	if _, err := e.svc.ImportCompany(as(plain), domain.HumanActor, doc, service.ImportOptions{Slug: "i1"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("import por users.role=member: %v", err)
	}
	if _, err := e.svc.GetCompany(bg, "i1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("a recusa não pode deixar empresa: %v", err)
	}
	adm := mkUser(t, e, "adm@x.com", domain.RoleAdmin)
	res, err := e.svc.ImportCompany(as(adm), domain.HumanActor, doc, service.ImportOptions{Slug: "i2"})
	if err != nil {
		t.Fatal(err)
	}
	var isOwner bool
	if err := e.svc.Pool.QueryRow(bg, `SELECT role='owner' FROM company_members WHERE company_id=$1 AND user_id=$2`,
		res.Company.ID, adm.ID).Scan(&isOwner); err != nil || !isOwner {
		t.Fatalf("quem importa deveria virar owner: %v %v", isOwner, err)
	}
}
