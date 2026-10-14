package views

// NavItem is a sidebar entry. Milestone is empty for implemented pages;
// otherwise the item leads to a "Em breve" page for that milestone.
type NavItem struct {
	Key       string
	Label     string
	Icon      string
	Path      string // relative to /c/{company}; "" = dashboard
	Milestone string
	Blurb     string
}

// NavGroup is a labelled block of the sidebar.
type NavGroup struct {
	Label string
	Items []NavItem
}

// Nav is the full sidebar of the Kubo DS.
var Nav = []NavGroup{
	{Items: []NavItem{
		{Key: "buscar", Label: "Buscar", Icon: "search", Milestone: "M3", Blurb: "Busca em tasks, personas, execuções e na memória da empresa."},
		{Key: "painel", Label: "Painel", Icon: "layout-grid"},
		{Key: "caixa", Label: "Caixa de entrada", Icon: "mail", Path: "/caixa"},
		{Key: "chat", Label: "Chat", Icon: "message-square", Milestone: "v0.2", Blurb: "Conversa direta com uma persona, fora de uma task."},
		{Key: "decisoes", Label: "Decisões", Icon: "circle-check", Path: "/decisoes"},
	}},
	{Label: "Trabalho", Items: []NavItem{
		{Key: "tasks", Label: "Tasks", Icon: "list", Path: "/tasks"},
		{Key: "projetos", Label: "Projetos", Icon: "layers", Milestone: "M3", Blurb: "Agrupe tasks por projeto, com workspace e orçamento próprios."},
		{Key: "fluxos", Label: "Fluxos", Icon: "workflow", Path: "/fluxos"},
		{Key: "metas", Label: "Metas", Icon: "trending-up", Milestone: "v0.2", Blurb: "Metas mensuráveis ligadas a projetos."},
		{Key: "artefatos", Label: "Artefatos", Icon: "file", Milestone: "v0.2", Blurb: "Documentos e arquivos produzidos pelas personas."},
		{Key: "workspaces", Label: "Workspaces", Icon: "git-branch", Milestone: "M3", Blurb: "Diretórios e repositórios onde as personas trabalham."},
	}},
	{Label: "Organização", Items: []NavItem{
		{Key: "personas", Label: "Personas", Icon: "bot", Path: "/personas"},
		{Key: "organograma", Label: "Organograma", Icon: "network", Path: "/organograma"},
		{Key: "skills", Label: "Skills", Icon: "sparkles", Milestone: "v0.2", Blurb: "Habilidades reutilizáveis habilitadas por persona."},
		{Key: "integracoes", Label: "Integrações", Icon: "link", Milestone: "v0.2", Blurb: "Serviços externos conectados à empresa."},
		{Key: "gates", Label: "Gates", Icon: "shield", Path: "/gates"},
	}},
	{Label: "Supervisão", Items: []NavItem{
		{Key: "execucoes", Label: "Execuções", Icon: "activity", Path: "/execucoes"},
		{Key: "custos", Label: "Custos", Icon: "database", Path: "/custos"},
		{Key: "atividade", Label: "Atividade", Icon: "clock", Path: "/atividade"},
	}},
}

// FindNav returns the nav item with key.
func FindNav(key string) (NavItem, bool) {
	for _, g := range Nav {
		for _, it := range g.Items {
			if it.Key == key {
				return it, true
			}
		}
	}
	return NavItem{}, false
}

// Href is the URL of the item under the company base.
func (it NavItem) Href(base string) string {
	if it.Milestone != "" {
		return base + "/em-breve/" + it.Key
	}
	return base + it.Path + func() string {
		if it.Path == "" {
			return "/"
		}
		return ""
	}()
}
