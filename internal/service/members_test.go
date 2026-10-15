package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// mkUser insere um usuário local direto no banco (sem argon2id) com o papel de instalação dado.
func mkUser(t *testing.T, e *env, email string, role domain.UserRole) domain.User {
	t.Helper()
	id := domain.NewID()
	_, err := e.svc.Pool.Exec(context.Background(),
		`INSERT INTO users (id, email, name, provider, password_hash, role) VALUES ($1,$2,$3,'local','x',$4)`,
		id, email, email, string(role))
	if err != nil {
		t.Fatalf("mkUser %s: %v", email, err)
	}
	u, err := e.svc.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// as devolve um contexto com a identidade (sessão) do usuário.
func as(u domain.User) context.Context {
	return auth.WithIdentity(context.Background(), auth.Identity{User: u, Via: auth.ViaSession})
}

// join põe o usuário na empresa direto no banco com o papel dado.
func join(t *testing.T, e *env, u domain.User, role domain.MemberRole) {
	t.Helper()
	_, err := e.svc.Pool.Exec(context.Background(),
		`INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,$3)`, e.co.ID, u.ID, string(role))
	if err != nil {
		t.Fatalf("join: %v", err)
	}
}

func TestRoleMatrix(t *testing.T) {
	e := newEnv(t)
	e.svc.ExternalIdentity = true // a regra do convite a e-mail sem conta (provider local) tem teste próprio
	bg := context.Background()
	p := e.persona(t, "Ana", nil)
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	approval, err := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "ok?", TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}

	users := map[domain.MemberRole]domain.User{}
	for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
		u := mkUser(t, e, string(r)+"@x.com", domain.RoleMember)
		join(t, e, u, r)
		users[r] = u
	}
	budget := int64(1000)

	type op struct {
		name string
		min  domain.MemberRole
		run  func(ctx context.Context, u domain.User) error
	}
	ops := []op{
		{"ListTasks", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
			return err
		}},
		{"GetCompany", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.GetCompany(ctx, e.co.Slug)
			return err
		}},
		{"Dashboard", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.Dashboard(ctx, e.co.ID)
			return err
		}},
		{"ListMembers", domain.MemberViewer, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.ListMembers(ctx, e.co.ID)
			return err
		}},
		{"CreateTask", domain.MemberMember, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateTask(ctx, u.Actor(), e.co.ID, service.CreateTaskInput{Title: "x"})
			return err
		}},
		{"AddComment", domain.MemberMember, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.AddComment(ctx, u.Actor(), e.co.ID, tk.ID, service.AddCommentInput{Body: "oi"})
			return err
		}},
		{"HirePersona", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.HirePersona(ctx, u.Actor(), e.co.ID, service.HirePersonaInput{Name: "P-" + u.Email, Agent: "fake", Model: "m"})
			return err
		}},
		{"UpdatePersona", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.UpdatePersona(ctx, u.Actor(), e.co.ID, p.ID, service.UpdatePersonaInput{Title: ptr("t")})
			return err
		}},
		{"SetCompanyBudget", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.SetCompanyBudget(ctx, u.Actor(), e.co.ID, &budget)
			return err
		}},
		{"CreateSubscription", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, err := e.svc.CreateSubscription(ctx, u.Actor(), e.co.ID, service.CreateSubscriptionInput{Name: "S-" + u.Email, Agent: "claude", MonthlyUSD: 20})
			return err
		}},
		{"ListInvites", domain.MemberAdmin, func(ctx context.Context, _ domain.User) error {
			_, err := e.svc.ListInvites(ctx, e.co.ID)
			return err
		}},
		{"InviteMember", domain.MemberAdmin, func(ctx context.Context, u domain.User) error {
			_, _, err := e.svc.InviteMember(ctx, u.Actor(), e.co.ID, "convidado-"+string(u.Email[0])+"@x.com", domain.MemberViewer)
			return err
		}},
	}
	for _, o := range ops {
		for _, r := range []domain.MemberRole{domain.MemberViewer, domain.MemberMember, domain.MemberAdmin, domain.MemberOwner} {
			u := users[r]
			err := o.run(as(u), u)
			if r.AtLeast(o.min) {
				if err != nil {
					t.Errorf("%s como %s deveria passar: %v", o.name, r, err)
				}
			} else if !errors.Is(err, domain.ErrForbidden) || !errors.Is(err, domain.ErrNotAllowed) {
				t.Errorf("%s como %s deveria ser ErrForbidden: %v", o.name, r, err)
			}
		}
	}

	// gate: viewer não decide; member decide gate de task; hire só admin
	if _, err := e.svc.DecideGate(as(users[domain.MemberViewer]), users[domain.MemberViewer].Actor(), e.co.ID, approval.ID,
		service.DecideGateInput{Decision: domain.GateApproved}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("viewer decidindo gate: %v", err)
	}
	mu := users[domain.MemberMember]
	hire, err := e.svc.RequestGate(bg, domain.HumanActor, e.co.ID, service.RequestGateInput{Title: "contratar", Kind: domain.GateHire, TaskID: &tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideGate(as(mu), mu.Actor(), e.co.ID, hire.ID, service.DecideGateInput{Decision: domain.GateApproved}); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member decidindo gate de contratação: %v", err)
	}
	if _, err := e.svc.DecideGate(as(mu), mu.Actor(), e.co.ID, approval.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Errorf("member decidindo gate de task: %v", err)
	}
	ad := users[domain.MemberAdmin]
	if _, err := e.svc.DecideGate(as(ad), ad.Actor(), e.co.ID, hire.ID, service.DecideGateInput{Decision: domain.GateApproved}); err != nil {
		t.Errorf("admin decidindo gate de contratação: %v", err)
	}
}

