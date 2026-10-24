package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/secretbox"
	"github.com/renatobardi/hanko/internal/service"
)

// findingsOf devolve os achados de um tipo, indexados por segredo#campo.
func findingsOf(r service.HealthReport, kind domain.HealthKind) map[string]domain.HealthFinding {
	out := map[string]domain.HealthFinding{}
	for _, f := range r.Findings {
		if f.Kind == kind {
			out[f.Secret+"#"+f.Field] = f
		}
	}
	return out
}

// backdate move um instante do banco para trás: o relógio do teste não anda.
func (e *secEnv) backdate(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

func TestHealthRepeatedUsesHMACWithoutDecrypting(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "api-a", sentinel)
	e.create(t, "api-b", sentinel)
	// texto cifrado de api-b estragado: se a comparação decifrasse, o relatório falharia
	e.backdate(t, `UPDATE secret_fields SET value_enc = 'v1:deadbeef:AAAA'
		WHERE secret_id = (SELECT id FROM secrets WHERE name = 'api-b')`)

	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatalf("repetidos não deveria decifrar: %v", err)
	}
	dups := findingsOf(r, domain.HealthDuplicate)
	if len(dups) != 2 {
		t.Fatalf("repetidos = %d, quer 2: %+v", len(dups), dups)
	}
	if got := dups["api-a#value"].Related; len(got) != 1 || got[0] != "api-b#value" {
		t.Fatalf("relacionados de api-a = %v", got)
	}

	raw, _ := json.Marshal(r)
	mac, _ := e.box.MAC([]byte(sentinel))
	for _, forbidden := range []string{sentinel, mac} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("relatório vaza o valor ou o MAC: %s", raw)
		}
	}
}

func TestHealthEachCategoryHasItsFinding(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "velho", "valor-do-velho")
	days := 30
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "velho", service.SecretMetaInput{RotateAfterDays: &days}); err != nil {
		t.Fatal(err)
	}
	e.backdate(t, `UPDATE secret_fields SET rotated_at = now() - interval '100 days'
		WHERE secret_id = (SELECT id FROM secrets WHERE name = 'velho')`)
	e.create(t, "solto", "valor-do-solto")
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "velho", service.CreateSecretFieldInput{
		Name: "senha", SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}

	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if f, ok := findingsOf(r, domain.HealthStale)["velho#value"]; !ok || f.Detail != "sem rotação há 100 dias (prazo 30)" {
		t.Fatalf("velho: %+v %v", f, ok)
	}
	if _, ok := findingsOf(r, domain.HealthUnused)["solto#value"]; !ok {
		t.Fatalf("sem uso: campo nunca usado não apareceu")
	}
	if _, ok := findingsOf(r, domain.HealthOrphan)["solto#"]; !ok {
		t.Fatalf("órfão: segredo sem referência não apareceu: %+v", r.Findings)
	}
	if f, ok := findingsOf(r, domain.HealthWeak)["velho#senha"]; !ok || strings.Contains(f.Detail, "abc123") {
		t.Fatalf("fraco: %+v %v", f, ok)
	}
	if len(findingsOf(r, domain.HealthDuplicate)) != 0 {
		t.Fatalf("repetido sem valor igual: %+v", r.Findings)
	}
}

func TestHealthOrphanIgnoresReferencedSecret(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "chave-modelo", "valor-da-chave")
	enabled := true
	if _, err := e.svc.SetDecisionModel(bg, domain.HumanActor, e.co.ID, service.SetDecisionModelInput{
		BaseURL: "https://models.example.com", APIKeyRef: "${secret:chave-modelo}", Model: "modelo-1", TimeoutSec: 30, Enabled: &enabled}); err != nil {
		t.Fatalf("decision model: %v", err)
	}
	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findingsOf(r, domain.HealthOrphan)["chave-modelo#"]; ok {
		t.Fatal("segredo usado pelo modelo de decisão apareceu como órfão")
	}
}

