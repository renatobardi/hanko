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
	DeleteHref  string // POST: apagar o campo (campo arquivo se troca apagando e criando de novo)
	FileName    string // campo arquivo: nome e tamanho, nunca o conteúdo
	FileSize    string
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
	// Delegações do usuário a este segredo (só no escopo do usuário); o id do alvo não é segredo e volta num erro.
	Delegations    []DelegationRowVM
	DelegateHref   string // POST: delegar este segredo
	DelegateType   string
	DelegateTarget string
}

// DelegationRowVM é uma delegação na lista: alvo, prazo e estado; nunca o valor do segredo.
type DelegationRowVM struct {
	ID         string
	Target     string // tipo e id do alvo
	ExpiresAt  string
	State      string // ativa, inativa ou revogada
	Active     bool   // só a ativa tem o botão de revogar
	RevokeHref string // POST: revogar
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

// EnvImportRowVM é um segredo da prévia do import de .env: os campos que ele leva (nunca os valores).
type EnvImportRowVM struct {
	Secret string
	Fields string
	Exists bool
}

// EnvImportPlanVM é a prévia do import: o que seria gravado e as linhas ignoradas.
type EnvImportPlanVM struct {
	Collection string
	Rows       []EnvImportRowVM
	Skipped    []string
}

// EnvImportVM é a página de import de .env (ADR-0068 §8.1). Content volta ao formulário do jeito que a pessoa colou;
// Plan só existe depois de "Ver prévia"; NeedPassword pede a senha da conta quando o servidor pediu reautenticação.
type EnvImportVM struct {
	Sections     []TabLink
	Error        string
	BackHref     string
	PostHref     string
	Content      string
	Mode         string
	ItemName     string
	Collection   string
	Overwrite    bool
	Collections  []string
	Plan         *EnvImportPlanVM
	NeedPassword bool
	Version      string
	Firebase     FirebaseWeb // configuração do SDK para a prova pelo Google; vazia fora do provider firebase
}

// VaultFileVM é a página do cofre cifrado (.hkv, ADR-0068 §8.2 e §8.3): exportar e importar. ReauthFor diz qual formulário
// pede a senha da conta ("exportar" ou "importar"). Senha nenhuma volta ao HTML.
type VaultFileVM struct {
	Sections   []TabLink
	Error      string
	BackHref   string
	ExportHref string
	ImportHref string
	ReauthFor  string
	Version    string
	Firebase   FirebaseWeb
}