func TestNonMemberSeesNotFound(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	tk := e.task(t, service.CreateTaskInput{Title: "t"})
	stranger := mkUser(t, e, "estranho@x.com", domain.RoleMember)
	ctx := as(stranger)
	checks := map[string]error{}
	_, checks["GetCompany por slug"] = e.svc.GetCompany(ctx, e.co.Slug)
	_, checks["GetCompany por id"] = e.svc.GetCompany(ctx, e.co.ID.String())
	_, checks["ListTasks"] = e.svc.ListTasks(ctx, e.co.ID, service.TaskFilter{})
	_, checks["GetTask"] = e.svc.GetTask(ctx, e.co.ID, tk.Key)
	_, checks["CreateTask"] = e.svc.CreateTask(ctx, stranger.Actor(), e.co.ID, service.CreateTaskInput{Title: "x"})
	_, checks["ListMembers"] = e.svc.ListMembers(ctx, e.co.ID)
	_, _, checks["InviteMember"] = e.svc.InviteMember(ctx, stranger.Actor(), e.co.ID, "a@x.com", domain.MemberViewer)
	_, checks["HirePersona"] = e.svc.HirePersona(ctx, stranger.Actor(), e.co.ID, service.HirePersonaInput{Name: "X", Agent: "fake", Model: "m"})
	checks["RemoveMember"] = e.svc.RemoveMember(ctx, stranger.Actor(), e.co.ID, stranger.ID)
	for name, err := range checks {
		if !errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrNotAllowed) {
			t.Errorf("%s: want ErrNotFound (não vazar existência), got %v", name, err)
		}
	}
	cos, err := e.svc.ListCompanies(ctx)
	if err != nil || len(cos) != 0 {
		t.Fatalf("ListCompanies do estranho: %v %v", cos, err)
	}
	// contexto sem identidade (trusted/persona/heartbeat) continua sem restrição
	if cos, _ := e.svc.ListCompanies(bg); len(cos) != 1 {
		t.Fatalf("ListCompanies sem identidade: %v", cos)
	}
}

