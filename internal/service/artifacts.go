package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/settings"
	"github.com/renatobardi/hanko/internal/storage"
	"github.com/renatobardi/hanko/internal/store"
	"github.com/renatobardi/hanko/internal/textdiff"
)

// Artefatos (ADR-0028, ADR-0064). Quem publica é member ou uma persona (run token, sem papel); admin exclui. O limite
// por arquivo e a cota por empresa vêm da configuração (HANKO_ARTIFACT_MAX_BYTES / HANKO_ARTIFACT_QUOTA_BYTES). Cada
// publicação é uma versão de um documento de identidade estável, numerada MAJOR.MINOR.

const (
	// DefaultArtifactMaxBytes é o tamanho máximo de um arquivo quando a configuração não diz.
	DefaultArtifactMaxBytes int64 = 5 << 20
	// DefaultArtifactQuotaBytes é a cota por empresa (soma de todas as versões) quando a configuração não diz.
	DefaultArtifactQuotaBytes int64 = 512 << 20
	// MaxArtifactMaxBytes é o teto do limite por arquivo que a configuração pode escolher.
	MaxArtifactMaxBytes int64 = 16 << 20
	// MaxArtifactMaxBytesBlob é o teto com um driver de blobs externo (s3, ADR-0051).
	MaxArtifactMaxBytesBlob int64 = 64 << 20

	maxArtifactNameRunes = 200
	// maxDiffBytes é o maior texto comparado no diff entre versões.
	maxDiffBytes = 512 << 10
)

// artifactLimits são o limite efetivo de um arquivo e a cota da empresa.
func (s *Service) artifactLimits() (maxFile, quota int64) {
	maxFile, quota = s.ArtifactMaxBytes, s.ArtifactQuotaBytes
	if s.Settings != nil { // settings da instância: env > banco > configuração (ADR-0050)
		maxFile, quota = s.Settings.Int(settings.ArtifactsMaxBytes), s.Settings.Int(settings.ArtifactsQuotaBytes)
	}
	if maxFile <= 0 {
		maxFile = DefaultArtifactMaxBytes
	}
	if quota <= 0 {
		quota = DefaultArtifactQuotaBytes
	}
	if quota < maxFile {
		quota = maxFile
	}
	return maxFile, quota
}

// blobs devolve o registro de drivers de conteúdo; sem configuração vale só o postgres (o comportamento do ADR-0028).
func (s *Service) blobs() *storage.Registry {
	if s.Blobs != nil {
		return s.Blobs
	}
	return storage.NewRegistry(storage.NewPostgres(s.Pool))
}

// deleteBlobsAfterCommit apaga, depois do commit, o conteúdo guardado fora do Postgres (ADR-0051). Falha vira log.
func (t *txc) deleteBlobsAfterCommit(companyID domain.ID, ids []domain.ID, drivers []string) {
	s := t.s
	t.after = append(t.after, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for i, id := range ids {
			if drivers[i] == storage.DriverPostgres {
				continue // saiu junto com a linha (cascata)
			}
			if err := s.deleteBlob(ctx, drivers[i], storage.Key{CompanyID: companyID, ArtifactID: id}); err != nil && s.Log != nil {
				s.Log.Warn("blobstore: objeto órfão (não foi possível apagar)", "driver", drivers[i], "artifact_id", id, "err", err.Error())
			}
		}
	})
}

func (s *Service) deleteBlob(ctx context.Context, driver string, k storage.Key) error {
	d, err := s.blobs().Driver(driver)
	if err != nil {
		return err
	}
	return d.Delete(ctx, k)
}

// ArtifactLimits devolve o tamanho máximo de um arquivo e a cota por empresa em vigor.
func (s *Service) ArtifactLimits() (maxFileBytes, quotaBytes int64) { return s.artifactLimits() }

