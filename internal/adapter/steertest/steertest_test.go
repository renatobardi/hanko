// Package steertest guards that the static capability table (domain.SteerModeFor, which the UI reads) agrees with
// what each real adapter declares (adapter.Steerer).
package steertest

import (
	"testing"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/adapter/acp"
	"github.com/renatobardi/hanko/internal/adapter/claude"
	"github.com/renatobardi/hanko/internal/adapter/codex"
	"github.com/renatobardi/hanko/internal/adapter/fake"
	"github.com/renatobardi/hanko/internal/domain"
)

func TestSteerCapabilityTableMatchesAdapters(t *testing.T) {
	adapters := []adapter.Adapter{claude.New(claude.Options{}), codex.New(codex.Options{}), acp.New(acp.Options{}), fake.New(fake.Options{})}
	want := map[string]domain.SteerMode{"claude": domain.SteerInject, "acp": domain.SteerInject, "fake": domain.SteerInject, "codex": domain.SteerRestart}
	for _, a := range adapters {
		got := adapter.SteerModeOf(a)
		if got != domain.SteerModeFor(a.Name()) {
			t.Errorf("%s: o adapter declara %s e domain.SteerModeFor diz %s", a.Name(), got, domain.SteerModeFor(a.Name()))
		}
		if got != want[a.Name()] {
			t.Errorf("%s: modo %s, esperado %s", a.Name(), got, want[a.Name()])
		}
	}
}
