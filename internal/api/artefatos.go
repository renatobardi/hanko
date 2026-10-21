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

// Artefatos (ADR-0028). O envio é JSON com o conteúdo em base64 (cabe no mesmo cliente de todo o resto e na
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
	ID          string    `json:"id" format:"uuid"`
	CompanyID   string    `json:"company_id" format:"uuid"`
	Name        string    `json:"name" example:"plano.md"`
	Mime        string    `json:"mime" example:"text/markdown"`
	Kind        string    `json:"kind" enum:"markdown,text,image,other"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	Version     int       `json:"version" doc:"Número da versão na cadeia (empresa, task, nome)."`
	Versions    int       `json:"versions" doc:"Quantas versões a cadeia tem."`
	TaskID      string    `json:"task_id,omitempty" format:"uuid"`
	TaskKey     string    `json:"task_key,omitempty"`
	TaskTitle   string    `json:"task_title,omitempty"`
	RunID       string    `json:"run_id,omitempty" format:"uuid"`
	RunName     string    `json:"run_name,omitempty"`
	ProjectID   string    `json:"project_id,omitempty" format:"uuid"`
	ProjectSlug string    `json:"project_slug,omitempty"`
	ProjectName string    `json:"project_name,omitempty"`
	PersonaID   string    `json:"persona_id,omitempty" format:"uuid"`
	PersonaSlug string    `json:"persona_slug,omitempty"`
	PersonaName string    `json:"persona_name,omitempty"`
	Author      ActorRef  `json:"author"`
	CreatedAt   time.Time `json:"created_at"`
	// Class: artifact (versionado, produzido pelo trabalho) ou attachment (anexo, ADR-0052).
	Class          string `json:"class" enum:"artifact,attachment"`
	CommentID      string `json:"comment_id,omitempty" format:"uuid" doc:"Anexo: o comentário da task a que pertence."`
	ConversationID string `json:"conversation_id,omitempty" format:"uuid" doc:"Anexo: a conversa do chat a que pertence."`
	MessageID      string `json:"message_id,omitempty" format:"uuid" doc:"Anexo: a mensagem da conversa a que pertence."`
	// History só vem no detalhe: todas as versões da cadeia, da mais nova para a mais antiga.
	History []ArtifactVersion `json:"history,omitempty"`
}

// ArtifactVersion é uma linha do histórico de versões.
type ArtifactVersion struct {
	ID        string    `json:"id" format:"uuid"`
	Version   int       `json:"version"`
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

// ArtifactDiff é a comparação de texto entre duas versões da mesma cadeia.
type ArtifactDiff struct {
	From    int                `json:"from_version"`
	To      int                `json:"to_version"`
	Added   int                `json:"added"`
	Removed int                `json:"removed"`
	Coarse  bool               `json:"coarse,omitempty" doc:"O trecho alterado era grande demais para o diff fino: aparece tudo removido e tudo adicionado."`
	Lines   []ArtifactDiffLine `json:"lines"`
}

type ArtifactPath struct {
	CompanyPath
	Artifact string `path:"artifact" format:"uuid" doc:"Id da versão do artefato."`
}

type putArtifactIn struct {
	CompanyPath
	Body struct {
		Name    string `json:"name" minLength:"1" maxLength:"200" example:"plano.md" doc:"Nome do arquivo (sem barras). O mesmo nome na mesma task cria a versão seguinte."`
		Mime    string `json:"mime,omitempty" example:"text/markdown" doc:"Tipo do conteúdo; vazio = deduzido do nome e do conteúdo."`
		Content []byte `json:"content" doc:"Conteúdo do arquivo em base64." minLength:"1"`
		Task    string `json:"task,omitempty" doc:"Id ou chave da task. Run token: sempre a da execução."`
		Run     string `json:"run_id,omitempty" format:"uuid" doc:"Execução de origem. Run token: sempre a do token."`
		Project string `json:"project,omitempty" doc:"Id ou slug do projeto; vazio herda o da task."`
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
	AllVersions bool   `query:"all_versions" doc:"Todas as versões, não só a mais nova de cada artefato."`
	Class       string `query:"class" enum:"artifact,attachment,all" doc:"Padrão artifact; attachment lista os anexos; all, os dois."`
}

type diffArtifactIn struct {
	ArtifactPath
	Against string `query:"against" format:"uuid" doc:"Versão anterior a comparar; vazio = a versão imediatamente anterior."`
}

func artifactOut(v service.ArtifactView) Artifact {
	return Artifact{ID: idStr(v.ID), CompanyID: idStr(v.CompanyID), Name: v.Name, Mime: v.Mime, Kind: string(domain.KindOf(v.Mime)),
		SizeBytes: v.SizeBytes, SHA256: v.SHA256, Version: v.Version, Versions: v.Versions,
		TaskID: idPtr(v.TaskID), TaskKey: v.TaskKey, TaskTitle: v.TaskTitle, RunID: idPtr(v.RunID), RunName: v.RunName,
		ProjectID: idPtr(v.ProjectID), ProjectSlug: v.ProjectSlug, ProjectName: v.ProjectName,
		PersonaID: idPtr(v.PersonaID), PersonaSlug: v.PersonaSlug, PersonaName: v.PersonaName,
		Author: actorRef(v.Author), CreatedAt: v.CreatedAt, Class: string(v.Class),
		CommentID: idPtr(v.CommentID), ConversationID: idPtr(v.ConversationID), MessageID: idPtr(v.MessageID)}
}

func artifactVersionOut(a domain.Artifact) ArtifactVersion {
	return ArtifactVersion{ID: idStr(a.ID), Version: a.Version, SizeBytes: a.SizeBytes, SHA256: a.SHA256,
		Author: actorRef(a.Author), RunID: idPtr(a.RunID), CreatedAt: a.CreatedAt}
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
		"Publica um artefato (nova versão se o nome já existe na task)"))
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
		v, err := s.svc.PutArtifact(ctx, sc.Actor, cid, service.PutArtifactInput{Name: b.Name, Mime: b.Mime, Content: b.Content,
			TaskID: task, RunID: run, ProjectID: project})
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

	huma.Register(api, op("get-artifact", http.MethodGet, "/companies/{company}/artifacts/{artifact}", "artifacts", "Obtém uma versão de artefato e o histórico da cadeia"),
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
			out := ArtifactDiff{From: from.Version, To: cur.Version, Added: r.Added, Removed: r.Removed, Coarse: r.Coarse, Lines: make([]ArtifactDiffLine, 0, len(r.Lines))}
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
