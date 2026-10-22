package views

// Conexões OAuth (ADR-0043): a aba Conexões em Integrações.

// ConnectionRowVM é uma conexão na lista.
type ConnectionRowVM struct {
	Name, Slug, URL string
	Identity        string // rótulo: "Empresa" ou "Pessoal"
	Status          Badge
	Expires         string // validade do token, já formatada
	Setup           string // o passo seguinte, em texto ("Autorizar", "Reconectar"...); vazio = nada
	Error           string
	IntegrationHref string
	GateHref        string
	Connected       bool
	CanOperate      bool
	AuthorizeLabel  string
	AuthorizeHref   string
	ProbeHref       string
	RefreshHref     string
	DisconnectHref  string
	DeleteHref      string
}

// ConnectionsVM é a página Conexões.
type ConnectionsVM struct {
	Notice, Error string
	Rows          []ConnectionRowVM
	CreateHref    string
	IdentityOpts  []Option
	// NoPublicURL: o servidor não tem HANKO_PUBLIC_URL; sem ela o navegador não volta do OAuth.
	NoPublicURL bool
}
