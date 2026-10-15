package service

import (
	"encoding/json"
	"testing"

	"github.com/renatobardi/hanko/internal/domain"
)

// run.failed cobre failed, expired e interrupted (órfã achada na recuperação); done e cancelled não.
func TestWebhookEventForRunFinished(t *testing.T) {
	for st, want := range map[domain.RunStatus]bool{
		domain.RunFailed: true, domain.RunExpired: true, domain.RunInterrupted: true,
		domain.RunDone: false, domain.RunCancelled: false, domain.RunRunning: false,
	} {
		data, _ := json.Marshal(map[string]any{"status": st})
		ev, ok := webhookEventFor("run.finished", data)
		if ev != domain.EventRunFailed || ok != want {
			t.Errorf("status %s: ev=%q ok=%v, quer ok=%v", st, ev, ok, want)
		}
	}
}
