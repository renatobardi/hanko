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

const goodPass = "uma-senha-bem-longa"

func mustOwner(t *testing.T, e *env) domain.User {
	t.Helper()
	u, err := e.svc.CreateOwner(context.Background(), "Dono@Exemplo.com", "Dona", goodPass)
	if err != nil {
		t.Fatalf("CreateOwner: %v", err)
	}
	return u
}

func TestCreateOwner(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	if u.Role != domain.RoleOwner || u.Provider != domain.ProviderLocal || u.Email != "dono@exemplo.com" || u.Name != "Dona" {
		t.Fatalf("usuário: %+v", u)
	}
	if !strings.HasPrefix(u.PasswordHash, "$argon2id$") || strings.Contains(u.PasswordHash, goodPass) {
		t.Fatalf("senha deve ser guardada como argon2id: %q", u.PasswordHash)
	}
	if _, err := e.svc.CreateOwner(ctx, "outro@x.com", "Outro", goodPass); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("segundo dono: %v", err)
	}
	if n, _ := e.svc.CountUsers(ctx); n != 1 {
		t.Fatalf("usuários: %d", n)
	}
}

func TestCreateOwnerValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, c := range map[string][3]string{
		"e-mail inválido": {"nao-e-email", "N", goodPass},
		"e-mail com nome": {"Ana <ana@x.com>", "N", goodPass},
		"sem nome":        {"a@x.com", "  ", goodPass},
		"senha curta":     {"a@x.com", "N", "curta"},
	} {
		if _, err := e.svc.CreateOwner(ctx, c[0], c[1], c[2]); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n, _ := e.svc.CountUsers(ctx); n != 0 {
		t.Fatalf("nada deveria ter sido criado: %d", n)
	}
}

func TestCreateOwnerRace(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, conflict := 0, 0
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			for try := 0; try < 20; try++ { // cliente real repete no 503 (ErrBusy): o semáforo do argon2id lota sob -race
				if _, err = e.svc.CreateOwner(context.Background(), "x"+string(rune('a'+i))+"@x.com", "N", goodPass); !errors.Is(err, auth.ErrBusy) {
					break
				}
			}
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrConflict):
				conflict++
			default:
				t.Errorf("erro inesperado: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || conflict != 5 {
		t.Fatalf("exatamente um dono: ok=%d conflict=%d", ok, conflict)
	}
}