func TestInstallationOwnerSeesEverything(t *testing.T) {
	e := newEnv(t)
	dono := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	// o dono da instalação não precisa de linha em company_members
	ctx := as(dono)
	if _, err := e.svc.GetCompany(ctx, e.co.Slug); err != nil {
		t.Fatal(err)
	}
	if cos, _ := e.svc.ListCompanies(ctx); len(cos) != 1 {
		t.Fatalf("lista: %v", cos)
	}
	if _, err := e.svc.CreateTask(ctx, dono.Actor(), e.co.ID, service.CreateTaskInput{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.InviteMember(ctx, dono.Actor(), e.co.ID, "novo@x.com", domain.MemberOwner); err != nil {
		t.Fatal(err)
	}
}

func TestCreateCompanyMakesCreatorOwner(t *testing.T) {
	e := newEnv(t)
	adm := mkUser(t, e, "adm@x.com", domain.RoleAdmin)
	co, err := e.svc.CreateCompany(as(adm), adm.Actor(), service.CreateCompanyInput{Name: "Nova"})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := e.svc.ListMembers(as(adm), co.ID)
	if len(ms) != 1 || ms[0].UserID != adm.ID || ms[0].Role != domain.MemberOwner {
		t.Fatalf("membros: %+v", ms)
	}
	if cos, _ := e.svc.ListCompanies(as(adm)); len(cos) != 1 || cos[0].ID != co.ID {
		t.Fatalf("o admin só vê a empresa que criou (não é membro da outra): %v", cos)
	}
	mem := mkUser(t, e, "mem@x.com", domain.RoleMember)
	if _, err := e.svc.CreateCompany(as(mem), mem.Actor(), service.CreateCompanyInput{Name: "Outra"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("usuário member da instalação não cria empresa: %v", err)
	}
}

func TestLateUserIsNotMember(t *testing.T) {
	// um usuário criado depois da migração não vira membro sozinho
	e := newEnv(t)
	late := mkUser(t, e, "tarde@x.com", domain.RoleMember)
	if _, err := e.svc.GetCompany(as(late), e.co.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestLastOwnerProtected(t *testing.T) {
	e := newEnv(t)
	o1 := mkUser(t, e, "o1@x.com", domain.RoleMember)
	join(t, e, o1, domain.MemberOwner)
	ctx := as(o1)
	if _, err := e.svc.SetMemberRole(ctx, o1.Actor(), e.co.ID, o1.ID, domain.MemberAdmin); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("rebaixar o último owner: %v", err)
	}
	if err := e.svc.RemoveMember(ctx, o1.Actor(), e.co.ID, o1.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remover o último owner (sair): %v", err)
	}
	o2 := mkUser(t, e, "o2@x.com", domain.RoleMember)
	join(t, e, o2, domain.MemberOwner)
	if _, err := e.svc.SetMemberRole(ctx, o1.Actor(), e.co.ID, o1.ID, domain.MemberAdmin); err != nil {
		t.Fatalf("com dois owners pode rebaixar: %v", err)
	}
	// agora o2 é o único owner
	if err := e.svc.RemoveMember(as(o2), o2.Actor(), e.co.ID, o2.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remover o novo último owner: %v", err)
	}
}

func TestLastOwnerRace(t *testing.T) {
	e := newEnv(t)
	o1 := mkUser(t, e, "r1@x.com", domain.RoleMember)
	o2 := mkUser(t, e, "r2@x.com", domain.RoleMember)
	join(t, e, o1, domain.MemberOwner)
	join(t, e, o2, domain.MemberOwner)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, u := range []domain.User{o1, o2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = e.svc.RemoveMember(as(u), u.Actor(), e.co.ID, u.ID) // cada um tenta sair
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("exatamente um deve sair: %v", errs)
	}
	ms, _ := e.svc.ListMembers(context.Background(), e.co.ID)
	if len(ms) != 1 || ms[0].Role != domain.MemberOwner {
		t.Fatalf("sobrou: %+v", ms)
	}
}

func TestAdminLimits(t *testing.T) {
	e := newEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	adm := mkUser(t, e, "adm@x.com", domain.RoleMember)
	mem := mkUser(t, e, "mem@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	join(t, e, adm, domain.MemberAdmin)
	join(t, e, mem, domain.MemberMember)
	mkUser(t, e, "x@x.com", domain.RoleMember) // e-mail com conta: admin pode convidar
	ctx := as(adm)
	for _, r := range []domain.MemberRole{domain.MemberAdmin, domain.MemberOwner} {
		if _, _, err := e.svc.InviteMember(ctx, adm.Actor(), e.co.ID, "x@x.com", r); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("admin convidando %s: %v", r, err)
		}
	}
	if _, _, err := e.svc.InviteMember(ctx, adm.Actor(), e.co.ID, "x@x.com", domain.MemberMember); err != nil {
		t.Errorf("admin convidando member: %v", err)
	}
	if _, err := e.svc.SetMemberRole(ctx, adm.Actor(), e.co.ID, mem.ID, domain.MemberAdmin); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("admin promovendo a admin: %v", err)
	}
	if _, err := e.svc.SetMemberRole(ctx, adm.Actor(), e.co.ID, mem.ID, domain.MemberViewer); err != nil {
		t.Errorf("admin rebaixando member: %v", err)
	}
	if _, err := e.svc.SetMemberRole(ctx, adm.Actor(), e.co.ID, own.ID, domain.MemberViewer); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("admin rebaixando owner: %v", err)
	}
	if err := e.svc.RemoveMember(ctx, adm.Actor(), e.co.ID, own.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("admin removendo owner: %v", err)
	}
	if err := e.svc.RemoveMember(as(mem), mem.Actor(), e.co.ID, adm.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("member removendo admin: %v", err)
	}
	if err := e.svc.RemoveMember(as(mem), mem.Actor(), e.co.ID, mem.ID); err != nil {
		t.Errorf("member saindo: %v", err)
	}
	if err := e.svc.RemoveMember(ctx, adm.Actor(), e.co.ID, mem.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("remover quem já saiu: %v", err)
	}
	if _, err := e.svc.SetMemberRole(as(own), own.Actor(), e.co.ID, adm.ID, domain.MemberRole("rei")); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("papel inválido: %v", err)
	}
	// a auditoria registrou
	acts, _ := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{})
	seen := map[string]bool{}
	for _, a := range acts {
		seen[a.Verb] = true
	}
	for _, v := range []string{"member.invited", "member.role_changed", "member.removed"} {
		if !seen[v] {
			t.Errorf("activity %s ausente", v)
		}
	}
}

func clockEnv(t *testing.T) (*env, *time.Time) {
	e := newEnv(t)
	now := time.Now().UTC()
	e.svc.Now = func() time.Time { return now }
	return e, &now
}

