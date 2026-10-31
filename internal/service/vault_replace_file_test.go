package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Troca do conteúdo de um campo arquivo (#117): o campo mantém nome e id e sobe a versão; o conteúdo novo é o que a leitura
// devolve; o evento secret.field_updated não leva conteúdo. Pulam sem HANKO_TEST_DATABASE_URL.

const (
	conteudoAntigoArquivo = "SENTINELA-kubeconfig-antigo-3d2a"
	conteudoNovoArquivo   = "SENTINELA-kubeconfig-novo-8f4b"
)

// lerKubeconfig lê o campo kubeconfig de prd-cluster pela ferramenta da persona, como a run lê.
func lerKubeconfig(t *testing.T, e *toolsEnv) string {
	t.Helper()
	ana := e.persona(t, "Ana", nil)
	c, _ := toolRun(t, e, ana)
	e.policy(t, e.toolPolicy(&ana, domain.PolicyTargetNative, nil, service.HankoSecretGetTool, domain.PolicyAllow))
	d, err := getSecret(e, c, "prd-cluster", "kubeconfig")
	if err != nil {
		t.Fatalf("ler kubeconfig: %v", err)
	}
	return d.Value
}

func TestTrocaArquivoSubstituiOConteudoSemMudarOCampo(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	kubeconfigSecret(t, e, []byte(conteudoAntigoArquivo))
	fieldID := fieldIDOf(t, e, "kubeconfig")

	v, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-cluster", "kubeconfig",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "kubeconfig-novo.yaml"})
	if err != nil {
		t.Fatalf("trocar arquivo: %v", err)
	}
	if v.RevealedValue != "" || v.FileName != "kubeconfig-novo.yaml" || v.SizeBytes != len(conteudoNovoArquivo) {
		t.Fatalf("resposta do service = %+v", v.SecretField)
	}
	if fieldIDOf(t, e, "kubeconfig") != fieldID {
		t.Fatal("a troca mudou o id do campo")
	}
	if n := e.count(t, `SELECT version FROM secret_fields WHERE id=$1`, fieldID); n != 2 {
		t.Fatalf("versão do campo = %d depois da troca, quer 2 (a troca sobe a versão, ADR-0068 §3.1)", n)
	}
	if got := lerKubeconfig(t, e); got != conteudoNovoArquivo {
		t.Fatalf("leitura depois da troca = %q, quer o conteúdo novo", got)
	}
	raw := conteudoDoCampo(t, e, fieldID)
	if strings.Contains(string(raw), conteudoNovoArquivo) || strings.Contains(string(raw), conteudoAntigoArquivo) {
		t.Fatal("o blob guarda o conteúdo em claro")
	}
	if e.count(t, `SELECT count(*) FROM activity_events WHERE company_id=$1 AND verb='secret.field_updated' AND (data::jsonb)->>'field'='kubeconfig'`, e.co.ID) < 1 {
		t.Fatal("a troca não gravou secret.field_updated")
	}
	if n := e.count(t, `SELECT count(*) FROM activity_events WHERE company_id=$1 AND data::text LIKE '%'||$2||'%'`, e.co.ID, conteudoNovoArquivo); n != 0 {
		t.Fatalf("o conteúdo novo foi para o evento (%d)", n)
	}
}

func TestTrocaArquivoRecusaCampoDeTexto(t *testing.T) {
	e := newToolsEnv(t)
	ctx := context.Background()
	createSecretWithValue(t, e, "prd-token", "valor-de-texto")

	_, err := e.svc.ReplaceSecretFieldFile(ctx, domain.HumanActor, e.co.ID, domain.SecretScopeCompany, "prd-token", "value",
		service.ReplaceFieldFileInput{File: []byte(conteudoNovoArquivo), FileName: "x"})
	if err == nil || !strings.Contains(err.Error(), "campo de texto") {
		t.Fatalf("campo de texto aceitou arquivo: %v", err)
	}
}
