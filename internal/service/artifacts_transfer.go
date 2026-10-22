package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/export"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
)

// Exportação e importação de artefatos (ADR-0028). O campo `artifacts` é opcional no hanko.company.v1: um arquivo
// sem ele continua válido, e um leitor antigo o ignora.

// MaxExportArtifactBytes é o total de conteúdo de artefatos que uma exportação aceita. O corpo de uma importação
// pela API é limitado a 32 MiB e o base64 acrescenta um terço: acima disto o arquivo gerado não poderia ser importado.
const MaxExportArtifactBytes int64 = 20 << 20

// exportArtifacts monta os metadados dos artefatos no documento e guarda os ids para o conteúdo sair em fluxo.
func (s *Service) exportArtifacts(ctx context.Context, q *store.Queries, companyID domain.ID, st *ExportStream, doc *export.Document,
	peSlug map[domain.ID]string, runKey map[domain.ID]string) error {
	rows, err := q.ExportArtifacts(ctx, companyID)
	if err != nil {
		return fmt.Errorf("export artifacts: %w", err)
	}
	var total int64
	for _, r := range rows {
		total += r.SizeBytes
	}
	if total > MaxExportArtifactBytes {
		return domain.Invalid("with_artifacts", fmt.Sprintf("artifacts total %d bytes; an export carries up to %d bytes so the file can be imported", total, MaxExportArtifactBytes))
	}
	for _, r := range rows {
		a := export.Artifact{Name: r.Name, Mime: r.Mime, Version: int(r.Version), Task: r.TaskNumber, Project: r.ProjectSlug,
			Persona: r.PersonaSlug, Run: refSlug(runKey, r.RunID), Author: exportActor(r.AuthorType, r.AuthorID, peSlug),
			SHA256: r.Sha256, SizeBytes: r.SizeBytes, CreatedAt: r.CreatedAt,
			Document: r.DocumentID.String(), Major: int(r.VersionMajor), Minor: int(r.VersionMinor), Change: r.ChangeKind,
			ChangeReason: r.ChangeReason, Tags: r.DocTags, DocumentProject: r.DocProjectSlug}
		doc.Artifacts = append(doc.Artifacts, a)
		st.artIDs = append(st.artIDs, r.ID)
		st.artStore = append(st.artStore, r.Storage)
	}
	return nil
}

// planArtifacts valida os artefatos do arquivo antes de gravar qualquer coisa.
func (s *Service) planArtifacts(doc export.Document, tk map[int64]int, pe, pr, runKeys map[string]int) error {
	if len(doc.Artifacts) == 0 {
		return nil
	}
	maxFile, quota := s.artifactLimits()
	var total int64
	last := map[string]int{}
	seen := map[string]bool{}
	for i, a := range doc.Artifacts {
		name, err := cleanArtifactName(a.Name)
		if err != nil {
			return domain.Invalid(iv("artifacts", i, "name"), invalidReason(err))
		}
		n := int64(len(a.Content))
		switch {
		case n == 0:
			return domain.Invalid(iv("artifacts", i, "content"), "required")
		case n > maxFile:
			return domain.Invalid(iv("artifacts", i, "content"), fmt.Sprintf("too large (%d bytes; max %d)", n, maxFile))
		}
		if total += n; total > quota {
			return domain.Invalid("artifacts", fmt.Sprintf("total content exceeds the artifact quota (%d bytes)", quota))
		}
		if _, err := normalizeMime(a.Mime, name, a.Content); err != nil {
			return domain.Invalid(iv("artifacts", i, "mime"), invalidReason(err))
		}
		if sum := sha256.Sum256(a.Content); a.SHA256 != "" && !strings.EqualFold(a.SHA256, hex.EncodeToString(sum[:])) {
			return domain.Invalid(iv("artifacts", i, "sha256"), "does not match the content")
		}
		if a.Task != 0 {
			if _, ok := tk[a.Task]; !ok {
				return domain.Invalid(iv("artifacts", i, "task"), fmt.Sprintf("task %d not found", a.Task))
			}
		}
		if err := ref("artifacts", i, "project", a.Project, pr); err != nil {
			return err
		}
		if err := ref("artifacts", i, "document_project", a.DocumentProject, pr); err != nil {
			return err
		}
		if err := ref("artifacts", i, "persona", a.Persona, pe); err != nil {
			return err
		}
		if err := checkActor(iv("artifacts", i, "author"), a.Author, pe); err != nil {
			return err
		}
		if a.Run != "" {
			ri, ok := runKeys[a.Run]
			if !ok {
				return domain.Invalid(iv("artifacts", i, "run"), fmt.Sprintf("run %q not in the file", a.Run))
			}
			if rt := doc.Runs[ri].Task; a.Task != 0 && rt != a.Task {
				return domain.Invalid(iv("artifacts", i, "run"), "the run belongs to another task")
			}
		}
		// versões de um documento: 1, 2, 3... em ordem, com MAJOR.MINOR distintos (ADR-0064)
		k := importDocKey(a, name)
		if a.Version != last[k]+1 {
			return domain.Invalid(iv("artifacts", i, "version"), fmt.Sprintf("expected version %d after %d in the same artifact", last[k]+1, last[k]))
		}
		last[k] = a.Version
		sv, change := importSemVer(a)
		if sv.Major < 1 || sv.Minor < 0 || !change.Valid() || (change == domain.ChangeMajor && sv.Minor != 0) {
			return domain.Invalid(iv("artifacts", i, "major"), "invalid MAJOR.MINOR or change")
		}
		sk := k + "@" + sv.String()
		if seen[sk] {
			return domain.Invalid(iv("artifacts", i, "major"), fmt.Sprintf("version %s repeated in the same artifact", sv))
		}
		seen[sk] = true
		if len([]rune(a.ChangeReason)) > domain.MaxChangeReasonRunes {
			return domain.Invalid(iv("artifacts", i, "change_reason"), "too long")
		}
		if _, err := domain.NormalizeTags(a.Tags); err != nil {
			return domain.Invalid(iv("artifacts", i, "tags"), invalidReason(err))
		}
	}
	return nil
}

