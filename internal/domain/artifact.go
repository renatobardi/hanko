package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Artefatos (ADR-0028, ADR-0064): arquivos produzidos pelas personas durante as runs (ou enviados por um humano). Cada
// registro é uma versão de um documento (ArtifactDocument), que tem identidade estável: alterar é publicar uma versão
// nova do mesmo documento, classificada como major ou minor (MAJOR.MINOR).

// Artifact é uma versão de um artefato. O conteúdo não faz parte dele: lê-se à parte (ArtifactContent).
type Artifact struct {
	ID        ID     `json:"id"`
	CompanyID ID     `json:"company_id"`
	TaskID    *ID    `json:"task_id,omitempty"`
	RunID     *ID    `json:"run_id,omitempty"`
	ProjectID *ID    `json:"project_id,omitempty"`
	PersonaID *ID    `json:"persona_id,omitempty"`
	Author    Actor  `json:"author"`
	Name      string `json:"name"`
	Mime      string `json:"mime"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	// DocumentID é o documento (identidade estável) de que esta é uma versão.
	DocumentID ID `json:"document_id"`
	// Version é o ordinal da publicação dentro do documento, a partir de 1 (ordena; Major.Minor é o que se mostra).
	Version int `json:"version"`
	Major   int `json:"major"`
	Minor   int `json:"minor"`
	// Change é a classificação final da mudança em relação à versão anterior (a primeira versão é major).
	// ChangeDeclared é o que quem publicou declarou (nil: o servidor decidiu pela medida); ChangeRatio é a fração de
	// linhas mudadas (nil: não medido); ChangeDisputed marca a declaração contrariada pela medida.
	Change         ChangeKind  `json:"change"`
	ChangeDeclared *ChangeKind `json:"change_declared,omitempty"`
	ChangeReason   string      `json:"change_reason,omitempty"`
	ChangeRatio    *float64    `json:"change_ratio,omitempty"`
	ChangeDisputed bool        `json:"change_disputed,omitempty"`
	// RestoredFrom é a versão restaurada quando esta versão é uma restauração.
	RestoredFrom *ID       `json:"restored_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// Class diz se é um artefato (versionado, produzido pelo trabalho) ou um anexo (ADR-0052). Um anexo é sempre a
	// versão 1 e pode estar ligado a um comentário, a uma conversa do chat ou a uma mensagem dela.
	Class          ArtifactClass `json:"class"`
	CommentID      *ID           `json:"comment_id,omitempty"`
	ConversationID *ID           `json:"conversation_id,omitempty"`
	MessageID      *ID           `json:"message_id,omitempty"`
}

// SemVer devolve a versão como "MAJOR.MINOR".
func (a Artifact) SemVer() SemVer { return SemVer{Major: a.Major, Minor: a.Minor} }

