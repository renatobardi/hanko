package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// ${secret:ITEM#campo} lê o campo pedido, não o value; ${secret:ITEM} segue lendo o value. Campo inexistente falha
// na checagem de salvar a configuração e na resolução da run (ADR-0068 §2).
func TestReferenciaLeOCampoPedido(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	ana := e.persona(t, "Ana", nil)
	e.create(t, "gh", sentinelV1)
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gh", service.CreateSecretFieldInput{
		Name: "senha", SecretFieldValueInput: service.SecretFieldValueInput{Value: sentinelCampo}}); err != nil {
		t.Fatal(err)
	}

	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:gh}"); err != nil || got != sentinelV1 {
		t.Fatalf("${secret:gh} = %q %v, quer o value", got, err)
	}
	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:gh#senha}"); err != nil || got != sentinelCampo {
		t.Fatalf("${secret:gh#senha} = %q %v, quer o campo senha", got, err)
	}
	if _, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:gh#nope}"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("campo inexistente na resolução: %v", err)
	}

	// a run da persona: o campo certo sai; o campo que não existe falha cedo, nomeando o campo
	cfg := json.RawMessage(`{"api_key_ref":"${secret:gh#senha}"}`)
	if out, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, ana.ID, cfg); err != nil || !strings.Contains(string(out), sentinelCampo) {
		t.Fatalf("persona com o campo senha: %s %v", out, err)
	}
	bad := json.RawMessage(`{"api_key_ref":"${secret:gh#nope}"}`)
	if _, _, err := e.svc.ResolveAdapterConfig(bg, e.co.ID, ana.ID, bad); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("persona com campo inexistente: %v", err)
	}

	// salvar a persona com campo inexistente é recusado pela checagem de referências
	_, err := e.svc.HirePersona(bg, domain.HumanActor, e.co.ID, service.HirePersonaInput{Name: "Beto", Agent: "fake", Model: "m1",
		AdapterConfig: bad})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("contratar persona com campo inexistente: %v", err)
	}
	if strings.Contains(err.Error(), sentinelV1) || strings.Contains(err.Error(), sentinelCampo) {
		t.Fatalf("o erro vazou um valor: %v", err)
	}
}

// Rotacionar HANKO_SECRET_KEY reescreve também os campos e o histórico, com o AAD de cada um: depois do rekey só a
// chave nova basta, e os campos e as versões antigas seguem decifrando (ADR-0068 §1.3 e §3.1).
func TestRekeyCamposEHistorico(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gh", sentinelV1)
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gh", service.CreateSecretFieldInput{
		Name: "senha", SecretFieldValueInput: service.SecretFieldValueInput{Value: sentinelCampo}}); err != nil {
		t.Fatal(err)
	}
	// a rotação arquiva o valor anterior do campo senha no histórico
	if _, err := e.svc.RotateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gh", "senha",
		service.SecretFieldValueInput{Value: sentinelV2}); err != nil {
		t.Fatal(err)
	}

	// chave nova + anterior: tudo ainda decifra
	both, _ := secretbox.New(keyTwo, keyOne)
	e.svc.Integrations = service.IntegrationsConfig{Secrets: both}
	n, err := e.svc.RekeyIntegrationSecrets(bg)
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	// segredo (cópia legada), campo value, campo senha e a linha do histórico de senha
	if n < 4 {
		t.Fatalf("rekey reescreveu %d linhas, quer ao menos 4 (segredo, 2 campos, 1 versão)", n)
	}
	if n2, err := e.svc.RekeyIntegrationSecrets(bg); err != nil || n2 != 0 {
		t.Fatalf("rekey de novo: %d %v, quer 0", n2, err)
	}

	// só a chave nova: campos e histórico seguem legíveis
	only, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations = service.IntegrationsConfig{Secrets: only}
	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:gh#senha}"); err != nil || got != sentinelV2 {
		t.Fatalf("campo senha depois do rekey = %q %v", got, err)
	}
	if got, _, err := e.svc.ResolveSecretText(bg, e.co.ID, "${secret:gh}"); err != nil || got != sentinelV1 {
		t.Fatalf("campo value depois do rekey = %q %v", got, err)
	}
	if _, val, err := e.svc.RevealSecretFieldVersion(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gh", "senha", 1); err != nil || val != sentinelCampo {
		t.Fatalf("histórico de senha depois do rekey = %q %v", val, err)
	}
}