func TestInviteFlowLocal(t *testing.T) {
	e, _ := clockEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	ctx := as(own)

	inv, tok, err := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "  Nova@Exemplo.com ", domain.MemberMember)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Email != "nova@exemplo.com" || inv.Role != domain.MemberMember || inv.ExpiresAt.Sub(inv.CreatedAt) != 7*24*time.Hour {
		t.Fatalf("convite: %+v", inv)
	}
	// o token nunca fica em claro no banco
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM company_invites WHERE token_hash = $1`, tok).Scan(&n); err != nil || n != 0 {
		t.Fatalf("token em claro no banco: %d %v", n, err)
	}
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM company_invites WHERE token_hash = $1`, auth.HashToken(tok)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hash do token: %d %v", n, err)
	}
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM activity_events WHERE data::text LIKE '%' || $1 || '%'`, tok).Scan(&n); err != nil || n != 0 {
		t.Fatalf("token vazou para a atividade: %d %v", n, err)
	}

	pv, err := e.svc.PreviewInvite(context.Background(), tok)
	if err != nil || pv.CompanyName != "Acme Corp" || pv.Email != "nova@exemplo.com" || pv.HasAccount {
		t.Fatalf("preview: %+v %v", pv, err)
	}
	if _, err := e.svc.PreviewInvite(context.Background(), tok+"x"); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("token errado: %v", err)
	}
	if _, err := e.svc.PreviewInvite(context.Background(), ""); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("token vazio: %v", err)
	}

	if _, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, "", goodPass); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("sem nome: %v", err)
	}
	if _, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, "Nova", "curta"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("senha curta: %v", err)
	}
	u, m, err := e.svc.AcceptInviteLocal(context.Background(), tok, "Nova", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "nova@exemplo.com" || u.Role != domain.RoleMember || m.Role != domain.MemberMember || m.CompanyID != e.co.ID {
		t.Fatalf("aceite: %+v %+v", u, m)
	}
	if _, err := e.svc.AuthenticateLocal(context.Background(), "nova@exemplo.com", goodPass); err != nil {
		t.Fatalf("login da conta criada: %v", err)
	}
	// uso único
	if _, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, "Nova", goodPass); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("reuso: %v", err)
	}
	if _, err := e.svc.AcceptInvite(context.Background(), tok, u); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("reuso (logado): %v", err)
	}
	// agora a nova usuária vê a empresa e cria task, mas não contrata
	if _, err := e.svc.CreateTask(as(u), u.Actor(), e.co.ID, service.CreateTaskInput{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.HirePersona(as(u), u.Actor(), e.co.ID, service.HirePersonaInput{Name: "P", Agent: "fake", Model: "m"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("%v", err)
	}
	if invs, _ := e.svc.ListInvites(ctx, e.co.ID); len(invs) != 0 {
		t.Fatalf("convite aceito não é mais aberto: %v", invs)
	}
	acts, _ := e.svc.ListActivity(context.Background(), e.co.ID, service.ActivityFilter{})
	joined := false
	for _, a := range acts {
		joined = joined || a.Verb == "member.joined"
	}
	if !joined {
		t.Fatal("member.joined ausente")
	}
}

func TestInviteExpiredRevokedReplacedAndWrongEmail(t *testing.T) {
	e, now := clockEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	ctx := as(own)
	bg := context.Background()

	// expirado
	_, tok, _ := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "a@x.com", domain.MemberViewer)
	*now = now.Add(7*24*time.Hour - time.Second)
	if _, err := e.svc.PreviewInvite(bg, tok); err != nil {
		t.Fatalf("ainda vale 1 s antes: %v", err)
	}
	*now = now.Add(time.Second)
	if _, err := e.svc.PreviewInvite(bg, tok); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("expirado: %v", err)
	}
	if _, _, err := e.svc.AcceptInviteLocal(bg, tok, "A", goodPass); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("aceite expirado: %v", err)
	}

	// revogado
	inv, tok, _ := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "b@x.com", domain.MemberViewer)
	if err := e.svc.RevokeInvite(ctx, own.Actor(), e.co.ID, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PreviewInvite(bg, tok); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("revogado: %v", err)
	}
	if err := e.svc.RevokeInvite(ctx, own.Actor(), e.co.ID, inv.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revogar de novo: %v", err)
	}

	// reconvidar invalida o anterior
	_, old, _ := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "c@x.com", domain.MemberViewer)
	_, fresh, _ := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "C@x.com", domain.MemberMember)
	if _, err := e.svc.PreviewInvite(bg, old); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("convite antigo deveria ter sido substituído: %v", err)
	}
	if _, err := e.svc.PreviewInvite(bg, fresh); err != nil {
		t.Fatal(err)
	}
	if invs, _ := e.svc.ListInvites(ctx, e.co.ID); len(invs) != 2 { // c@ e... a@ (vencido, não revogado) + c@ = 2
		t.Fatalf("abertos: %v", invs)
	}

	// e-mail diferente
	other := mkUser(t, e, "outro@x.com", domain.RoleMember)
	if _, err := e.svc.AcceptInvite(as(other), fresh, other); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("e-mail diferente: %v", err)
	}
	if _, err := e.svc.GetCompany(as(other), e.co.Slug); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("não virou membro: %v", err)
	}
	// o convite continua utilizável pelo dono do e-mail
	c := mkUser(t, e, "c@x.com", domain.RoleMember)
	if pv, _ := e.svc.PreviewInvite(bg, fresh); !pv.HasAccount {
		t.Fatal("HasAccount deveria ser true")
	}
	if _, _, err := e.svc.AcceptInviteLocal(bg, fresh, "C", goodPass); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("conta já existe: %v", err)
	}
	m, err := e.svc.AcceptInvite(as(c), fresh, c)
	if err != nil || m.Role != domain.MemberMember {
		t.Fatalf("aceite pelo dono do e-mail (caixa diferente): %+v %v", m, err)
	}

	// já é membro: convidar de novo é conflito
	if _, _, err := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "c@x.com", domain.MemberViewer); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("convidar membro: %v", err)
	}
	// e-mail inválido
	if _, _, err := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "nao-e-email", domain.MemberViewer); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("e-mail inválido: %v", err)
	}
	if _, _, err := e.svc.InviteMember(ctx, own.Actor(), e.co.ID, "z@x.com", "rei"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("papel inválido: %v", err)
	}
}

func TestAcceptInviteRace(t *testing.T) {
	e := newEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	_, tok, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "corrida@x.com", domain.MemberViewer)
	var wg sync.WaitGroup
	var mu sync.Mutex
	okN := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for try := 0; try < 20; try++ {
				_, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, "C", goodPass)
				if errors.Is(err, auth.ErrBusy) {
					continue
				}
				if err == nil {
					mu.Lock()
					okN++
					mu.Unlock()
				}
				return
			}
		}()
	}
	wg.Wait()
	if okN != 1 {
		t.Fatalf("o convite é de uso único: %d aceites", okN)
	}
}

func TestFirebaseInviteAdmission(t *testing.T) {
	e := newEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	_, tok, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "conv@x.com", domain.MemberViewer)
	bg := context.Background()
	base := service.FirebaseLogin{UID: "uid-1", Email: "conv@x.com", Name: "Conv", ListsConfigured: true, Admitted: false, OwnerEmail: "own@x.com"}

	if _, err := e.svc.LoginFirebase(bg, base); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("sem convite fora das listas: %v", err)
	}
	bad := base
	bad.InviteToken = "token-qualquer"
	if _, err := e.svc.LoginFirebase(bg, bad); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("convite inválido: %v", err)
	}
	wrongMail := base
	wrongMail.UID, wrongMail.Email, wrongMail.InviteToken = "uid-2", "outro@x.com", tok
	if _, err := e.svc.LoginFirebase(bg, wrongMail); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("e-mail do ID token diferente do convite: %v", err)
	}
	good := base
	good.InviteToken = tok
	u, err := e.svc.LoginFirebase(bg, good)
	if err != nil || u.Role != domain.RoleMember {
		t.Fatalf("login com convite: %+v %v", u, err)
	}
	if _, err := e.svc.AcceptInvite(bg, tok, u); err != nil {
		t.Fatal(err)
	}
	// admitido por convite: sessão continua valendo mesmo fora das listas
	e.svc.Admission = auth.Admission{Emails: []string{"own@x.com"}}
	secret, _, err := e.svc.CreateSession(bg, u.ID, service.SessionMeta{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveSession(bg, secret); err != nil {
		t.Fatalf("sessão do convidado: %v", err)
	}
	// login seguinte, sem token, também passa
	if _, err := e.svc.LoginFirebase(bg, base); err != nil {
		t.Fatalf("relogin do convidado: %v", err)
	}
	if !strings.Contains(u.Email, "@") {
		t.Fatal("x")
	}
}

func invitedFlag(t *testing.T, e *env, id domain.ID) bool {
	t.Helper()
	var v bool
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT invited FROM users WHERE id = $1`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// users.invited só marca conta CRIADA pelo convite, e some quando a pessoa fica sem nenhuma empresa.
func TestInvitedFlagLifecycle(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	own := mkUser(t, e, "own@x.com", domain.RoleOwner)
	join(t, e, own, domain.MemberOwner)

	// conta que já existia aceita convite: não vira "invited"
	old := mkUser(t, e, "velha@x.com", domain.RoleMember)
	_, tok, err := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "velha@x.com", domain.MemberMember)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvite(bg, tok, old); err != nil {
		t.Fatal(err)
	}
	if invitedFlag(t, e, old.ID) {
		t.Fatal("conta pré-existente não é marcada como invited")
	}

	// conta criada pelo convite: invited; continua enquanto houver outra empresa
	_, tok, _ = e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "nova@x.com", domain.MemberMember)
	nova, _, err := e.svc.AcceptInviteLocal(bg, tok, "Nova", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	if !invitedFlag(t, e, nova.ID) {
		t.Fatal("conta criada pelo convite deve ficar invited")
	}
	co2, err := e.svc.CreateCompany(as(own), own.Actor(), service.CreateCompanyInput{Name: "Segunda"})
	if err != nil {
		t.Fatal(err)
	}
	_, tok2, err := e.svc.InviteMember(as(own), own.Actor(), co2.ID, "nova@x.com", domain.MemberViewer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvite(bg, tok2, nova); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveMember(as(own), own.Actor(), e.co.ID, nova.ID); err != nil {
		t.Fatal(err)
	}
	if !invitedFlag(t, e, nova.ID) {
		t.Fatal("ainda tem uma empresa: continua invited")
	}
	if err := e.svc.RemoveMember(as(own), own.Actor(), co2.ID, nova.ID); err != nil {
		t.Fatal(err)
	}
	if invitedFlag(t, e, nova.ID) {
		t.Fatal("sem nenhuma empresa: invited zera")
	}
}

// Firebase com listas: quem entrou por convite e foi removido de todas as empresas não entra mais.
func TestFirebaseRemovedInviteeLosesAdmission(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	own := mkUser(t, e, "own@x.com", domain.RoleOwner)
	join(t, e, own, domain.MemberOwner)
	_, tok, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "conv@x.com", domain.MemberViewer)
	in := service.FirebaseLogin{UID: "uid-1", Email: "conv@x.com", ListsConfigured: true, OwnerEmail: "own@x.com", InviteToken: tok}
	u, err := e.svc.LoginFirebase(bg, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvite(bg, tok, u); err != nil {
		t.Fatal(err)
	}
	e.svc.Admission = auth.Admission{Emails: []string{"own@x.com"}}
	secret, _, _ := e.svc.CreateSession(bg, u.ID, service.SessionMeta{}, "")
	if _, err := e.svc.ResolveSession(bg, secret); err != nil {
		t.Fatalf("convidado ativo: %v", err)
	}
	if err := e.svc.RemoveMember(as(own), own.Actor(), e.co.ID, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveSession(bg, secret); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("removido de tudo e fora das listas: sessão deve cair, veio %v", err)
	}
	in.InviteToken = ""
	if _, err := e.svc.LoginFirebase(bg, in); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("relogin sem convite: %v", err)
	}
}