// importDocKey agrupa as versões de um documento: pela chave do arquivo (ADR-0064) ou, num arquivo antigo, pela
// cadeia (task, nome).
func importDocKey(a export.Artifact, name string) string {
	if a.Document != "" {
		return "doc:" + a.Document
	}
	return fmt.Sprintf("chain:%d|%s", a.Task, name)
}

// importSemVer lê MAJOR.MINOR e a classificação do arquivo; um arquivo antigo numera vN como N.0 (major).
func importSemVer(a export.Artifact) (domain.SemVer, domain.ChangeKind) {
	if a.Major == 0 {
		return domain.SemVer{Major: a.Version}, domain.ChangeMajor
	}
	c := domain.ChangeKind(a.Change)
	if c == "" {
		c = domain.ChangeMinor
		if a.Minor == 0 {
			c = domain.ChangeMajor
		}
	}
	return domain.SemVer{Major: a.Major, Minor: a.Minor}, c
}

// importArtifacts grava os artefatos do arquivo na empresa nova.
func (t *txc) importArtifacts(cid domain.ID, doc export.Document, taskID map[int64]domain.ID, prID, peID, runID map[string]domain.ID) error {
	ctx := t.ctx
	docs := map[string]domain.ID{}
	for i, a := range doc.Artifacts {
		name, err := cleanArtifactName(a.Name)
		if err != nil {
			return err
		}
		mt, err := normalizeMime(a.Mime, name, a.Content)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(a.Content)
		var task *domain.ID
		if a.Task != 0 {
			id := taskID[a.Task]
			task = &id
		}
		run := optID(runID, a.Run)
		if run != nil && task == nil { // a task vem da run
			for _, r := range doc.Runs {
				if r.Key == a.Run && r.Task != 0 { // run de chat não tem task
					id := taskID[r.Task]
					task = &id
				}
			}
		}
		persona := optID(peID, a.Persona)
		authorID := optID(peID, a.Author.Persona)
		k := importDocKey(a, name)
		docID, ok := docs[k]
		if !ok {
			tags, _ := domain.NormalizeTags(a.Tags) // validadas em planArtifacts
			project := optID(prID, a.Project)
			if a.DocumentProject != "" {
				project = optID(prID, a.DocumentProject)
			}
			d, err := t.q.CreateArtifactDocument(ctx, store.CreateArtifactDocumentParams{ID: domain.NewID(), CompanyID: cid,
				Kind: string(domain.ClassArtifact), Name: name, ProjectID: project, Tags: tags, CreatedAt: a.CreatedAt})
			if err != nil {
				return mapErr(err, fmt.Sprintf("import artifact %d document", i))
			}
			docID, docs[k] = d.ID, d.ID
		} else if err := t.q.TouchArtifactDocument(ctx, store.TouchArtifactDocumentParams{CompanyID: cid, ID: docID, Name: name, UpdatedAt: a.CreatedAt}); err != nil {
			return mapErr(err, fmt.Sprintf("import artifact %d document", i))
		}
		sv, change := importSemVer(a)
		id := domain.NewID()
		row, err := t.q.CreateArtifact(ctx, store.CreateArtifactParams{ID: id, CompanyID: cid, TaskID: task, RunID: run,
			ProjectID: optID(prID, a.Project), PersonaID: persona, AuthorType: a.Author.Type, AuthorID: authorID, Name: name, Mime: mt,
			SizeBytes: int64(len(a.Content)), Sha256: hex.EncodeToString(sum[:]), Version: i32(a.Version), CreatedAt: a.CreatedAt, Storage: t.s.blobs().Active().Name(), Kind: string(domain.ClassArtifact),
			DocumentID: docID, VersionMajor: i32(sv.Major), VersionMinor: i32(sv.Minor), ChangeKind: string(change),
			ChangeReason: strings.TrimSpace(a.ChangeReason)})
		if err != nil {
			return mapErr(err, fmt.Sprintf("import artifact %d", i))
		}
		drv := t.s.blobs().Active()
		key := storage.Key{CompanyID: cid, ArtifactID: row.ID}
		if err := drv.Put(storage.WithTx(ctx, t.q), key, a.Content, mt); err != nil {
			return fmt.Errorf("import artifact blob: %w", err)
		}
		if !drv.Transactional() {
			t.onRollback = append(t.onRollback, func() {
				dctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				_ = drv.Delete(dctx, key)
			})
		}
	}
	return nil
}
