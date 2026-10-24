package service

import (
	"context"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// ${secretfile:} só vale no adapter_config da persona (ADR-0068 §5.2): nas demais configurações o checkSecretRefs recusa ao
// salvar, com erro claro, em vez de deixar a referência morta até o despacho. A recusa vem antes de qualquer consulta ao
// banco, então este teste não precisa de um.
func TestCheckSecretRefsRecusaSecretfileForaDoAdapterConfig(t *testing.T) {
	s := &Service{}
	var companyID domain.ID
	err := s.checkSecretRefs(context.Background(), nil, companyID, "channel.token", "use ${secretfile:prd#kubeconfig}")
	if err == nil || !strings.Contains(err.Error(), "só vale no adapter_config da persona") {
		t.Fatalf("secretfile fora do adapter_config precisa ser recusado com a dica: %v", err)
	}
	if !strings.Contains(err.Error(), "prd#kubeconfig") {
		t.Fatalf("o erro deve nomear a referência, não o valor: %v", err)
	}
}
