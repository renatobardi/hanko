package domain

import (
	"testing"
	"time"
)

func TestBackupKindsAndFlowKeys(t *testing.T) {
	for _, k := range []string{BackupManual, BackupScheduled, BackupPreUpdate, BackupPreDeploy} {
		if !ValidBackupKind(k) {
			t.Errorf("%q deveria ser válido", k)
		}
	}
	for _, k := range []string{"", "auto", "MANUAL"} {
		if ValidBackupKind(k) {
			t.Errorf("%q não deveria ser válido", k)
		}
	}
	if !ValidSystemFlowKey(SystemFlowBackup) || ValidSystemFlowKey("restore") {
		t.Error("chaves de fluxo de sistema")
	}
}

func TestInstanceBackupDuration(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	b := InstanceBackup{StartedAt: start}
	if b.Duration() != 0 {
		t.Fatal("em andamento tem duração zero")
	}
	end := start.Add(90 * time.Second)
	b.FinishedAt = &end
	if b.Duration() != 90*time.Second {
		t.Fatalf("duração = %v", b.Duration())
	}
}
