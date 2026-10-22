//go:build integration

package ui_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/renatobardi/hanko/internal/backup"
)

type uiFakeBackups struct{ err error }

func (f uiFakeBackups) Create(_ context.Context, o backup.CreateOptions) (backup.Entry, error) {
	if f.err != nil {
		return backup.Entry{}, f.err
	}
	return backup.Entry{Name: "hanko-20260310T030000Z.tar", Size: 3 << 20, Manifest: backup.Manifest{SchemaVersion: 180, Kind: o.Kind}}, nil
}

func TestUIBackupsPage(t *testing.T) {
	u := newSecretsUI(t)
	u.svc.Backups = uiFakeBackups{}
	u.svc.BackupDir = "/var/lib/hanko/backups"
	const page = "/c/acme/configuracoes/backups"

	rec := u.get(page)
	b := bodyOf(rec)
	if rec.Code != 200 || !strings.Contains(b, "Agendamento") || !strings.Contains(b, "Nenhum backup feito") || !strings.Contains(b, "/var/lib/hanko/backups") || !strings.Contains(b, "0 3 * * *") {
		t.Fatalf("página: %d\n%s", rec.Code, b)
	}
	// a aba existe nas outras telas de configurações
	if !strings.Contains(bodyOf(u.get("/c/acme/configuracoes/instancia")), "/configuracoes/backups") {
		t.Fatal("aba Backups ausente")
	}

	// agendamento: cron inválido volta 422 com a mensagem e o que foi digitado
	rec = u.form(page+"/agenda", url.Values{"enabled": {"true"}, "cron": {"99 * * * *"}, "timezone": {"UTC"}})
	if rec.Code != 422 || !strings.Contains(bodyOf(rec), "Cron inválido") || !strings.Contains(bodyOf(rec), "99 * * * *") {
		t.Fatalf("cron inválido: %d", rec.Code)
	}
	rec = u.form(page+"/agenda", url.Values{"enabled": {"true"}, "cron": {"30 2 * * *"}, "timezone": {"America/Sao_Paulo"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=agenda") {
		t.Fatalf("salvar agenda: %d %s", rec.Code, bodyOf(rec))
	}
	b = bodyOf(u.get(page + "?ok=agenda"))
	if !strings.Contains(b, "30 2 * * *") || !strings.Contains(b, "America/Sao_Paulo") || !strings.Contains(b, "Agendamento salvo.") {
		t.Fatalf("agenda não aparece: %s", b)
	}

	// backup agora (em segundo plano); depois de terminar aparece no histórico
	rec = u.form(page, url.Values{"label": {"antes do upgrade"}})
	if rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "ok=iniciado") {
		t.Fatalf("iniciar: %d %s", rec.Code, bodyOf(rec))
	}
	u.svc.WaitBackups()
	b = bodyOf(u.get(page))
	for _, want := range []string{"antes do upgrade", "hanko-20260310T030000Z.tar", "concluído", "3 MiB"} {
		if !strings.Contains(b, want) {
			t.Errorf("histórico sem %q", want)
		}
	}

	// falha aparece como falha, com a mensagem
	u.svc.Backups = uiFakeBackups{err: errors.New("pg_dump: sem conexão")}
	u.form(page, url.Values{})
	u.svc.WaitBackups()
	b = bodyOf(u.get(page))
	if !strings.Contains(b, "falhou") || !strings.Contains(b, "pg_dump: sem conexão") {
		t.Fatalf("falha não aparece: %s", b)
	}
}

func TestUIBackupsUnavailableExplains(t *testing.T) {
	u := newSecretsUI(t)
	b := bodyOf(u.get("/c/acme/configuracoes/backups"))
	if !strings.Contains(b, "não consegue fazer backups") {
		t.Fatalf("deveria explicar a indisponibilidade:\n%s", b)
	}
}
