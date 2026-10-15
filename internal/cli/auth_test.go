package cli

import (
	"net/http"

	"context"
	"errors"
	"fmt"
	"github.com/renatobardi/hanko/internal/api"
	"strings"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
)

// fakeAdmin é um AuthAdmin em memória.
type fakeAdmin struct {
	users    []domain.User
	keys     []domain.APIKey
	closed   int
	gotPW    string
	gotAs    string
	removed  []string
	err      error
	audit    []domain.AdminAuditEntry
	gotLimit int
}

func (f *fakeAdmin) CreateOwner(_ context.Context, email, name, password string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	if len(f.users) > 0 {
		return domain.User{}, fmt.Errorf("criar dono: %w", domain.ErrConflict)
	}
	f.gotPW = password
	u := domain.User{ID: domain.NewID(), Email: strings.ToLower(email), Name: name, Role: domain.RoleOwner, Provider: domain.ProviderLocal}
	f.users = append(f.users, u)
	return u, nil
}
func (f *fakeAdmin) ListUsers(context.Context) ([]domain.User, error) { return f.users, nil }
func (f *fakeAdmin) GetUserByEmail(_ context.Context, email string) (domain.User, error) {
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return domain.User{}, fmt.Errorf("get user: %w", domain.ErrNotFound)
}
func (f *fakeAdmin) CreateAPIKey(_ context.Context, uid domain.ID, name string) (string, domain.APIKey, error) {
	k := domain.APIKey{ID: domain.NewID(), UserID: uid, Name: name, Prefix: "hk_abcdef", CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	f.keys = append(f.keys, k)
	return "hk_abcdef-SEGREDO-completo", k, nil
}
func (f *fakeAdmin) ListAPIKeys(_ context.Context, uid domain.ID) ([]domain.APIKey, error) {
	var out []domain.APIKey
	for _, k := range f.keys {
		if k.UserID == uid {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeAdmin) RevokeAPIKey(_ context.Context, uid, kid domain.ID) (domain.APIKey, error) {
	for i := range f.keys {
		if f.keys[i].ID == kid && f.keys[i].UserID == uid && f.keys[i].RevokedAt == nil {
			now := time.Now()
			f.keys[i].RevokedAt = &now
			return f.keys[i], nil
		}
	}
	return domain.APIKey{}, domain.ErrNotFound
}
func (f *fakeAdmin) AdminListUsers(_ context.Context, as string) ([]domain.User, error) {
	f.gotAs = as
	return f.users, f.err
}
func (f *fakeAdmin) AdminResetPassword(_ context.Context, as, email, pw string) (string, error) {
	f.gotAs, f.gotPW = as, pw
	if f.err != nil {
		return "", f.err
	}
	if pw != "" {
		return "", nil
	}
	return "temp-gerada-unica", nil
}
func (f *fakeAdmin) AdminRemoveUser(_ context.Context, as, email string) error {
	f.gotAs = as
	if f.err == nil {
		f.removed = append(f.removed, email)
	}
	return f.err
}
func (f *fakeAdmin) AdminListAudit(_ context.Context, as string, limit int) ([]domain.AdminAuditEntry, error) {
	f.gotAs, f.gotLimit = as, limit
	return f.audit, f.err
}
func (f *fakeAdmin) Close() { f.closed++ }

func authRT(f *fakeAdmin) Runtime {
	return Runtime{Auth: func(context.Context) (AuthAdmin, error) { return f, nil }}
}

func TestAuthCreateOwner(t *testing.T) {
	t.Run("senha por env", func(t *testing.T) {
		f := &fakeAdmin{}
		code, out, errOut := exec(t, authRT(f), map[string]string{ownerPasswordEnv: "senha-do-env-123"}, "", "auth", "create-owner", "--email", "Dona@X.com", "--name", "Dona")
		if code != 0 || f.gotPW != "senha-do-env-123" || !strings.Contains(out, "Dono criado: Dona <dona@x.com>") || f.closed != 1 {
			t.Fatalf("%d out=%q err=%q pw=%q closed=%d", code, out, errOut, f.gotPW, f.closed)
		}
		if strings.Contains(out+errOut, "senha-do-env-123") {
			t.Fatal("a senha nunca pode aparecer na saída")
		}
	})
	t.Run("senha por stdin", func(t *testing.T) {
		f := &fakeAdmin{}
		code, _, errOut := exec(t, authRT(f), nil, "senha-da-stdin-1\nresto\n", "auth", "create-owner", "--email", "a@x.com", "--name", "A")
		if code != 0 || f.gotPW != "senha-da-stdin-1" {
			t.Fatalf("%d %q pw=%q", code, errOut, f.gotPW)
		}
	})
	t.Run("sem senha", func(t *testing.T) {
		code, _, errOut := exec(t, authRT(&fakeAdmin{}), nil, "", "auth", "create-owner", "--email", "a@x.com", "--name", "A")
		if code != 2 || !strings.Contains(errOut, ownerPasswordEnv) {
			t.Fatalf("%d %q", code, errOut)
		}
	})
	t.Run("flags obrigatórias", func(t *testing.T) {
		if code, _, _ := exec(t, authRT(&fakeAdmin{}), map[string]string{ownerPasswordEnv: "x"}, "", "auth", "create-owner", "--email", "a@x.com"); code != 2 {
			t.Fatalf("sem --name: %d", code)
		}
	})
	t.Run("já existe usuário", func(t *testing.T) {
		f := &fakeAdmin{users: []domain.User{{ID: domain.NewID(), Email: "x@x.com"}}}
		code, _, errOut := exec(t, authRT(f), map[string]string{ownerPasswordEnv: "senha-do-env-123"}, "", "auth", "create-owner", "--email", "a@x.com", "--name", "A")
		if code != 1 || !strings.Contains(errOut, "já existe um usuário") {
			t.Fatalf("%d %q", code, errOut)
		}
	})
	t.Run("sem runtime", func(t *testing.T) {
		code, _, errOut := exec(t, Runtime{}, map[string]string{ownerPasswordEnv: "x"}, "", "auth", "create-owner", "--email", "a@x.com", "--name", "A")
		if code != 1 || !strings.Contains(errOut, "runtime não conectado") {
			t.Fatalf("%d %q", code, errOut)
		}
	})
}

func TestAuthTokenLifecycle(t *testing.T) {
	owner := domain.User{ID: domain.NewID(), Email: "dona@x.com", Name: "Dona"}
	f := &fakeAdmin{users: []domain.User{owner}}
	rt := authRT(f)

	code, out, errOut := exec(t, rt, nil, "", "auth", "token", "create", "--name", "notebook")
	if code != 0 || strings.TrimSpace(out) != "hk_abcdef-SEGREDO-completo" || !strings.Contains(errOut, "não será exibida de novo") {
		t.Fatalf("create: %d out=%q err=%q", code, out, errOut)
	}
	if code, _, _ := exec(t, rt, nil, "", "auth", "token", "create"); code != 2 {
		t.Fatalf("--name é obrigatório: %d", code)
	}

	code, out, _ = exec(t, rt, nil, "", "auth", "token", "list")
	if code != 0 || !strings.Contains(out, "notebook") || !strings.Contains(out, "hk_abcdef…") || !strings.Contains(out, "ativa") || strings.Contains(out, "SEGREDO") {
		t.Fatalf("list: %d %q", code, out)
	}

	kid := f.keys[0].ID.String()
	code, out, _ = exec(t, rt, nil, "", "auth", "token", "revoke", kid)
	if code != 0 || !strings.Contains(out, `"notebook" revogada`) {
		t.Fatalf("revoke: %d %q", code, out)
	}
	if code, _, errOut := exec(t, rt, nil, "", "auth", "token", "revoke", kid); code != 1 || !strings.Contains(errOut, "não encontrado") {
		t.Fatalf("revogar de novo: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, rt, nil, "", "auth", "token", "revoke", "nao-e-id"); code != 2 {
		t.Fatalf("id inválido: %d", code)
	}
	_, out, _ = exec(t, rt, nil, "", "auth", "token", "list")
	if !strings.Contains(out, "revogada") {
		t.Fatalf("list após revogar: %q", out)
	}
	_, out, _ = exec(t, rt, nil, "", "auth", "token", "list", "--json")
	if !strings.Contains(out, `"revoked_at"`) {
		t.Fatalf("list --json: %q", out)
	}
	if f.closed == 0 {
		t.Fatal("a conexão deve ser fechada")
	}
}

func TestAuthTokenOwnerSelection(t *testing.T) {
	a := domain.User{ID: domain.NewID(), Email: "a@x.com"}
	b := domain.User{ID: domain.NewID(), Email: "b@x.com"}

	f := &fakeAdmin{users: []domain.User{a, b}}
	code, _, errOut := exec(t, authRT(f), nil, "", "auth", "token", "create", "--name", "k")
	if code != 2 || !strings.Contains(errOut, "--email") {
		t.Fatalf("vários usuários exigem --email: %d %q", code, errOut)
	}
	if code, _, _ := exec(t, authRT(f), nil, "", "auth", "token", "create", "--name", "k", "--email", "B@x.com"); code != 0 || f.keys[0].UserID != b.ID {
		t.Fatalf("--email deveria escolher o usuário b: %d", code)
	}
	if code, _, errOut := exec(t, authRT(f), nil, "", "auth", "token", "create", "--name", "k", "--email", "z@x.com"); code != 1 || !strings.Contains(errOut, "não encontrado") {
		t.Fatalf("usuário inexistente: %d %q", code, errOut)
	}
	empty := &fakeAdmin{}
	if code, _, errOut := exec(t, authRT(empty), nil, "", "auth", "token", "list"); code != 1 || !strings.Contains(errOut, "create-owner") {
		t.Fatalf("sem usuários: %d %q", code, errOut)
	}
	boom := Runtime{Auth: func(context.Context) (AuthAdmin, error) { return nil, errors.New("banco fora") }}
	if code, _, errOut := exec(t, boom, nil, "", "auth", "token", "list"); code != 1 || !strings.Contains(errOut, "banco fora") {
		t.Fatalf("erro ao abrir: %d %q", code, errOut)
	}
}

func TestClienteEnviaAPIKeyComoBearer(t *testing.T) {
	ts, f := newFakeAPI(t, map[string]http.HandlerFunc{"GET /api/v1/companies": jsonResp(200, list[api.Company]())})
	code, _, errOut := exec(t, Runtime{}, map[string]string{"HANKO_TOKEN": "hk_minha-chave", "HANKO_URL": ts.URL}, "", "company", "list")
	if code != 0 {
		t.Fatalf("%d %s", code, errOut)
	}
	if got := f.last().Auth; got != "Bearer hk_minha-chave" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestAuthUser(t *testing.T) {
	f := &fakeAdmin{users: []domain.User{{ID: domain.NewID(), Email: "dono@x.com", Name: "Dono", Role: domain.RoleOwner, Provider: domain.ProviderLocal}}}
	code, out, errOut := exec(t, authRT(f), nil, "", "auth", "user", "list", "--as", "dono@x.com")
	if code != 0 || !strings.Contains(out, "dono@x.com") || f.gotAs != "dono@x.com" {
		t.Fatalf("list: %d %q %q as=%q", code, out, errOut, f.gotAs)
	}

	// sem senha no ambiente: gera, imprime uma vez na saída padrão (o aviso vai ao stderr)
	code, out, errOut = exec(t, authRT(f), nil, "", "auth", "user", "reset-password", "a@x.com")
	if code != 0 || strings.TrimSpace(out) != "temp-gerada-unica" || !strings.Contains(errOut, "uma única vez") {
		t.Fatalf("reset gerado: %d %q %q", code, out, errOut)
	}
	// senha pelo ambiente: nunca ecoada
	code, out, errOut = exec(t, authRT(f), map[string]string{userPasswordEnv: "senha-do-env-123"}, "", "auth", "user", "reset-password", "a@x.com")
	if code != 0 || f.gotPW != "senha-do-env-123" || strings.Contains(out+errOut, "senha-do-env-123") {
		t.Fatalf("reset por env: %d %q %q pw=%q", code, out, errOut, f.gotPW)
	}

	code, out, _ = exec(t, authRT(f), nil, "", "auth", "user", "remove", "a@x.com")
	if code != 0 || len(f.removed) != 1 || !strings.Contains(out, "a@x.com removida") {
		t.Fatalf("remove: %d %q %v", code, out, f.removed)
	}

	f.err = domain.ErrForbidden
	code, _, errOut = exec(t, authRT(f), nil, "", "auth", "user", "remove", "b@x.com")
	if code == 0 || !strings.Contains(errOut, "dono da instalação") {
		t.Fatalf("não-dono: %d %q", code, errOut)
	}
}

func TestAuthUserAudit(t *testing.T) {
	at := time.Date(2026, 10, 15, 9, 30, 0, 0, time.UTC)
	f := &fakeAdmin{audit: []domain.AdminAuditEntry{
		{ID: domain.NewID(), At: at, ActorEmail: "dono@x.com", Action: "user.remove", TargetEmail: "alvo@x.com", Result: "ok"},
		{ID: domain.NewID(), At: at.Add(-time.Hour), ActorEmail: "comum@x.com", Action: "user.reset_password", TargetEmail: "alvo@x.com", Result: "denied"},
	}}
	code, out, errOut := exec(t, authRT(f), nil, "", "auth", "user", "audit", "--as", "dono@x.com", "--limit", "5")
	if code != 0 || f.gotAs != "dono@x.com" || f.gotLimit != 5 || f.closed != 1 {
		t.Fatalf("%d err=%q as=%q limit=%d closed=%d", code, errOut, f.gotAs, f.gotLimit, f.closed)
	}
	for _, want := range []string{"QUANDO", "OPERADOR", "AÇÃO", "ALVO", "RESULTADO", "user.remove", "alvo@x.com", "denied", "comum@x.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída sem %q:\n%s", want, out)
		}
	}
	// padrão do limite e saída JSON
	f = &fakeAdmin{audit: f.audit}
	_, out, _ = exec(t, authRT(f), nil, "", "auth", "user", "audit", "--json")
	if f.gotLimit != 100 || !strings.Contains(out, `"action": "user.remove"`) {
		t.Fatalf("limit=%d out=%s", f.gotLimit, out)
	}
	// vazio
	if _, out, _ = exec(t, authRT(&fakeAdmin{}), nil, "", "auth", "user", "audit"); !strings.Contains(out, "Nenhum registro de auditoria") {
		t.Fatalf("vazio: %q", out)
	}
	// em inglês
	if _, out, _ = exec(t, authRT(&fakeAdmin{audit: f.audit}), envLang("en"), "", "auth", "user", "audit"); !strings.Contains(out, "OPERATOR") || strings.Contains(out, "OPERADOR") {
		t.Fatalf("en: %q", out)
	}
	// quem não é o dono da instalação
	code, _, errOut = exec(t, authRT(&fakeAdmin{err: domain.ErrForbidden}), nil, "", "auth", "user", "audit")
	if code != 1 || !strings.Contains(errOut, "só o dono da instalação") {
		t.Fatalf("%d %q", code, errOut)
	}
}
