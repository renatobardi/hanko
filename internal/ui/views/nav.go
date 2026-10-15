package views

import "github.com/renatobardi/hanko/internal/i18n"

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
		{Key: "buscar", Label: i18n.N("Buscar"), Icon: "search", Milestone: "M3", Blurb: i18n.N("Busca em tasks, personas, execuções e na memória da empresa.")},
		{Key: "painel", Label: i18n.N("Painel"), Icon: "layout-grid"},
		{Key: "caixa", Label: i18n.N("Caixa de entrada"), Icon: "mail", Path: "/caixa"},
		{Key: "chat", Label: i18n.N("Chat"), Icon: "message-square", Milestone: "v0.2", Blurb: i18n.N("Conversa direta com uma persona, fora de uma task.")},
		{Key: "decisoes", Label: i18n.N("Decisões"), Icon: "circle-check", Path: "/decisoes"},
	}},
	{Label: i18n.N("Trabalho"), Items: []NavItem{
		{Key: "tasks", Label: i18n.N("Tasks"), Icon: "list", Path: "/tasks"},
		{Key: "projetos", Label: i18n.N("Projetos"), Icon: "layers", Path: "/projetos"},
		{Key: "fluxos", Label: i18n.N("Fluxos"), Icon: "workflow", Path: "/fluxos"},
		{Key: "metas", Label: i18n.N("Metas"), Icon: "trending-up", Path: "/metas"},
		{Key: "artefatos", Label: i18n.N("Artefatos"), Icon: "file", Path: "/artefatos"},
		{Key: "workspaces", Label: i18n.N("Workspaces"), Icon: "git-branch", Path: "/workspaces"},
	}},
	{Label: i18n.N("Organização"), Items: []NavItem{
		{Key: "personas", Label: i18n.N("Personas"), Icon: "bot", Path: "/personas"},
		{Key: "organograma", Label: i18n.N("Organograma"), Icon: "network", Path: "/organograma"},
		{Key: "skills", Label: i18n.N("Skills"), Icon: "sparkles", Milestone: "v0.2", Blurb: i18n.N("Habilidades reutilizáveis habilitadas por persona.")},
		{Key: "integracoes", Label: i18n.N("Integrações"), Icon: "link", Milestone: "v0.2", Blurb: i18n.N("Serviços externos conectados à empresa.")},
		{Key: "gates", Label: i18n.N("Gates"), Icon: "shield", Path: "/gates"},
		{Key: "configuracoes", Label: i18n.N("Configurações"), Icon: "settings", Path: "/configuracoes"},
	}},
	{Label: i18n.N("Supervisão"), Items: []NavItem{
		{Key: "execucoes", Label: i18n.N("Execuções"), Icon: "activity", Path: "/execucoes"},
		{Key: "custos", Label: i18n.N("Custos"), Icon: "database", Path: "/custos"},
		{Key: "atividade", Label: i18n.N("Atividade"), Icon: "clock", Path: "/atividade"},
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
