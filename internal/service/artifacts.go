package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Artefatos (ADR-0028). Quem publica é member ou uma persona (run token, sem papel); admin exclui. O limite por
// arquivo e a cota por empresa vêm da configuração (HANKO_ARTIFACT_MAX_BYTES / HANKO_ARTIFACT_QUOTA_BYTES).

const (
	// DefaultArtifactMaxBytes é o tamanho máximo de um arquivo quando a configuração não diz.
	DefaultArtifactMaxBytes int64 = 5 << 20
	// DefaultArtifactQuotaBytes é a cota por empresa (soma de todas as versões) quando a configuração não diz.
	DefaultArtifactQuotaBytes int64 = 512 << 20
	// MaxArtifactMaxBytes é o teto do limite por arquivo que a configuração pode escolher.
	MaxArtifactMaxBytes int64 = 16 << 20

	maxArtifactNameRunes = 200
	// maxDiffBytes é o maior texto comparado no diff entre versões.
	maxDiffBytes = 512 << 10
)

// artifactLimits são o limite efetivo de um arquivo e a cota da empresa.
func (s *Service) artifactLimits() (maxFile, quota int64) {
	maxFile, quota = s.ArtifactMaxBytes, s.ArtifactQuotaBytes
	if maxFile <= 0 {
		maxFile = DefaultArtifactMaxBytes
	}
	if quota <= 0 {
		quota = DefaultArtifactQuotaBytes
	}
	return maxFile, quota
}

// ArtifactLimits devolve o tamanho máximo de um arquivo e a cota por empresa em vigor.
func (s *Service) ArtifactLimits() (maxFileBytes, quotaBytes int64) { return s.artifactLimits() }

// PutArtifactInput publica uma versão de artefato. TaskID/RunID/ProjectID são opcionais; com task e sem projeto
// explícito, o projeto da task é herdado; com run e sem task, a task da run.
type PutArtifactInput struct {
	Name    string
	Mime    string // vazio = deduzido da extensão e do conteúdo
	Content []byte

	TaskID, RunID, ProjectID *domain.ID

	// createdAt (importação) preserva o instante do arquivo de origem; zero = agora.
	createdAt time.Time
}