func TestHealthNeedsAdmin(t *testing.T) {
	e := newSecEnv(t)
	e.create(t, "base", "valor")
	u := mkUser(t, e.env, "membro@x.com", domain.RoleMember)
	join(t, e.env, u, domain.MemberMember)
	if _, err := e.svc.VaultHealth(as(u), e.co.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member lendo o relatório: %v, quer ErrForbidden", err)
	}
}

func TestHealthFailsWhenAPasswordCannotBeOpened(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "conta", "valor")
	if _, err := e.svc.CreateSecretField(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "conta", service.CreateSecretFieldInput{
		Name: "password", SecretFieldValueInput: service.SecretFieldValueInput{Value: "abc123"}}); err != nil {
		t.Fatal(err)
	}
	e.backdate(t, `UPDATE secret_fields SET value_enc = 'v1:deadbeef:AAAA' WHERE name = 'password'`)
	if _, err := e.svc.VaultHealth(bg, e.co.ID); err == nil {
		t.Fatal("senha que não abre deveria falhar o relatório, não sumir dele")
	}
}

func TestRotationAlertOpensAndClosesWhenRotated(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "gh", sentinelV1)
	days := 1
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "gh", service.SecretMetaInput{RotateAfterDays: &days}); err != nil {
		t.Fatal(err)
	}
	e.backdate(t, `UPDATE secret_fields SET rotated_at = now() - interval '10 days'
		WHERE secret_id = (SELECT id FROM secrets WHERE name = 'gh')`)

	if _, err := e.svc.EvaluateAlerts(bg); err != nil {
		t.Fatal(err)
	}
	alert := rotationAlert(t, e, "gh")
	if alert == nil {
		t.Fatal("alerta de rotação não abriu")
	}
	if strings.Contains(alert.Message, sentinelV1) {
		t.Fatalf("mensagem do alerta vaza o valor: %q", alert.Message)
	}

	// rotacionar o campo tira a condição: o alerta fecha sozinho
	if _, err := e.svc.RotateSecret(bg, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "gh", service.RotateSecretInput{Value: sentinelV2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(bg); err != nil {
		t.Fatal(err)
	}
	if rotationAlert(t, e, "gh") != nil {
		t.Fatal("alerta continuou aberto depois da rotação")
	}
}

func TestRotationAlertStaysQuietInsideTheLimit(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "fresco", sentinelV1)
	days := 90
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "fresco", service.SecretMetaInput{RotateAfterDays: &days}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.EvaluateAlerts(bg); err != nil {
		t.Fatal(err)
	}
	if rotationAlert(t, e, "fresco") != nil {
		t.Fatal("alerta abriu dentro do prazo")
	}
}

