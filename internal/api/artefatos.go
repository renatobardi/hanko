package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/artifactsrv"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Artefatos (ADR-0028, ADR-0064). Cada versão pertence a um documento de identidade estável e é numerada
// MAJOR.MINOR; as rotas por {artifact} recebem o id de uma versão e as por {document}, o do documento. O envio é JSON com o conteúdo em base64 (cabe no mesmo cliente de todo o resto e na
// ferramenta MCP); o download é bruto, com cabeçalhos de segurança (artifactsrv). Um run token publica só na
// própria task/run; apagar é de admin humano.

// artifactBodyLimit é o teto do corpo de um envio: o limite de arquivo configurado (HANKO_ARTIFACT_MAX_BYTES) em
// base64 (x4/3) mais o envelope JSON. Sem o limite configurado à mão, vale o máximo absoluto (16 MiB).
func (s *server) artifactBodyLimit() int64 {
	maxFile := service.MaxArtifactMaxBytesBlob
	if l, ok := s.svc.(interface{ ArtifactLimits() (int64, int64) }); ok {
		if m, _ := l.ArtifactLimits(); m > 0 && m < maxFile {
			maxFile = m
		}
	}
	return maxFile*4/3 + 64<<10
}

// Artifact é uma versão de um artefato (sem o conteúdo).
type Artifact struct {
	ID         string `json:"id" format:"uuid"`
	CompanyID  string `json:"company_id" format:"uuid"`
	Name       string `json:"name" example:"plano.md"`
	Mime       string `json:"mime" example:"text/markdown"`
	Kind       string `json:"kind" enum:"markdown,text,image,other"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	Version    int    `json:"version" doc:"Ordinal da publicação dentro do documento (1, 2, 3...)."`
	Versions   int    `json:"versions" doc:"Quantas versões o documento tem."`
	DocumentID string `json:"document_id" format:"uuid" doc:"Documento (identidade estável) de que esta é uma versão."`
	SemVer     string `json:"semver" example:"2.1" doc:"Versão MAJOR.MINOR."`
	Major      int    `json:"major"`
	Minor      int    `json:"minor"`
	Change     string `json:"change" enum:"major,minor" doc:"Classificação em relação à versão anterior (a primeira é major)."`
	// Classificação: o que foi declarado (vazio = o servidor decidiu), o motivo, a fração de linhas mudadas e se a
	// medida contradiz a declaração.
	ChangeDeclared      string    `json:"change_declared,omitempty" enum:"major,minor"`
	ChangeReason        string    `json:"change_reason,omitempty"`
	ChangeRatio         *float64  `json:"change_ratio,omitempty" doc:"Fração das linhas mudadas (0 a 1); ausente se não medido."`
	ChangeDisputed      bool      `json:"change_disputed,omitempty" doc:"A medida do diff contradiz a classificação declarada."`
	RestoredFrom        string    `json:"restored_from,omitempty" format:"uuid" doc:"Versão restaurada, quando esta versão é uma restauração."`
	Tags                []string  `json:"tags" doc:"Tags do documento."`
	DocumentProjectSlug string    `json:"document_project_slug,omitempty" doc:"Projeto do documento (pode diferir do desta versão)."`
	WorkspaceID         string    `json:"workspace_id,omitempty" format:"uuid" doc:"Workspace do documento."`
	WorkspaceSlug       string    `json:"workspace_slug,omitempty"`
	TaskID              string    `json:"task_id,omitempty" format:"uuid"`
	TaskKey             string    `json:"task_key,omitempty"`
	TaskTitle           string    `json:"task_title,omitempty"`
	RunID               string    `json:"run_id,omitempty" format:"uuid"`
	RunName             string    `json:"run_name,omitempty"`
	ProjectID           string    `json:"project_id,omitempty" format:"uuid"`
	ProjectSlug         string    `json:"project_slug,omitempty"`
	ProjectName         string    `json:"project_name,omitempty"`
	PersonaID           string    `json:"persona_id,omitempty" format:"uuid"`
	PersonaSlug         string    `json:"persona_slug,omitempty"`
	PersonaName         string    `json:"persona_name,omitempty"`
	Author              ActorRef  `json:"author"`
	CreatedAt           time.Time `json:"created_at"`
	// Class: artifact (versionado, produzido pelo trabalho) ou attachment (anexo, ADR-0052).
	Class          string `json:"class" enum:"artifact,attachment"`
	CommentID      string `json:"comment_id,omitempty" format:"uuid" doc:"Anexo: o comentário da task a que pertence."`
	ConversationID string `json:"conversation_id,omitempty" format:"uuid" doc:"Anexo: a conversa do chat a que pertence."`
	MessageID      string `json:"message_id,omitempty" format:"uuid" doc:"Anexo: a mensagem da conversa a que pertence."`
	// History só vem no detalhe: todas as versões do documento, da mais nova para a mais antiga.
	History []ArtifactVersion `json:"history,omitempty"`
}

// ArtifactVersion é uma linha do histórico de versões.
type ArtifactVersion struct {
	ID        string    `json:"id" format:"uuid"`
	Version   int       `json:"version"`
	SemVer    string    `json:"semver" example:"1.2"`
	Name      string    `json:"name"`
	Change    string    `json:"change" enum:"major,minor"`
	Reason    string    `json:"change_reason,omitempty"`
	Disputed  bool      `json:"change_disputed,omitempty"`
	TaskID    string    `json:"task_id,omitempty" format:"uuid"`
	SizeBytes int64     `json:"size_bytes"`
	SHA256    string    `json:"sha256"`
	Author    ActorRef  `json:"author"`
	RunID     string    `json:"run_id,omitempty" format:"uuid"`
	CreatedAt time.Time `json:"created_at"`
}

// ArtifactUsage é o espaço de artefatos usado pela empresa.
type ArtifactUsage struct {
	UsedBytes    int64 `json:"used_bytes"`
	Versions     int   `json:"versions"`
	QuotaBytes   int64 `json:"quota_bytes"`
	MaxFileBytes int64 `json:"max_file_bytes"`
}

// ArtifactDiffLine é uma linha do diff entre versões.
type ArtifactDiffLine struct {
	Op    string `json:"op" enum:"equal,insert,delete"`
	Text  string `json:"text"`
	OldNo int    `json:"old_no,omitempty"`
	NewNo int    `json:"new_no,omitempty"`
}

// ArtifactDiff é a comparação de texto entre duas versões do mesmo documento.
type ArtifactDiff struct {
	From       int                `json:"from_version"`
	To         int                `json:"to_version"`
	FromSemVer string             `json:"from_semver"`
	ToSemVer   string             `json:"to_semver"`
	Added      int                `json:"added"`
	Removed    int                `json:"removed"`
	Coarse     bool               `json:"coarse,omitempty" doc:"O trecho alterado era grande demais para o diff fino: aparece tudo removido e tudo adicionado."`
	Lines      []ArtifactDiffLine `json:"lines"`
}

type ArtifactPath struct {
	CompanyPath
	Artifact string `path:"artifact" format:"uuid" doc:"Id da versão do artefato."`
}

type putArtifactIn struct {
	CompanyPath
	Body struct {
		Name         string   `json:"name" minLength:"1" maxLength:"200" example:"plano.md" doc:"Nome do arquivo (sem barras). Sem document_id, o mesmo nome na mesma task cria a versão seguinte."`
		Document     string   `json:"document_id,omitempty" format:"uuid" doc:"Documento a alterar: a publicação vira a versão seguinte dele, mesmo com outro nome ou em outra task."`
		Change       string   `json:"change,omitempty" enum:"major,minor" doc:"Classificação declarada; vazio = o servidor decide pelo tamanho do diff."`
		ChangeReason string   `json:"change_reason,omitempty" maxLength:"280" doc:"Motivo curto da mudança; obrigatório para run token que declara change."`
		Tags         []string `json:"tags,omitempty" doc:"Substitui as tags do documento."`
		Mime         string   `json:"mime,omitempty" example:"text/markdown" doc:"Tipo do conteúdo; vazio = deduzido do nome e do conteúdo."`
		Content      []byte   `json:"content" doc:"Conteúdo do arquivo em base64." minLength:"1"`
		Task         string   `json:"task,omitempty" doc:"Id ou chave da task. Run token: sempre a da execução."`
		Run          string   `json:"run_id,omitempty" format:"uuid" doc:"Execução de origem. Run token: sempre a do token."`
		Project      string   `json:"project,omitempty" doc:"Id ou slug do projeto; vazio herda o da task."`
	}
}

type listArtifactsIn struct {
	CompanyPath
	Page
	Project     string `query:"project" doc:"Id ou slug do projeto."`
	Task        string `query:"task" doc:"Id ou chave da task."`
	Persona     string `query:"persona" doc:"Id ou slug da persona autora."`
	Run         string `query:"run" format:"uuid" doc:"Id da execução."`
	Kind        string `query:"kind" enum:"markdown,text,image,other" doc:"Tipo de conteúdo."`
	Q           string `query:"q" doc:"Busca pelo nome."`
	Workspace   string `query:"workspace" doc:"Id ou slug do workspace do documento."`
	Tag         string `query:"tag" doc:"Tag do documento."`
	Document    string `query:"document" format:"uuid" doc:"Só este documento (com all_versions, o histórico dele)."`
	AllVersions bool   `query:"all_versions" doc:"Todas as versões, não só a mais nova de cada documento."`
	Class       string `query:"class" enum:"artifact,attachment,all" doc:"Padrão artifact; attachment lista os anexos; all, os dois."`
}

type diffArtifactIn struct {
	ArtifactPath
	Against string `query:"against" format:"uuid" doc:"Versão anterior a comparar; vazio = a versão imediatamente anterior."`
}

func artifactOut(v service.ArtifactView) Artifact {
	o := Artifact{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Name: v.Name, Mime: v.Mime, Kind: string(domain.KindOf(v.Mime)),
		SizeBytes: v.SizeBytes, SHA256: v.SHA256, Version: v.Version, Versions: v.Versions,
		DocumentID: idStr(v.DocumentID), SemVer: v.SemVer, Major: v.Major, Minor: v.Minor, Change: string(v.Change),
		ChangeReason: v.ChangeReason, ChangeRatio: v.ChangeRatio, ChangeDisputed: v.ChangeDisputed, RestoredFrom: idPtr(v.RestoredFrom),
		Tags: v.Tags, DocumentProjectSlug: v.DocumentProjectSlug, WorkspaceID: idPtr(v.WorkspaceID), WorkspaceSlug: v.WorkspaceSlug,
		TaskID: idPtr(v.TaskID), TaskKey: v.TaskKey, TaskTitle: v.TaskTitle, RunID: idPtr(v.RunID), RunName: v.RunName,
		ProjectID: idPtr(v.ProjectID), ProjectSlug: v.ProjectSlug, ProjectName: v.ProjectName,
		PersonaID: idPtr(v.PersonaID), PersonaSlug: v.PersonaSlug, PersonaName: v.PersonaName,
		Author: actorRef(v.Author), CreatedAt: v.CreatedAt, Class: string(v.Class),
		CommentID: idPtr(v.CommentID), ConversationID: idPtr(v.ConversationID), MessageID: idPtr(v.MessageID)}
	if v.ChangeDeclared != nil {
		o.ChangeDeclared = string(*v.ChangeDeclared)
	}
	if o.Tags == nil {
		o.Tags = []string{}
	}
	return o
}

func artifactVersionOut(a domain.Artifact) ArtifactVersion {
	return ArtifactVersion{ID: idStr(a.ID), Version: a.Version, SemVer: a.SemVer().String(), Name: a.Name, Change: string(a.Change),
		Reason: a.ChangeReason, Disputed: a.ChangeDisputed, TaskID: idPtr(a.TaskID), SizeBytes: a.SizeBytes, SHA256: a.SHA256,
		Author: actorRef(a.Author), RunID: idPtr(a.RunID), CreatedAt: a.CreatedAt}
}

type DocumentPath struct {
	CompanyPath
	Document string `path:"document" format:"uuid" doc:"Id do documento de artefato."`
}

type documentVersionIn struct {
	DocumentPath
	Version string `path:"version" example:"1.2" doc:"MAJOR.MINOR, ou latest para a mais nova."`
}

type restoreArtifactIn struct {
	ArtifactPath
}

type reclassifyArtifactIn struct {
	ArtifactPath
	Body struct {
		Change string `json:"change" enum:"major,minor"`
	}
}

type patchArtifactDocumentIn struct {
	DocumentPath
	Body struct {
		Project   *string  `json:"project,omitempty" doc:"Id ou slug do projeto; vazio tira o projeto."`
		Workspace *string  `json:"workspace,omitempty" doc:"Id ou slug do workspace; vazio tira o workspace."`
		Tags      []string `json:"tags,omitempty" doc:"Substitui as tags (a-z, 0-9 e -; até 20)."`
	}
}

// ArtifactDocument é a identidade estável de um artefato.
type ArtifactDocument struct {
	ID          string    `json:"id" format:"uuid"`
	Class       string    `json:"class" enum:"artifact,attachment"`
	Name        string    `json:"name"`
	ProjectID   string    `json:"project_id,omitempty" format:"uuid"`
	WorkspaceID string    `json:"workspace_id,omitempty" format:"uuid"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *server) artifactID(ctx context.Context, ref string) (domain.ID, error) {
	id, err := domain.ParseID(ref)
	if err != nil {
		return domain.ID{}, s.fail(ctx, domain.Invalid("artifact", "não é um uuid"))
	}
	return id, nil
}

