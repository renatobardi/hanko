package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Ciclo de vida da empresa (ADR-0057): editar, arquivar (somente leitura, reversível), restaurar e excluir.
// O somente-leitura não mora aqui: txc.activity recusa qualquer evento de empresa arquivada (domain.ArchiveAllowsVerb).

// UpdateCompanyInput são os campos editáveis; nil mantém o valor atual.
type UpdateCompanyInput struct {
	Name        *string
	Timezone    *string
	Description *string
	// TaskPrefix só muda enquanto a empresa não tem nenhuma task.
	TaskPrefix *string
}

// maxCompanyDescriptionRunes é o teto da descrição (o banco confere em caracteres).
const maxCompanyDescriptionRunes = 2000

// UpdateCompany edita nome, fuso, descrição e (com zero tasks) o prefixo. Admin.
func (s *Service) UpdateCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, in UpdateCompanyInput) (domain.Company, error) {
	ctx, span := lifecycleSpan(ctx, "company.update")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Company{}, err
	}
	var out domain.Company
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetCompanyByIDForUpdate(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		if cur.ArchivedAt != nil {
			return domain.ErrCompanyArchived
		}
		name, tz, desc, prefix := cur.Name, cur.Timezone, cur.Description, cur.TaskPrefix
		changed := map[string]any{}
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" {
				return domain.Invalid("name", "required")
			}
			if len([]rune(n)) > 120 {
				return domain.Invalid("name", "máximo de 120 caracteres")
			}
			if n != name {
				changed["name"] = n
			}
			name = n
		}
		if in.Timezone != nil {
			z := strings.TrimSpace(*in.Timezone)
			if _, err := time.LoadLocation(z); err != nil || z == "" {
				return domain.Invalid("timezone", "unknown IANA time zone")
			}
			if z != tz {
				changed["timezone"] = z
			}
			tz = z
		}
		if in.Description != nil {
			d := strings.TrimSpace(*in.Description)
			if len([]rune(d)) > maxCompanyDescriptionRunes {
				return domain.Invalid("description", fmt.Sprintf("máximo de %d caracteres", maxCompanyDescriptionRunes))
			}
			if d != desc {
				changed["description"] = true
			}
			desc = d
		}
		if in.TaskPrefix != nil {
			p := strings.ToUpper(strings.TrimSpace(*in.TaskPrefix))
			if !prefixRe.MatchString(p) {
				return domain.Invalid("task_prefix", "must be 1-10 uppercase letters/digits starting with a letter")
			}
			if p != prefix {
				n, err := t.q.CountCompanyTasks(ctx, companyID)
				if err != nil {
					return fmt.Errorf("count tasks: %w", err)
				}
				if n > 0 {
					return fmt.Errorf("o prefixo só muda numa empresa sem tasks (esta tem %d): %w", n, domain.ErrConflict)
				}
				changed["task_prefix"] = p
			}
			prefix = p
		}
		row, err := t.q.UpdateCompanyProfile(ctx, store.UpdateCompanyProfileParams{ID: companyID, Name: name, Timezone: tz,
			Description: desc, TaskPrefix: prefix, Now: t.now})
		if err != nil {
			return mapErr(err, "update company")
		}
		out = companyFromRow(row)
		if len(changed) == 0 {
			return nil
		}
		return t.activity(actor, companyID, "company.updated", "company", companyID, changed, "")
	})
	return out, err
}

// archiveSnapshot é o que o arquivamento mexeu, para o "restaurar e retomar".
type archiveSnapshot struct {
	Personas []domain.ID `json:"personas"`
	Flows    []domain.ID `json:"flows"`
}