// PutArtifactInput publica uma versão de artefato. TaskID/RunID/ProjectID são opcionais; com task e sem projeto
// explícito, o projeto da task é herdado; com run e sem task, a task da run.
type PutArtifactInput struct {
	Name    string
	Mime    string // vazio = deduzido da extensão e do conteúdo
	Content []byte

	// DocumentID é o documento a alterar (ADR-0064). Vazio: o documento de mesmo nome na mesma task (ou no "sem
	// task" da empresa), se houver; senão um documento novo.
	DocumentID *domain.ID
	// Change é a classificação declarada (nil: o servidor decide pela medida); ChangeReason, o motivo (obrigatório
	// para persona que declara).
	Change       *domain.ChangeKind
	ChangeReason string
	// Tags substitui as tags do documento quando não é nil.
	Tags []string
	// restoredFrom marca a publicação como restauração dessa versão (sempre major).
	restoredFrom *domain.ID

	TaskID, RunID, ProjectID *domain.ID

	// Class vazio = artefato. Anexo (ADR-0052): sempre versão 1, ligado a uma task (e a um comentário dela) ou a uma
	// conversa do chat (e a uma mensagem dela); o nome repetido ganha sufixo em vez de virar versão.
	Class                                domain.ArtifactClass
	CommentID, ConversationID, MessageID *domain.ID

	// createdAt (importação) preserva o instante do arquivo de origem; zero = agora.
	createdAt time.Time
}

// ArtifactView é uma versão com os nomes das ligações, os dados do documento e o número de versões dele.
type ArtifactView struct {
	domain.Artifact
	// SemVer é a versão "MAJOR.MINOR".
	SemVer string `json:"semver"`
	// Projeto, workspace e tags do documento (podem diferir do projeto em que esta versão foi publicada).
	DocumentProjectID   *domain.ID `json:"document_project_id,omitempty"`
	DocumentProjectSlug string     `json:"document_project_slug,omitempty"`
	WorkspaceID         *domain.ID `json:"workspace_id,omitempty"`
	WorkspaceSlug       string     `json:"workspace_slug,omitempty"`
	Tags                []string   `json:"tags"`
	TaskKey             string     `json:"task_key,omitempty"`
	TaskTitle           string     `json:"task_title,omitempty"`
	ProjectSlug         string     `json:"project_slug,omitempty"`
	ProjectName         string     `json:"project_name,omitempty"`
	PersonaSlug         string     `json:"persona_slug,omitempty"`
	PersonaName         string     `json:"persona_name,omitempty"`
	RunName             string     `json:"run_name,omitempty"`
	// Versions é quantas versões o documento tem.
	Versions int `json:"versions"`
}

// ArtifactFilter filtra a listagem. Por padrão só a versão mais nova de cada documento (aí TaskID traz os documentos
// com alguma versão na task, e projeto/workspace são os do documento); AllVersions lista todas (a lista de uma run:
// o que aquela run publicou).
type ArtifactFilter struct {
	ProjectID, TaskID, PersonaID, RunID *domain.ID
	DocumentID, WorkspaceID             *domain.ID
	Tag                                 string
	// Class filtra artefato (padrão) ou anexo; AllClasses lista os dois. CommentID, ConversationID e MessageID
	// filtram os anexos de um comentário, de uma conversa ou de uma mensagem.
	Class                                domain.ArtifactClass
	AllClasses                           bool
	CommentID, ConversationID, MessageID *domain.ID
	Kind                                 domain.ArtifactKind
	Query                                string
	AllVersions                          bool
	Limit, Offset                        int // padrão 100, máx. 500
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
	a := domain.Artifact{ID: r.ID, CompanyID: r.CompanyID, TaskID: r.TaskID, RunID: r.RunID, ProjectID: r.ProjectID,
		PersonaID: r.PersonaID, Author: actorOf(r.AuthorType, r.AuthorID), Name: r.Name, Mime: r.Mime, SizeBytes: r.SizeBytes,
		SHA256: r.Sha256, Version: int(r.Version), CreatedAt: r.CreatedAt, Class: domain.ArtifactClass(r.Kind),
		CommentID: r.CommentID, ConversationID: r.ConversationID, MessageID: r.MessageID,
		DocumentID: r.DocumentID, Major: int(r.VersionMajor), Minor: int(r.VersionMinor), Change: domain.ChangeKind(r.ChangeKind),
		ChangeReason: r.ChangeReason, ChangeRatio: r.ChangeRatio, ChangeDisputed: r.ChangeDisputed, RestoredFrom: r.RestoredFrom}
	if r.ChangeDeclared != nil {
		k := domain.ChangeKind(*r.ChangeDeclared)
		a.ChangeDeclared = &k
	}
	return a
}