func TestAuthenticateLocal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mustOwner(t, e)
	if _, err := e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "u1", Email: "fb@x.com", ListsConfigured: true, Admitted: true}); err != nil {
		t.Fatal(err)
	}
	u, err := e.svc.AuthenticateLocal(ctx, " DONO@exemplo.com ", goodPass)
	if err != nil || u.Email != "dono@exemplo.com" {
		t.Fatalf("login: %+v %v", u, err)
	}
	if u.LastLoginAt == nil && func() bool { x, _ := e.svc.GetUserByEmail(ctx, "dono@exemplo.com"); return x.LastLoginAt == nil }() {
		t.Fatal("last_login_at deveria ser preenchido")
	}
	for name, c := range map[string][2]string{
		"senha errada":       {"dono@exemplo.com", "senha-errada-123"},
		"e-mail inexistente": {"ninguem@x.com", goodPass},
		"usuário firebase":   {"fb@x.com", goodPass},
		"vazio":              {"", ""},
	} {
		if _, err := e.svc.AuthenticateLocal(ctx, c[0], c[1]); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestSessionLifecycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }

	tok, sess, err := e.svc.CreateSession(ctx, u.ID, service.SessionMeta{UserAgent: "UA", IP: "1.2.3.4"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) < 43 || !sess.ExpiresAt.Equal(clock.Add(auth.SessionTTL)) || sess.UserAgent != "UA" || sess.IP != "1.2.3.4" {
		t.Fatalf("sessão: %q %+v", tok, sess)
	}
	// só o hash vai ao banco
	var stored string
	if err := e.svc.Pool.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE id=$1`, sess.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == tok || stored != auth.HashToken(tok) {
		t.Fatalf("token_hash inesperado: %q", stored)
	}

	got, err := e.svc.ResolveSession(ctx, tok)
	if err != nil || got.ID != u.ID {
		t.Fatalf("resolver: %v", err)
	}
	expires := func() time.Time {
		var x time.Time
		_ = e.svc.Pool.QueryRow(ctx, `SELECT expires_at FROM sessions WHERE id=$1`, sess.ID).Scan(&x)
		return x
	}

	// dentro de 1 min não renova
	clock = clock.Add(30 * time.Second)
	if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if !expires().Equal(sess.ExpiresAt) {
		t.Fatal("não deveria renovar antes de 1 min")
	}
	// depois de 10 dias renova (deslizante)
	clock = clock.Add(10 * 24 * time.Hour)
	if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if want := clock.Add(auth.SessionTTL); !expires().Equal(want) {
		t.Fatalf("validade deveria deslizar para %v, está %v", want, expires())
	}
	// 29 dias sem uso ainda vale; passados 30 expira
	clock = clock.Add(29 * 24 * time.Hour)
	if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
		t.Fatalf("29 dias depois ainda deveria valer: %v", err)
	}
	clock = clock.Add(31 * 24 * time.Hour)
	if _, err := e.svc.ResolveSession(ctx, tok); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expirada: %v", err)
	}
	// token desconhecido e vazio
	for _, bad := range []string{"", "x", tok + "x"} {
		if _, err := e.svc.ResolveSession(ctx, bad); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSessionEndAndRotate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)

	a, _, _ := e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, "")
	b, _, err := e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, a) // login troca de privilégio: rotaciona
	if err != nil || a == b {
		t.Fatalf("rotação: %v", err)
	}
	if _, err := e.svc.ResolveSession(ctx, a); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("a sessão antiga deveria ter sido removida: %v", err)
	}
	if _, err := e.svc.ResolveSession(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EndSession(ctx, b); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ResolveSession(ctx, b); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("sair: %v", err)
	}
	if err := e.svc.EndSession(ctx, b); err != nil || e.svc.EndSession(ctx, "") != nil {
		t.Fatal("EndSession deve ser idempotente")
	}
}

func TestSessionPurgesExpired(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	_, _, _ = e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, "")
	clock = clock.Add(31 * 24 * time.Hour)
	_, _, _ = e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, "")
	var n int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&n)
	if n != 1 {
		t.Fatalf("sessões expiradas deveriam ser removidas ao abrir outra: %d", n)
	}
}

func TestAPIKeys(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }

	if _, _, err := e.svc.CreateAPIKey(ctx, u.ID, "  "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("nome vazio: %v", err)
	}
	key, k, err := e.svc.CreateAPIKey(ctx, u.ID, "notebook")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "hk_") || !strings.HasPrefix(key, k.Prefix) || k.Name != "notebook" || !k.Active() {
		t.Fatalf("chave: %q %+v", key, k)
	}
	var hash string
	_ = e.svc.Pool.QueryRow(ctx, `SELECT hash FROM api_keys WHERE id=$1`, k.ID).Scan(&hash)
	if hash != auth.HashToken(key) || strings.Contains(hash, key) {
		t.Fatalf("só o hash deve ser guardado: %q", hash)
	}

	got, err := e.svc.ResolveAPIKey(ctx, key)
	if err != nil || got.ID != u.ID {
		t.Fatalf("resolver: %v", err)
	}
	keys, _ := e.svc.ListAPIKeys(ctx, u.ID)
	if len(keys) != 1 || keys[0].LastUsedAt == nil || !keys[0].LastUsedAt.Equal(clock) {
		t.Fatalf("last_used_at: %+v", keys)
	}
	// 1×/min
	clock = clock.Add(20 * time.Second)
	_, _ = e.svc.ResolveAPIKey(ctx, key)
	keys, _ = e.svc.ListAPIKeys(ctx, u.ID)
	if !keys[0].LastUsedAt.Equal(clock.Add(-20 * time.Second)) {
		t.Fatal("last_used_at só deveria avançar após 1 min")
	}
	clock = clock.Add(2 * time.Minute)
	_, _ = e.svc.ResolveAPIKey(ctx, key)
	keys, _ = e.svc.ListAPIKeys(ctx, u.ID)
	if !keys[0].LastUsedAt.Equal(clock) {
		t.Fatal("last_used_at deveria avançar")
	}

	for _, bad := range []string{"", "hk_", "hk_nada", "Bearer " + key, key + "x"} {
		if _, err := e.svc.ResolveAPIKey(ctx, bad); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Errorf("%q: %v", bad, err)
		}
	}

	// outro usuário não revoga a chave alheia
	other, err := e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "u2", Email: "m@x.com", ListsConfigured: true, Admitted: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevokeAPIKey(ctx, other.ID, k.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revogar chave alheia: %v", err)
	}
	if _, err := e.svc.ResolveAPIKey(ctx, key); err != nil {
		t.Fatal("chave alheia não pode ter sido revogada")
	}
	rk, err := e.svc.RevokeAPIKey(ctx, u.ID, k.ID)
	if err != nil || rk.RevokedAt == nil {
		t.Fatalf("revogar: %v", err)
	}
	if _, err := e.svc.ResolveAPIKey(ctx, key); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("chave revogada: %v", err)
	}
	if _, err := e.svc.RevokeAPIKey(ctx, u.ID, k.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revogar de novo: %v", err)
	}
	keys, _ = e.svc.ListAPIKeys(ctx, u.ID)
	if len(keys) != 1 || keys[0].Active() {
		t.Fatalf("lista deve manter a revogada: %+v", keys)
	}
}

func TestLoginFirebasePolicy(t *testing.T) {
	ctx := context.Background()
	fb := func(uid, email string, lists, admitted bool) service.FirebaseLogin {
		return service.FirebaseLogin{UID: uid, Email: email, Name: "N " + uid, ListsConfigured: lists, Admitted: admitted}
	}

	t.Run("sem listas: primeiro vira owner, demais recusados", func(t *testing.T) {
		e := newEnv(t)
		a, err := e.svc.LoginFirebase(ctx, fb("a", "a@x.com", false, false))
		if err != nil || a.Role != domain.RoleOwner || a.Provider != domain.ProviderFirebase || a.ExternalID != "a" || a.PasswordHash != "" {
			t.Fatalf("primeiro: %+v %v", a, err)
		}
		if _, err := e.svc.LoginFirebase(ctx, fb("b", "b@x.com", false, false)); !errors.Is(err, domain.ErrNotAllowed) {
			t.Fatalf("segundo sem lista: %v", err)
		}
		again, err := e.svc.LoginFirebase(ctx, fb("a", "a@x.com", false, false))
		if err != nil || again.ID != a.ID {
			t.Fatalf("o dono volta a entrar: %v", err)
		}
		if n, _ := e.svc.CountUsers(ctx); n != 1 {
			t.Fatalf("usuários: %d", n)
		}
	})

	t.Run("com listas: só admitidos; primeiro admitido é owner", func(t *testing.T) {
		e := newEnv(t)
		if _, err := e.svc.LoginFirebase(ctx, fb("x", "x@fora.com", true, false)); !errors.Is(err, domain.ErrNotAllowed) {
			t.Fatalf("fora da lista: %v", err)
		}
		a, err := e.svc.LoginFirebase(ctx, fb("a", "a@x.com", true, true))
		if err != nil || a.Role != domain.RoleOwner {
			t.Fatalf("primeiro admitido: %+v %v", a, err)
		}
		b, err := e.svc.LoginFirebase(ctx, fb("b", "b@x.com", true, true))
		if err != nil || b.Role != domain.RoleMember {
			t.Fatalf("segundo admitido: %+v %v", b, err)
		}
		// removido da lista perde o acesso
		if _, err := e.svc.LoginFirebase(ctx, fb("b", "b@x.com", true, false)); !errors.Is(err, domain.ErrNotAllowed) {
			t.Fatalf("removido da lista: %v", err)
		}
	})

	t.Run("atualiza e-mail e nome; e-mail alheio é conflito", func(t *testing.T) {
		e := newEnv(t)
		a, _ := e.svc.LoginFirebase(ctx, fb("a", "a@x.com", true, true))
		_, _ = e.svc.LoginFirebase(ctx, fb("b", "b@x.com", true, true))
		up, err := e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "a", Email: "Novo@X.com", Name: "", ListsConfigured: true, Admitted: true})
		if err != nil || up.ID != a.ID || up.Email != "novo@x.com" || up.Name != "N a" {
			t.Fatalf("atualização: %+v %v", up, err)
		}
		if _, err := e.svc.LoginFirebase(ctx, service.FirebaseLogin{UID: "b", Email: "novo@x.com", ListsConfigured: true, Admitted: true}); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("e-mail em uso: %v", err)
		}
	})

	t.Run("e-mail de usuário local não é vinculado", func(t *testing.T) {
		e := newEnv(t)
		mustOwner(t, e)
		if _, err := e.svc.LoginFirebase(ctx, fb("z", "dono@exemplo.com", true, true)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("sem vínculo automático: %v", err)
		}
		// outro uid, mesmo e-mail de um usuário firebase
		e2 := newEnv(t)
		_, _ = e2.svc.LoginFirebase(ctx, fb("a", "a@x.com", true, true))
		if _, err := e2.svc.LoginFirebase(ctx, fb("a2", "A@x.com", true, true)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("mesmo e-mail, uid diferente: %v", err)
		}
	})

	t.Run("e-mail inválido", func(t *testing.T) {
		e := newEnv(t)
		if _, err := e.svc.LoginFirebase(ctx, fb("a", "sem-arroba", true, true)); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("got %v", err)
		}
	})
}

// CheckSession revalida sem efeito colateral: N pings não mudam expires_at nem last_seen_at; ResolveSession (uso
// de verdade) continua renovando.
func TestCheckSessionNaoRenova(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	tok, sess, err := e.svc.CreateSession(ctx, u.ID, service.SessionMeta{}, "")
	if err != nil {
		t.Fatal(err)
	}
	state := func() (exp, seen time.Time) {
		if err := e.svc.Pool.QueryRow(ctx, `SELECT expires_at, last_seen_at FROM sessions WHERE id=$1`, sess.ID).Scan(&exp, &seen); err != nil {
			t.Fatal(err)
		}
		return
	}
	exp0, seen0 := state()

	for range 5 { // 5 pings, cada um bem depois de 1 min (quando ResolveSession renovaria)
		clock = clock.Add(15 * time.Minute)
		if got, err := e.svc.CheckSession(ctx, tok); err != nil || got.ID != u.ID {
			t.Fatalf("CheckSession: %v", err)
		}
	}
	if exp, seen := state(); !exp.Equal(exp0) || !seen.Equal(seen0) {
		t.Fatalf("CheckSession mudou a sessão: expires %v -> %v, last_seen %v -> %v", exp0, exp, seen0, seen)
	}
	// controle: o uso real renova
	if _, err := e.svc.ResolveSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if exp, _ := state(); !exp.After(exp0) {
		t.Fatal("ResolveSession deveria renovar a validade")
	}
	// e o Check continua recusando o que não vale
	clock = clock.Add(60 * 24 * time.Hour)
	if _, err := e.svc.CheckSession(ctx, tok); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("expirada: %v", err)
	}
	if _, err := e.svc.CheckSession(ctx, ""); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("vazio: %v", err)
	}
}

// CheckAPIKey não anota o último uso.
func TestCheckAPIKeyNaoAnotaUso(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := mustOwner(t, e)
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return clock }
	key, k, err := e.svc.CreateAPIKey(ctx, u.ID, "ping")
	if err != nil {
		t.Fatal(err)
	}
	lastUsed := func() *time.Time {
		var x *time.Time
		if err := e.svc.Pool.QueryRow(ctx, `SELECT last_used_at FROM api_keys WHERE id=$1`, k.ID).Scan(&x); err != nil {
			t.Fatal(err)
		}
		return x
	}
	for range 5 {
		clock = clock.Add(10 * time.Minute)
		if _, err := e.svc.CheckAPIKey(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if lastUsed() != nil {
		t.Fatal("CheckAPIKey anotou last_used_at")
	}
	if _, err := e.svc.ResolveAPIKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	if lastUsed() == nil {
		t.Fatal("ResolveAPIKey deveria anotar o uso")
	}
	if _, err := e.svc.CheckAPIKey(ctx, "hk_inexistente"); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("chave inválida: %v", err)
	}
}