// rotationAlert devolve o alerta de rotação aberto do segredo, ou nil.
func rotationAlert(t *testing.T, e *secEnv, name string) *domain.Alert {
	t.Helper()
	alerts, err := e.svc.ListOpenAlerts(context.Background(), e.co.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i := range alerts {
		if alerts[i].Kind == domain.AlertSecretRotationDue && strings.Contains(alerts[i].Message, "Segredo "+name+":") {
			return &alerts[i]
		}
	}
	return nil
}

func TestRekeyRefreshesHMACSoRepeatsStillMatch(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	e.create(t, "a", sentinel)
	e.create(t, "b", sentinel)
	both, _ := secretbox.New(keyTwo, keyOne)
	e.svc.Integrations = service.IntegrationsConfig{Secrets: both}
	if _, err := e.svc.RekeyIntegrationSecrets(bg); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	only, _ := secretbox.New(keyTwo, "")
	e.svc.Integrations = service.IntegrationsConfig{Secrets: only}

	want, _ := only.MAC([]byte(sentinel))
	var got string
	if err := e.svc.Pool.QueryRow(bg, `SELECT value_hmac FROM secret_fields
		WHERE secret_id = (SELECT id FROM secrets WHERE name = 'a') AND name = 'value'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("MAC depois do rekey = %q, quer o da chave nova", got)
	}
	r, err := e.svc.VaultHealth(bg, e.co.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(findingsOf(r, domain.HealthDuplicate)) != 2 {
		t.Fatalf("repetidos depois do rekey: %+v", r.Findings)
	}
}

func TestGenerateValueFollowsTheOptions(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()

	def, err := e.svc.GenerateValue(bg, domain.GenOptions{})
	if err != nil || !strings.HasPrefix(def.Value, "hk_") || def.EntropyBits != 256 {
		t.Fatalf("padrão = %+v %v", def, err)
	}
	digits, err := e.svc.GenerateValue(bg, domain.GenOptions{Length: 16, Classes: []domain.GenClass{domain.GenDigits}})
	if err != nil || len(digits.Value) != 16 || strings.Trim(digits.Value, "0123456789") != "" {
		t.Fatalf("só dígitos = %+v %v", digits, err)
	}
	words, err := e.svc.GenerateValue(bg, domain.GenOptions{PassphraseWords: 4})
	if err != nil || len(strings.Split(words.Value, "-")) != 4 {
		t.Fatalf("frase = %+v %v", words, err)
	}
	var ve *domain.ValidationError
	if _, err := e.svc.GenerateValue(bg, domain.GenOptions{Length: 3}); !errors.As(err, &ve) {
		t.Fatalf("tamanho inválido: %v, quer ValidationError", err)
	}
}

func TestCreateSecretWithGeneratorOptions(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	v, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "pin", Generate: true,
		Gen: domain.GenOptions{Length: 12, Classes: []domain.GenClass{domain.GenDigits}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.RevealedValue) != 12 || strings.Trim(v.RevealedValue, "0123456789") != "" {
		t.Fatalf("valor gerado = %q, quer 12 dígitos", v.RevealedValue)
	}
	// opções sem gerar: recusado, não ignorado em silêncio
	var ve *domain.ValidationError
	if _, err := e.svc.CreateSecret(bg, domain.HumanActor, e.co.ID, service.CreateSecretInput{Name: "pin2", Value: "x",
		Gen: domain.GenOptions{Length: 12}}); !errors.As(err, &ve) {
		t.Fatalf("opções sem generate: %v, quer ValidationError", err)
	}
}

func TestUpdateSecretMetaChangesOnlyMeta(t *testing.T) {
	e := newSecEnv(t)
	bg := context.Background()
	before := e.create(t, "meta", "valor-original")
	desc := "conta do time"
	days := 45
	v, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "meta", service.SecretMetaInput{Description: &desc, RotateAfterDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if v.Description != desc || v.RotateAfterDays == nil || *v.RotateAfterDays != 45 {
		t.Fatalf("metadados não gravados: %+v", v.Secret)
	}
	if v.Version != before.Version {
		t.Fatalf("versão subiu com troca de metadado: %d → %d", before.Version, v.Version)
	}
	zero := 0
	if v, err = e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "meta", service.SecretMetaInput{RotateAfterDays: &zero}); err != nil {
		t.Fatal(err)
	}
	if v.RotateAfterDays != nil || v.Description != desc {
		t.Fatalf("0 deveria tirar o prazo e manter a descrição: %+v", v.Secret)
	}

	var ve *domain.ValidationError
	big := 3651
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "meta", service.SecretMetaInput{RotateAfterDays: &big}); !errors.As(err, &ve) {
		t.Fatalf("prazo acima do máximo: %v", err)
	}
	long := strings.Repeat("x", 301)
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "meta", service.SecretMetaInput{Description: &long}); !errors.As(err, &ve) {
		t.Fatalf("descrição longa: %v", err)
	}
	if _, err := e.svc.UpdateSecretMeta(bg, domain.HumanActor, e.co.ID, "nao-existe", service.SecretMetaInput{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("segredo inexistente: %v, quer ErrNotFound", err)
	}
	u := mkUser(t, e.env, "membro-meta@x.com", domain.RoleMember)
	join(t, e.env, u, domain.MemberMember)
	if _, err := e.svc.UpdateSecretMeta(as(u), domain.HumanActor, e.co.ID, "meta", service.SecretMetaInput{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("member editou metadado de segredo da empresa: %v, quer ErrForbidden", err)
	}
}