// ArchiveCompany põe a empresa em somente leitura: pausa as personas, cancela as execuções, desliga os fluxos e
// marca archived_at, tudo numa transação. Admin. Já arquivada = ErrConflict.
func (s *Service) ArchiveCompany(ctx context.Context, actor domain.Actor, companyID domain.ID) (domain.Company, error) {
	ctx, span := lifecycleSpan(ctx, "company.archive")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Company{}, err
	}
	var out domain.Company
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetCompanyByIDForUpdate(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		if cur.ArchivedAt != nil {
			return fmt.Errorf("a empresa já está arquivada: %w", domain.ErrConflict)
		}
		// Personas primeiro: com elas pausadas, o cancelamento de uma execução não acorda ninguém.
		personas, err := t.q.ArchivePausePersonas(ctx, store.ArchivePausePersonasParams{CompanyID: companyID, Now: t.now})
		if err != nil {
			return fmt.Errorf("pause personas: %w", err)
		}
		flows, err := t.q.ArchiveDisableFlows(ctx, store.ArchiveDisableFlowsParams{CompanyID: companyID, Now: t.now})
		if err != nil {
			return fmt.Errorf("disable flows: %w", err)
		}
		runIDs, err := t.q.ListActiveRunIDs(ctx, companyID)
		if err != nil {
			return fmt.Errorf("list active runs: %w", err)
		}
		cancelled := 0
		for _, id := range runIDs {
			if err := t.lockTaskOfReviewRun(companyID, id); err != nil {
				return err
			}
			run, err := t.q.GetRunByIDForUpdate(ctx, store.GetRunByIDForUpdateParams{CompanyID: companyID, ID: id})
			if err != nil {
				return mapErr(err, "get run")
			}
			if !domain.RunStatus(run.Status).Active() {
				continue
			}
			if err := t.cancelRun(actor, companyID, run, map[string]any{"reason": "company_archived"}); err != nil {
				return err
			}
			cancelled++
		}
		snap, err := json.Marshal(archiveSnapshot{Personas: nonNilIDs(personas), Flows: nonNilIDs(flows)})
		if err != nil {
			return fmt.Errorf("marshal snapshot: %w", err)
		}
		var by *domain.ID
		if id, ok := auth.IdentityFrom(ctx); ok {
			uid := id.User.ID
			by = &uid
		}
		row, err := t.q.SetCompanyArchived(ctx, store.SetCompanyArchivedParams{ID: companyID, ArchivedBy: by, Snapshot: snap, Now: &t.now})
		if err != nil {
			return mapErr(err, "archive company")
		}
		out = companyFromRow(row)
		return t.activity(actor, companyID, "company.archived", "company", companyID, map[string]any{
			"personas_paused": len(personas), "flows_disabled": len(flows), "runs_cancelled": cancelled}, "")
	})
	return out, err
}

func nonNilIDs(ids []domain.ID) []domain.ID {
	if ids == nil {
		return []domain.ID{}
	}
	return ids
}

// RestoreCompany tira a empresa do somente leitura. Nada volta a trabalhar sozinho: personas e fluxos seguem
// pausados/desligados. Com resume, reativa exatamente o que o arquivamento mexeu (e que continua como ele deixou).
// Admin. Não arquivada = ErrConflict.
func (s *Service) RestoreCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, resume bool) (domain.Company, error) {
	ctx, span := lifecycleSpan(ctx, "company.restore")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return domain.Company{}, err
	}
	var out domain.Company
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetCompanyByIDForUpdate(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		if cur.ArchivedAt == nil {
			return fmt.Errorf("a empresa não está arquivada: %w", domain.ErrConflict)
		}
		var snap archiveSnapshot
		if len(cur.ArchiveSnapshot) > 0 {
			if err := json.Unmarshal(cur.ArchiveSnapshot, &snap); err != nil {
				return fmt.Errorf("decode archive snapshot: %w", err)
			}
		}
		row, err := t.q.ClearCompanyArchived(ctx, store.ClearCompanyArchivedParams{ID: companyID, Now: t.now})
		if err != nil {
			return mapErr(err, "restore company")
		}
		out = companyFromRow(row)
		resumedPersonas, resumedFlows := 0, 0
		if resume {
			ids, err := t.q.RestoreResumePersonas(ctx, store.RestoreResumePersonasParams{CompanyID: companyID, Now: t.now, Ids: snap.Personas})
			if err != nil {
				return fmt.Errorf("resume personas: %w", err)
			}
			resumedPersonas = len(ids)
			for _, fid := range snap.Flows {
				f, err := t.q.GetFlowByID(ctx, store.GetFlowByIDParams{CompanyID: companyID, ID: fid})
				if isNoRows(err) || err == nil && f.Enabled {
					continue
				}
				if err != nil {
					return fmt.Errorf("get flow: %w", err)
				}
				next, err := NextCron(f.Cron, f.Timezone, t.now)
				if err != nil {
					continue // cron que deixou de valer: o fluxo fica desligado
				}
				if _, err := t.q.UpdateFlow(ctx, store.UpdateFlowParams{Name: f.Name, Description: f.Description, Cron: f.Cron,
					Timezone: f.Timezone, Enabled: true, PersonaID: f.PersonaID, ProjectID: f.ProjectID, Priority: f.Priority,
					TaskTitle: f.TaskTitle, TaskDescription: f.TaskDescription, NextFireAt: &next, Now: t.now,
					CompanyID: companyID, ID: fid}); err != nil {
					return mapErr(err, "enable flow")
				}
				resumedFlows++
			}
		}
		return t.activity(actor, companyID, "company.restored", "company", companyID, map[string]any{
			"resume": resume, "personas_resumed": resumedPersonas, "flows_enabled": resumedFlows}, "")
	})
	return out, err
}

