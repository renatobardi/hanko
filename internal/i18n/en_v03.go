package i18n

// enV03 holds the English texts shared by the v0.3 parity screens (navigation labels and blurbs).
var enV03 = map[string]string{
	"Sala": "Room",
	"Uma conversa com várias personas ao mesmo tempo.": "One conversation with several personas at once.",
	"Fila de revisão": "Review queue",
	"Os itens de pipeline que esperam a sua decisão.": "The pipeline items waiting for your decision.",
	"Pipelines": "Pipelines",
	"Etapas com aprovação e automação por persona.": "Stages with approval and automation by persona.",
	"Cases": "Cases",
	"Registros estruturados que as personas mantêm e o time acompanha.": "Structured records the personas keep and the team follows.",
	"Status": "Status",
	"Resumos vivos das tasks que importam, mantidos por uma persona.": "Living summaries of the tasks that matter, kept by a persona.",
	"Aprendizados": "Learnings",
	"O que as decisões de revisão ensinaram ao time.": "What the review decisions taught the team.",
}

func init() { addCatalog("v03", enV03) }
