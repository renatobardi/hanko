package views

// Pacotes de time hanko.team.v1 (ADR-0044) dentro do Catálogo de times.

// PackageCard é um pacote do catálogo embutido.
type PackageCard struct {
	Slug, Name, Desc, Version string
	Personas, Skills, Flows   int
	Agents                    string
	PreviewHref               string
}

// PackageInstallRow é uma instalação já feita.
type PackageInstallRow struct {
	When, Name, Version, Source string
}

// PackagesVM é a seção de pacotes da página Catálogo de times.
type PackagesVM struct {
	Cards       []PackageCard
	UploadHref  string
	ExportHref  string
	ExportOpts  []Option // personas que se pode exportar
	Installs    []PackageInstallRow
	CanAdmin    bool
	Unavailable bool
}

// PackageItemVM é uma linha da prévia: o item do pacote e a ação.
type PackageItemVM struct {
	Kind, Key, Name, Target string
	Action                  Badge
	// Conflict: o item já existe no destino; Select é o nome do campo (r:tipo/chave) e Actions as opções.
	Conflict bool
	Select   string
	Actions  []Option
}

// PackageAgentVM é um harness exigido e o seletor da assinatura.
type PackageAgentVM struct {
	Agent  string
	Field  string // sub:<agent>
	Active string // assinaturas ativas, em texto
	Opts   []Option
}

// PackagePreviewVM é a página de prévia/instalação de um pacote.
type PackagePreviewVM struct {
	Name, Version, Desc, Author string
	Source                      string // "catálogo" ou "arquivo"
	Notice, Error               string
	Items                       []PackageItemVM
	Counts                      []CountVM
	Agents                      []PackageAgentVM
	Secrets                     []PackageSecretVM
	Warnings                    []string
	DefaultOpts                 []Option
	PreviewHref, InstallHref    string
	BackHref                    string
	Installed                   bool
}

// CountVM é uma contagem rotulada.
type CountVM struct {
	Label string
	N     int
}

// PackageSecretVM é um segredo citado pelo pacote.
type PackageSecretVM struct {
	Name   string
	Exists bool
}
