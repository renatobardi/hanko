package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

func TestCLIAuthFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mkUser(t, e, "ana@x.com", domain.RoleMember)

	st, err := e.svc.StartCLIAuth(ctx, "  notebook da Ana ", domain.KeyScopeRead)
	if err != nil || st.Code == "" || st.PollToken == "" {
		t.Fatalf("start: %+v %v", st, err)
	}
	if _, err := e.svc.StartCLIAuth(ctx, " ", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("device vazio: %v", err)
	}
	if _, err := e.svc.StartCLIAuth(ctx, "x", "root"); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("escopo ruim: %v", err)
	}

	// pendente: a página enxerga, a CLI espera
	req, err := e.svc.GetCLIAuth(ctx, st.Code)
	if err != nil || req.Device != "notebook da Ana" || req.Scope != domain.KeyScopeRead {
		t.Fatalf("get: %+v %v", req, err)
	}
	if p, err := e.svc.PollCLIAuth(ctx, st.PollToken); err != nil || p.Status != domain.CLIAuthPending || p.APIKey != "" {
		t.Fatalf("poll pendente: %+v %v", p, err)
	}
	if _, err := e.svc.GetCLIAuth(ctx, "codigo-que-nao-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("código desconhecido: %v", err)
	}
	if _, err := e.svc.PollCLIAuth(ctx, "poll-que-nao-existe"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("poll desconhecido: %v", err)
	}

	// autoriza; decidir de novo não vale
	if _, err := e.svc.DecideCLIAuth(ctx, u.ID, st.Code, true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideCLIAuth(ctx, u.ID, st.Code, false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("decidir duas vezes: %v", err)
	}
	if _, err := e.svc.GetCLIAuth(ctx, st.Code); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a página não mostra pedido decidido: %v", err)
	}

	// a chave sai uma vez só, com escopo e dispositivo
	p, err := e.svc.PollCLIAuth(ctx, st.PollToken)
	if err != nil || p.Status != domain.CLIAuthApproved || p.APIKey == "" {
		t.Fatalf("poll aprovado: %+v %v", p, err)
	}
	if p.Key.Scope != domain.KeyScopeRead || p.Key.Device != "notebook da Ana" || p.User.ID != u.ID {
		t.Errorf("chave: %+v", p.Key)
	}
	got, info, err := e.svc.ResolveAPIKeyInfo(ctx, p.APIKey)
	if err != nil || got.ID != u.ID || info.Scope != domain.KeyScopeRead || info.ID != p.Key.ID {
		t.Fatalf("resolve: %v %+v %v", got.ID, info, err)
	}
	again, err := e.svc.PollCLIAuth(ctx, st.PollToken)
	if err != nil || again.Status != domain.CLIAuthConsumed || again.APIKey != "" {
		t.Fatalf("segunda consulta: %+v %v", again, err)
	}
	keys, _ := e.svc.ListAPIKeys(ctx, u.ID)
	if len(keys) != 1 || keys[0].Scope != domain.KeyScopeRead {
		t.Errorf("chaves: %+v", keys)
	}

	// revogar na UI/serviço derruba a chave
	if _, err := e.svc.RevokeAPIKey(ctx, u.ID, p.Key.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ResolveAPIKeyInfo(ctx, p.APIKey); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Errorf("chave revogada: %v", err)
	}

	// negado
	st2, _ := e.svc.StartCLIAuth(ctx, "outro", "")
	if _, err := e.svc.DecideCLIAuth(ctx, u.ID, st2.Code, false); err != nil {
		t.Fatal(err)
	}
	if p, err := e.svc.PollCLIAuth(ctx, st2.PollToken); err != nil || p.Status != domain.CLIAuthDenied || p.APIKey != "" {
		t.Fatalf("negado: %+v %v", p, err)
	}

	// auditoria
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM admin_audit WHERE action LIKE 'cli_auth.%'`).Scan(&n); err != nil || n != 3 {
		t.Errorf("auditoria: %d %v", n, err)
	}
}

func TestCLIAuthExpira(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mkUser(t, e, "ana@x.com", domain.RoleMember)
	st, _ := e.svc.StartCLIAuth(ctx, "d", "")
	base := time.Now().UTC()
	e.svc.Now = func() time.Time { return base.Add(service.CLIAuthTTL + time.Second) }
	if _, err := e.svc.GetCLIAuth(ctx, st.Code); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get expirado: %v", err)
	}
	if _, err := e.svc.DecideCLIAuth(ctx, u.ID, st.Code, true); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("decidir expirado: %v", err)
	}
	if _, err := e.svc.PollCLIAuth(ctx, st.PollToken); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("poll expirado: %v", err)
	}
}

func TestCLIAuthTetoDePedidosAbertos(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO cli_auth_requests (id, code_hash, poll_hash, device, scope, expires_at)
		SELECT gen_random_uuid(), 'c' || g, 'p' || g, 'd', 'full', now() + interval '5 minutes' FROM generate_series(1, $1) g`, service.MaxOpenCLIAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartCLIAuth(ctx, "d", ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("acima do teto: %v", err)
	}
}

