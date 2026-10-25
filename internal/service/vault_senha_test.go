package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// A marca explícita de senha (ADR-0074) põe no achado fraco um campo de qualquer nome. Sem a marca, só password e senha
// entram; campo forte marcado não vira achado.
func TestHealthWeakUsesThePasswordMark(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "conta", "valor")
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "chave", Password: true, SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "forte", Password: true, SecretFieldValueInput: service.SecretFieldValueInput{Value: "Tr0ub4dor&3-horse-battery"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "outra", SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}

	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	weak := findingsOf(r, domain.HealthWeak)
	if _, ok := weak["conta#chave"]; !ok {
		t.Fatalf("campo marcado como senha ficou de fora dos fracos: %+v", weak)
	}
	if _, ok := weak["conta#forte"]; ok {
		t.Fatal("senha forte marcada entrou nos fracos")
	}
	if _, ok := weak["conta#outra"]; ok {
		t.Fatal("campo sem marca nem nome de senha entrou nos fracos")
	}
}

// Campo com a marca de senha é secreto; tirar o secreto dele falha, e tirar a marca devolve o campo ao critério do nome.
func TestPasswordMarkNeedsASecretTextField(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "conta", "valor")
	visible := false
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "login", Sensitive: &visible, Password: true, SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err == nil {
		t.Fatal("campo de senha visível foi aceito")
	}
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "chave", Password: true, SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSecretFieldMeta(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", "chave", &visible, nil, nil); err == nil {
		t.Fatal("campo de senha virou visível")
	}

	unmark := false
	if _, err := e.svc.UpdateSecretFieldMeta(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", "chave", nil, &unmark, nil); err != nil {
		t.Fatalf("tirar a marca de senha: %v", err)
	}
	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findingsOf(r, domain.HealthWeak)["conta#chave"]; ok {
		t.Fatal("campo sem marca entrou nos fracos")
	}
}
