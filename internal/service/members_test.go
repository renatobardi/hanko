package service_test

import (
	"context"
	"errors"
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