func artifactDocumentFromRow(r store.ArtifactDocument) domain.ArtifactDocument {
	return domain.ArtifactDocument{ID: r.ID, CompanyID: r.CompanyID, Class: domain.ArtifactClass(r.Kind), Name: r.Name,
		ProjectID: r.ProjectID, WorkspaceID: r.WorkspaceID, Tags: r.Tags, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// artifactView monta a vista de uma versão a partir das colunas comuns de ListArtifactViews e GetArtifactView.
func artifactView(r store.ListArtifactViewsRow) ArtifactView {
	a := r.Artifact
	v := ArtifactView{Artifact: artifactFromRow(a), TaskTitle: r.TaskTitle, ProjectSlug: r.ProjectSlug, ProjectName: r.ProjectName,
		PersonaSlug: r.PersonaSlug, PersonaName: r.PersonaName, RunName: r.RunName, Versions: int(r.Versions),
		DocumentProjectID: r.DocProjectID, DocumentProjectSlug: r.DocProjectSlug, WorkspaceID: r.DocWorkspaceID,
		WorkspaceSlug: r.DocWorkspaceSlug, Tags: r.DocTags}
	v.SemVer = v.Artifact.SemVer().String()
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if a.TaskID != nil {
		v.TaskKey = domain.TaskKey(r.TaskPrefix, r.TaskNumber)
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
		// Cf (formatação) inclui os caracteres bidi (U+202E...) que disfarçam a extensão do arquivo
		if r == '/' || r == '\\' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", domain.Invalid("name", "must not contain slashes or control characters")
		}
	}
	return name, nil
}

// PutArtifact grava uma versão do artefato (idempotente: o mesmo conteúdo do mesmo nome na mesma task não cria
// versão nova e devolve a existente). Member ou persona.
func (s *Service) PutArtifact(ctx context.Context, actor domain.Actor, companyID domain.ID, in PutArtifactInput) (ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberMember); err != nil {
		return ArtifactView{}, err
	}
	return s.putArtifact(ctx, actor, companyID, in)
}