func TestJoinRequests(t *testing.T) {
	e := newEnv(t)
	e.svc.ExternalIdentity = true
	ctx := context.Background()
	admin := mkUser(t, e, "adm@x.com", domain.RoleMember)
	join(t, e, admin, domain.MemberAdmin)
	member := mkUser(t, e, "mem@x.com", domain.RoleMember)
	join(t, e, member, domain.MemberMember)
	owner := mkUser(t, e, "own@x.com", domain.RoleMember)
	join(t, e, owner, domain.MemberOwner)
	cand := mkUser(t, e, "cand@x.com", domain.RoleMember)
	cand2 := mkUser(t, e, "cand2@x.com", domain.RoleMember)

	// só admin cuida do link
	if _, _, err := e.svc.CreateJoinLink(as(member), member.Actor(), e.co.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member não gera link: %v", err)
	}
	if _, err := e.svc.JoinLinkInfo(ctx, "nada"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("token inexistente: %v", err)
	}
	tok, link, err := e.svc.CreateJoinLink(as(admin), admin.Actor(), e.co.ID)
	if err != nil || tok == "" || !link.Enabled {
		t.Fatalf("link: %v %+v", err, link)
	}
	if co, err := e.svc.JoinLinkInfo(ctx, tok); err != nil || co.ID != e.co.ID {
		t.Fatalf("info: %v %v", co, err)
	}

	// pedido
	req, err := e.svc.RequestJoin(as(cand), cand.ID, tok, "  me deixa entrar  ")
	if err != nil || req.Status != domain.JoinPending || req.Message != "me deixa entrar" {
		t.Fatalf("pedido: %+v %v", req, err)
	}
	if _, err := e.svc.RequestJoin(as(cand), cand.ID, tok, ""); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("pedido repetido: %v", err)
	}
	if _, err := e.svc.RequestJoin(as(member), member.ID, tok, ""); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("já é membro: %v", err)
	}
	if _, err := e.svc.RequestJoin(as(cand2), cand2.ID, "token-errado", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("token errado: %v", err)
	}
	if _, err := e.svc.ListJoinRequests(as(member), e.co.ID, true); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member não lista: %v", err)
	}
	list, err := e.svc.ListJoinRequests(as(admin), e.co.ID, true)
	if err != nil || len(list) != 1 || list[0].Email != "cand@x.com" {
		t.Fatalf("lista: %+v %v", list, err)
	}

	// admin não concede admin; concede viewer
	if _, err := e.svc.DecideJoinRequest(as(admin), admin.Actor(), e.co.ID, req.ID, true, domain.MemberAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin concedendo admin: %v", err)
	}
	dec, err := e.svc.DecideJoinRequest(as(admin), admin.Actor(), e.co.ID, req.ID, true, domain.MemberViewer)
	if err != nil || dec.Status != domain.JoinApproved || dec.Role == nil || *dec.Role != domain.MemberViewer {
		t.Fatalf("aprovar: %+v %v", dec, err)
	}
	if role, err := e.svc.MyRole(as(cand), e.co.ID); err != nil || role != domain.MemberViewer {
		t.Fatalf("papel do aprovado: %v %v", role, err)
	}
	if _, err := e.svc.DecideJoinRequest(as(admin), admin.Actor(), e.co.ID, req.ID, true, domain.MemberViewer); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("decidir duas vezes: %v", err)
	}

	// negar; e o owner pode conceder admin
	r2, err := e.svc.RequestJoin(as(cand2), cand2.ID, tok, "")
	if err != nil {
		t.Fatal(err)
	}
	if d, err := e.svc.DecideJoinRequest(as(admin), admin.Actor(), e.co.ID, r2.ID, false, ""); err != nil || d.Status != domain.JoinDenied {
		t.Fatalf("negar: %+v %v", d, err)
	}
	if _, err := e.svc.MyRole(as(cand2), e.co.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("negado continua de fora: %v", err)
	}
	r3, err := e.svc.RequestJoin(as(cand2), cand2.ID, tok, "de novo") // depois de negado pode pedir de novo
	if err != nil {
		t.Fatal(err)
	}
	if d, err := e.svc.DecideJoinRequest(as(owner), owner.Actor(), e.co.ID, r3.ID, true, domain.MemberAdmin); err != nil || *d.Role != domain.MemberAdmin {
		t.Fatalf("owner concede admin: %+v %v", d, err)
	}

	// regenerar invalida o token antigo; desligar também
	tok2, _, err := e.svc.CreateJoinLink(as(admin), admin.Actor(), e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinLinkInfo(ctx, tok); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("token antigo: %v", err)
	}
	if err := e.svc.RevokeJoinLink(as(admin), admin.Actor(), e.co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinLinkInfo(ctx, tok2); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("link desligado: %v", err)
	}
	if st, err := e.svc.JoinLinkState(as(admin), e.co.ID); err != nil || st.Enabled {
		t.Errorf("estado: %+v %v", st, err)
	}

	// a atividade registra tudo
	acts, _ := e.svc.ListActivity(ctx, e.co.ID, service.ActivityFilter{})
	seen := map[string]int{}
	for _, a := range acts {
		seen[a.Verb]++
	}
	for _, v := range []string{"join_link.created", "join_link.revoked", "join.requested", "join.approved", "join.denied", "member.joined"} {
		if seen[v] == 0 {
			t.Errorf("sem %s na atividade: %v", v, seen)
		}
	}
}

