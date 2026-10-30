package config

import (
	"strings"
	"testing"
)

// O listener de ingestão é só da tailnet (ADR-0076): endereço curinga expõe a ingestão em todas as interfaces.
func TestIngestAddrRefusesWildcardHosts(t *testing.T) {
	for addr, wildcard := range map[string]bool{
		":7421": true, "0.0.0.0:7421": true, "[::]:7421": true, "100.64.0.1:7421": false, "127.0.0.1:7421": false,
	} {
		c := Config{DatabaseURL: "postgres://u:p@h/db", Addr: "127.0.0.1:7420", IngestAddr: addr}
		err := c.Validate()
		got := err != nil && strings.Contains(err.Error(), "todas as interfaces")
		if got != wildcard {
			t.Errorf("HANKO_INGEST_ADDR=%q: recusado como curinga = %v, quero %v (erro: %v)", addr, got, wildcard, err)
		}
	}
}
