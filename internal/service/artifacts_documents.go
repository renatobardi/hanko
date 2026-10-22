package service

import (
	"context"
	"fmt"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Documentos de artefato (ADR-0064): restaurar uma versão anterior, reclassificar a mais nova, editar projeto,
// workspace e tags, e resolver uma versão por MAJOR.MINOR.

// GetArtifactDocument devolve o documento.
func (s *Service) GetArtifactDocument(ctx context.Context, companyID, id domain.ID) (domain.ArtifactDocument, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.ArtifactDocument{}, err
	}
	d, err := s.read().GetArtifactDocument(ctx, store.GetArtifactDocumentParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.ArtifactDocument{}, mapErr(err, "artifact document")
	}
	return artifactDocumentFromRow(d), nil
}

// ArtifactVersionID resolve a versão de um documento: "M.m" ou vazio (a mais nova).
func (s *Service) ArtifactVersionID(ctx context.Context, companyID, documentID domain.ID, version string) (domain.ID, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.ID{}, err
	}
	q := s.read()
	if version == "" {
		if _, err := q.GetArtifactDocument(ctx, store.GetArtifactDocumentParams{CompanyID: companyID, ID: documentID}); err != nil {
			return domain.ID{}, mapErr(err, "artifact document")
		}
		a, err := q.LatestArtifactOfDocument(ctx, documentID)
		if err != nil {
			return domain.ID{}, mapErr(err, "artifact")
		}
		return a.ID, nil
	}
	v, err := domain.ParseSemVer(version)
	if err != nil {
		return domain.ID{}, err
	}
	a, err := q.GetArtifactBySemVer(ctx, store.GetArtifactBySemVerParams{CompanyID: companyID, DocumentID: documentID,
		Major: i32(v.Major), Minor: i32(v.Minor)})
	if err != nil {
		return domain.ID{}, mapErr(err, "artifact version")
	}
	return a.ID, nil
}

// RestoreArtifact publica, por cima da mais nova, uma versão nova com o conteúdo da versão versionID. É sempre major
// e registra de onde veio (ADR-0064). Member; persona pela regra do documento (projeto da sua task ou run).
func (s *Service) RestoreArtifact(ctx context.Context, actor domain.Actor, companyID, versionID domain.ID, taskID, runID *domain.ID) (ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ArtifactView{}, err
	}
	src, content, err := s.ArtifactContent(ctx, companyID, versionID)
	if err != nil {
		return ArtifactView{}, err
	}
	if src.Class != domain.ClassArtifact {
		return ArtifactView{}, domain.Invalid("artifact", "attachments have a single version")
	}
	latest, err := s.read().LatestArtifactOfDocument(ctx, src.DocumentID)
	if err != nil {
		return ArtifactView{}, mapErr(err, "artifact")
	}
	if latest.ID == src.ID {
		return ArtifactView{}, domain.Invalid("version", "already the latest version")
	}
	doc := src.DocumentID
	from := src.ID
	return s.putArtifact(ctx, actor, companyID, PutArtifactInput{Name: src.Name, Mime: src.Mime, Content: content,
		DocumentID: &doc, TaskID: taskID, RunID: runID, restoredFrom: &from,
		ChangeReason: "restaurada de " + src.SemVer().String()})
}

// ReclassifyArtifact troca a classificação da versão mais nova do documento e a renumera (ex.: 1.3 → 2.0). Versões
// anteriores nunca são renumeradas: podem já ter sido citadas (ADR-0064). Member.
func (s *Service) ReclassifyArtifact(ctx context.Context, actor domain.Actor, companyID, versionID domain.ID, kind domain.ChangeKind) (ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ArtifactView{}, err
	}
	if !kind.Valid() {
		return ArtifactView{}, domain.Invalid("change", fmt.Sprintf("unknown change %q (use major or minor)", kind))
	}
	err := s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetArtifactByID(t.ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: versionID})
		if err != nil {
			return mapErr(err, "artifact")
		}
		if cur.Kind != string(domain.ClassArtifact) {
			return domain.Invalid("artifact", "attachments have a single version")
		}
		if err := t.q.LockArtifactDocument(t.ctx, cur.DocumentID); err != nil {
			return fmt.Errorf("lock artifact document: %w", err)
		}
		latest, err := t.q.LatestArtifactOfDocument(t.ctx, cur.DocumentID)
		if err != nil {
			return mapErr(err, "artifact")
		}
		switch {
		case latest.ID != cur.ID:
			return domain.Invalid("version", "only the latest version can be reclassified")
		case cur.Version == 1:
			return domain.Invalid("version", "the first version is always 1.0")
		}
		prev, err := t.q.ListArtifactDocumentVersions(t.ctx, store.ListArtifactDocumentVersionsParams{CompanyID: companyID, DocumentID: cur.DocumentID})
		if err != nil || len(prev) < 2 {
			return fmt.Errorf("artifact versions: %w", err)
		}
		before := artifactFromRow(prev[1]).SemVer() // a lista vem da mais nova para a mais antiga
		next := before.Next(kind)
		from := artifactFromRow(cur).SemVer()
		if _, err := t.q.ReclassifyArtifactVersion(t.ctx, store.ReclassifyArtifactVersionParams{CompanyID: companyID, ID: cur.ID,
			Major: i32(next.Major), Minor: i32(next.Minor), ChangeKind: string(kind)}); err != nil {
			return mapErr(err, "reclassify artifact")
		}
		return t.activity(actor, companyID, "artifact.reclassified", "artifact", cur.ID, map[string]any{
			"name": cur.Name, "document_id": cur.DocumentID, "from": from.String(), "to": next.String(), "change": kind}, "")
	})
	if err != nil {
		return ArtifactView{}, err
	}
	return s.GetArtifactView(ctx, companyID, versionID)
}