// ArtifactView é uma versão com os nomes das ligações e o número de versões da cadeia.
type ArtifactView struct {
	domain.Artifact
	TaskKey     string `json:"task_key,omitempty"`
	TaskTitle   string `json:"task_title,omitempty"`
	ProjectSlug string `json:"project_slug,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	PersonaSlug string `json:"persona_slug,omitempty"`
	PersonaName string `json:"persona_name,omitempty"`
	RunName     string `json:"run_name,omitempty"`
	// Versions é quantas versões a cadeia (task, nome) tem.
	Versions int `json:"versions"`
}

// ArtifactFilter filtra a listagem. Por padrão só a versão mais nova de cada cadeia; AllVersions lista todas
// (a lista de uma run: o que aquela run publicou).
type ArtifactFilter struct {
	ProjectID, TaskID, PersonaID, RunID *domain.ID
	Kind                                domain.ArtifactKind
	Query                               string
	AllVersions                         bool
	Limit, Offset                       int // padrão 100, máx. 500
}

// ArtifactLimitError é a recusa de um artefato por tamanho (acima do limite por arquivo) ou por cota (a empresa já
// usa espaço demais). É um erro de validação (422) que a tela sabe explicar com os números.
type ArtifactLimitError struct {
	Quota bool  // verdadeiro: cota da empresa; falso: limite por arquivo
	Limit int64 // o limite que valeu, em bytes
	Used  int64 // só na cota: o espaço já usado
	Size  int64 // o tamanho do arquivo recusado
	err   *domain.ValidationError
}

// NewArtifactLimitError monta a recusa por tamanho (quota=false) ou por cota (quota=true).
func NewArtifactLimitError(quota bool, limit, used, size int64) *ArtifactLimitError {
	msg := fmt.Sprintf("too large (%d bytes; max %d)", size, limit)
	if quota {
		msg = fmt.Sprintf("company artifact quota exceeded (%d of %d bytes used)", used, limit)
	}
	return &ArtifactLimitError{Quota: quota, Limit: limit, Used: used, Size: size, err: &domain.ValidationError{Field: "content", Reason: msg}}
}

func (e *ArtifactLimitError) Error() string { return e.err.Error() }
func (e *ArtifactLimitError) Unwrap() error { return e.err }

// ArtifactUsage é o espaço usado pela empresa contra a cota.
type ArtifactUsage struct {
	UsedBytes    int64 `json:"used_bytes"`
	Versions     int   `json:"versions"`
	QuotaBytes   int64 `json:"quota_bytes"`
	MaxFileBytes int64 `json:"max_file_bytes"`
}

func artifactFromRow(r store.Artifact) domain.Artifact {
	return domain.Artifact{ID: r.ID, CompanyID: r.CompanyID, TaskID: r.TaskID, RunID: r.RunID, ProjectID: r.ProjectID,
		PersonaID: r.PersonaID, Author: actorOf(r.AuthorType, r.AuthorID), Name: r.Name, Mime: r.Mime, SizeBytes: r.SizeBytes,
		SHA256: r.Sha256, Version: int(r.Version), CreatedAt: r.CreatedAt}
}

func artifactView(a store.Artifact, prefix string, taskNum int64, taskTitle, prSlug, prName, peSlug, peName, runName string, versions int64) ArtifactView {
	v := ArtifactView{Artifact: artifactFromRow(a), TaskTitle: taskTitle, ProjectSlug: prSlug, ProjectName: prName,
		PersonaSlug: peSlug, PersonaName: peName, RunName: runName, Versions: int(versions)}
	if a.TaskID != nil {
		v.TaskKey = domain.TaskKey(prefix, taskNum)
	}
	return v
}

// extMime são as extensões comuns em artefatos de agentes; o resto cai no mime.TypeByExtension e no sniff.
var extMime = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".txt": "text/plain", ".log": "text/plain",
	".csv": "text/csv", ".tsv": "text/tab-separated-values", ".html": "text/html", ".htm": "text/html", ".css": "text/css",
	".json": "application/json", ".yaml": "application/yaml", ".yml": "application/yaml", ".toml": "application/toml",
	".xml": "application/xml", ".svg": "image/svg+xml", ".pdf": "application/pdf",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".go": "text/plain", ".py": "text/plain", ".js": "text/plain", ".ts": "text/plain", ".sh": "text/plain",
	".sql": "text/plain", ".rs": "text/plain", ".java": "text/plain", ".rb": "text/plain", ".c": "text/plain", ".h": "text/plain",
}

// aliasMime junta apelidos comuns no MIME canônico.
var aliasMime = map[string]string{
	"text/x-markdown": "text/markdown", "application/x-yaml": "application/yaml", "text/yaml": "application/yaml",
	"text/json": "application/json", "application/x-json": "application/json", "image/jpg": "image/jpeg",
}

// normalizeMime devolve "tipo/subtipo" em minúsculas, sem parâmetros. Vazio (ou application/octet-stream) é deduzido
// da extensão do nome e, se não bastar, do conteúdo.
func normalizeMime(declared, name string, content []byte) (string, error) {
	declared = strings.TrimSpace(declared)
	if declared != "" {
		mt, _, err := mime.ParseMediaType(declared)
		if err != nil || !strings.Contains(mt, "/") || len(mt) > 100 {
			return "", domain.Invalid("mime", "not a valid media type")
		}
		if a, ok := aliasMime[mt]; ok {
			mt = a
		}
		if mt != "application/octet-stream" {
			return mt, nil
		}
	}
	if m, ok := extMime[strings.ToLower(path.Ext(name))]; ok {
		return m, nil
	}
	if m := mime.TypeByExtension(strings.ToLower(path.Ext(name))); m != "" {
		if mt, _, err := mime.ParseMediaType(m); err == nil {
			return mt, nil
		}
	}
	head := content
	if len(head) > 512 {
		head = head[:512]
	}
	mt, _, err := mime.ParseMediaType(http.DetectContentType(head))
	if err != nil {
		return "application/octet-stream", nil
	}
	return mt, nil
}

// cleanArtifactName valida e apara o nome: sem separadores de caminho nem caracteres de controle (o nome vira o
// nome do arquivo no download).
func cleanArtifactName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", domain.Invalid("name", "required")
	case utf8.RuneCountInString(name) > maxArtifactNameRunes:
		return "", domain.Invalid("name", fmt.Sprintf("too long (max %d characters)", maxArtifactNameRunes))
	case !utf8.ValidString(name):
		return "", domain.Invalid("name", "must be valid UTF-8")
	case name == "." || name == "..":
		return "", domain.Invalid("name", "not a valid file name")
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return "", domain.Invalid("name", "must not contain slashes or control characters")
		}
	}
	return name, nil
}

func chainKey(companyID domain.ID, taskID *domain.ID, name string) string {
	t := ""
	if taskID != nil {
		t = taskID.String()
	}
	return companyID.String() + "|" + t + "|" + name
}

// PutArtifact grava uma versão do artefato (idempotente: o mesmo conteúdo do mesmo nome na mesma task não cria
// versão nova e devolve a existente). Member ou persona.
func (s *Service) PutArtifact(ctx context.Context, actor domain.Actor, companyID domain.ID, in PutArtifactInput) (ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ArtifactView{}, err
	}
	name, err := cleanArtifactName(in.Name)
	if err != nil {
		return ArtifactView{}, err
	}
	maxFile, quota := s.artifactLimits()
	switch {
	case len(in.Content) == 0:
		return ArtifactView{}, domain.Invalid("content", "required")
	case int64(len(in.Content)) > maxFile:
		return ArtifactView{}, NewArtifactLimitError(false, maxFile, 0, int64(len(in.Content)))
	}
	mt, err := normalizeMime(in.Mime, name, in.Content)
	if err != nil {
		return ArtifactView{}, err
	}
	sum := sha256.Sum256(in.Content)
	digest := hex.EncodeToString(sum[:])

	var id domain.ID
	err = s.withTx(ctx, func(t *txc) error {
		ctx := t.ctx
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		taskID, runID, projectID := in.TaskID, in.RunID, in.ProjectID
		if runID != nil {
			run, err := t.q.GetRunByID(ctx, store.GetRunByIDParams{CompanyID: companyID, ID: *runID})
			if err != nil {
				return domain.Invalid("run_id", "run not found in company")
			}
			// uma run de chat não tem task (run.TaskID nulo): o artefato fica só com a run
			switch {
			case taskID == nil && run.TaskID != nil:
				tid := domain.ID(*run.TaskID)
				taskID = &tid
			case taskID != nil && (run.TaskID == nil || *taskID != domain.ID(*run.TaskID)):
				return domain.Invalid("run_id", "run belongs to another task")
			}
		}
		if taskID != nil {
			task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *taskID})
			if err != nil {
				return domain.Invalid("task_id", "task not found in company")
			}
			if projectID == nil {
				projectID = task.ProjectID
			}
		}
		if projectID != nil {
			if _, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *projectID}); err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
		}
		var personaID *domain.ID
		if actor.Type == domain.ActorPersona && actor.ID != nil {
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID}); err != nil {
				return domain.Invalid("persona", "persona not found in company")
			}
			personaID = actor.ID
		}

		if err := t.q.LockArtifactChain(ctx, chainKey(companyID, taskID, name)); err != nil {
			return fmt.Errorf("lock artifact chain: %w", err)
		}
		version := int32(1)
		latest, err := t.q.LatestArtifactInChain(ctx, store.LatestArtifactInChainParams{CompanyID: companyID, TaskID: taskID, Name: name})
		switch {
		case err == nil:
			if latest.Sha256 == digest && latest.Mime == mt {
				id = latest.ID // mesmo conteúdo: nada a gravar
				return nil
			}
			version = latest.Version + 1
		case !isNoRows(err):
			return fmt.Errorf("latest artifact: %w", err)
		}
		usage, err := t.q.ArtifactCompanyUsage(ctx, companyID)
		if err != nil {
			return fmt.Errorf("artifact usage: %w", err)
		}
		if usage.UsedBytes+int64(len(in.Content)) > quota {
			return NewArtifactLimitError(true, quota, usage.UsedBytes, int64(len(in.Content)))
		}
		at := t.now
		if !in.createdAt.IsZero() {
			at = in.createdAt.UTC()
		}
		row, err := t.q.CreateArtifact(ctx, store.CreateArtifactParams{ID: domain.NewID(), CompanyID: companyID, TaskID: taskID,
			RunID: runID, ProjectID: projectID, PersonaID: personaID, AuthorType: string(actor.Type), AuthorID: actor.ID,
			Name: name, Mime: mt, SizeBytes: int64(len(in.Content)), Sha256: digest, Version: version, CreatedAt: at})
		if err != nil {
			return mapErr(err, "create artifact")
		}
		if err := t.q.CreateArtifactBlob(ctx, store.CreateArtifactBlobParams{ArtifactID: row.ID, CompanyID: companyID, Content: in.Content}); err != nil {
			return fmt.Errorf("create artifact blob: %w", err)
		}
		id = row.ID
		return t.activity(actor, companyID, "artifact.created", "artifact", row.ID, map[string]any{
			"name": name, "version": version, "mime": mt, "size_bytes": len(in.Content), "task_id": taskID, "run_id": runID}, "")
	})
	if err != nil {
		return ArtifactView{}, err
	}
	return s.GetArtifactView(ctx, companyID, id)
}

// ListArtifacts lista os artefatos da empresa (a versão mais nova de cada um, a menos que AllVersions), do mais
// recente para o mais antigo.
func (s *Service) ListArtifacts(ctx context.Context, companyID domain.ID, f ArtifactFilter) ([]ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListArtifactViewsParams{CompanyID: companyID, ProjectID: f.ProjectID, TaskID: f.TaskID, PersonaID: f.PersonaID,
		RunID: f.RunID, LatestOnly: !f.AllVersions, Lim: i32(clamp(f.Limit, 100, 500)), Off: i32(max(f.Offset, 0))}
	if f.Kind != "" {
		if !f.Kind.Valid() {
			return nil, domain.Invalid("kind", fmt.Sprintf("unknown kind %q", f.Kind))
		}
		k := string(f.Kind)
		p.Kind = &k
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		q = escapeLike(q)
		p.Q = &q
	}
	rows, err := s.read().ListArtifactViews(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	out := make([]ArtifactView, len(rows))
	for i, r := range rows {
		out[i] = artifactView(r.Artifact, r.TaskPrefix, r.TaskNumber, r.TaskTitle, r.ProjectSlug, r.ProjectName, r.PersonaSlug, r.PersonaName, r.RunName, r.Versions)
	}
	return out, nil
}

// GetArtifactView devolve uma versão (por id) com os nomes das ligações.
func (s *Service) GetArtifactView(ctx context.Context, companyID, id domain.ID) (ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ArtifactView{}, err
	}
	r, err := s.read().GetArtifactView(ctx, store.GetArtifactViewParams{CompanyID: companyID, ID: id})
	if err != nil {
		return ArtifactView{}, mapErr(err, "get artifact")
	}
	return artifactView(r.Artifact, r.TaskPrefix, r.TaskNumber, r.TaskTitle, r.ProjectSlug, r.ProjectName, r.PersonaSlug, r.PersonaName, r.RunName, r.Versions), nil
}

// ArtifactVersions lista todas as versões da cadeia a que id pertence, da mais nova para a mais antiga.
func (s *Service) ArtifactVersions(ctx context.Context, companyID, id domain.ID) ([]domain.Artifact, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	cur, err := q.GetArtifactByID(ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return nil, mapErr(err, "get artifact")
	}
	rows, err := q.ListArtifactChain(ctx, store.ListArtifactChainParams{CompanyID: companyID, TaskID: cur.TaskID, Name: cur.Name})
	if err != nil {
		return nil, fmt.Errorf("list artifact versions: %w", err)
	}
	out := make([]domain.Artifact, len(rows))
	for i, r := range rows {
		out[i] = artifactFromRow(r)
	}
	return out, nil
}

// ArtifactContent devolve a versão e os bytes dela.
func (s *Service) ArtifactContent(ctx context.Context, companyID, id domain.ID) (domain.Artifact, []byte, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return domain.Artifact{}, nil, err
	}
	q := s.read()
	a, err := q.GetArtifactByID(ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return domain.Artifact{}, nil, mapErr(err, "get artifact")
	}
	b, err := q.GetArtifactContent(ctx, store.GetArtifactContentParams{CompanyID: companyID, ArtifactID: id})
	if err != nil {
		return domain.Artifact{}, nil, mapErr(err, "get artifact content")
	}
	return artifactFromRow(a), b, nil
}

// DeleteArtifact apaga o artefato com todas as versões (admin). Libera a cota.
func (s *Service) DeleteArtifact(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetArtifactByID(t.ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get artifact")
		}
		if err := t.q.LockArtifactChain(t.ctx, chainKey(companyID, cur.TaskID, cur.Name)); err != nil {
			return fmt.Errorf("lock artifact chain: %w", err)
		}
		n, err := t.q.DeleteArtifactChain(t.ctx, store.DeleteArtifactChainParams{CompanyID: companyID, TaskID: cur.TaskID, Name: cur.Name})
		if err != nil {
			return fmt.Errorf("delete artifact: %w", err)
		}
		return t.activity(actor, companyID, "artifact.deleted", "artifact", id,
			map[string]any{"name": cur.Name, "versions": n, "task_id": cur.TaskID}, "")
	})
}

// GetArtifactUsage devolve o espaço usado pela empresa, a cota e o limite por arquivo.
func (s *Service) GetArtifactUsage(ctx context.Context, companyID domain.ID) (ArtifactUsage, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return ArtifactUsage{}, err
	}
	u, err := s.read().ArtifactCompanyUsage(ctx, companyID)
	if err != nil {
		return ArtifactUsage{}, fmt.Errorf("artifact usage: %w", err)
	}
	maxFile, quota := s.artifactLimits()
	return ArtifactUsage{UsedBytes: u.UsedBytes, Versions: int(u.Versions), QuotaBytes: quota, MaxFileBytes: maxFile}, nil
}

// ArtifactDiff compara o texto de duas versões da mesma cadeia (from → to). Só texto, até 512 KiB cada.
func (s *Service) ArtifactDiff(ctx context.Context, companyID, from, to domain.ID) (textdiff.Result, error) {
	a, ab, err := s.ArtifactContent(ctx, companyID, from)
	if err != nil {
		return textdiff.Result{}, err
	}
	b, bb, err := s.ArtifactContent(ctx, companyID, to)
	if err != nil {
		return textdiff.Result{}, err
	}
	switch {
	case a.Name != b.Name || !sameID(a.TaskID, b.TaskID):
		return textdiff.Result{}, domain.Invalid("to", "versions belong to different artifacts")
	case !domain.IsText(a.Mime) || !domain.IsText(b.Mime):
		return textdiff.Result{}, domain.Invalid("mime", "diff is only available for text")
	case len(ab) > maxDiffBytes || len(bb) > maxDiffBytes:
		return textdiff.Result{}, domain.Invalid("content", fmt.Sprintf("too large to compare (max %d bytes)", maxDiffBytes))
	case !utf8.Valid(ab) || !utf8.Valid(bb):
		return textdiff.Result{}, domain.Invalid("content", "not valid UTF-8 text")
	}
	return textdiff.Lines(string(ab), string(bb)), nil
}