// Provider local: o link do convite é a prova de identidade. Convidar e-mail sem conta só o owner da empresa.
func TestInviteNewEmailNeedsOwnerOnLocal(t *testing.T) {
	e := newEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleMember)
	adm := mkUser(t, e, "adm@x.com", domain.RoleMember)
	join(t, e, own, domain.MemberOwner)
	join(t, e, adm, domain.MemberAdmin)
	known := mkUser(t, e, "known@x.com", domain.RoleMember)

	if _, _, err := e.svc.InviteMember(as(adm), adm.Actor(), e.co.ID, "novo@x.com", domain.MemberMember); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("admin convidando e-mail sem conta no provider local: %v", err)
	}
	if _, _, err := e.svc.InviteMember(as(adm), adm.Actor(), e.co.ID, known.Email, domain.MemberMember); err != nil {
		t.Fatalf("admin convidando conta existente: %v", err)
	}
	inv, tok, err := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "novo@x.com", domain.MemberMember)
	if err != nil {
		t.Fatalf("owner convidando e-mail sem conta: %v", err)
	}
	// com identidade externa (firebase) o admin também pode: o ID token prova o e-mail
	e.svc.ExternalIdentity = true
	if _, _, err := e.svc.InviteMember(as(adm), adm.Actor(), e.co.ID, "outro@x.com", domain.MemberMember); err != nil {
		t.Fatalf("admin com provider externo: %v", err)
	}

	// o aceite registra quem convidou em member.joined
	u, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, "Novo", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	var by string
	if err := e.svc.Pool.QueryRow(context.Background(),
		`SELECT data->>'invited_by' FROM activity_events WHERE verb = 'member.joined' AND subject_id = $1`, u.ID).Scan(&by); err != nil {
		t.Fatal(err)
	}
	if by != own.ID.String() || inv.InvitedBy == nil || inv.InvitedBy.String() != by {
		t.Fatalf("invited_by = %q, quer %s", by, own.ID)
	}
}

