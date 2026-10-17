package views

// ---- Configurações › Segredos (ADR-0050) ----

// SecretRowVM é uma linha da lista de segredos. Nenhum campo carrega o valor.
type SecretRowVM struct {
	Name        string
	Reference   string // ${secret:NOME}
	Description string
	Provider    string // local | vault
	ExternalRef string
	Version     int
	Revealed    bool
	LastUsed    string
	UsedBy      []string
	RotateHref  string
	RevealHref  string
	DeleteHref  string
}

// SecretFormVM é o formulário de criação (e o que volta num erro, sem o valor).
type SecretFormVM struct {
	Action      string
	Name        string
	Description string
	Provider    string
	ExternalRef string
	Generate    bool
	User        bool
	FieldErr    map[string]string
}

// SecretRevealVM é o valor mostrado uma única vez (criação ou rotação gerada, ou "revelar").
type SecretRevealVM struct {
	Name      string
	Reference string
	Value     string
}

// SecretsVM é a página Configurações › Segredos.
type SecretsVM struct {
	Sections []TabLink
	Notice   string
	Error    string
	KeySet   bool // o servidor tem HANKO_SECRET_KEY
	VaultOn  bool // há cofre externo configurado
	// UserOK: há uma pessoa autenticada (segredos de usuário exigem identidade).
	UserOK  bool
	Company []SecretRowVM
	User    []SecretRowVM
	Form    SecretFormVM
	UForm   SecretFormVM
	Reveal  *SecretRevealVM
}

// ---- Configurações › Instância (ADR-0050 §C, ADR-0051) ----

// SettingRowVM é uma chave de instance_settings.
type SettingRowVM struct {
	Key       string
	Label     string
	Help      string
	Kind      string // bool | int | float | duration | string_list
	Value     string // valor efetivo para exibir
	Input     string // valor para o campo de edição
	BoolOn    bool
	Source    string // env | db | default
	SourceTxt string
	Env       string
	Locked    bool
	Stored    bool // há valor no banco
	Action    string
	ClearHref string
}

// SettingGroupVM agrupa as chaves por área.
type SettingGroupVM struct {
	Title string
	Rows  []SettingRowVM
}

// BlobUsageVM é o que há em um driver de conteúdo de artefatos.
type BlobUsageVM struct {
	Driver   string
	Versions int64
	Size     string
	Active   bool
}

// SettingAuditRowVM é uma mudança de configuração.
type SettingAuditRowVM struct {
	When, Key, Old, New, Actor string
}

// InstanceVM é a página Configurações › Instância.
type InstanceVM struct {
	Sections  []TabLink
	Notice    string
	Error     string
	Forbidden bool // só o dono da instalação
	Groups    []SettingGroupVM
	Blobs     []BlobUsageVM
	BlobNote  string
	Audit     []SettingAuditRowVM
}
