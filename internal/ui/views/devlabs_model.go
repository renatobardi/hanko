package views

import "github.com/renatobardi/hanko/internal/i18n"

// Telas de desenvolvimento (ADR-0058 §E): /design-guide (catálogo do Kubo DS) e /dev/ux (ux-labs). Só admin/dev.

// DGMode escolhe os temas do catálogo: "ambos" (lado a lado), "claro" ou "escuro".
const (
	DGBoth  = "ambos"
	DGLight = "claro"
	DGDark  = "escuro"
)

// DevNavItem é um link do cabeçalho das telas de desenvolvimento.
type DevNavItem struct {
	Href  string
	Label string
	On    bool
}

// DesignGuideVM alimenta o catálogo do DS.
type DesignGuideVM struct {
	Mode    string
	Version string
	Nav     []DevNavItem
}

// DGSwatch é uma amostra de token de cor; Key vira a classe `sw-<Key>` do CSS.
type DGSwatch struct {
	Key   string
	Token string
}

// DGSwatches são os tokens exibidos no catálogo, na ordem do DS.
var DGSwatches = []DGSwatch{
	{"background", "--background"}, {"foreground", "--foreground"}, {"card", "--card"}, {"primary", "--primary"},
	{"primary-foreground", "--primary-foreground"}, {"secondary", "--secondary"}, {"muted", "--muted"},
	{"muted-foreground", "--muted-foreground"}, {"accent", "--accent"}, {"border", "--border"}, {"ring", "--ring"},
	{"destructive", "--destructive"}, {"warn", "--warn"}, {"chart-1", "--chart-1"}, {"chart-2", "--chart-2"},
	{"chart-3", "--chart-3"}, {"chart-4", "--chart-4"}, {"chart-5", "--chart-5"},
}

// DGTextSizes são os degraus tipográficos do DS (o app roda em 14 px).
var DGTextSizes = []struct{ Key, Token, Sample string }{
	{"2xl", "--text-2xl", i18n.N("Título de página")}, {"xl", "--text-xl", i18n.N("Título de seção")}, {"lg", "--text-lg", i18n.N("Subtítulo")},
	{"base", "--text-base", i18n.N("Texto de leitura")}, {"sm", "--text-sm", i18n.N("Texto de interface (padrão)")},
	{"xs", "--text-xs", i18n.N("Legenda e metadados")}, {"2xs", "--text-2xs", i18n.N("Microtexto")},
}

// DGRadii são os raios do DS.
var DGRadii = []struct{ Key, Token string }{
	{"sm", "--radius-sm"}, {"md", "--radius-md"}, {"lg", "--radius-lg"}, {"xl", "--radius-xl"}, {"2xl", "--radius-2xl"}, {"4xl", "--radius-4xl"},
}

// DGLogKinds são linhas de exemplo do log de run, uma por tipo de evento.
var DGLogKinds = []LogLine{
	{Seq: 1, Time: "12:00:01", Kind: "system", Label: "system", Text: "run iniciada · persona ada · modelo m1"},
	{Seq: 2, Time: "12:00:02", Kind: "assistant", Label: "assistant", Text: "Vou ler o briefing e o arquivo de requisitos."},
	{Seq: 3, Time: "12:00:03", Kind: "tool_use", Label: "tool_use", Text: "Read docs/requisitos.md"},
	{Seq: 4, Time: "12:00:03", Kind: "tool_result", Label: "tool_result", Text: "412 linhas lidas"},
	{Seq: 5, Time: "12:00:05", Kind: "stdout", Label: "stdout", Text: "build ok"},
	{Seq: 6, Time: "12:00:06", Kind: "stderr", Label: "stderr", Text: "aviso: arquivo temporário não removido"},
}

// DGMessages são dois comentários de exemplo (persona e humano).
var DGMessages = []Msg{
	{Author: "Ada", Body: HTML("<p>Terminei o rascunho. Veja o artefato <code>plano.md</code> e diga se posso seguir.</p>"), When: "10:02"},
	{Author: "Você", Human: true, Body: HTML("<p>Pode seguir. Atenção ao orçamento da semana.</p>"), When: "10:05"},
}

// UXThreadVM alimenta o laboratório do thread longo.
type UXThreadVM struct {
	Count   int
	Version string
	Nav     []DevNavItem
	Thread  []Msg
}

// UXRunVM alimenta o laboratório da run com muitas linhas.
type UXRunVM struct {
	Total   int
	RunID   string
	API     string
	Version string
	Nav     []DevNavItem
	LogMsgs string
	Lang    string
	Lines   []LogLine // amostra do fallback sem JS (as primeiras linhas)
}

// UXIndexVM lista os cenários.
type UXIndexVM struct {
	Version string
	Nav     []DevNavItem
	Labs    []UXLab
}

// UXLab é um cenário do ux-labs.
type UXLab struct {
	Href, Title, Blurb string
}
