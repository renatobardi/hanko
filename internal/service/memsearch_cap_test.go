package service_test

import (
	"context"
	"testing"

	"github.com/renatobardi/hanko/internal/memory"
	"github.com/renatobardi/hanko/internal/service"
)

type capStub struct {
	memory.Noop
	supported bool
}

func (capStub) Name() string                           { return "stub" }
func (c capStub) SearchSupported(context.Context) bool { return c.supported }

type plainStub struct{ memory.Noop }

func (plainStub) Name() string { return "stub" }

// MemorySearchAvailable (busca) e MemoryAvailable (consolidação) são predicados separados (ADR-0024, adendo).
func TestMemorySearchAvailableSeparadoDaConsolidacao(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name        string
		mem         memory.Provider
		search, any bool
	}{
		{"sem provider", nil, false, false},
		{"none", memory.Noop{}, false, false},
		{"sem a capacidade (supõe suportado)", plainStub{}, true, true},
		{"tool de busca presente", capStub{supported: true}, true, true},
		{"tool de busca ausente", capStub{supported: false}, false, true},
	}
	for _, c := range cases {
		s := &service.Service{Memory: c.mem}
		if got := s.MemorySearchAvailable(ctx); got != c.search {
			t.Errorf("%s: MemorySearchAvailable = %v, quer %v", c.name, got, c.search)
		}
		if got := s.MemoryAvailable(); got != c.any {
			t.Errorf("%s: MemoryAvailable = %v, quer %v", c.name, got, c.any)
		}
	}
}