// putArtifact é o PutArtifact sem a checagem de papel: quem chama já autorizou a escrita (o Hanko publicando o
// resultado de um card de status, por exemplo, sem pessoa autenticada no contexto).
func (s *Service) putArtifact(ctx context.Context, actor domain.Actor, companyID domain.ID, in PutArtifactInput) (ArtifactView, error) {
	if strings.TrimSpace(in.Name) == "" && in.DocumentID != nil { // alterar sem nome mantém o nome do documento
		d, err := s.read().GetArtifactDocument(ctx, store.GetArtifactDocumentParams{CompanyID: companyID, ID: *in.DocumentID})
		if err != nil {
			return ArtifactView{}, domain.Invalid("artifact", "artifact document not found in company")
		}
		in.Name = d.Name
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
	if in.Class == "" {
		in.Class = domain.ClassArtifact
	}
	if !in.Class.Valid() {
		return ArtifactView{}, domain.Invalid("class", fmt.Sprintf("unknown class %q", in.Class))
	}
	attach := in.Class == domain.ClassAttachment
	if !attach && (in.CommentID != nil || in.ConversationID != nil || in.MessageID != nil) {
		return ArtifactView{}, domain.Invalid("class", "comment, conversation and message links are for attachments")
	}
	if !attach {
		if err := validateArtifactChange(actor, &in); err != nil {
			return ArtifactView{}, err
		}
	}
	sum := sha256.Sum256(in.Content)
	digest := hex.EncodeToString(sum[:])

	// O conteúdo vai para o driver ativo. Postgres: dentro da transação do artefato. Externo (s3): ANTES da transação,
	// para não segurar a trava de cota durante um upload; se a transação não criar a versão, o objeto é apagado.
	drv := s.blobs().Active()
	newID := domain.NewID()
	blobKey := storage.Key{CompanyID: companyID, ArtifactID: newID}
	uploaded := false
	if !drv.Transactional() {
		if err := drv.Put(ctx, blobKey, in.Content, mt); err != nil {
			return ArtifactView{}, fmt.Errorf("guardar conteúdo do artefato: %w", err)
		}
		uploaded = true
	}
	var id domain.ID
	err = s.withTx(ctx, func(t *txc) error {
		ctx := t.ctx
		if _, err := t.q.GetCompanyByID(ctx, companyID); err != nil {
			return mapErr(err, "company")
		}
		if attach {
			aid, err := t.putAttachment(actor, companyID, in, name, mt, digest, quota, newID, drv, blobKey)
			id = aid
			return err
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
		var workspaceID *domain.ID
		if taskID != nil {
			task, err := t.q.GetTaskByID(ctx, store.GetTaskByIDParams{CompanyID: companyID, ID: *taskID})
			if err != nil {
				return domain.Invalid("task_id", "task not found in company")
			}
			if projectID == nil {
				projectID = task.ProjectID
			}
			workspaceID = task.WorkspaceID
		}
		if projectID != nil {
			pr, err := t.q.GetProjectByID(ctx, store.GetProjectByIDParams{CompanyID: companyID, ID: *projectID})
			if err != nil {
				return domain.Invalid("project_id", "project not found in company")
			}
			if workspaceID == nil {
				workspaceID = pr.WorkspaceID
			}
		}
		var personaID *domain.ID
		if actor.Type == domain.ActorPersona && actor.ID != nil {
			if _, err := t.q.GetPersonaByID(ctx, store.GetPersonaByIDParams{CompanyID: companyID, ID: *actor.ID}); err != nil {
				return domain.Invalid("persona", "persona not found in company")
			}
			personaID = actor.ID
		}

		// primeiro a trava da empresa (a cota é lida e consumida sob ela e a busca do documento por nome não corre
		// com outra criação), depois a do documento (numeração da versão)
		if err := t.q.LockArtifactCompany(ctx, companyID); err != nil {
			return fmt.Errorf("lock artifact company: %w", err)
		}
		doc, found, err := t.findArtifactDocument(actor, companyID, in.DocumentID, taskID, projectID, name)
		if err != nil {
			return err
		}
		ver, ordinal := domain.FirstSemVer, int32(1)
		change, declared, disputed := domain.ChangeMajor, (*domain.ChangeKind)(nil), false
		var ratio *float64
		if found {
			if err := t.q.LockArtifactDocument(ctx, doc.ID); err != nil {
				return fmt.Errorf("lock artifact document: %w", err)
			}
			latest, err := t.q.LatestArtifactOfDocument(ctx, doc.ID)
			if err != nil {
				return fmt.Errorf("latest artifact: %w", err)
			}
			if latest.Sha256 == digest && latest.Mime == mt && latest.Name == name && in.restoredFrom == nil {
				id = latest.ID // mesmo conteúdo: nada a gravar
				return nil
			}
			// a leitura do conteúdo anterior usa a própria transação: uma conexão a mais do pool, com a trava da
			// empresa segura, esgota o pool sob publicações concorrentes
			m := t.s.measureArtifactChange(storage.WithTx(ctx, t.q), latest, mt, in.Content)
			if m.Measured {
				r := m.Ratio
				ratio = &r
			}
			declared = in.Change
			change, disputed = domain.ClassifyChange(declared, m)
			if in.restoredFrom != nil { // restaurar é sempre major (ADR-0064)
				change, disputed = domain.ChangeMajor, false
			}
			ordinal = latest.Version + 1
			ver = artifactFromRow(latest).SemVer().Next(change)
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
		tags := in.Tags
		if tags == nil {
			tags = doc.Tags
		}
		if tags == nil {
			tags = []string{}
		}
		if !found {
			if doc, err = t.q.CreateArtifactDocument(ctx, store.CreateArtifactDocumentParams{ID: domain.NewID(), CompanyID: companyID,
				Kind: string(domain.ClassArtifact), Name: name, ProjectID: projectID, WorkspaceID: workspaceID, Tags: tags, CreatedAt: at}); err != nil {
				return mapErr(err, "create artifact document")
			}
		} else {
			if err := t.q.TouchArtifactDocument(ctx, store.TouchArtifactDocumentParams{CompanyID: companyID, ID: doc.ID, Name: name, UpdatedAt: at}); err != nil {
				return mapErr(err, "update artifact document")
			}
			if in.Tags != nil {
				if _, err := t.q.UpdateArtifactDocumentMeta(ctx, store.UpdateArtifactDocumentMetaParams{CompanyID: companyID, ID: doc.ID,
					ProjectID: doc.ProjectID, WorkspaceID: doc.WorkspaceID, Tags: tags, UpdatedAt: at}); err != nil {
					return mapErr(err, "update artifact document")
				}
			}
		}
		var declaredCol *string
		if declared != nil {
			d := string(*declared)
			declaredCol = &d
		}
		row, err := t.q.CreateArtifact(ctx, store.CreateArtifactParams{ID: newID, CompanyID: companyID, TaskID: taskID,
			RunID: runID, ProjectID: projectID, PersonaID: personaID, AuthorType: string(actor.Type), AuthorID: actor.ID,
			Name: name, Mime: mt, SizeBytes: int64(len(in.Content)), Sha256: digest, Version: ordinal, CreatedAt: at, Storage: drv.Name(),
			Kind: string(domain.ClassArtifact), DocumentID: doc.ID, VersionMajor: i32(ver.Major), VersionMinor: i32(ver.Minor),
			ChangeKind: string(change), ChangeDeclared: declaredCol, ChangeReason: strings.TrimSpace(in.ChangeReason),
			ChangeRatio: ratio, ChangeDisputed: disputed, RestoredFrom: in.restoredFrom})
		if err != nil {
			return mapErr(err, "create artifact")
		}
		if drv.Transactional() {
			if err := drv.Put(storage.WithTx(ctx, t.q), blobKey, in.Content, mt); err != nil {
				return fmt.Errorf("create artifact blob: %w", err)
			}
		}
		id = row.ID
		verb, payload := "artifact.created", map[string]any{"name": name, "document_id": doc.ID, "version": ver.String(),
			"change": change, "change_disputed": disputed, "mime": mt, "size_bytes": len(in.Content), "task_id": taskID, "run_id": runID}
		if in.restoredFrom != nil {
			verb, payload["restored_from"] = "artifact.restored", in.restoredFrom
		}
		return t.activity(actor, companyID, verb, "artifact", row.ID, payload, "")
	})
	if uploaded && (err != nil || id != newID) {
		// nenhuma versão nova apontou para este objeto (falha, mesmo conteúdo ou cota): apaga o que subiu
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		if derr := drv.Delete(dctx, blobKey); derr != nil && s.Log != nil {
			s.Log.Warn("blobstore: objeto órfão (não foi possível apagar)", "driver", drv.Name(), "artifact_id", newID, "err", derr.Error())
		}
		cancel()
	}
	if err != nil {
		return ArtifactView{}, err
	}
	return s.GetArtifactView(ctx, companyID, id)
}

// validateArtifactChange confere a classificação declarada, o motivo e as tags de uma publicação (ADR-0064).
func validateArtifactChange(actor domain.Actor, in *PutArtifactInput) error {
	in.ChangeReason = strings.TrimSpace(in.ChangeReason)
	if in.Change != nil && !in.Change.Valid() {
		return domain.Invalid("change", fmt.Sprintf("unknown change %q (use major or minor)", *in.Change))
	}
	if utf8.RuneCountInString(in.ChangeReason) > domain.MaxChangeReasonRunes {
		return domain.Invalid("change_reason", fmt.Sprintf("too long (max %d characters)", domain.MaxChangeReasonRunes))
	}
	if in.Change != nil && actor.Type == domain.ActorPersona && in.ChangeReason == "" && in.restoredFrom == nil {
		return domain.Invalid("change_reason", "required when a persona declares the change")
	}
	if in.Tags != nil {
		tags, err := domain.NormalizeTags(in.Tags)
		if err != nil {
			return err
		}
		in.Tags = tags
	}
	return nil
}

// findArtifactDocument acha o documento a versionar: o informado (id) ou, sem id, o de mesmo nome que já tem versão
// na mesma task (ou no "sem task" da empresa). found falso: a publicação cria um documento novo. A persona só
// versiona documento do projeto da sua task ou run, ou sem projeto (ADR-0064).
func (t *txc) findArtifactDocument(actor domain.Actor, companyID domain.ID, docID, taskID, projectID *domain.ID, name string) (store.ArtifactDocument, bool, error) {
	var (
		doc store.ArtifactDocument
		err error
	)
	if docID != nil {
		doc, err = t.q.GetArtifactDocument(t.ctx, store.GetArtifactDocumentParams{CompanyID: companyID, ID: *docID})
		if err != nil || doc.Kind != string(domain.ClassArtifact) {
			return doc, false, domain.Invalid("artifact", "artifact document not found in company")
		}
	} else {
		doc, err = t.q.FindArtifactDocumentByChain(t.ctx, store.FindArtifactDocumentByChainParams{CompanyID: companyID, Name: name, TaskID: taskID})
		if isNoRows(err) {
			return store.ArtifactDocument{}, false, nil
		}
		if err != nil {
			return doc, false, fmt.Errorf("find artifact document: %w", err)
		}
	}
	if actor.Type == domain.ActorPersona && doc.ProjectID != nil && (projectID == nil || *projectID != *doc.ProjectID) {
		return doc, false, fmt.Errorf("artifact: a persona versions only documents of its own project: %w", domain.ErrNotAllowed)
	}
	return doc, true, nil
}

// measureArtifactChange mede a versão nova contra a mais nova do documento (diff por linha). Binário, texto grande
// demais ou conteúdo ilegível não são medidos: a declaração vale sozinha.
func (s *Service) measureArtifactChange(ctx context.Context, latest store.Artifact, mt string, content []byte) domain.ChangeMeasure {
	mimeChanged := latest.Mime != mt
	if !domain.IsText(latest.Mime) || !domain.IsText(mt) || len(content) > maxDiffBytes || latest.SizeBytes > maxDiffBytes || !utf8.Valid(content) {
		if mimeChanged {
			return domain.ChangeMeasure{Measured: true, Ratio: 1, Suggested: domain.ChangeMajor}
		}
		return domain.ChangeMeasure{}
	}
	d, err := s.blobs().Driver(latest.Storage)
	if err != nil {
		return domain.ChangeMeasure{}
	}
	prev, err := d.Get(ctx, storage.Key{CompanyID: latest.CompanyID, ArtifactID: latest.ID})
	if err != nil || !utf8.Valid(prev) {
		return domain.ChangeMeasure{}
	}
	r := textdiff.Lines(string(prev), string(content))
	return domain.MeasureChange(r.Removed, r.Added, countLines(prev), countLines(content), mimeChanged)
}

// countLines conta as linhas de um texto (a última sem \n também conta).
func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// ListArtifacts lista os artefatos da empresa (a versão mais nova de cada um, a menos que AllVersions), do mais
// recente para o mais antigo.
func (s *Service) ListArtifacts(ctx context.Context, companyID domain.ID, f ArtifactFilter) ([]ArtifactView, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	p := store.ListArtifactViewsParams{CompanyID: companyID, ProjectID: f.ProjectID, TaskID: f.TaskID, PersonaID: f.PersonaID,
		RunID: f.RunID, CommentID: f.CommentID, ConversationID: f.ConversationID, MessageID: f.MessageID,
		DocumentID: f.DocumentID, WorkspaceID: f.WorkspaceID,
		LatestOnly: !f.AllVersions, Lim: i32(clamp(f.Limit, 100, 500)), Off: i32(max(f.Offset, 0))}
	if tag := strings.ToLower(strings.TrimSpace(f.Tag)); tag != "" {
		p.Tag = &tag
	}
	switch {
	case f.AllClasses:
		all := "all"
		p.Class = &all
	case f.Class != "":
		if !f.Class.Valid() {
			return nil, domain.Invalid("class", fmt.Sprintf("unknown class %q", f.Class))
		}
		c := string(f.Class)
		p.Class = &c
	}
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
		out[i] = artifactView(r)
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
	return artifactView(store.ListArtifactViewsRow(r)), nil
}

// ArtifactVersions lista todas as versões do documento a que a versão id pertence, da mais nova para a mais antiga.
func (s *Service) ArtifactVersions(ctx context.Context, companyID, id domain.ID) ([]domain.Artifact, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	q := s.read()
	cur, err := q.GetArtifactByID(ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id})
	if err != nil {
		return nil, mapErr(err, "get artifact")
	}
	rows, err := q.ListArtifactDocumentVersions(ctx, store.ListArtifactDocumentVersionsParams{CompanyID: companyID, DocumentID: cur.DocumentID})
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
	d, err := s.blobs().Driver(a.Storage)
	if err != nil {
		return domain.Artifact{}, nil, fmt.Errorf("get artifact content: %w", err)
	}
	b, err := d.Get(ctx, storage.Key{CompanyID: companyID, ArtifactID: id})
	if errors.Is(err, storage.ErrNotFound) {
		return domain.Artifact{}, nil, notFound("artifact content")
	}
	if err != nil {
		return domain.Artifact{}, nil, fmt.Errorf("get artifact content: %w", err)
	}
	return artifactFromRow(a), b, nil
}

// DeleteArtifact apaga o documento com todas as versões (admin). Libera a cota.
func (s *Service) DeleteArtifact(ctx context.Context, actor domain.Actor, companyID, id domain.ID) error {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetArtifactByID(t.ctx, store.GetArtifactByIDParams{CompanyID: companyID, ID: id})
		if err != nil {
			return mapErr(err, "get artifact")
		}
		if cur.Kind == string(domain.ClassAttachment) { // um anexo é uma linha só
			gone, err := t.q.DeleteAttachment(t.ctx, store.DeleteAttachmentParams{CompanyID: companyID, ID: id})
			if err != nil {
				return mapErr(err, "delete attachment")
			}
			t.deleteBlobsAfterCommit(companyID, []domain.ID{gone.ID}, []string{gone.Storage})
			return t.activity(actor, companyID, "attachment.deleted", "artifact", id,
				map[string]any{"name": cur.Name, "task_id": cur.TaskID, "conversation_id": cur.ConversationID}, "")
		}
		if err := t.q.LockArtifactDocument(t.ctx, cur.DocumentID); err != nil {
			return fmt.Errorf("lock artifact document: %w", err)
		}
		gone, err := t.q.DeleteArtifactDocumentVersions(t.ctx, store.DeleteArtifactDocumentVersionsParams{CompanyID: companyID, DocumentID: cur.DocumentID})
		if err != nil {
			return fmt.Errorf("delete artifact: %w", err)
		}
		if err := t.q.DeleteArtifactDocument(t.ctx, store.DeleteArtifactDocumentParams{CompanyID: companyID, ID: cur.DocumentID}); err != nil {
			return fmt.Errorf("delete artifact document: %w", err)
		}
		ids, drivers := make([]domain.ID, len(gone)), make([]string, len(gone))
		for i, g := range gone {
			ids[i], drivers[i] = g.ID, g.Storage
		}
		t.deleteBlobsAfterCommit(companyID, ids, drivers)
		return t.activity(actor, companyID, "artifact.deleted", "artifact", id,
			map[string]any{"name": cur.Name, "document_id": cur.DocumentID, "versions": len(gone), "task_id": cur.TaskID}, "")
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

// ArtifactDiff compara o texto de duas versões do mesmo documento (from → to). Só texto, até 512 KiB cada.
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
	case a.DocumentID != b.DocumentID:
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
