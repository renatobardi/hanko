package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/auth/passkey"
	"github.com/renatobardi/hanko/internal/auth/passkey/authtest"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// fakeIDVerifier é o verificador do Firebase nos testes: devolve as claims que o teste montou.
type fakeIDVerifier struct {
	claims auth.FirebaseClaims
	err    error
}

func (f fakeIDVerifier) Verify(context.Context, string) (auth.FirebaseClaims, error) {
	return f.claims, f.err
}

// reauthFixture: serviço com relógio controlado, um dono com senha e uma empresa dele (onde os fatos caem).
type reauthFixture struct {
	e     *env
	clk   *time.Time
	owner domain.User
	co    domain.Company
}

func newReauthFixture(t *testing.T) *reauthFixture {
	t.Helper()
	e := newEnv(t)
	clk := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clk }
	ctx := context.Background()
	owner, err := e.svc.CreateOwner(ctx, "dona@exemplo.com", "Dona", goodPass)
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	// Com a identidade no contexto, quem cria a empresa vira o dono dela (companies.go).
	asOwner := auth.WithIdentity(ctx, auth.Identity{User: owner, Via: auth.ViaSession})
	co, err := e.svc.CreateCompany(asOwner, owner.Actor(), service.CreateCompanyInput{Name: "Dona Ltda"})
	if err != nil {
		t.Fatalf("create company: %v", err)
	}
	return &reauthFixture{e: e, clk: &clk, owner: owner, co: co}
}

func (f *reauthFixture) advance(d time.Duration) { *f.clk = f.clk.Add(d) }

// identityOf monta a identidade que o Guard montaria para o token: o estado da sessão é lido agora.
func (f *reauthFixture) identityOf(t *testing.T, tok string) auth.Identity {
	t.Helper()
	u, info, err := f.e.svc.ResolveSessionInfo(context.Background(), tok)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	return auth.Identity{User: u, Via: auth.ViaSession, Session: info}
}

// session abre uma sessão para u e devolve o token e a identidade.
func (f *reauthFixture) session(t *testing.T, u domain.User) (string, auth.Identity) {
	t.Helper()
	tok, _, err := f.e.svc.CreateSession(context.Background(), u.ID, service.SessionMeta{}, "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return tok, f.identityOf(t, tok)
}

// provedSession abre uma sessão já reautenticada por senha e devolve a identidade com essa prova.
func (f *reauthFixture) provedSession(t *testing.T) (string, auth.Identity) {
	t.Helper()
	tok, id := f.session(t, f.owner)
	if _, err := f.e.svc.ReauthWithPassword(context.Background(), id, goodPass); err != nil {
		t.Fatalf("prova por senha: %v", err)
	}
	return tok, f.identityOf(t, tok)
}

// passkeyOn liga a passkey no serviço (RP ID localhost) e devolve o autenticador de software que responde por ela.
func (f *reauthFixture) passkeyOn(t *testing.T) *authtest.Authenticator {
	t.Helper()
	pk, err := passkey.Config{RPID: "localhost", Origins: []string{localOrigin}}.New()
	if err != nil {
		t.Fatal(err)
	}
	f.e.svc.Passkeys = pk
	a := authtest.New()
	a.Origin = localOrigin
	return a
}

const localOrigin = "https://localhost:8080"

// requireReauth confere que a ação sensível é recusada pedindo prova, com a janela padrão de cinco minutos.
func requireReauth(t *testing.T, svc *service.Service, id auth.Identity) {
	t.Helper()
	_, err := svc.RequireReauth(id)
	var re *domain.ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("RequireReauth = %v, quer ReauthError", err)
	}
	if re.WindowMinutes != 5 {
		t.Fatalf("janela = %d min, quer 5", re.WindowMinutes)
	}
	if !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("ReauthError precisa valer como ErrUnauthenticated (401)")
	}
}

func TestSensivelSemProvaRecentePedeReauth(t *testing.T) {
	f := newReauthFixture(t)
	_, id := f.session(t, f.owner)
	requireReauth(t, f.e.svc, id)
}