func TestInstallOwnerUserAdmin(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	inst := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	join(t, e, inst, domain.MemberOwner)
	plain := mkUser(t, e, "comum@x.com", domain.RoleMember)
	join(t, e, plain, domain.MemberAdmin)
	sole := mkUser(t, e, "unico@x.com", domain.RoleMember)
	co2, err := e.svc.CreateCompany(bg, domain.HumanActor, service.CreateCompanyInput{Name: "Outra"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(bg, `INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,'owner')`, co2.ID, sole.ID); err != nil {
		t.Fatal(err)
	}

	// só o dono da instalação (papel no banco), nunca admin de empresa nem contexto sem usuário
	for name, ctx := range map[string]context.Context{"admin": as(plain), "anônimo": bg, "papel forjado no contexto": as(domain.User{ID: plain.ID, Role: domain.RoleOwner})} {
		if _, err := e.svc.AdminListUsers(ctx); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("list como %s: %v", name, err)
		}
		if _, _, err := e.svc.AdminResetPassword(ctx, plain.Email, ""); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("reset como %s: %v", name, err)
		}
		if err := e.svc.AdminRemoveUser(ctx, plain.Email); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("remove como %s: %v", name, err)
		}
	}
	us, err := e.svc.AdminListUsers(as(inst))
	if err != nil || len(us) != 3 {
		t.Fatalf("list: %d %v", len(us), err)
	}

	// reset: senha temporária só volta uma vez, a antiga deixa de valer e as sessões caem
	if _, err := e.svc.GetUserByEmail(bg, plain.Email); err != nil {
		t.Fatal(err)
	}
	_, tok, _ := e.svc.InviteMember(as(inst), inst.Actor(), e.co.ID, "criada@x.com", domain.MemberMember)
	created, _, err := e.svc.AcceptInviteLocal(bg, tok, "Criada", goodPass)
	if err != nil {
		t.Fatal(err)
	}
	secret, _, _ := e.svc.CreateSession(bg, created.ID, service.SessionMeta{}, "")
	_, tmp, err := e.svc.AdminResetPassword(as(inst), "CRIADA@x.com", "")
	if err != nil || len(tmp) < auth.MinPasswordLen {
		t.Fatalf("reset: %q %v", tmp, err)
	}
	if _, err := e.svc.AuthenticateLocal(bg, created.Email, goodPass); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("senha antiga ainda vale: %v", err)
	}
	if _, err := e.svc.AuthenticateLocal(bg, created.Email, tmp); err != nil {
		t.Fatalf("senha temporária: %v", err)
	}
	if _, err := e.svc.ResolveSession(bg, secret); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("sessão sobrevive ao reset: %v", err)
	}
	if _, pw, err := e.svc.AdminResetPassword(as(inst), created.Email, "outra-senha-longa"); err != nil || pw != "" {
		t.Fatalf("senha informada não é devolvida: %q %v", pw, err)
	}
	if _, _, err := e.svc.AdminResetPassword(as(inst), created.Email, "curta"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("senha curta: %v", err)
	}
	if _, _, err := e.svc.AdminResetPassword(as(inst), "nao@existe.com", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("inexistente: %v", err)
	}

	// remove: não o dono da instalação, não quem é único owner de uma empresa
	if err := e.svc.AdminRemoveUser(as(inst), inst.Email); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remover o dono da instalação: %v", err)
	}
	if err := e.svc.AdminRemoveUser(as(inst), sole.Email); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("remover único owner de %s: %v", co2.Slug, err)
	}
	if err := e.svc.AdminRemoveUser(as(inst), created.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetUserByEmail(bg, created.Email); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("usuário removido ainda existe: %v", err)
	}
}

