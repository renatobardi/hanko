package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renatobardi/hanko/internal/backup"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Backups da instalação e o fluxo de sistema que os agenda (ADR-0062).

// fakeBackups é o executor de backups: devolve uma entrada fixa, ou o erro, e pode ficar preso até ser liberado.
type fakeBackups struct {
	mu      sync.Mutex
	calls   []backup.CreateOptions
	err     error
	started chan struct{} // fechado quando a primeira chamada começa
	release chan struct{} // se não nulo, Create espera ser fechado
	once    sync.Once
}

func (f *fakeBackups) Create(_ context.Context, o backup.CreateOptions) (backup.Entry, error) {
	f.mu.Lock()
	f.calls = append(f.calls, o)
	f.mu.Unlock()
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	if f.err != nil {
		return backup.Entry{}, f.err
	}
	return backup.Entry{Name: "hanko-20260310T030000Z.tar", Size: 4096,
		Manifest: backup.Manifest{SchemaVersion: 180, Blobs: []backup.BlobEntry{{CompanyID: "c", ArtifactID: "a"}}, Kind: o.Kind, CreatedAt: time.Now()}}, nil
}

func (f *fakeBackups) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestBackupOwnerOnlyAndHistory(t *testing.T) {
	e := newEnv(t)
	fb := &fakeBackups{}
	e.svc.Backups = fb
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	member := mkUser(t, e, "m@x.com", domain.RoleMember)
	join(t, e, member, domain.MemberOwner) // dono da empresa, não da instalação

	if _, err := e.svc.RunBackup(as(member), domain.HumanActor, "x"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("não-dono: %v", err)
	}
	if _, err := e.svc.ListBackups(as(member), 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("listar não-dono: %v", err)
	}
	if _, err := e.svc.SetBackupSchedule(as(member), domain.HumanActor, service.SetBackupScheduleInput{}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agenda não-dono: %v", err)
	}
	if _, err := e.svc.RunBackup(as(owner), domain.Actor{Type: domain.ActorPersona}, "x"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("persona: %v", err)
	}

	rec, err := e.svc.RunBackup(as(owner), domain.HumanActor, "  antes do upgrade  ")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != domain.BackupOK || rec.FileName != "hanko-20260310T030000Z.tar" || rec.SizeBytes != 4096 || rec.Blobs != 1 || rec.SchemaVersion != 180 ||
		rec.Label != "antes do upgrade" || rec.Kind != domain.BackupManual || rec.ActorID == nil || rec.FinishedAt == nil {
		t.Fatalf("registro = %+v", rec)
	}
	if fb.calls[0].Kind != domain.BackupManual || fb.calls[0].Prune {
		t.Fatalf("opções = %+v", fb.calls[0])
	}
	list, err := e.svc.ListBackups(as(owner), 10)
	if err != nil || len(list) != 1 || list[0].ID != rec.ID {
		t.Fatalf("lista = %+v %v", list, err)
	}
}

func TestBackupFailureIsRecordedNotLost(t *testing.T) {
	e := newEnv(t)
	e.svc.Backups = &fakeBackups{err: errors.New("pg_dump: conexão recusada")}
	rec, err := e.svc.RunBackup(context.Background(), domain.HumanActor, "")
	if err != nil {
		t.Fatalf("a falha do backup vira registro, não erro: %v", err)
	}
	if rec.Status != domain.BackupFailed || !strings.Contains(rec.Error, "conexão recusada") || rec.FileName != "" {
		t.Fatalf("registro = %+v", rec)
	}
	// a vaga foi liberada: dá para tentar de novo
	e.svc.Backups = &fakeBackups{}
	if again, err := e.svc.RunBackup(context.Background(), domain.HumanActor, ""); err != nil || again.Status != domain.BackupOK {
		t.Fatalf("segunda tentativa: %+v %v", again, err)
	}
}

func TestBackupUnavailableWithoutRunner(t *testing.T) {
	e := newEnv(t)
	if e.svc.BackupsAvailable() {
		t.Fatal("sem executor")
	}
	var ve *domain.ValidationError
	if _, err := e.svc.RunBackup(context.Background(), domain.HumanActor, ""); !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	if _, err := e.svc.StartBackup(context.Background(), domain.HumanActor, ""); !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
}

func TestStartBackupRunsInBackgroundAndRefusesConcurrent(t *testing.T) {
	e := newEnv(t)
	fb := &fakeBackups{started: make(chan struct{}), release: make(chan struct{})}
	e.svc.Backups = fb
	ctx := context.Background()

	rec, err := e.svc.StartBackup(ctx, domain.HumanActor, "bg")
	if err != nil || rec.Status != domain.BackupRunning {
		t.Fatalf("start = %+v %v", rec, err)
	}
	<-fb.started // sincroniza por evento: o executor já está rodando
	if _, err := e.svc.StartBackup(ctx, domain.HumanActor, "outro"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("segundo backup simultâneo: %v", err)
	}
	if _, err := e.svc.RunBackup(ctx, domain.HumanActor, "outro"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("RunBackup simultâneo: %v", err)
	}
	close(fb.release)
	e.svc.WaitBackups()
	list, _ := e.svc.ListBackups(ctx, 10)
	if len(list) != 1 || list[0].Status != domain.BackupOK {
		t.Fatalf("lista = %+v", list)
	}
}

