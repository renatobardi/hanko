package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Artefatos e metas pelo MCP (ADR-0028). Desde o ADR-0064 a persona também lista, lê e restaura documentos de
// artefato: para alterar um documento ela publica com artifact=<id> em vez de inventar outro nome.

const (
	// maxArtifactTextBytes é o maior artefato de texto devolvido inline por hanko_artifact_get.
	maxArtifactTextBytes = 512 << 10
	// maxArtifactBinaryBytes é o maior artefato binário devolvido em base64.
	maxArtifactBinaryBytes = 2 << 20
)

// artifactDocBackend é a parte do service que lê e restaura documentos de artefato (ADR-0064).
type artifactDocBackend interface {
	ListArtifacts(ctx context.Context, companyID domain.ID, f service.ArtifactFilter) ([]service.ArtifactView, error)
	GetArtifactView(ctx context.Context, companyID, id domain.ID) (service.ArtifactView, error)
	ArtifactContent(ctx context.Context, companyID, id domain.ID) (domain.Artifact, []byte, error)
	ArtifactVersions(ctx context.Context, companyID, id domain.ID) ([]domain.Artifact, error)
	ArtifactVersionID(ctx context.Context, companyID, documentID domain.ID, version string) (domain.ID, error)
	RestoreArtifact(ctx context.Context, actor domain.Actor, companyID, versionID domain.ID, taskID, runID *domain.ID) (service.ArtifactView, error)
}

var _ artifactDocBackend = (*service.Service)(nil)

type artifactPutIn struct {
	Company       string   `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Name          string   `json:"name,omitempty" jsonschema:"nome do arquivo, sem barras (ex. relatorio.md); ao alterar com artifact, omita para manter o nome do documento"`
	Artifact      string   `json:"artifact,omitempty" jsonschema:"id do documento a ALTERAR (veja o briefing ou hanko_artifact_list); com ele a publicação vira a versão seguinte do mesmo documento, mesmo com outro nome ou em outra task. Sem ele, o mesmo nome na mesma task também versiona; outro nome cria documento novo"`
	Change        string   `json:"change,omitempty" jsonschema:"ao alterar: major (mudança pesada: estrutura, premissa, conclusão, reescrita) ou minor (ajuste pontual, correção, acréscimo pequeno); vazio = o servidor decide pelo tamanho do diff"`
	ChangeReason  string   `json:"change_reason,omitempty" jsonschema:"motivo curto da mudança (até 280 caracteres); obrigatório quando você informa change"`
	Content       string   `json:"content,omitempty" jsonschema:"conteúdo em texto (UTF-8): markdown, código, JSON, CSV. Para binário use content_base64"`
	ContentBase64 string   `json:"content_base64,omitempty" jsonschema:"conteúdo em base64, para arquivos binários (imagens, PDF). Use content OU content_base64"`
	Mime          string   `json:"mime,omitempty" jsonschema:"tipo do conteúdo (ex. text/markdown); vazio = deduzido do nome"`
	Task          string   `json:"task,omitempty" jsonschema:"chave da task; com token de run é sempre a task da sua execução"`
	Project       string   `json:"project,omitempty" jsonschema:"slug do projeto; vazio = o da task"`
	Tags          []string `json:"tags,omitempty" jsonschema:"tags livres de um documento NOVO (a-z, 0-9 e -); as de um documento existente são do humano"`
}

type artifactOut struct {
	ID        string   `json:"id" jsonschema:"id desta versão"`
	Document  string   `json:"artifact" jsonschema:"id do documento: use em artifact= para alterá-lo"`
	Name      string   `json:"name"`
	Version   string   `json:"version" jsonschema:"MAJOR.MINOR (1.0 na primeira publicação)"`
	Change    string   `json:"change" jsonschema:"major ou minor em relação à versão anterior"`
	Disputed  bool     `json:"change_disputed,omitempty" jsonschema:"a medida do diff contradiz a classificação declarada; o humano pode reclassificar"`
	Versions  int      `json:"versions" jsonschema:"quantas versões o documento tem"`
	Mime      string   `json:"mime"`
	SizeBytes int64    `json:"size_bytes"`
	TaskKey   string   `json:"task_key,omitempty"`
	Project   string   `json:"project,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	SHA256    string   `json:"sha256"`
}

type artifactListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Task    string `json:"task,omitempty" jsonschema:"chave da task: documentos com alguma versão nela"`
	Project string `json:"project,omitempty" jsonschema:"slug do projeto do documento"`
	Tag     string `json:"tag,omitempty"`
	Query   string `json:"query,omitempty" jsonschema:"parte do nome"`
}

type artifactListOut struct {
	Artifacts []artifactOut `json:"artifacts"`
}

type artifactGetIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Artifact string `json:"artifact" jsonschema:"id do documento (veja o briefing ou hanko_artifact_list)"`
	Version  string `json:"version,omitempty" jsonschema:"MAJOR.MINOR (ex. 1.2); vazio = a mais nova"`
	History  bool   `json:"history,omitempty" jsonschema:"também lista todas as versões (número, classificação, motivo, data)"`
}

type artifactVersionOut struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Change    string `json:"change"`
	Reason    string `json:"change_reason,omitempty"`
	Disputed  bool   `json:"change_disputed,omitempty"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type artifactGetOut struct {
	Artifact  artifactOut          `json:"artifact"`
	Content   string               `json:"content,omitempty"`
	Base64    string               `json:"content_base64,omitempty"`
	Truncated bool                 `json:"too_large,omitempty" jsonschema:"grande demais para ler por aqui"`
	History   []artifactVersionOut `json:"history,omitempty"`
}

type artifactRestoreIn struct {
	Company  string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Artifact string `json:"artifact" jsonschema:"id do documento"`
	Version  string `json:"version" jsonschema:"MAJOR.MINOR da versão a restaurar; vira a versão mais nova (major)"`
}

type artifactPutOut struct {
	Artifact artifactOut `json:"artifact"`
}

type goalListIn struct {
	Company string `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Status  string `json:"status,omitempty" jsonschema:"active (padrão), achieved, abandoned ou all"`
	Project string `json:"project,omitempty" jsonschema:"slug do projeto: só as metas dele"`
}