// CompanyDeletion é o registro de uma exclusão, com o export guardado (sem o conteúdo).
type CompanyDeletion struct {
	ID          domain.ID
	CompanyID   domain.ID
	Slug, Name  string
	DeletedBy   *domain.ID
	DeletedAt   time.Time
	ExportBytes int64
	SHA256      string
}

func deletionFromRow(r store.ListCompanyDeletionsRow) CompanyDeletion {
	return CompanyDeletion{ID: r.ID, CompanyID: r.CompanyID, Slug: r.Slug, Name: r.Name, DeletedBy: r.DeletedBy,
		DeletedAt: r.DeletedAt, ExportBytes: r.ExportBytes, SHA256: r.Sha256}
}

// DeleteCompany apaga a empresa. Só o owner dela, só arquivada, com o nome digitado por inteiro. Antes de apagar, guarda
// o export automático (execuções e, quando cabem, os artefatos) em company_deletions; como a empresa está arquivada, o
// arquivo é consistente. A atividade fica em admin_audit (a da empresa some com ela).
func (s *Service) DeleteCompany(ctx context.Context, actor domain.Actor, companyID domain.ID, confirm string) (CompanyDeletion, error) {
	ctx, span := lifecycleSpan(ctx, "company.delete")
	defer span.End()
	if err := s.require(ctx, companyID, domain.MemberOwner); err != nil {
		return CompanyDeletion{}, err
	}
	cur, err := s.read().GetCompanyByID(ctx, companyID)
	if err != nil {
		return CompanyDeletion{}, mapErr(err, "get company")
	}
	if cur.ArchivedAt == nil {
		return CompanyDeletion{}, fmt.Errorf("arquive a empresa antes de excluí-la: %w", domain.ErrConflict)
	}
	if strings.TrimSpace(confirm) != cur.Name {
		return CompanyDeletion{}, domain.Invalid("confirm", "digite o nome exato da empresa para confirmar")
	}
	raw, gz, sum, err := s.exportForDeletion(ctx, companyID)
	if err != nil {
		return CompanyDeletion{}, err
	}
	var out CompanyDeletion
	err = s.withTx(ctx, func(t *txc) error {
		now, err := t.q.GetCompanyByIDForUpdate(ctx, companyID)
		if err != nil {
			return mapErr(err, "get company")
		}
		if now.ArchivedAt == nil || !now.ArchivedAt.Equal(*cur.ArchivedAt) {
			return fmt.Errorf("a empresa mudou durante a exclusão (restaurada?); tente de novo: %w", domain.ErrConflict)
		}
		blobs, err := t.q.ListCompanyBlobArtifacts(ctx, companyID)
		if err != nil {
			return fmt.Errorf("list blobs: %w", err)
		}
		var by *domain.ID
		if id, ok := auth.IdentityFrom(ctx); ok {
			uid := id.User.ID
			by = &uid
		}
		row, err := t.q.InsertCompanyDeletion(ctx, store.InsertCompanyDeletionParams{ID: domain.NewID(), CompanyID: companyID,
			Slug: cur.Slug, Name: cur.Name, DeletedBy: by, Now: t.now, ExportGz: gz, ExportBytes: raw, Sha256: sum})
		if err != nil {
			return mapErr(err, "record deletion")
		}
		out = CompanyDeletion{ID: row.ID, CompanyID: row.CompanyID, Slug: row.Slug, Name: row.Name, DeletedBy: row.DeletedBy,
			DeletedAt: row.DeletedAt, ExportBytes: row.ExportBytes, SHA256: row.Sha256}
		if n, err := t.q.DeleteCompany(ctx, companyID); err != nil {
			return fmt.Errorf("delete company: %w", err)
		} else if n != 1 {
			return notFound("company")
		}
		ids, drivers := make([]domain.ID, len(blobs)), make([]string, len(blobs))
		for i, b := range blobs {
			ids[i], drivers[i] = b.ID, b.Storage
		}
		t.deleteBlobsAfterCommit(companyID, ids, drivers)
		return s.recordAdmin(ctx, t.q, "company.delete", cur.Slug, &companyID, "ok")
	})
	if err != nil {
		return CompanyDeletion{}, err
	}
	s.logAdmin(ctx, slog.LevelInfo, "company.delete", cur.Slug, &companyID, "ok", "export_bytes", raw)
	_ = actor
	return out, nil
}

