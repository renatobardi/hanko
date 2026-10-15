package domain

import (
	"strings"
	"time"
)

// Artefatos (ADR-0028): arquivos produzidos pelas personas durante as runs (ou enviados por um humano). Cada
// registro é uma versão; o mesmo nome na mesma task cria a versão seguinte.

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
	// Version é o número da versão dentro da cadeia (empresa, task, nome), a partir de 1.
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

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
