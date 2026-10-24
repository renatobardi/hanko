package views

// ---- Configurações › Segredos › campos e coleções (ADR-0068, fatia 1) ----

// SecretFieldRowVM é um campo do segredo. VisibleText só é preenchido para campo visível; campo secreto nunca
// carrega valor.
type SecretFieldRowVM struct {
	Name        string
	Reference   string
	Kind        string
	Sensitive   bool
	Version     int
	Revealed    bool
	VisibleText string
	ValueHref   string // POST: trocar o valor
	MetaHref    string // POST: mudar visível/secreto
}

// SecretFieldsVM é a página de um segredo com seus campos.
type SecretFieldsVM struct {
	Sections    []TabLink
	Notice      string
	Error       string
	Name        string
	User        bool
	CanWrite    bool
	BackHref    string
	CreateHref  string
	Fields      []SecretFieldRowVM
	NewName     string // o que voltou num erro; o valor nunca volta
	NewKind     string
	NewVisible  bool
	NewGenerate bool
	Reveal      *SecretRevealVM
}

// CollectionRowVM é uma coleção na lista.
type CollectionRowVM struct {
	Name        string
	Description string
	IsDefault   bool
	Secrets     int
	RotateDays  string
	Href        string
}

// CollectionSecretVM é um segredo dentro de uma coleção.
type CollectionSecretVM struct {
	Name       string
	RemoveHref string
}

// CollectionsVM é a página de coleções (lista e criação).
// SecretsHealthVM é a página de saúde dos segredos (ADR-0068 §7.2).
type SecretsHealthVM struct {
	Sections []TabLink
	Error    string
	BackHref string
	Counts   []HealthCountVM
	Rows     []HealthRowVM
}

// HealthCountVM é a contagem de uma categoria do relatório.
type HealthCountVM struct {
	Label string
	Count int
}

// HealthRowVM é um achado do relatório, sem valor.
type HealthRowVM struct {
	Kind   string
	Secret string
	Field  string
	Detail string
}

type CollectionsVM struct {
	Sections   []TabLink
	Notice     string
	Error      string
	CanAdmin   bool
	BackHref   string
	CreateHref string
	Rows       []CollectionRowVM
	NewName    string
	NewDesc    string
	NewDays    string
}

// CollectionVM é uma coleção com os segredos dela.
type CollectionVM struct {
	Sections   []TabLink
	Notice     string
	Error      string
	CanAdmin   bool
	BackHref   string
	Name       string
	Desc       string
	IsDefault  bool
	RotateDays string
	Secrets    []CollectionSecretVM
	AddHref    string
	DeleteHref string
	GrantCount int
}