func TestProvaSenhaVale5MinutosEDepoisPedeDeNovo(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	tok, id := f.session(t, f.owner)

	proof, err := f.e.svc.ReauthWithPassword(ctx, id, goodPass)
	if err != nil {
		t.Fatalf("ReauthWithPassword: %v", err)
	}
	if proof.Method != "password" || !proof.ValidUntil.Equal(proof.At.Add(5*time.Minute)) {
		t.Fatalf("prova = %+v", proof)
	}
	grant, err := f.e.svc.RequireReauth(f.identityOf(t, tok))
	if err != nil || grant.Via != "session" || grant.Method != "password" {
		t.Fatalf("RequireReauth após a prova = %+v, %v", grant, err)
	}

	f.advance(4*time.Minute + 59*time.Second)
	if _, err := f.e.svc.RequireReauth(f.identityOf(t, tok)); err != nil {
		t.Fatalf("aos 4m59s a prova ainda vale: %v", err)
	}
	f.advance(2 * time.Second)
	requireReauth(t, f.e.svc, f.identityOf(t, tok))
}

func TestSenhaErradaContaNoLimiteDoLogin(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	f.e.svc.ReauthLimiter = &auth.LoginLimiter{}
	_, id := f.session(t, f.owner)

	for i := 0; i < 5; i++ {
		if _, err := f.e.svc.ReauthWithPassword(ctx, id, "senha-errada-xx"); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatalf("tentativa %d com senha errada = %v, quer ErrUnauthenticated", i+1, err)
		}
	}
	if _, err := f.e.svc.ReauthWithPassword(ctx, id, goodPass); !errors.Is(err, domain.ErrTooManyAttempts) {
		t.Fatalf("senha certa depois do limite = %v, quer ErrTooManyAttempts", err)
	}
	requireReauth(t, f.e.svc, id)
}

func TestProvaFirebaseExigeUidEAuthTimeRecente(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	fb, err := f.e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "fb-1", Email: "fb@exemplo.com", Name: "Fb",
		ListsConfigured: true, Admitted: true})
	if err != nil {
		t.Fatalf("LoginFirebase: %v", err)
	}
	_, id := f.session(t, fb)

	f.e.svc.IDVerifier = fakeIDVerifier{claims: auth.FirebaseClaims{UID: "fb-1", AuthTime: f.clk.Add(-time.Minute)}}
	proof, err := f.e.svc.ReauthWithFirebase(ctx, id, "id-token")
	if err != nil || proof.Method != "firebase" {
		t.Fatalf("prova firebase com auth_time recente = %+v, %v", proof, err)
	}

	// Token de outro usuário do Firebase: não prova esta conta.
	f.e.svc.IDVerifier = fakeIDVerifier{claims: auth.FirebaseClaims{UID: "fb-outro", AuthTime: f.clk.Add(-time.Minute)}}
	if _, err := f.e.svc.ReauthWithFirebase(ctx, id, "id-token"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("uid de outra conta = %v, quer ErrUnauthenticated", err)
	}

	// auth_time antigo (login de mais de 5 min): pede prova de novo, em vez de aceitar.
	f.e.svc.IDVerifier = fakeIDVerifier{claims: auth.FirebaseClaims{UID: "fb-1", AuthTime: f.clk.Add(-6 * time.Minute)}}
	_, id = f.session(t, fb)
	_, err = f.e.svc.ReauthWithFirebase(ctx, id, "id-token")
	var re *domain.ReauthError
	if !errors.As(err, &re) {
		t.Fatalf("auth_time vencido = %v, quer ReauthError", err)
	}
}

func TestProvaSenhaSoNoProviderLocal(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	fb, err := f.e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "fb-2", Email: "fb2@exemplo.com", Name: "Fb2",
		ListsConfigured: true, Admitted: true})
	if err != nil {
		t.Fatalf("LoginFirebase: %v", err)
	}
	_, id := f.session(t, fb)
	if _, err := f.e.svc.ReauthWithPassword(ctx, id, goodPass); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("senha de conta firebase = %v, quer ErrUnauthenticated", err)
	}
}

