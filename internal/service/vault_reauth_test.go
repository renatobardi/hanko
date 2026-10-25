package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// As ações sensíveis do cofre da fatia 1 (revelar campo ou versão, ver histórico, restaurar, apagar campo e
// conceder/revogar coleção) exigem reautenticação recente (ADR-0070 §1): sessão sem prova recebe ReauthError e nada
// muda; com a prova, a ação roda. Modo trusted (sem identidade no contexto) passa, como nas outras ações sensíveis.
func TestAcoesSensiveisDoCofrePedemReauth(t *testing.T) {
	f := newReauthFixture(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	f.e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	bg := context.Background()
	if _, err := f.e.svc.CreateSecret(bg, domain.HumanActor, f.co.ID, service.CreateSecretInput{Name: "gh", Value: "v-antigo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.svc.CreateSecretField(bg, domain.HumanActor, f.co.ID, domain.SecretScopeCompany, "gh",
		service.CreateSecretFieldInput{Name: "senha", SecretFieldValueInput: service.SecretFieldValueInput{Value: "v-um"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.e.svc.RotateSecretField(bg, domain.HumanActor, f.co.ID, domain.SecretScopeCompany, "gh", "senha",
		service.SecretFieldValueInput{Value: "v-dois"}); err != nil {
		t.Fatal(err)
	}

	_, noProof := f.session(t, f.owner)
	semProva := auth.WithIdentity(bg, noProof)
	c := f.co.ID
	cases := []struct {
		nome string
		call func(ctx context.Context) error
	}{
		{"revelar campo", func(ctx context.Context) error {
			_, err := f.e.svc.RevealSecretField(ctx, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha")
			return err
		}},
		{"apagar campo", func(ctx context.Context) error {
			return f.e.svc.DeleteSecretField(ctx, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha")
		}},
		{"ver histórico", func(ctx context.Context) error {
			_, err := f.e.svc.ListSecretFieldHistory(ctx, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha")
			return err
		}},
		{"revelar versão", func(ctx context.Context) error {
			_, _, err := f.e.svc.RevealSecretFieldVersion(ctx, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha", 1)
			return err
		}},
		{"restaurar versão", func(ctx context.Context) error {
			_, err := f.e.svc.RestoreSecretFieldVersion(ctx, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha", 1)
			return err
		}},
		{"criar concessão", func(ctx context.Context) error {
			_, err := f.e.svc.CreateCollectionGrant(ctx, f.owner.Actor(), c, domain.DefaultCollectionName,
				service.GrantInput{SubjectType: domain.CollectionSubjectAllPersonas, Permission: domain.CollectionUse})
			return err
		}},
	}
	var re *domain.ReauthError
	for _, tc := range cases {
		if err := tc.call(semProva); !errors.As(err, &re) {
			t.Errorf("%s sem prova = %v, quer ReauthError", tc.nome, err)
		}
	}
	// revogar também pede prova: cria com prova para ter o id e revoga sem
	_, proved := f.provedSession(t)
	comProva := auth.WithIdentity(bg, proved)
	// a padrão já nasce concedida a all_personas (migration 00204): a concessão da prova é view para a própria dona
	g, err := f.e.svc.CreateCollectionGrant(comProva, f.owner.Actor(), c, domain.DefaultCollectionName,
		service.GrantInput{SubjectType: domain.CollectionSubjectUser, Subject: f.owner.Email, Permission: domain.CollectionView})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.svc.DeleteCollectionGrant(semProva, f.owner.Actor(), c, domain.DefaultCollectionName, g.ID); !errors.As(err, &re) {
		t.Errorf("revogar sem prova = %v, quer ReauthError", err)
	}

	// com a prova, cada ação roda (e as recusas de cima não mudaram nada: revelar ainda devolve o valor atual)
	v, err := f.e.svc.RevealSecretField(comProva, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha")
	if err != nil || v.RevealedValue != "v-dois" {
		t.Fatalf("revelar com prova = %+v %v", v, err)
	}
	hist, err := f.e.svc.ListSecretFieldHistory(comProva, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha")
	if err != nil || len(hist) != 1 || hist[0].Version != 1 {
		t.Fatalf("histórico com prova = %+v %v", hist, err)
	}
	_, val, err := f.e.svc.RevealSecretFieldVersion(comProva, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha", 1)
	if err != nil || val != "v-um" {
		t.Fatalf("revelar versão com prova = %q %v", val, err)
	}
	r, err := f.e.svc.RestoreSecretFieldVersion(comProva, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha", 1)
	if err != nil || r.Version != 3 {
		t.Fatalf("restaurar com prova = %+v %v", r, err)
	}
	if err := f.e.svc.DeleteCollectionGrant(comProva, f.owner.Actor(), c, domain.DefaultCollectionName, g.ID); err != nil {
		t.Fatalf("revogar com prova: %v", err)
	}
	if err := f.e.svc.DeleteSecretField(comProva, f.owner.Actor(), c, domain.SecretScopeCompany, "gh", "senha"); err != nil {
		t.Fatalf("apagar campo com prova: %v", err)
	}

	// modo trusted (sem identidade): a prova passa (ADR-0070 §5)
	if _, err := f.e.svc.RevealSecretField(bg, domain.HumanActor, c, domain.SecretScopeCompany, "gh", "value"); err != nil {
		t.Fatalf("revelar no modo trusted: %v", err)
	}
}

// O segredo pessoal (user-secrets) passa pelo mesmo gate: o dono sem prova recebe ReauthError (não Forbidden) e, com
// a prova, revela.
func TestSegredoPessoalPedeReauthParaRevelar(t *testing.T) {
	f := newReauthFixture(t)
	box, err := secretbox.New(keyOne, "")
	if err != nil {
		t.Fatal(err)
	}
	f.e.svc.Integrations = service.IntegrationsConfig{Secrets: box}
	bg := context.Background()
	_, proved := f.provedSession(t)
	comProva := auth.WithIdentity(bg, proved)
	if _, err := f.e.svc.CreateSecret(comProva, f.owner.Actor(), f.co.ID,
		service.CreateSecretInput{Scope: domain.SecretScopeUser, Name: "pessoal", Value: "v-pessoal"}); err != nil {
		t.Fatal(err)
	}
	_, noProof := f.session(t, f.owner)
	semProva := auth.WithIdentity(bg, noProof)
	var re *domain.ReauthError
	if _, err := f.e.svc.RevealSecretField(semProva, f.owner.Actor(), f.co.ID, domain.SecretScopeUser, "pessoal", "value"); !errors.As(err, &re) {
		t.Fatalf("revelar segredo pessoal sem prova = %v, quer ReauthError", err)
	}
	v, err := f.e.svc.RevealSecretField(comProva, f.owner.Actor(), f.co.ID, domain.SecretScopeUser, "pessoal", "value")
	if err != nil || v.RevealedValue != "v-pessoal" {
		t.Fatalf("revelar segredo pessoal com prova = %+v %v", v, err)
	}
}
