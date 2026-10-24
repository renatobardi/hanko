package service

import (
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// O fato de uma ação sensível leva o id da chave quando ela foi liberada por API key full (ADR-0070 §5). Nenhuma
// ação de passkey aceita a chave hoje (requireSession), então esse trecho é testado direto; as ações do cofre o usam pelo fluxo.
func TestGrantDataLevaIdDaChaveQuandoHa(t *testing.T) {
	kid := domain.NewID()
	d := grantData(ReauthGrant{Via: "api_key", KeyID: &kid})
	if d["via"] != "api_key" || d["key_id"] != kid.String() {
		t.Fatalf("grantData com chave = %v, quer via api_key e key_id %s", d, kid)
	}
	if _, ok := d["method"]; ok {
		t.Fatalf("grantData com chave tem method: %v", d)
	}

	s := grantData(ReauthGrant{Via: "session", Method: "password"})
	if _, ok := s["key_id"]; ok {
		t.Fatalf("grantData de sessão não deve ter key_id: %v", s)
	}
	if s["method"] != "password" {
		t.Fatalf("grantData de sessão = %v, quer method password", s)
	}
}