func TestPasskeyProvaCadastroEDesafioDeUsoUnico(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	a := f.passkeyOn(t)

	tok, id := f.session(t, f.owner)
	if _, err := f.e.svc.BeginPasskeyRegistration(ctx, id); err == nil {
		t.Fatal("cadastrar passkey sem prova recente deve pedir reautenticação")
	} else {
		requireReauth(t, f.e.svc, id)
	}
	if _, err := f.e.svc.ReauthWithPassword(ctx, id, goodPass); err != nil {
		t.Fatalf("prova por senha: %v", err)
	}
	id = f.identityOf(t, tok)

	reg, err := f.e.svc.BeginPasskeyRegistration(ctx, id)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	resp, err := a.Create(reg.Options)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.svc.FinishPasskeyRegistration(ctx, id, reg.ID, "Notebook", resp)
	if err != nil {
		t.Fatalf("FinishPasskeyRegistration: %v", err)
	}
	list, err := f.e.svc.ListPasskeys(ctx, f.owner.ID)
	if err != nil || len(list) != 1 || list[0].ID != p.ID || list[0].Name != "Notebook" {
		t.Fatalf("ListPasskeys = %+v, %v", list, err)
	}

	// Prova por passkey: as opções vêm em ReauthOptionsFor, com o desafio que o navegador assina.
	opts, err := f.e.svc.ReauthOptionsFor(ctx, id)
	if err != nil {
		t.Fatalf("ReauthOptionsFor: %v", err)
	}
	if opts.Passkey == nil || !hasMethod(opts.Methods, "passkey") || !hasMethod(opts.Methods, "password") {
		t.Fatalf("opções de prova = %+v", opts)
	}
	assertion, err := a.Get(opts.Passkey.Options)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.e.svc.FinishPasskeyReauth(ctx, id, opts.Passkey.ID, assertion)
	if err != nil || proof.Method != "passkey" {
		t.Fatalf("FinishPasskeyReauth = %+v, %v", proof, err)
	}

	// Desafio de uso único: a mesma resposta, de novo, não passa.
	if _, err := f.e.svc.FinishPasskeyReauth(ctx, id, opts.Passkey.ID, assertion); err == nil {
		t.Fatal("desafio já usado não pode servir a segunda prova")
	}

	if err := f.e.svc.DeletePasskey(ctx, f.identityOf(t, tok), p.ID); err != nil {
		t.Fatalf("DeletePasskey com prova recente: %v", err)
	}
}

func TestRemoverPasskeySemProvaPedeReauth(t *testing.T) {
	f := newReauthFixture(t)
	_, id := f.session(t, f.owner)
	requireReauth(t, f.e.svc, id)
	if err := f.e.svc.DeletePasskey(context.Background(), id, domain.NewID()); err == nil {
		t.Fatal("remover passkey sem prova recente deve pedir reautenticação")
	}
}

func TestAPIKeyReadRecusadaEFullPassaComIdDaChaveNoGrant(t *testing.T) {
	f := newReauthFixture(t)

	read := auth.Identity{User: f.owner, Via: auth.ViaAPIKey, KeyScope: domain.KeyScopeRead, KeyID: ptrID(domain.NewID())}
	if _, err := f.e.svc.RequireReauth(read); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("API key read em ação sensível = %v, quer ErrNotAllowed (403)", err)
	}

	kid := domain.NewID()
	full := auth.Identity{User: f.owner, Via: auth.ViaAPIKey, KeyScope: domain.KeyScopeFull, KeyID: &kid}
	grant, err := f.e.svc.RequireReauth(full)
	if err != nil || grant.Via != "api_key" || grant.KeyID == nil || *grant.KeyID != kid {
		t.Fatalf("API key full = %+v, %v", grant, err)
	}
}