// runScope devolve a task e a run que um run token pode usar; o token só publica na própria execução. O token de
// uma run de chat não tem task: o artefato fica só com a run, e informar uma task é recusado.
func runScope(sc scope, task, run *domain.ID) (*domain.ID, *domain.ID, error) {
	if sc.Claims == nil {
		return task, run, nil
	}
	rid, rerr := domain.ParseID(sc.Claims.RunID)
	if rerr != nil {
		return nil, nil, problem(http.StatusForbidden, "o token não identifica uma execução")
	}
	if run != nil && *run != rid {
		return nil, nil, problem(http.StatusForbidden, "o token só publica na própria execução")
	}
	if sc.Claims.TaskID == "" {
		if task != nil {
			return nil, nil, problem(http.StatusForbidden, "o token de uma execução de chat não tem task; omita task")
		}
		return nil, &rid, nil
	}
	tid, terr := domain.ParseID(sc.Claims.TaskID)
	if terr != nil {
		return nil, nil, problem(http.StatusForbidden, "o token não identifica uma execução")
	}
	if task != nil && *task != tid {
		return nil, nil, problem(http.StatusForbidden, "o token só publica na própria execução")
	}
	return &tid, &rid, nil
}

func (s *server) registerArtifacts(api huma.API) {
	put := created(op("put-artifact", http.MethodPost, "/companies/{company}/artifacts", "artifacts",
		"Publica um artefato (nova versão do documento informado, ou do mesmo nome na task)"))
	put.MaxBodyBytes = s.artifactBodyLimit()
	huma.Register(api, put, func(ctx context.Context, in *putArtifactIn) (*ItemOut[Artifact], error) {
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		cid := sc.Company.ID
		b := in.Body
		task, err := s.taskRef(ctx, cid, b.Task)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		project, err := s.projectRef(ctx, cid, b.Project)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		var run *domain.ID
		if b.Run != "" {
			id, err := domain.ParseID(b.Run)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("run_id", "não é um uuid"))
			}
			run = &id
		}
		if task, run, err = runScope(sc, task, run); err != nil {
			return nil, err
		}
		pi := service.PutArtifactInput{Name: b.Name, Mime: b.Mime, Content: b.Content, TaskID: task, RunID: run, ProjectID: project,
			ChangeReason: b.ChangeReason, Tags: b.Tags}
		if b.Document != "" {
			id, err := domain.ParseID(b.Document)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("document_id", "não é um uuid"))
			}
			pi.DocumentID = &id
		}
		if b.Change != "" {
			k := domain.ChangeKind(b.Change)
			pi.Change = &k
		}
		v, err := s.svc.PutArtifact(ctx, sc.Actor, cid, pi)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[Artifact]{Body: artifactOut(v)}, nil
	})

	huma.Register(api, op("list-artifacts", http.MethodGet, "/companies/{company}/artifacts", "artifacts", "Lista artefatos"),
		func(ctx context.Context, in *listArtifactsIn) (*ListOut[Artifact], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			f := service.ArtifactFilter{Kind: domain.ArtifactKind(in.Kind), Query: in.Q, AllVersions: in.AllVersions, Limit: in.Limit, Offset: in.Offset}
			switch in.Class {
			case "all":
				f.AllClasses = true
			default:
				f.Class = domain.ArtifactClass(in.Class)
			}
			if f.ProjectID, err = s.projectRef(ctx, cid, in.Project); err != nil {
				return nil, s.fail(ctx, err)
			}
			if f.TaskID, err = s.taskRef(ctx, cid, in.Task); err != nil {
				return nil, s.fail(ctx, err)
			}
			if f.PersonaID, err = s.personaRef(ctx, cid, in.Persona); err != nil {
				return nil, s.fail(ctx, err)
			}
			if f.WorkspaceID, err = s.workspaceRef(ctx, cid, in.Workspace); err != nil {
				return nil, s.fail(ctx, err)
			}
			f.Tag = in.Tag
			if in.Document != "" {
				id, err := domain.ParseID(in.Document)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("document", "não é um uuid"))
				}
				f.DocumentID = &id
			}
			if in.Run != "" {
				id, err := domain.ParseID(in.Run)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("run", "não é um uuid"))
				}
				f.RunID = &id
			}
			vs, err := s.svc.ListArtifacts(ctx, cid, f)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(vs, in.Page, artifactOut), nil
		})

	huma.Register(api, op("get-artifact", http.MethodGet, "/companies/{company}/artifacts/{artifact}", "artifacts", "Obtém uma versão de artefato e o histórico do documento"),
		func(ctx context.Context, in *ArtifactPath) (*ItemOut[Artifact], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.artifactID(ctx, in.Artifact)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.GetArtifactView(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			hist, err := s.svc.ArtifactVersions(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := artifactOut(v)
			for _, h := range hist {
				out.History = append(out.History, artifactVersionOut(h))
			}
			return &ItemOut[Artifact]{Body: out}, nil
		})

	contentOp := op("get-artifact-content", http.MethodGet, "/companies/{company}/artifacts/{artifact}/content", "artifacts",
		"Baixa o conteúdo bruto (inline só para texto puro, markdown e imagens raster; o resto como download)")
	contentOp.Responses = map[string]*huma.Response{"200": {Description: "Conteúdo do artefato, com X-Content-Type-Options: nosniff e CSP restritiva.",
		Content: map[string]*huma.MediaType{"*/*": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}}
	huma.Register(api, contentOp, func(ctx context.Context, in *ArtifactPath) (*huma.StreamResponse, error) {
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		id, err := s.artifactID(ctx, in.Artifact)
		if err != nil {
			return nil, err
		}
		a, content, err := s.svc.ArtifactContent(ctx, sc.Company.ID, id)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			artifactsrv.Headers(hctx.SetHeader, a)
			hctx.SetStatus(http.StatusOK)
			_, _ = hctx.BodyWriter().Write(content)
		}}, nil
	})

	huma.Register(api, op("diff-artifact", http.MethodGet, "/companies/{company}/artifacts/{artifact}/diff", "artifacts",
		"Compara o texto de uma versão com a anterior (ou com `against`)"),
		func(ctx context.Context, in *diffArtifactIn) (*ItemOut[ArtifactDiff], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			to, err := s.artifactID(ctx, in.Artifact)
			if err != nil {
				return nil, err
			}
			hist, err := s.svc.ArtifactVersions(ctx, sc.Company.ID, to)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			var cur, from domain.Artifact
			var haveFrom bool
			for _, h := range hist {
				if h.ID == to {
					cur = h
				}
			}
			if in.Against != "" {
				id, err := domain.ParseID(in.Against)
				if err != nil {
					return nil, s.fail(ctx, domain.Invalid("against", "não é um uuid"))
				}
				for _, h := range hist {
					if h.ID == id {
						from, haveFrom = h, true
					}
				}
				if !haveFrom {
					return nil, s.fail(ctx, domain.Invalid("against", "não é uma versão deste artefato"))
				}
			} else {
				for _, h := range hist { // da mais nova para a mais antiga: a primeira abaixo de cur
					if h.Version < cur.Version {
						from, haveFrom = h, true
						break
					}
				}
				if !haveFrom {
					return nil, s.fail(ctx, domain.Invalid("against", "esta é a primeira versão: não há o que comparar"))
				}
			}
			r, err := s.svc.ArtifactDiff(ctx, sc.Company.ID, from.ID, to)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := ArtifactDiff{From: from.Version, To: cur.Version, FromSemVer: from.SemVer().String(), ToSemVer: cur.SemVer().String(), Added: r.Added, Removed: r.Removed, Coarse: r.Coarse, Lines: make([]ArtifactDiffLine, 0, len(r.Lines))}
			for _, l := range r.Lines {
				op := "equal"
				switch l.Op {
				case '+':
					op = "insert"
				case '-':
					op = "delete"
				}
				out.Lines = append(out.Lines, ArtifactDiffLine{Op: op, Text: l.Text, OldNo: l.OldNo, NewNo: l.NewNo})
			}
			return &ItemOut[ArtifactDiff]{Body: out}, nil
		})

	del := op("delete-artifact", http.MethodDelete, "/companies/{company}/artifacts/{artifact}", "artifacts", "Apaga o artefato com todas as versões, ou o anexo (admin)")
	del.DefaultStatus = http.StatusNoContent
	huma.Register(api, del, func(ctx context.Context, in *ArtifactPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		id, err := s.artifactID(ctx, in.Artifact)
		if err != nil {
			return nil, err
		}
		if err := s.svc.DeleteArtifact(ctx, sc.Actor, sc.Company.ID, id); err != nil {
			return nil, s.fail(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, op("get-artifact-document-version", http.MethodGet, "/companies/{company}/artifact-documents/{document}/versions/{version}", "artifacts",
		"Obtém uma versão do documento por MAJOR.MINOR (ou latest), com o histórico"),
		func(ctx context.Context, in *documentVersionIn) (*ItemOut[Artifact], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			doc, err := domain.ParseID(in.Document)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("document", "não é um uuid"))
			}
			ver := in.Version
			if ver == "latest" {
				ver = ""
			}
			id, err := s.svc.ArtifactVersionID(ctx, sc.Company.ID, doc, ver)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			v, err := s.svc.GetArtifactView(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			hist, err := s.svc.ArtifactVersions(ctx, sc.Company.ID, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := artifactOut(v)
			for _, h := range hist {
				out.History = append(out.History, artifactVersionOut(h))
			}
			return &ItemOut[Artifact]{Body: out}, nil
		})

	huma.Register(api, created(op("restore-artifact", http.MethodPost, "/companies/{company}/artifacts/{artifact}/restore", "artifacts",
		"Restaura esta versão: o conteúdo dela vira a versão mais nova do documento (sempre major)")),
		func(ctx context.Context, in *restoreArtifactIn) (*ItemOut[Artifact], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.artifactID(ctx, in.Artifact)
			if err != nil {
				return nil, err
			}
			task, run, err := runScope(sc, nil, nil)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.RestoreArtifact(ctx, sc.Actor, sc.Company.ID, id, task, run)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Artifact]{Body: artifactOut(v)}, nil
		})

	huma.Register(api, op("reclassify-artifact", http.MethodPost, "/companies/{company}/artifacts/{artifact}/reclassify", "artifacts",
		"Reclassifica a versão mais nova do documento (major ou minor) e a renumera"),
		func(ctx context.Context, in *reclassifyArtifactIn) (*ItemOut[Artifact], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := s.artifactID(ctx, in.Artifact)
			if err != nil {
				return nil, err
			}
			v, err := s.svc.ReclassifyArtifact(ctx, sc.Actor, sc.Company.ID, id, domain.ChangeKind(in.Body.Change))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Artifact]{Body: artifactOut(v)}, nil
		})

	huma.Register(api, op("update-artifact-document", http.MethodPatch, "/companies/{company}/artifact-documents/{document}", "artifacts",
		"Muda projeto, workspace e tags do documento (não cria versão)"),
		func(ctx context.Context, in *patchArtifactDocumentIn) (*ItemOut[ArtifactDocument], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			cid := sc.Company.ID
			doc, err := domain.ParseID(in.Document)
			if err != nil {
				return nil, s.fail(ctx, domain.Invalid("document", "não é um uuid"))
			}
			p := service.ArtifactDocumentPatch{Tags: in.Body.Tags}
			if pr := in.Body.Project; pr != nil {
				if *pr == "" {
					p.ClearProject = true
				} else if p.ProjectID, err = s.projectRef(ctx, cid, *pr); err != nil {
					return nil, s.fail(ctx, err)
				}
			}
			if ws := in.Body.Workspace; ws != nil {
				if *ws == "" {
					p.ClearWorkspace = true
				} else if p.WorkspaceID, err = s.workspaceRef(ctx, cid, *ws); err != nil {
					return nil, s.fail(ctx, err)
				}
			}
			d, err := s.svc.UpdateArtifactDocument(ctx, sc.Actor, cid, doc, p)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := ArtifactDocument{ID: idStr(d.ID), Class: string(d.Class), Name: d.Name, ProjectID: idPtr(d.ProjectID),
				WorkspaceID: idPtr(d.WorkspaceID), Tags: d.Tags, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt}
			if out.Tags == nil {
				out.Tags = []string{}
			}
			return &ItemOut[ArtifactDocument]{Body: out}, nil
		})

	huma.Register(api, op("get-artifact-usage", http.MethodGet, "/companies/{company}/artifact-usage", "artifacts", "Espaço de artefatos usado contra a cota da empresa"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[ArtifactUsage], error) {
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			u, err := s.svc.GetArtifactUsage(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ArtifactUsage]{Body: ArtifactUsage(u)}, nil
		})
}