// ArtifactDocument é a identidade estável de um artefato. Projeto, workspace e tags identificam e filtram, mas não
// fazem parte da identidade: mudá-los não cria documento novo. O nome é o da versão mais nova.
type ArtifactDocument struct {
	ID          ID            `json:"id"`
	CompanyID   ID            `json:"company_id"`
	Class       ArtifactClass `json:"class"`
	Name        string        `json:"name"`
	ProjectID   *ID           `json:"project_id,omitempty"`
	WorkspaceID *ID           `json:"workspace_id,omitempty"`
	Tags        []string      `json:"tags"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// ArtifactClass separa o artefato do anexo (coluna artifacts.kind).
type ArtifactClass string

const (
	ClassArtifact   ArtifactClass = "artifact"
	ClassAttachment ArtifactClass = "attachment"
)

// Valid diz se c é uma classe conhecida.
func (c ArtifactClass) Valid() bool { return c == ClassArtifact || c == ClassAttachment }

// ArtifactKind agrupa tipos MIME para o filtro "tipo" das listagens.
type ArtifactKind string

const (
	KindMarkdown ArtifactKind = "markdown"
	KindText     ArtifactKind = "text"
	KindImage    ArtifactKind = "image"
	KindOther    ArtifactKind = "other"
)

// Valid diz se k é um tipo conhecido do filtro.
func (k ArtifactKind) Valid() bool {
	switch k {
	case KindMarkdown, KindText, KindImage, KindOther:
		return true
	}
	return false
}

// KindOf classifica um MIME (já normalizado) no filtro de tipo; espelha o CASE da consulta de listagem.
func KindOf(mime string) ArtifactKind {
	switch {
	case mime == "text/markdown":
		return KindMarkdown
	case strings.HasPrefix(mime, "text/"):
		return KindText
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	}
	return KindOther
}

// inlineSafe são os únicos tipos servidos inline (no navegador): texto puro, markdown e imagens raster. Tudo o mais,
// inclusive HTML e SVG (que executam script), é entregue como download.
var inlineSafe = map[string]bool{
	"text/plain": true, "text/markdown": true,
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// InlineSafe diz se o conteúdo com este MIME pode ser servido com Content-Disposition: inline.
func InlineSafe(mime string) bool { return inlineSafe[mime] }

// Preview é como a tela mostra o conteúdo de um artefato.
type Preview string

const (
	PreviewMarkdown Preview = "markdown" // renderizado
	PreviewText     Preview = "text"     // texto/código em <pre>
	PreviewImage    Preview = "image"    // <img> (só tipos seguros)
	PreviewNone     Preview = "none"     // sem prévia: só download
)

// textual são os tipos de aplicação que são texto e merecem prévia em <pre> (e diff entre versões).
var textual = map[string]bool{
	"application/json": true, "application/xml": true, "application/yaml": true, "application/x-yaml": true,
	"application/toml": true, "application/javascript": true, "application/x-sh": true, "application/sql": true,
	"image/svg+xml": true, // o fonte do SVG é texto; nunca é desenhado como imagem
}

// IsText diz se o MIME é texto (a prévia em <pre> e o diff valem).
func IsText(mime string) bool { return strings.HasPrefix(mime, "text/") || textual[mime] }

// PreviewOf escolhe a prévia pelo MIME.
func PreviewOf(mime string) Preview {
	switch {
	case mime == "text/markdown":
		return PreviewMarkdown
	case InlineSafe(mime) && strings.HasPrefix(mime, "image/"):
		return PreviewImage
	case IsText(mime):
		return PreviewText
	}
	return PreviewNone
}

// ChangeKind classifica uma versão em relação à anterior: major (alteração pesada) ou minor (alteração mínima).
type ChangeKind string

const (
	ChangeMajor ChangeKind = "major"
	ChangeMinor ChangeKind = "minor"
)

// Valid diz se k é uma classificação conhecida.
func (k ChangeKind) Valid() bool { return k == ChangeMajor || k == ChangeMinor }

// SemVer é a versão MAJOR.MINOR de um documento de artefato. A primeira é 1.0.
type SemVer struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
}

// FirstSemVer é a versão da primeira publicação de um documento.
var FirstSemVer = SemVer{Major: 1, Minor: 0}

func (v SemVer) String() string { return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) }

// Next é a versão seguinte para uma mudança do tipo k: major leva a (M+1).0, minor a M.(m+1).
func (v SemVer) Next(k ChangeKind) SemVer {
	if k == ChangeMajor {
		return SemVer{Major: v.Major + 1}
	}
	return SemVer{Major: v.Major, Minor: v.Minor + 1}
}

// ParseSemVer lê "M.m" (aceita "vM.m" e "M", que vale M.0).
func ParseSemVer(s string) (SemVer, error) {
	s = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(s)), "v")
	maj, min, found := strings.Cut(s, ".")
	if !found {
		min = "0"
	}
	M, err1 := strconv.Atoi(maj)
	m, err2 := strconv.Atoi(min)
	if err1 != nil || err2 != nil || M < 1 || m < 0 || strings.ContainsAny(maj+min, "+-") {
		return SemVer{}, Invalid("version", fmt.Sprintf("%q is not a version (use MAJOR.MINOR, e.g. 1.2)", s))
	}
	return SemVer{Major: M, Minor: m}, nil
}

// Limiares da medida (ADR-0064): a partir de MajorChangeRatio das linhas mudadas a medida sugere major; abaixo de
// MinorChangeRatio, minor; entre os dois não opina.
const (
	MinorChangeRatio = 0.10
	MajorChangeRatio = 0.40
)

// ChangeMeasure é a medida de uma mudança contra a versão anterior. Measured falso: binário, grande demais ou sem
// versão anterior (não há o que medir; Ratio vale 0). Suggested vazio: a medida não opina.
type ChangeMeasure struct {
	Measured  bool
	Ratio     float64
	Suggested ChangeKind
}

// MeasureChange mede a mudança pelo diff por linha: ratio = (removidas + adicionadas) / (linhas antigas + novas).
// Mudar o tipo do conteúdo sugere major por si só.
func MeasureChange(removed, added, oldLines, newLines int, mimeChanged bool) ChangeMeasure {
	m := ChangeMeasure{Measured: true}
	if total := oldLines + newLines; total > 0 {
		m.Ratio = min(1, float64(removed+added)/float64(total))
	}
	switch {
	case mimeChanged || m.Ratio >= MajorChangeRatio:
		m.Suggested = ChangeMajor
	case m.Ratio < MinorChangeRatio:
		m.Suggested = ChangeMinor
	}
	return m
}

// ClassifyChange decide a classificação final: a declaração (de quem escreveu) vale; a medida só a questiona
// (disputed) quando sugere o contrário. Sem declaração vale a sugestão; sem sugestão, minor.
func ClassifyChange(declared *ChangeKind, m ChangeMeasure) (final ChangeKind, disputed bool) {
	if declared != nil {
		return *declared, m.Suggested != "" && m.Suggested != *declared
	}
	if m.Suggested != "" {
		return m.Suggested, false
	}
	return ChangeMinor, false
}

const (
	maxArtifactTags     = 20
	maxArtifactTagRunes = 40
	// MaxChangeReasonRunes é o tamanho máximo do motivo de uma mudança.
	MaxChangeReasonRunes = 280
)

// NormalizeTags apara, põe em minúsculas, tira repetidas e valida as tags livres de um documento ([a-z0-9-],
// 1 a 40 caracteres, até 20). A ordem de entrada é preservada.
func NormalizeTags(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if len(t) > maxArtifactTagRunes {
			return nil, Invalid("tags", fmt.Sprintf("tag %q too long (max %d characters)", t, maxArtifactTagRunes))
		}
		for _, r := range t {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return nil, Invalid("tags", fmt.Sprintf("tag %q: use only a-z, 0-9 and -", t))
			}
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxArtifactTags {
		return nil, Invalid("tags", fmt.Sprintf("too many tags (max %d)", maxArtifactTags))
	}
	return out, nil
}
