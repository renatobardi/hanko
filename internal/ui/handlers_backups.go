package ui

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
	"github.com/renatobardi/hanko/internal/service"
	"github.com/renatobardi/hanko/internal/ui/views"
)

// Configurações › Backups (ADR-0060): histórico, backup agora e agendamento (fluxo de sistema). Dono da instalação.

type backupsBackend interface {
	ListBackups(ctx context.Context, limit int) ([]domain.InstanceBackup, error)
	StartBackup(ctx context.Context, actor domain.Actor, label string) (domain.InstanceBackup, error)
	GetBackupSchedule(ctx context.Context) (service.BackupSchedule, error)
	SetBackupSchedule(ctx context.Context, actor domain.Actor, in service.SetBackupScheduleInput) (service.BackupSchedule, error)
	BackupsAvailable() bool
	BackupsDir() string
}

func (s *server) backupsBE() (backupsBackend, bool) {
	b, ok := s.be.(backupsBackend)
	return b, ok
}

var backupNotices = map[string]string{
	"iniciado": i18n.N("Backup iniciado. Ele roda em segundo plano; recarregue a página para ver o resultado."),
	"agenda":   i18n.N("Agendamento salvo."),
}

func backupKindLabel(ctx context.Context, kind string) string {
	switch kind {
	case domain.BackupManual:
		return i18n.T(ctx, "manual")
	case domain.BackupScheduled:
		return i18n.T(ctx, "agendado")
	case domain.BackupPreUpdate:
		return i18n.T(ctx, "antes de atualizar")
	case domain.BackupPreDeploy:
		return i18n.T(ctx, "antes do deploy")
	}
	return kind
}

func backupStatusLabel(ctx context.Context, st string) string {
	switch st {
	case domain.BackupOK:
		return i18n.T(ctx, "concluído")
	case domain.BackupFailed:
		return i18n.T(ctx, "falhou")
	}
	return i18n.T(ctx, "em andamento")
}

func fmtBackupTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "—"
	}
	return t.Local().Format("02/01/2006 15:04")
}

func backupErrorMsg(ctx context.Context, err error) string {
	var ve *domain.ValidationError
	switch {
	case errors.As(err, &ve):
		switch ve.Field {
		case "cron":
			return i18n.T(ctx, "Cron inválido: use 5 campos (minuto hora dia mês dia-da-semana), ex.: 0 3 * * *.")
		case "timezone":
			return i18n.T(ctx, "Fuso horário desconhecido: use um nome IANA, ex.: America/Sao_Paulo.")
		case "backups":
			return i18n.T(ctx, "Este servidor não consegue fazer backups: falta o diretório de backups ou o pg_dump.")
		}
		return ve.Field + ": " + ve.Reason + "."
	case errors.Is(err, domain.ErrConflict):
		return i18n.T(ctx, "Já há um backup em andamento.")
	}
	_, title, msg := friendly(ctx, err)
	return title + ". " + msg
}

func (s *server) backupsVM(r *http.Request, co domain.Company, be backupsBackend) views.BackupsVM {
	ctx := r.Context()
	base := "/c/" + co.Slug
	vm := views.BackupsVM{Sections: settingsSections(ctx, base, "backups"), Notice: i18n.T(ctx, backupNotices[r.URL.Query().Get("ok")]),
		Available: be.BackupsAvailable(), Dir: be.BackupsDir(), ActionRun: base + "/configuracoes/backups", ActionSchedule: base + "/configuracoes/backups/agenda"}
	sch, err := be.GetBackupSchedule(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrNotAllowed) || errors.Is(err, domain.ErrForbidden) {
			vm.Forbidden = true
			return vm
		}
		vm.Error = backupErrorMsg(ctx, err)
		return vm
	}
	vm.Enabled, vm.Cron, vm.Timezone = sch.Enabled, sch.Cron, sch.Timezone
	vm.Next, vm.Last = fmtBackupTime(sch.NextFireAt), fmtBackupTime(sch.LastFiredAt)
	list, err := be.ListBackups(ctx, 50)
	if err != nil {
		vm.Error = backupErrorMsg(ctx, err)
		return vm
	}
	for _, b := range list {
		if b.Status == domain.BackupRunning {
			vm.Running = true
		}
		row := views.BackupRowVM{When: fmtBackupTime(&b.StartedAt), Kind: backupKindLabel(ctx, b.Kind), Status: b.Status, StatusTx: backupStatusLabel(ctx, b.Status),
			File: b.FileName, Label: b.Label, Error: b.Error, Blobs: b.Blobs, Actor: b.ActorType, Size: "—", Duration: "—"}
		if b.FileName == "" {
			row.File = "—"
		}
		if b.Status == domain.BackupOK {
			row.Size = fmtBytes(ctx, b.SizeBytes)
		}
		if d := b.Duration(); d > 0 {
			row.Duration = d.Round(time.Second).String()
		}
		vm.Rows = append(vm.Rows, row)
	}
	return vm
}

func (s *server) renderBackups(w http.ResponseWriter, r *http.Request, co domain.Company, status int, vm views.BackupsVM) {
	sh := s.settingsShell(r, co, i18n.T(r.Context(), "Backups"))
	s.render(w, r, status, views.BackupsPage(sh, vm))
}

func (s *server) handleBackups(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.backupsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	s.renderBackups(w, r, co, http.StatusOK, s.backupsVM(r, co, be))
}

func (s *server) handleStartBackup(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.backupsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	if _, err := be.StartBackup(ctx, actorFrom(ctx), strings.TrimSpace(r.PostFormValue("label"))); err != nil {
		vm := s.backupsVM(r, co, be)
		vm.Error = backupErrorMsg(ctx, err)
		s.renderBackups(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/backups?ok=iniciado")
}

func (s *server) handleSaveBackupSchedule(w http.ResponseWriter, r *http.Request, co domain.Company) {
	be, ok := s.backupsBE()
	if !ok {
		s.unavailable(w, r)
		return
	}
	ctx := r.Context()
	enabled := r.PostFormValue("enabled") == "true"
	cron, tz := strings.TrimSpace(r.PostFormValue("cron")), strings.TrimSpace(r.PostFormValue("timezone"))
	if _, err := be.SetBackupSchedule(ctx, actorFrom(ctx), service.SetBackupScheduleInput{Enabled: &enabled, Cron: &cron, Timezone: &tz}); err != nil {
		vm := s.backupsVM(r, co, be)
		vm.Error = backupErrorMsg(ctx, err)
		// o formulário volta com o que a pessoa digitou, não com o valor salvo
		vm.Enabled, vm.Cron, vm.Timezone = enabled, cron, tz
		s.renderBackups(w, r, co, statusFor(err), vm)
		return
	}
	redirect(w, r, "/c/"+co.Slug+"/configuracoes/backups?ok=agenda")
}