// exportForDeletion monta o export automático (execuções e artefatos, sem logs) e o devolve comprimido.
func (s *Service) exportForDeletion(ctx context.Context, companyID domain.ID) (rawBytes int64, gz []byte, sum string, err error) {
	open := func(withArtifacts bool) (*ExportStream, error) {
		return s.OpenExport(ctx, companyID, ExportOptions{WithRuns: true, WithArtifacts: withArtifacts})
	}
	st, err := open(true)
	var ve *domain.ValidationError
	if errors.As(err, &ve) && ve.Field == "with_artifacts" { // não cabe no teto de importação: segue sem os artefatos
		st, err = open(false)
	}
	if err != nil {
		return 0, nil, "", err
	}
	defer st.Close()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	h := sha256.New()
	cw := &countWriter{w: io.MultiWriter(zw, h)}
	if err := st.WriteTo(ctx, cw); err != nil {
		return 0, nil, "", fmt.Errorf("export before delete: %w", err)
	}
	if err := zw.Close(); err != nil {
		return 0, nil, "", fmt.Errorf("compress export: %w", err)
	}
	return cw.n, buf.Bytes(), hex.EncodeToString(h.Sum(nil)), nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// ListCompanyDeletions lista as exclusões (mais recente primeiro). Só o dono da instalação, ou, em modo trusted, todos.
func (s *Service) ListCompanyDeletions(ctx context.Context) ([]CompanyDeletion, error) {
	q := s.read()
	if _, ok := auth.IdentityFrom(ctx); ok {
		if err := s.requireInstallOwner(ctx, q); err != nil {
			return nil, err
		}
	}
	rows, err := q.ListCompanyDeletions(ctx, 200)
	if err != nil {
		return nil, fmt.Errorf("list deletions: %w", err)
	}
	out := make([]CompanyDeletion, len(rows))
	for i, r := range rows {
		out[i] = deletionFromRow(r)
	}
	return out, nil
}

// CompanyDeletionExport devolve o hanko.company.v1 guardado na exclusão, já descomprimido por quem consome (gzip).
// Dono da instalação ou quem apagou a empresa.
func (s *Service) CompanyDeletionExport(ctx context.Context, id domain.ID) (CompanyDeletion, []byte, error) {
	q := s.read()
	row, err := q.GetCompanyDeletion(ctx, id)
	if err != nil {
		return CompanyDeletion{}, nil, mapErr(err, "company deletion")
	}
	if ident, ok := auth.IdentityFrom(ctx); ok && ident.User.Role != domain.RoleOwner && (row.DeletedBy == nil || *row.DeletedBy != ident.User.ID) {
		return CompanyDeletion{}, nil, notFound("company deletion")
	}
	return CompanyDeletion{ID: row.ID, CompanyID: row.CompanyID, Slug: row.Slug, Name: row.Name, DeletedBy: row.DeletedBy,
		DeletedAt: row.DeletedAt, ExportBytes: row.ExportBytes, SHA256: row.Sha256}, row.ExportGz, nil
}