type goalOut struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title"`
	Metric      string  `json:"metric,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	Start       float64 `json:"start_value"`
	Target      float64 `json:"target_value"`
	Current     float64 `json:"current_value"`
	Progress    float64 `json:"progress_percent" jsonschema:"0 a 100"`
	Status      string  `json:"status"`
	DueDate     string  `json:"due_date,omitempty"`
	ProjectSlug string  `json:"project_slug,omitempty"`
	Tasks       int     `json:"tasks" jsonschema:"quantas tasks servem a esta meta"`
	Parent      string  `json:"parent,omitempty" jsonschema:"slug da meta-pai (árvore de metas)"`
	Chain       string  `json:"chain,omitempty" jsonschema:"cadeia da raiz até esta meta, separada por ›"`
	Rollup      bool    `json:"rollup,omitempty" jsonschema:"o progresso é o agregado das submetas: registre nas submetas, não nesta"`
}

type goalListOut struct {
	Goals []goalOut `json:"goals"`
}

type goalUpdateIn struct {
	Company string  `json:"company,omitempty" jsonschema:"slug ou id da empresa (obrigatório só sem token de run)"`
	Goal    string  `json:"goal" jsonschema:"slug da meta (veja hanko_goal_list)"`
	Value   float64 `json:"value" jsonschema:"o valor ATUAL da métrica (não o incremento); ao cruzar o alvo a meta é atingida"`
	Note    string  `json:"note,omitempty" jsonschema:"nota curta sobre a medição (de onde veio o número)"`
}

type goalUpdateOut struct {
	Goal goalOut `json:"goal"`
}

func registerArtifactTools(s *sdk.Server, t *tools) {
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_artifact_put",
		Description: "Publica um arquivo que você produziu (relatório, plano, dados, imagem) como artefato, para o humano abrir na tela Artefatos. " +
			"Para ALTERAR um documento que já existe (pedido de mudança, revisão, correção), informe artifact=<id> (do briefing ou de hanko_artifact_list) e declare change=major|minor com change_reason: " +
			"isso cria a versão seguinte do mesmo documento (MAJOR.MINOR) em vez de um documento novo; não invente outro nome (plano-v2.md) para uma alteração. " +
			"Leia a versão atual com hanko_artifact_get antes de alterar. O mesmo conteúdo não duplica. Texto em content; binário em content_base64. " +
			"O tamanho máximo do arquivo é HANKO_ARTIFACT_MAX_BYTES (padrão 5 MiB).",
	}, t.artifactPut)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_artifact_list",
		Description: "Lista os documentos de artefato da empresa (só a versão mais nova de cada), com id, nome, versão MAJOR.MINOR, task, projeto e tags. Use para achar o documento a ler ou alterar.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.artifactList)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_artifact_get",
		Description: "Lê um documento de artefato: a versão mais nova ou uma anterior (version=1.2), com o conteúdo (texto em content, binário em content_base64) e, com history=true, o histórico de versões.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.artifactGet)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_artifact_restore",
		Description: "Restaura uma versão anterior de um documento: o conteúdo dela vira a versão mais nova (sempre major, registrada como restauração). O histórico não é apagado.",
	}, t.artifactRestore)
	sdk.AddTool(s, &sdk.Tool{
		Name:        "hanko_goal_list",
		Description: "Lista as metas da empresa com o progresso (padrão: as ativas). Use para saber o que o trabalho deve mover e quais metas pode medir.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, t.goalList)
	sdk.AddTool(s, &sdk.Tool{
		Name: "hanko_goal_update",
		Description: "Registra o valor atual de uma meta (hanko_goal_list mostra as metas). Informe o valor medido, não o incremento. " +
			"Ao cruzar o alvo a meta é marcada como atingida e o humano é avisado. Só registre números que você mediu de fato.",
	}, t.goalUpdate)
}

func toArtifactOut(v service.ArtifactView) artifactOut {
	return artifactOut{ID: v.ID.String(), Document: v.DocumentID.String(), Name: v.Name, Version: v.SemVer, Change: string(v.Change),
		Disputed: v.ChangeDisputed, Versions: v.Versions, Mime: v.Mime, SizeBytes: v.SizeBytes, TaskKey: v.TaskKey,
		Project: v.DocumentProjectSlug, Tags: v.Tags, SHA256: v.SHA256}
}

func toGoalOut(v service.GoalView) goalOut {
	o := goalOut{Slug: v.Slug, Title: v.Title, Metric: v.Metric, Unit: v.Unit, Start: v.StartValue, Target: v.TargetValue, Current: v.CurrentValue,
		Progress: v.ProgressPercent, Status: string(v.Status), ProjectSlug: v.ProjectSlug, Tasks: v.TasksTotal}
	if v.DueDate != nil {
		o.DueDate = v.DueDate.Format("2006-01-02")
	}
	o.Parent, o.Rollup = v.ParentSlug, v.Rollup
	if len(v.Path) > 0 {
		parts := make([]string, 0, len(v.Path)+1)
		for _, p := range v.Path {
			parts = append(parts, p.Title)
		}
		o.Chain = strings.Join(append(parts, v.Title), " › ")
	}
	return o
}

func (t *tools) artifactPut(ctx context.Context, _ *sdk.CallToolRequest, in artifactPutIn) (*sdk.CallToolResult, artifactPutOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, artifactPutOut{}, err
	}
	if strings.TrimSpace(in.Name) == "" && strings.TrimSpace(in.Artifact) == "" {
		return nil, artifactPutOut{}, errors.New("name é obrigatório (só pode faltar ao alterar um documento com artifact)")
	}
	var content []byte
	switch {
	case in.Content != "" && in.ContentBase64 != "":
		return nil, artifactPutOut{}, errors.New("use content OU content_base64, não os dois")
	case in.ContentBase64 != "":
		if content, err = base64.StdEncoding.DecodeString(strings.TrimSpace(in.ContentBase64)); err != nil {
			return nil, artifactPutOut{}, errors.New("content_base64 não é base64 válido")
		}
	case in.Content != "":
		content = []byte(in.Content)
	default:
		return nil, artifactPutOut{}, errors.New("content (texto) ou content_base64 é obrigatório")
	}
	pi := service.PutArtifactInput{Name: in.Name, Mime: in.Mime, Content: content, ChangeReason: in.ChangeReason, Tags: in.Tags}
	if ref := strings.TrimSpace(in.Artifact); ref != "" {
		doc, err := domain.ParseID(ref)
		if err != nil {
			return nil, artifactPutOut{}, fmt.Errorf("artifact %q não é um id de documento (veja hanko_artifact_list)", ref)
		}
		pi.DocumentID = &doc
	}
	if c := strings.TrimSpace(in.Change); c != "" {
		k := domain.ChangeKind(strings.ToLower(c))
		if !k.Valid() {
			return nil, artifactPutOut{}, fmt.Errorf("change %q inválido; use major ou minor", c)
		}
		pi.Change = &k
	}
	if tk := strings.TrimSpace(in.Task); tk != "" {
		v, err := t.b.GetTask(ctx, company, tk)
		if err != nil {
			return nil, artifactPutOut{}, toolErr("task", tk, err)
		}
		if id.ViaToken && (id.TaskID == nil || v.ID != *id.TaskID) {
			return nil, artifactPutOut{}, errors.New("com token de run o artefato é sempre da task da sua execução; omita task")
		}
		pi.TaskID = &v.ID
	}
	if id.ViaToken {
		pi.TaskID, pi.RunID = id.TaskID, id.RunID
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, artifactPutOut{}, toolErr("projeto", p, err)
		}
		pi.ProjectID = &pr.ID
	}
	v, err := t.b.PutArtifact(ctx, id.Actor, company, pi)
	if err != nil {
		return nil, artifactPutOut{}, opErr("publicar artefato", err)
	}
	where := ""
	if v.TaskKey != "" {
		where = " na task " + v.TaskKey
	}
	note := ""
	if v.ChangeDisputed {
		note = fmt.Sprintf(" Atenção: o diff mediu %.0f%% das linhas mudadas, o que contradiz %s; a versão ficou marcada para o humano conferir.", ratioPct(v.ChangeRatio), v.Change)
	}
	return textResult("Artefato %s %s (%s) publicado%s — documento %s (%s, %d bytes).%s", v.Name, v.SemVer, v.Change, where, v.DocumentID, v.Mime, v.SizeBytes, note),
		artifactPutOut{Artifact: toArtifactOut(v)}, nil
}

func ratioPct(r *float64) float64 {
	if r == nil {
		return 0
	}
	return *r * 100
}

func (t *tools) artifactDocs(ctx context.Context, company string) (artifactDocBackend, identity, domain.ID, error) {
	ab, ok := t.b.(artifactDocBackend)
	if !ok {
		return nil, identity{}, domain.ID{}, errors.New("leitura de artefatos indisponível neste servidor")
	}
	id, cid, err := t.scope(ctx, company)
	return ab, id, cid, err
}

func (t *tools) artifactList(ctx context.Context, _ *sdk.CallToolRequest, in artifactListIn) (*sdk.CallToolResult, artifactListOut, error) {
	ab, _, cid, err := t.artifactDocs(ctx, in.Company)
	if err != nil {
		return nil, artifactListOut{}, err
	}
	f := service.ArtifactFilter{Tag: in.Tag, Query: in.Query, Limit: 50}
	if tk := strings.TrimSpace(in.Task); tk != "" {
		v, err := t.b.GetTask(ctx, cid, tk)
		if err != nil {
			return nil, artifactListOut{}, toolErr("task", tk, err)
		}
		f.TaskID = &v.ID
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, cid, p)
		if err != nil {
			return nil, artifactListOut{}, toolErr("projeto", p, err)
		}
		f.ProjectID = &pr.ID
	}
	vs, err := ab.ListArtifacts(ctx, cid, f)
	if err != nil {
		return nil, artifactListOut{}, opErr("listar artefatos", err)
	}
	out := artifactListOut{Artifacts: make([]artifactOut, 0, len(vs))}
	var sb strings.Builder
	for _, v := range vs {
		o := toArtifactOut(v)
		out.Artifacts = append(out.Artifacts, o)
		fmt.Fprintf(&sb, "\n- %s — %s %s (%s)", o.Document, o.Name, o.Version, o.Mime)
		if o.TaskKey != "" {
			fmt.Fprintf(&sb, " · %s", o.TaskKey)
		}
		if len(o.Tags) > 0 {
			fmt.Fprintf(&sb, " · #%s", strings.Join(o.Tags, " #"))
		}
	}
	return textResult("%d documento(s)%s", len(out.Artifacts), sb.String()), out, nil
}

func (t *tools) artifactGet(ctx context.Context, _ *sdk.CallToolRequest, in artifactGetIn) (*sdk.CallToolResult, artifactGetOut, error) {
	ab, _, cid, err := t.artifactDocs(ctx, in.Company)
	if err != nil {
		return nil, artifactGetOut{}, err
	}
	doc, err := domain.ParseID(strings.TrimSpace(in.Artifact))
	if err != nil {
		return nil, artifactGetOut{}, fmt.Errorf("artifact %q não é um id de documento (veja hanko_artifact_list)", in.Artifact)
	}
	vid, err := ab.ArtifactVersionID(ctx, cid, doc, strings.TrimSpace(in.Version))
	if err != nil {
		return nil, artifactGetOut{}, opErr("achar a versão", err)
	}
	v, err := ab.GetArtifactView(ctx, cid, vid)
	if err != nil {
		return nil, artifactGetOut{}, opErr("ler artefato", err)
	}
	out := artifactGetOut{Artifact: toArtifactOut(v)}
	var hist strings.Builder
	if in.History {
		vs, err := ab.ArtifactVersions(ctx, cid, vid)
		if err != nil {
			return nil, artifactGetOut{}, opErr("listar versões", err)
		}
		hist.WriteString("\n\nVersões:")
		for _, a := range vs {
			h := artifactVersionOut{ID: a.ID.String(), Version: a.SemVer().String(), Change: string(a.Change), Reason: a.ChangeReason,
				Disputed: a.ChangeDisputed, Name: a.Name, CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339)}
			out.History = append(out.History, h)
			fmt.Fprintf(&hist, "\n- %s %s %s", h.Version, h.Change, h.Name)
			if h.Reason != "" {
				fmt.Fprintf(&hist, " — %s", h.Reason)
			}
		}
	}
	head := fmt.Sprintf("Artefato %s %s (documento %s, %s, %d bytes, %d versões)", v.Name, v.SemVer, v.DocumentID, v.Mime, v.SizeBytes, v.Versions)
	limit := int64(maxArtifactBinaryBytes)
	if domain.IsText(v.Mime) {
		limit = maxArtifactTextBytes
	}
	if v.SizeBytes > limit {
		out.Truncated = true
		return textResult("%s: grande demais para ler por aqui.%s", head, hist.String()), out, nil
	}
	_, body, err := ab.ArtifactContent(ctx, cid, vid)
	if err != nil {
		return nil, artifactGetOut{}, opErr("ler artefato", err)
	}
	if domain.IsText(v.Mime) && utf8.Valid(body) {
		out.Content = string(body)
		return textResult("%s:%s\n\n%s", head, hist.String(), out.Content), out, nil
	}
	out.Base64 = base64.StdEncoding.EncodeToString(body)
	return textResult("%s em base64 no campo content_base64.%s", head, hist.String()), out, nil
}

func (t *tools) artifactRestore(ctx context.Context, _ *sdk.CallToolRequest, in artifactRestoreIn) (*sdk.CallToolResult, artifactPutOut, error) {
	ab, id, cid, err := t.artifactDocs(ctx, in.Company)
	if err != nil {
		return nil, artifactPutOut{}, err
	}
	doc, err := domain.ParseID(strings.TrimSpace(in.Artifact))
	if err != nil {
		return nil, artifactPutOut{}, fmt.Errorf("artifact %q não é um id de documento (veja hanko_artifact_list)", in.Artifact)
	}
	if strings.TrimSpace(in.Version) == "" {
		return nil, artifactPutOut{}, errors.New("version é obrigatório (MAJOR.MINOR da versão a restaurar)")
	}
	vid, err := ab.ArtifactVersionID(ctx, cid, doc, strings.TrimSpace(in.Version))
	if err != nil {
		return nil, artifactPutOut{}, opErr("achar a versão", err)
	}
	var taskID, runID *domain.ID
	if id.ViaToken {
		taskID, runID = id.TaskID, id.RunID
	}
	v, err := ab.RestoreArtifact(ctx, id.Actor, cid, vid, taskID, runID)
	if err != nil {
		return nil, artifactPutOut{}, opErr("restaurar artefato", err)
	}
	return textResult("Versão %s restaurada: agora é a %s do documento %s.", in.Version, v.SemVer, v.DocumentID), artifactPutOut{Artifact: toArtifactOut(v)}, nil
}

func (t *tools) goalList(ctx context.Context, _ *sdk.CallToolRequest, in goalListIn) (*sdk.CallToolResult, goalListOut, error) {
	_, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, goalListOut{}, err
	}
	f := service.GoalFilter{Status: domain.GoalActive}
	switch s := strings.TrimSpace(in.Status); s {
	case "", "active":
	case "all":
		f.Status = ""
	default:
		f.Status = domain.GoalStatus(s)
		if !f.Status.Valid() {
			return nil, goalListOut{}, fmt.Errorf("status %q inválido; use active, achieved, abandoned ou all", s)
		}
	}
	if p := strings.TrimSpace(in.Project); p != "" {
		pr, err := t.b.GetProject(ctx, company, p)
		if err != nil {
			return nil, goalListOut{}, toolErr("projeto", p, err)
		}
		f.ProjectID = &pr.ID
	}
	list, err := t.b.ListGoalViews(ctx, company, f)
	if err != nil {
		return nil, goalListOut{}, opErr("listar metas", err)
	}
	out := goalListOut{Goals: make([]goalOut, 0, len(list))}
	var sb strings.Builder
	for _, g := range list {
		o := toGoalOut(g)
		out.Goals = append(out.Goals, o)
		fmt.Fprintf(&sb, "\n- %s — %s: %.0f%% (%v de %v %s), %s", o.Slug, o.Title, o.Progress, o.Current, o.Target, o.Unit, o.Status)
	}
	return textResult("%d meta(s)%s", len(out.Goals), sb.String()), out, nil
}

func (t *tools) goalUpdate(ctx context.Context, _ *sdk.CallToolRequest, in goalUpdateIn) (*sdk.CallToolResult, goalUpdateOut, error) {
	id, company, err := t.scope(ctx, in.Company)
	if err != nil {
		return nil, goalUpdateOut{}, err
	}
	ref := strings.TrimSpace(in.Goal)
	if ref == "" {
		return nil, goalUpdateOut{}, errors.New("goal (slug da meta) é obrigatório")
	}
	g, err := t.b.GetGoal(ctx, company, ref)
	if err != nil {
		return nil, goalUpdateOut{}, toolErr("meta", ref, err)
	}
	v, err := t.b.RecordGoalProgress(ctx, id.Actor, company, g.ID, in.Value, in.Note)
	if err != nil {
		return nil, goalUpdateOut{}, opErr("registrar progresso da meta", err)
	}
	msg := fmt.Sprintf("Meta %s: %.0f%% (%v de %v)", v.Slug, v.ProgressPercent, v.CurrentValue, v.TargetValue)
	if v.Status == domain.GoalAchieved {
		msg += " — meta atingida"
	}
	return textResult("%s", msg), goalUpdateOut{Goal: toGoalOut(v)}, nil
}
