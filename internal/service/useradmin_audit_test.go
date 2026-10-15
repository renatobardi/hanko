package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// A auditoria também vai para o banco (admin_audit), além do log: ok, negada e recusada, sem senha nem token, e só
// o dono da instalação lê.
func TestAdminAuditPersisteNoBanco(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inst := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	alvo := mkUser(t, e, "alvo@x.com", domain.RoleMember)
	plain := mkUser(t, e, "comum@x.com", domain.RoleMember)

	if _, _, err := e.svc.AdminResetPassword(as(plain), alvo.Email, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	_, tmp, err := e.svc.AdminResetPassword(as(inst), alvo.Email, "")
	if err != nil || tmp == "" {
		t.Fatalf("reset: %q %v", tmp, err)
	}
	if err := e.svc.AdminRemoveUser(as(inst), inst.Email); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err := e.svc.AdminRemoveUser(as(inst), alvo.Email); err != nil {
		t.Fatal(err)
	}

	got, err := e.svc.AdminListAudit(as(inst), 0)
	if err != nil {
		t.Fatal(err)
	}
	// da mais recente para a mais antiga
	want := []struct{ action, result, actor, target string }{
		{"user.remove", "ok", inst.Email, alvo.Email},
		{"user.remove", "refused", inst.Email, inst.Email},
		{"user.reset_password", "ok", inst.Email, alvo.Email},
		{"user.reset_password", "denied", plain.Email, alvo.Email},
	}
	if len(got) != len(want) {
		t.Fatalf("%d registros, esperava %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Action != w.action || g.Result != w.result || g.ActorEmail != w.actor || g.TargetEmail != w.target || g.ID == (domain.ID{}) || g.At.IsZero() {
			t.Errorf("registro %d = %+v, esperava %+v", i, g, w)
		}
	}
	if got[0].ActorID == nil || *got[0].ActorID != inst.ID || got[0].TargetID == nil || *got[0].TargetID != alvo.ID {
		t.Errorf("ids do registro ok: %+v", got[0])
	}
	// o alvo foi removido, o registro sobrevive (sem FK)
	if got[2].TargetID == nil || *got[2].TargetID != alvo.ID {
		t.Errorf("registro do reset perdeu o alvo: %+v", got[2])
	}
	// nunca a senha
	var leaked int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE admin_audit::text LIKE '%'||$1||'%'`, tmp).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("a senha temporária apareceu na auditoria: %d %v", leaked, err)
	}
	// limite
	if one, err := e.svc.AdminListAudit(as(inst), 1); err != nil || len(one) != 1 || one[0].Action != "user.remove" || one[0].Result != "ok" {
		t.Fatalf("limit 1: %+v %v", one, err)
	}
	// só o dono da instalação lê
	if _, err := e.svc.AdminListAudit(as(plain), 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("usuário comum lendo a auditoria: %v", err)
	}
	if _, err := e.svc.AdminListAudit(ctx, 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("sem identidade: %v", err)
	}
}

// O registro ok é gravado na MESMA transação da operação: se a gravação da auditoria falha, a operação é desfeita.
func TestAdminAuditNaMesmaTransacao(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inst := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	alvo := mkUser(t, e, "alvo@x.com", domain.RoleMember)
	// uma restrição que só a auditoria viola
	if _, err := e.svc.Pool.Exec(ctx, `ALTER TABLE admin_audit ADD CONSTRAINT sem_ok CHECK (result <> 'ok')`); err != nil {
		t.Fatal(err)
	}

	if _, _, err := e.svc.AdminResetPassword(as(inst), alvo.Email, "uma-senha-nova-longa"); err == nil {
		t.Fatal("o reset deveria falhar se a auditoria não pôde ser gravada")
	}
	var hash string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, alvo.ID).Scan(&hash); err != nil || hash != "x" {
		t.Fatalf("a senha mudou apesar da auditoria falhar: %q %v", hash, err)
	}
	if err := e.svc.AdminRemoveUser(as(inst), alvo.Email); err == nil {
		t.Fatal("a remoção deveria falhar se a auditoria não pôde ser gravada")
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE id=$1`, alvo.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("a conta foi removida apesar da auditoria falhar: %d %v", n, err)
	}
}