func TestStaleRunningBackupIsReleased(t *testing.T) {
	e := newEnv(t)
	e.svc.Backups = &fakeBackups{}
	ctx := context.Background()
	old := time.Now().Add(-5 * time.Hour)
	if _, err := e.svc.Pool.Exec(ctx, `INSERT INTO instance_backups (id, kind, status, actor_type, started_at) VALUES ($1,'manual','running','human',$2)`, domain.NewID(), old); err != nil {
		t.Fatal(err)
	}
	if rec, err := e.svc.RunBackup(ctx, domain.HumanActor, ""); err != nil || rec.Status != domain.BackupOK {
		t.Fatalf("um 'running' de 5 h é de um processo morto: %+v %v", rec, err)
	}
	list, _ := e.svc.ListBackups(ctx, 10)
	var failed int
	for _, b := range list {
		if b.Status == domain.BackupFailed && strings.Contains(b.Error, "interrompido") {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("lista = %+v", list)
	}
}

func TestBackupScheduleValidationAndAudit(t *testing.T) {
	e := newEnv(t)
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	octx := as(owner)
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return now }

	def, err := e.svc.GetBackupSchedule(octx)
	if err != nil || def.Enabled || def.Cron != service.DefaultBackupCron || def.NextFireAt != nil {
		t.Fatalf("padrão = %+v %v", def, err)
	}
	var ve *domain.ValidationError
	if _, err := e.svc.SetBackupSchedule(octx, domain.HumanActor, service.SetBackupScheduleInput{Cron: ptr("61 * * * *")}); !errors.As(err, &ve) {
		t.Fatalf("cron inválido: %v", err)
	}
	if _, err := e.svc.SetBackupSchedule(octx, domain.HumanActor, service.SetBackupScheduleInput{Timezone: ptr("Marte/Olympus")}); !errors.As(err, &ve) {
		t.Fatalf("fuso inválido: %v", err)
	}
	on := true
	got, err := e.svc.SetBackupSchedule(octx, domain.HumanActor, service.SetBackupScheduleInput{Enabled: &on, Cron: ptr("0   3 * * *"), Timezone: ptr("America/Sao_Paulo")})
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 3, 11, 6, 0, 0, 0, time.UTC) // 03:00 em São Paulo (UTC-3); hoje já passou (são 09:00 lá)
	if !got.Enabled || got.Cron != "0 3 * * *" || got.NextFireAt == nil || !got.NextFireAt.Equal(want) {
		t.Fatalf("agenda = %+v (quer próxima %v)", got, want)
	}
	// repetir a mesma configuração não gera auditoria nova
	if _, err := e.svc.SetBackupSchedule(octx, domain.HumanActor, service.SetBackupScheduleInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	off := false
	got, err = e.svc.SetBackupSchedule(octx, domain.HumanActor, service.SetBackupScheduleInput{Enabled: &off})
	if err != nil || got.Enabled || got.NextFireAt != nil {
		t.Fatalf("desligar = %+v %v", got, err)
	}
	audit, err := e.svc.ListInstanceSettingAudit(octx, 10)
	if err != nil || len(audit) != 2 {
		t.Fatalf("auditoria = %d %v", len(audit), err)
	}
	if audit[0].Key != "system_flow.backup" || audit[1].Old != nil || !strings.Contains(string(audit[1].New), "0 3 * * *") {
		t.Fatalf("auditoria = %+v", audit)
	}
}