func TestJoinLinkEmpresaArquivada(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	tok, _, err := e.svc.CreateJoinLink(ctx, domain.HumanActor, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ArchiveCompany(ctx, domain.HumanActor, e.co.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.JoinLinkInfo(ctx, tok); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("link de empresa arquivada: %v", err)
	}
}

func TestListInstanceUsersEWhoami(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inst := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	adm := mkUser(t, e, "adm@x.com", domain.RoleAdmin)
	mem := mkUser(t, e, "mem@x.com", domain.RoleMember)
	join(t, e, mem, domain.MemberMember)
	if _, _, err := e.svc.CreateAPIKeyScoped(ctx, mem.ID, "k", domain.KeyScopeRead, "laptop"); err != nil {
		t.Fatal(err)
	}

	for _, u := range []domain.User{inst, adm} {
		list, err := e.svc.ListInstanceUsers(as(u))
		if err != nil || len(list) != 3 {
			t.Fatalf("%s: %v %v", u.Email, list, err)
		}
		for _, row := range list {
			if row.User.Email == "mem@x.com" && (row.Companies != 1 || row.ActiveKeys != 1) {
				t.Errorf("contagens: %+v", row)
			}
		}
	}
	if _, err := e.svc.ListInstanceUsers(as(mem)); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member: %v", err)
	}
	if list, err := e.svc.ListInstanceUsers(ctx); err != nil || len(list) != 3 { // trusted
		t.Errorf("trusted: %v %v", list, err)
	}

	// whoami
	if w, err := e.svc.WhoamiOf(ctx); err != nil || w.Authenticated {
		t.Errorf("trusted: %+v %v", w, err)
	}
	keys, _ := e.svc.ListAPIKeys(ctx, mem.ID)
	kid := keys[0].ID
	w, err := e.svc.WhoamiOf(auth.WithIdentity(ctx, auth.Identity{User: mem, Via: auth.ViaAPIKey, KeyID: &kid}))
	if err != nil || !w.Authenticated || w.Key == nil || w.Key.Device != "laptop" || w.Key.Scope != domain.KeyScopeRead {
		t.Errorf("api key: %+v %v", w, err)
	}
	if w, err := e.svc.WhoamiOf(as(mem)); err != nil || w.Key != nil || w.Via != auth.ViaSession {
		t.Errorf("sessão: %+v %v", w, err)
	}
}
