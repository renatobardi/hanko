package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// As telas de exportar e importar o .hkv e a de importar .env provam a identidade por /api/v1/me/reauth (senha, Google ou
// passkey) e depois enviam o formulário (#92, ADR-0070 §3). O que o formulário leva ao service é a identidade da sessão,
// lida de novo do banco: com a prova de qualquer um dos três métodos, as três ações passam; sem ela, pedem a prova.
func TestProvaPorSenhaFirebaseEPasskeyLiberaExportEImportDoCofre(t *testing.T) {
	const filePass = "senha-do-arquivo-123"
	env := service.EnvImportInput{Content: "A=1\n", Mode: service.EnvImportItems}
	cases := []struct {
		nome   string
		method string
		prove  func(t *testing.T, f *reauthFixture) (domain.User, domain.Company, string)
	}{
		{"senha", "password", func(t *testing.T, f *reauthFixture) (domain.User, domain.Company, string) {
			tok, id := f.session(t, f.owner)
			if _, err := f.e.svc.ReauthWithPassword(context.Background(), id, goodPass); err != nil {
				t.Fatalf("prova por senha: %v", err)
			}
			return f.owner, f.co, tok
		}},
		{"firebase", "firebase", func(t *testing.T, f *reauthFixture) (domain.User, domain.Company, string) {
			bg := context.Background()
			fb, err := f.e.svc.LoginFirebase(bg, service.FirebaseLogin{UID: "fb-92", Email: "fb92@exemplo.com", Name: "Fb92",
				ListsConfigured: true, Admitted: true})
			if err != nil {
				t.Fatalf("LoginFirebase: %v", err)
			}
			tok, id := f.session(t, fb)
			if _, err := f.e.svc.Pool.Exec(bg, `INSERT INTO company_members (company_id, user_id, role) VALUES ($1,$2,$3)`,
				f.co.ID, fb.ID, string(domain.MemberAdmin)); err != nil {
				t.Fatalf("admin da empresa: %v", err)
			}
			f.e.svc.IDVerifier = fakeIDVerifier{claims: auth.FirebaseClaims{UID: "fb-92", AuthTime: f.clk.Add(-time.Minute)}}
			if _, err := f.e.svc.ReauthWithFirebase(bg, id, "id-token"); err != nil {
				t.Fatalf("prova por Google: %v", err)
			}
			return fb, f.co, tok
		}},
		{"passkey", "passkey", func(t *testing.T, f *reauthFixture) (domain.User, domain.Company, string) {
			bg := context.Background()
			a := f.passkeyOn(t)
			tok, id := f.session(t, f.owner)
			if _, err := f.e.svc.ReauthWithPassword(bg, id, goodPass); err != nil {
				t.Fatalf("prova por senha: %v", err)
			}
			id = f.identityOf(t, tok)
			reg, err := f.e.svc.BeginPasskeyRegistration(bg, id)
			if err != nil {
				t.Fatalf("BeginPasskeyRegistration: %v", err)
			}
			resp, err := a.Create(reg.Options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.e.svc.FinishPasskeyRegistration(bg, id, reg.ID, "Notebook", resp); err != nil {
				t.Fatalf("FinishPasskeyRegistration: %v", err)
			}
			// Sessão nova, sem a prova por senha: só a passkey a libera.
			tok2, id2 := f.session(t, f.owner)
			opts, err := f.e.svc.ReauthOptionsFor(bg, id2)
			if err != nil || opts.Passkey == nil {
				t.Fatalf("ReauthOptionsFor = %+v, %v", opts, err)
			}
			assertion, err := a.Get(opts.Passkey.Options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.e.svc.FinishPasskeyReauth(bg, id2, opts.Passkey.ID, assertion); err != nil {
				t.Fatalf("prova por passkey: %v", err)
			}
			return f.owner, f.co, tok2
		}},
	}
	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			f := newReauthFixture(t)
			box, err := secretbox.New(keyOne, "")
			if err != nil {
				t.Fatal(err)
			}
			f.e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
			user, co, tok := c.prove(t, f)

			// Sessão sem prova do mesmo usuário: as três ações pedem a prova e nada é gravado.
			_, semProva := f.session(t, user)
			sem := auth.WithIdentity(context.Background(), semProva)
			if _, err := f.e.svc.ExportVault(sem, user.Actor(), co.ID, filePass); !isReauth(err) {
				t.Fatalf("export sem prova = %v, quer ReauthError", err)
			}
			if _, err := f.e.svc.ImportVault(sem, user.Actor(), co.ID, []byte("x"), filePass); !isReauth(err) {
				t.Fatalf("import .hkv sem prova = %v, quer ReauthError", err)
			}
			if _, err := f.e.svc.ImportEnv(sem, user.Actor(), co.ID, env); !isReauth(err) {
				t.Fatalf("import .env sem prova = %v, quer ReauthError", err)
			}

			// Com a prova gravada na sessão (o que /me/reauth faz), o formulário sem senha da conta passa.
			id := f.identityOf(t, tok)
			if id.Session.ReauthAt == nil || id.Session.ReauthMethod != c.method {
				t.Fatalf("sessão = %+v, quer prova %q", id.Session, c.method)
			}
			ctx := auth.WithIdentity(context.Background(), id)
			res, err := f.e.svc.ExportVault(ctx, user.Actor(), co.ID, filePass)
			if err != nil {
				t.Fatalf("export com prova %s: %v", c.method, err)
			}
			if _, err := f.e.svc.ImportVault(ctx, user.Actor(), co.ID, res.File, filePass); err != nil {
				t.Fatalf("import .hkv com prova %s: %v", c.method, err)
			}
			if _, err := f.e.svc.ImportEnv(ctx, user.Actor(), co.ID, env); err != nil {
				t.Fatalf("import .env com prova %s: %v", c.method, err)
			}
		})
	}
}

func isReauth(err error) bool {
	var re *domain.ReauthError
	return errors.As(err, &re)
}