// Passkey é credencial de sessão: a API key, mesmo full, não cadastra nem remove (achado 1 da auditoria #70).
func TestAPIKeyFullNaoCadastraNemRemovePasskey(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	a := f.passkeyOn(t)

	// Uma sessão com reautenticação recente cria a passkey: o desafio fica preso a essa sessão.
	_, id := f.provedSession(t)
	reg, err := f.e.svc.BeginPasskeyRegistration(ctx, id)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration por sessão: %v", err)
	}
	resp, err := a.Create(reg.Options)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.e.svc.FinishPasskeyRegistration(ctx, id, reg.ID, "Notebook", resp)
	if err != nil {
		t.Fatalf("FinishPasskeyRegistration por sessão: %v", err)
	}

	kid := domain.NewID()
	full := auth.Identity{User: f.owner, Via: auth.ViaAPIKey, KeyScope: domain.KeyScopeFull, KeyID: &kid}
	if _, err := f.e.svc.BeginPasskeyRegistration(ctx, full); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("BeginPasskeyRegistration com API key full = %v, quer ErrNotAllowed (403)", err)
	}
	if _, err := f.e.svc.FinishPasskeyRegistration(ctx, full, reg.ID, "CLI", resp); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("FinishPasskeyRegistration com API key full = %v, quer ErrNotAllowed (403)", err)
	}
	if err := f.e.svc.DeletePasskey(ctx, full, p.ID); !errors.Is(err, domain.ErrNotAllowed) {
		t.Fatalf("DeletePasskey com API key full = %v, quer ErrNotAllowed (403)", err)
	}

	// A passkey continua lá: a chave full não a removeu.
	lista, err := f.e.svc.ListPasskeys(ctx, f.owner.ID)
	if err != nil || len(lista) != 1 {
		t.Fatalf("passkeys após tentativa com API key = %d, %v; quer 1", len(lista), err)
	}
}

func TestProvaRegistraFatoNaEmpresaDoUsuario(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	_, id := f.session(t, f.owner)
	if _, err := f.e.svc.ReauthWithPassword(ctx, id, goodPass); err != nil {
		t.Fatalf("prova: %v", err)
	}
	var method string
	err := f.e.svc.Pool.QueryRow(ctx, `SELECT data->>'method' FROM activity_events WHERE verb = 'auth.reauthenticated' AND company_id = $1`,
		f.co.ID).Scan(&method)
	if err != nil || method != "password" {
		t.Fatalf("fato auth.reauthenticated = %q, %v", method, err)
	}
}

// Com a passkey desligada (HANKO_PUBLIC_URL sem RP ID válido), quem já tem passkey recebe as outras provas, sem erro
// e sem desafio de passkey; o cadastro responde ErrPasskeyOff.
func TestOpcoesDeProvaSemPasskeyLigada(t *testing.T) {
	f := newReauthFixture(t)
	ctx := context.Background()
	a := f.passkeyOn(t)
	_, id := f.provedSession(t)
	reg, err := f.e.svc.BeginPasskeyRegistration(ctx, id)
	if err != nil {
		t.Fatalf("BeginPasskeyRegistration: %v", err)
	}
	resp, err := a.Create(reg.Options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.svc.FinishPasskeyRegistration(ctx, id, reg.ID, "Notebook", resp); err != nil {
		t.Fatalf("FinishPasskeyRegistration: %v", err)
	}

	f.e.svc.Passkeys = nil
	opts, err := f.e.svc.ReauthOptionsFor(ctx, id)
	if err != nil {
		t.Fatalf("ReauthOptionsFor com passkey desligada = %v, quer opções sem erro", err)
	}
	if hasMethod(opts.Methods, "passkey") || opts.Passkey != nil {
		t.Fatalf("passkey oferecida com o serviço desligado: %+v", opts)
	}
	if !hasMethod(opts.Methods, "password") {
		t.Fatalf("senha tem que continuar oferecida: %+v", opts.Methods)
	}
	if _, err := f.e.svc.BeginPasskeyRegistration(ctx, id); !errors.Is(err, service.ErrPasskeyOff) {
		t.Fatalf("cadastro com passkey desligada = %v, quer ErrPasskeyOff", err)
	}
}

func hasMethod(ms []string, want string) bool {
	for _, m := range ms {
		if m == want {
			return true
		}
	}
	return false
}

func ptrID(id domain.ID) *domain.ID { return &id }