// ArtifactDocumentPatch muda projeto, workspace e tags de um documento; campo nil não muda (Clear* esvazia).
type ArtifactDocumentPatch struct {
	ProjectID, WorkspaceID       *domain.ID
	ClearProject, ClearWorkspace bool
	Tags                         []string
}

// UpdateArtifactDocument edita projeto, workspace e tags do documento. Não cria versão: não são identidade. Member.
func (s *Service) UpdateArtifactDocument(ctx context.Context, actor domain.Actor, companyID, documentID domain.ID, p ArtifactDocumentPatch) (domain.ArtifactDocument, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return domain.ArtifactDocument{}, err
	}
	var out store.ArtifactDocument
	err := s.withTx(ctx, func(t *txc) error {
		d, err := t.q.GetArtifactDocument(t.ctx, store.GetArtifactDocumentParams{CompanyID: companyID, ID: documentID})
		if err != nil {
			return mapErr(err, "artifact document")
		}
		project, workspace, tags := d.ProjectID, d.WorkspaceID, d.Tags
		switch {
		case p.ClearProject:
			project = nil
		case p.ProjectID != nil:
			if _, err := t.q.GetProjectByID(t.ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *p.ProjectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
			project = p.ProjectID
		}
		switch {
		case p.ClearWorkspace:
			workspace = nil
		case p.WorkspaceID != nil:
			if _, err := t.q.GetWorkspaceByID(t.ctx, store.GetWorkspaceByIDParams{CompanyID: companyID, ID: *p.WorkspaceID}); err != nil {
				return domain.Invalid("workspace_id", "workspace not found in company")
			}
			workspace = p.WorkspaceID
		}
		if p.Tags != nil {
			if tags, err = domain.NormalizeTags(p.Tags); err != nil {
				return err
			}
		}
		if out, err = t.q.UpdateArtifactDocumentMeta(t.ctx, store.UpdateArtifactDocumentMetaParams{CompanyID: companyID, ID: documentID,
			ProjectID: project, WorkspaceID: workspace, Tags: tags, UpdatedAt: t.now}); err != nil {
			return mapErr(err, "update artifact document")
		}
		return t.activity(actor, companyID, "artifact.document_updated", "artifact_document", documentID, map[string]any{
			"name": d.Name, "project_id": project, "workspace_id": workspace, "tags": tags}, "")
	})
	if err != nil {
		return domain.ArtifactDocument{}, err
	}
	return artifactDocumentFromRow(out), nil
}

// artifactBriefs lista os documentos que a run deve conhecer (ADR-0064), sem repetir: primeiro os com versão na task,
// depois os do projeto da task, depois os que a persona publicou por último (o caso do chat, que não tem task).
func artifactBriefs(ctx context.Context, q *store.Queries, rc RunContext) ([]ArtifactBrief, error) {
	var filters []store.ListArtifactViewsParams
	base := store.ListArtifactViewsParams{CompanyID: rc.Company.ID, LatestOnly: true, Lim: MaxBriefingArtifacts}
	if rc.Task.ID != (domain.ID{}) {
		f := base
		id := rc.Task.ID
		f.TaskID = &id
		filters = append(filters, f)
		if rc.Task.ProjectID != nil {
			f := base
			f.ProjectID = rc.Task.ProjectID
			filters = append(filters, f)
		}
	}
	if rc.Persona.ID != (domain.ID{}) {
		f := base
		id := rc.Persona.ID
		f.PersonaID = &id
		filters = append(filters, f)
	}
	seen := map[domain.ID]bool{}
	var out []ArtifactBrief
	for i, f := range filters {
		if len(out) >= MaxBriefingArtifacts {
			break
		}
		rows, err := q.ListArtifactViews(ctx, f)
		if err != nil {
			return nil, fmt.Errorf("list artifacts for briefing: %w", err)
		}
		for _, r := range rows {
			if seen[r.Artifact.DocumentID] || len(out) >= MaxBriefingArtifacts {
				continue
			}
			seen[r.Artifact.DocumentID] = true
			v := artifactView(r)
			out = append(out, ArtifactBrief{DocumentID: v.DocumentID, Name: v.Name, SemVer: v.SemVer, Mime: v.Mime, TaskKey: v.TaskKey,
				Tags: v.Tags, UpdatedAt: v.CreatedAt, InTask: i == 0 && rc.Task.ID != (domain.ID{})})
		}
	}
	return out, nil
}
