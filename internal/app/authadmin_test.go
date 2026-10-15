package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/events"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/testdb"
)

func TestAuthAdminUserCommandsNeedInstallOwner(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	svc := service.New(pool, noopJobs{}, events.NewBroker())
	a := &authAdmin{Service: svc, provider: "local", closePool: func() {}}
	owner, err := svc.CreateOwner(ctx, "dono@x.com", "Dono", "senha-do-dono-123")
	if err != nil {
		t.Fatal(err)
	}
	// único dono: --as pode ser omitido
	us, err := a.AdminListUsers(ctx, "")
	if err != nil || len(us) != 1 || us[0].ID != owner.ID {
		t.Fatalf("list: %v %v", us, err)
	}
	// um usuário comum, mesmo apontado em --as, é recusado pelo service (papel no banco)
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, name, provider, password_hash, role) VALUES ($1,'comum@x.com','C','local','x','member')`, domain.NewID()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AdminListUsers(ctx, "comum@x.com"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("comum como operador: %v", err)
	}
	tmp, err := a.AdminResetPassword(ctx, "dono@x.com", "comum@x.com", "")
	if err != nil || len(tmp) < 10 || strings.Contains(tmp, " ") {
		t.Fatalf("reset: %q %v", tmp, err)
	}
	if err := a.AdminRemoveUser(ctx, "", "comum@x.com"); err != nil {
		t.Fatal(err)
	}
}