// O sinal de Kelvin (U+212A) é "k" para EqualFold/ToLower, mas é outra caixa de correio: não vale como o
// e-mail convidado.
func TestInviteEmailNotFoldedWithKelvin(t *testing.T) {
	const kelvin = "K"
	if service.SameEmail("k@x.com", kelvin+"@x.com") || !service.SameEmail(" K@X.com", "k@x.com") || service.SameEmail("lixo", "lixo") {
		t.Fatal("SameEmail")
	}
	if got, _ := service.NormalizeEmail(kelvin + "@x.com"); got != kelvin+"@x.com" {
		t.Fatalf("NormalizeEmail aproximou o Kelvin de ASCII: %q", got)
	}

	e := newEnv(t)
	bg := context.Background()
	own := mkUser(t, e, "own@x.com", domain.RoleOwner)
	join(t, e, own, domain.MemberOwner)
	_, tok, err := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "k@x.com", domain.MemberMember)
	if err != nil {
		t.Fatal(err)
	}
	// aceite logado: conta com e-mail Kelvin não aceita o convite de "k@x.com"
	imp := domain.User{ID: domain.NewID(), Email: kelvin + "@x.com", Provider: domain.ProviderLocal, Role: domain.RoleMember}
	if _, err := e.svc.AcceptInvite(bg, tok, imp); !errors.Is(err, domain.ErrInviteInvalid) {
		t.Fatalf("aceite com e-mail Kelvin: %v", err)
	}
	// firebase: o ID token com e-mail Kelvin não ganha admissão pelo convite de "k@x.com"
	_, err = e.svc.LoginFirebase(bg, service.FirebaseLogin{UID: "u-k", Email: kelvin + "@x.com", ListsConfigured: true, OwnerEmail: "own@x.com", InviteToken: tok})
	if !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("login firebase com Kelvin: %v", err)
	}
	// e o e-mail certo continua entrando
	if _, err := e.svc.LoginFirebase(bg, service.FirebaseLogin{UID: "u-k2", Email: "K@x.com", ListsConfigured: true, OwnerEmail: "own@x.com", InviteToken: tok}); err != nil {
		t.Fatalf("login firebase com o e-mail do convite: %v", err)
	}
}

func TestAcceptInviteLocalNameLimit(t *testing.T) {
	e := newEnv(t)
	own := mkUser(t, e, "own@x.com", domain.RoleOwner)
	join(t, e, own, domain.MemberOwner)
	_, tok, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "n@x.com", domain.MemberViewer)
	if _, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, strings.Repeat("a", 81), goodPass); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("nome de 81 caracteres: %v", err)
	}
	if _, _, err := e.svc.AcceptInviteLocal(context.Background(), tok, strings.Repeat("é", 80), goodPass); err != nil {
		t.Fatalf("nome de 80 caracteres: %v", err)
	}
}