func enableSchedule(t *testing.T, e *env, owner domain.User, cron string) service.BackupSchedule {
	t.Helper()
	on := true
	s, err := e.svc.SetBackupSchedule(as(owner), domain.HumanActor, service.SetBackupScheduleInput{Enabled: &on, Cron: &cron, Timezone: ptr("UTC")})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSystemFlowFiresOnceAndAdvances(t *testing.T) {
	e := newEnv(t)
	fb := &fakeBackups{}
	e.svc.Backups = fb
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	ctx := context.Background()
	t0 := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return t0 }
	sch := enableSchedule(t, e, owner, "0 15 * * *")
	due := *sch.NextFireAt // 15:00

	if d, _ := e.svc.DueSystemFlows(ctx, t0); len(d) != 0 {
		t.Fatalf("ainda não venceu: %+v", d)
	}
	e.svc.Now = func() time.Time { return due.Add(20 * time.Second) }
	d, err := e.svc.DueSystemFlows(ctx, e.svc.Now())
	if err != nil || len(d) != 1 || d[0].Key != domain.SystemFlowBackup {
		t.Fatalf("due = %+v %v", d, err)
	}
	res, err := e.svc.FireSystemFlow(ctx, d[0].Key, *d[0].NextFireAt)
	if err != nil || res.Skipped || res.Backup == nil || res.Backup.Status != domain.BackupOK || res.Backup.Kind != domain.BackupScheduled || res.Backup.ActorType != "system" {
		t.Fatalf("fire = %+v %v", res, err)
	}
	if res.Backup.ScheduledFor == nil || !res.Backup.ScheduledFor.Equal(due) {
		t.Fatalf("scheduled_for = %v, quer %v", res.Backup.ScheduledFor, due)
	}
	if fb.calls[0].Kind != domain.BackupScheduled || !fb.calls[0].Prune {
		t.Fatalf("opções = %+v", fb.calls[0])
	}
	after, _ := e.svc.GetBackupSchedule(as(owner))
	if after.NextFireAt == nil || !after.NextFireAt.Equal(due.Add(24*time.Hour)) || after.LastFiredAt == nil {
		t.Fatalf("depois = %+v", after)
	}
	// o mesmo disparo repetido (outro nó, retentativa) não gera outro backup
	res2, err := e.svc.FireSystemFlow(ctx, domain.SystemFlowBackup, due)
	if err != nil || fb.count() != 1 {
		t.Fatalf("repetido: %+v %v calls=%d", res2, err, fb.count())
	}
	if !res2.Skipped || res2.SkipReason != "duplicate" {
		t.Fatalf("res2 = %+v", res2)
	}
}

func TestSystemFlowCatchUpFiresOnlyLatest(t *testing.T) {
	e := newEnv(t)
	fb := &fakeBackups{}
	e.svc.Backups = fb
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	ctx := context.Background()
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) }
	sch := enableSchedule(t, e, owner, "0 * * * *") // de hora em hora
	first := *sch.NextFireAt                        // 13:00
	// o servidor ficou parado 4 h
	e.svc.Now = func() time.Time { return first.Add(4*time.Hour + 5*time.Minute) } // 17:05
	res, err := e.svc.FireSystemFlow(ctx, domain.SystemFlowBackup, first)
	if err != nil || res.Backup == nil || fb.count() != 1 {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, fb.count())
	}
	if want := first.Add(4 * time.Hour); !res.Backup.ScheduledFor.Equal(want) { // 17:00, a mais recente
		t.Fatalf("scheduled_for = %v, quer %v", res.Backup.ScheduledFor, want)
	}
	after, _ := e.svc.GetBackupSchedule(as(owner))
	if want := first.Add(5 * time.Hour); !after.NextFireAt.Equal(want) { // 18:00
		t.Fatalf("próxima = %v, quer %v", after.NextFireAt, want)
	}
}

func TestSystemFlowSkips(t *testing.T) {
	e := newEnv(t)
	owner := mkUser(t, e, "dono@x.com", domain.RoleOwner)
	ctx := context.Background()
	e.svc.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) }

	// sem executor: nada roda e a agenda não avança
	e.svc.Backups = nil
	if res, err := e.svc.FireSystemFlow(ctx, domain.SystemFlowBackup, time.Now()); err != nil || !res.Skipped || res.SkipReason != "unavailable" {
		t.Fatalf("sem executor: %+v %v", res, err)
	}
	if _, err := e.svc.FireSystemFlow(ctx, "restore", time.Now()); err == nil {
		t.Fatal("fluxo desconhecido")
	}

	fb := &fakeBackups{started: make(chan struct{}), release: make(chan struct{})}
	e.svc.Backups = fb
	sch := enableSchedule(t, e, owner, "0 * * * *")
	// desligado no meio: pula
	off := false
	if _, err := e.svc.SetBackupSchedule(as(owner), domain.HumanActor, service.SetBackupScheduleInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.svc.FireSystemFlow(ctx, domain.SystemFlowBackup, *sch.NextFireAt); !res.Skipped || res.SkipReason != "disabled" {
		t.Fatalf("desligado: %+v", res)
	}
	// há um backup manual rodando: o agendado não empilha
	enableSchedule(t, e, owner, "0 * * * *")
	if _, err := e.svc.StartBackup(ctx, domain.HumanActor, "manual"); err != nil {
		t.Fatal(err)
	}
	<-fb.started
	e.svc.Now = func() time.Time { return sch.NextFireAt.Add(time.Minute) }
	if res, err := e.svc.FireSystemFlow(ctx, domain.SystemFlowBackup, *sch.NextFireAt); err != nil || !res.Skipped || res.SkipReason != "running" {
		t.Fatalf("em andamento: %+v %v", res, err)
	}
	close(fb.release)
	e.svc.WaitBackups()
}