// Entrou pelo convite (login firebase cria a conta com invited=true) e nunca o aceitou: sem nenhuma empresa, a
// marca não vale. O próximo login volta a depender das listas e a marca é limpa.
func TestFirebaseInvitedSemEmpresaNaoFicaOrfao(t *testing.T) {
	e := newEnv(t)
	bg := context.Background()
	own := mkUser(t, e, "own@x.com", domain.RoleOwner)
	join(t, e, own, domain.MemberOwner)
	_, tok, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "conv@x.com", domain.MemberViewer)
	in := service.FirebaseLogin{UID: "uid-1", Email: "conv@x.com", ListsConfigured: true, OwnerEmail: "own@x.com", InviteToken: tok}
	u, err := e.svc.LoginFirebase(bg, in)
	if err != nil {
		t.Fatal(err)
	}
	if !invitedFlag(t, e, u.ID) {
		t.Fatal("conta criada pelo convite começa invited")
	}
	// o aceite nunca acontece (aba fechada, convite revogado, erro de rede...)
	in.InviteToken = ""
	if _, err := e.svc.LoginFirebase(bg, in); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("sem empresa e fora das listas: o relogin deve ser recusado, veio %v", err)
	}
	if invitedFlag(t, e, u.ID) {
		t.Fatal("a marca invited órfã deveria ter sido limpa, mesmo com a recusa")
	}

	// quem aceitou (tem empresa) continua admitido
	_, tok2, _ := e.svc.InviteMember(as(own), own.Actor(), e.co.ID, "ok@x.com", domain.MemberViewer)
	in2 := service.FirebaseLogin{UID: "uid-2", Email: "ok@x.com", ListsConfigured: true, OwnerEmail: "own@x.com", InviteToken: tok2}
	u2, err := e.svc.LoginFirebase(bg, in2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AcceptInvite(bg, tok2, u2); err != nil {
		t.Fatal(err)
	}
	in2.InviteToken = ""
	if _, err := e.svc.LoginFirebase(bg, in2); err != nil {
		t.Fatalf("convidado com empresa continua admitido: %v", err)
	}
	if !invitedFlag(t, e, u2.ID) {
		t.Fatal("convidado com empresa mantém invited")
	}
}

// reset-password e remove deixam registro estruturado (quem operou, em quem, o quê), sem a senha; tentativa
// negada também fica registrada.
func TestAdminUserOperationsAreAudited(t *testing.T) {
	e := newEnv(t)
	var buf bytes.Buffer
	e.svc.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	inst := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	alvo := mkUser(t, e, "alvo@x.com", domain.RoleMember)
	plain := mkUser(t, e, "comum@x.com", domain.RoleMember)

	lines := func() []map[string]any {
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if l == "" {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("log não é JSON: %q", l)
			}
			out = append(out, m)
		}
		return out
	}

	if _, _, err := e.svc.AdminResetPassword(as(plain), alvo.Email, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatal(err)
	}
	ls := lines()
	if len(ls) != 1 || ls[0]["action"] != "user.reset_password" || ls[0]["result"] != "denied" || ls[0]["actor_email"] != plain.Email ||
		ls[0]["target_email"] != alvo.Email || ls[0]["level"] != "WARN" {
		t.Fatalf("tentativa negada: %v", ls)
	}

	buf.Reset()
	_, tmp, err := e.svc.AdminResetPassword(as(inst), alvo.Email, "")
	if err != nil || tmp == "" {
		t.Fatalf("reset: %q %v", tmp, err)
	}
	ls = lines()
	if len(ls) != 1 {
		t.Fatalf("reset: %v", ls)
	}
	r := ls[0]
	if r["action"] != "user.reset_password" || r["result"] != "ok" || r["actor_email"] != inst.Email || r["actor_id"] != inst.ID.String() ||
		r["target_email"] != alvo.Email || r["target_id"] != alvo.ID.String() || r["level"] != "INFO" {
		t.Fatalf("reset: %v", r)
	}
	if strings.Contains(buf.String(), tmp) {
		t.Fatal("a senha temporária não pode ir para o log")
	}

	buf.Reset()
	if err := e.svc.AdminRemoveUser(as(inst), alvo.Email); err != nil {
		t.Fatal(err)
	}
	ls = lines()
	if len(ls) != 1 || ls[0]["action"] != "user.remove" || ls[0]["result"] != "ok" || ls[0]["actor_email"] != inst.Email ||
		ls[0]["target_email"] != alvo.Email || ls[0]["target_id"] != alvo.ID.String() {
		t.Fatalf("remove: %v", ls)
	}

	buf.Reset() // recusa de regra de negócio (o dono não é removível) também fica registrada
	if err := e.svc.AdminRemoveUser(as(inst), inst.Email); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if ls = lines(); len(ls) != 1 || ls[0]["result"] != "refused" {
		t.Fatalf("remove recusado: %v", ls)
	}
}
