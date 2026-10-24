package domain

import (
	"fmt"
	"regexp"
	"time"
)

// Cofre para agentes (ADR-0068), envio (ADR-0069) e reautenticação recente (ADR-0070). Como em secret.go, nenhum
// destes tipos carrega valor: o valor só existe cifrado no banco e, durante uma run, em memória.

// ---------- campos ----------

// SecretFieldKind é o tipo de um campo do segredo.
type SecretFieldKind string

const (
	SecretFieldText SecretFieldKind = "text" // valor de até MaxSecretFieldTextBytes
	SecretFieldTOTP SecretFieldKind = "totp" // semente; sai só o código atual (ADR-0068 §4.3)
	SecretFieldFile SecretFieldKind = "file" // arquivo cifrado no BlobStore; entra na run como caminho (ADR-0068 §5)
)

// Valid informa se o tipo é conhecido.
func (k SecretFieldKind) Valid() bool {
	return k == SecretFieldText || k == SecretFieldTOTP || k == SecretFieldFile
}

// AlwaysSensitive informa se o tipo é sempre secreto (totp e arquivo nunca são visíveis).
func (k SecretFieldKind) AlwaysSensitive() bool { return k != SecretFieldText }

const (
	// DefaultSecretField é o campo de `${secret:X}` sem `#campo`: o segredo do ADR-0050 vira um item com esse campo.
	DefaultSecretField = "value"
	// SecretFieldNamePattern é o formato do nome de um campo (o mesmo da constraint do banco).
	SecretFieldNamePattern = `[A-Za-z][A-Za-z0-9_-]{0,63}` //nolint:gosec // regex de nome, não credencial
	// MaxSecretFieldTextBytes é o limite de um campo texto ou totp (o mesmo do valor do ADR-0050).
	MaxSecretFieldTextBytes = 8 << 10
	// MaxSecretFieldFileBytes é o limite de um campo arquivo.
	MaxSecretFieldFileBytes = 1 << 20
	// MaxSecretFields é o máximo de campos de um segredo.
	MaxSecretFields = 32
)

var secretFieldNameRe = regexp.MustCompile(`^` + SecretFieldNamePattern + `$`)

// ValidSecretFieldName informa se name serve de nome de campo.
func ValidSecretFieldName(name string) bool { return secretFieldNameRe.MatchString(name) }

// SecretField são os metadados de um campo (nunca o valor).
type SecretField struct {
	ID       ID              `json:"id"`
	SecretID ID              `json:"secret_id"`
	Name     string          `json:"name"`
	Kind     SecretFieldKind `json:"kind"`
	// Sensitive: secreto (só revelar uma vez por versão) ou visível (legível por member). Sempre cifrado.
	Sensitive bool           `json:"sensitive"`
	Provider  SecretProvider `json:"provider"`
	// ExternalRef é o caminho no provider externo; não é o valor.
	ExternalRef string `json:"external_ref,omitempty"`
	// FileName, ContentType e SizeBytes descrevem um campo arquivo.
	FileName    string     `json:"file_name,omitempty"`
	ContentType string     `json:"content_type,omitempty"`
	SizeBytes   int        `json:"size_bytes,omitempty"`
	Position    int        `json:"position"`
	Version     int        `json:"version"`
	RevealedAt  *time.Time `json:"revealed_at,omitempty"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RotatedAt   time.Time  `json:"rotated_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// ValidateSecretField confere as regras de um campo que não dependem do banco.
func ValidateSecretField(name string, kind SecretFieldKind, sensitive bool, provider SecretProvider) error {
	if !ValidSecretFieldName(name) {
		return Invalid("name", "nome de campo inválido (letra inicial; letras, números, _ e -; até 64)")
	}
	if !kind.Valid() {
		return Invalid("kind", fmt.Sprintf("tipo de campo desconhecido: %q", kind))
	}
	if !provider.Valid() {
		return Invalid("provider", fmt.Sprintf("provider desconhecido: %q", provider))
	}
	if kind.AlwaysSensitive() && !sensitive {
		return Invalid("sensitive", fmt.Sprintf("campo %s é sempre secreto", kind))
	}
	if kind == SecretFieldFile && provider != SecretProviderLocal {
		return Invalid("provider", "campo arquivo só existe no provider local")
	}
	return nil
}

// SecretFieldVersion é uma linha do histórico de um campo (ADR-0068 §3), sem valor.
type SecretFieldVersion struct {
	ID         ID             `json:"id"`
	FieldID    ID             `json:"field_id"`
	Version    int            `json:"version"`
	Provider   SecretProvider `json:"provider"`
	RevealedAt *time.Time     `json:"revealed_at,omitempty"`
	ReplacedBy Actor          `json:"replaced_by"`
	CreatedAt  time.Time      `json:"created_at"`
	ReplacedAt time.Time      `json:"replaced_at"`
}

const (
	// DefaultHistoryKeepVersions e DefaultHistoryKeepDays são os padrões da retenção do histórico.
	DefaultHistoryKeepVersions = 5
	DefaultHistoryKeepDays     = 90
)

// PruneHistory devolve os índices das versões de histórico que passaram da retenção. replacedAt vem da mais nova
// para a mais antiga. Fica a versão que está entre as keepVersions mais novas E tem até keepDays dias: vale o limite
// que guardar menos (ADR-0068 §3.2). keepVersions ou keepDays <= 0 caem no padrão.
func PruneHistory(replacedAt []time.Time, now time.Time, keepVersions, keepDays int) []int {
	if keepVersions <= 0 {
		keepVersions = DefaultHistoryKeepVersions
	}
	if keepDays <= 0 {
		keepDays = DefaultHistoryKeepDays
	}
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	var drop []int
	for i, at := range replacedAt {
		if i >= keepVersions || at.Before(cutoff) {
			drop = append(drop, i)
		}
	}
	return drop
}

// ---------- coleções ----------

// CollectionSubject é a quem uma coleção é concedida.
type CollectionSubject string

const (
	CollectionSubjectPersona     CollectionSubject = "persona"      // uma persona
	CollectionSubjectPersonaTree CollectionSubject = "persona_tree" // a persona e toda a cadeia abaixo dela (reports_to)
	CollectionSubjectAllPersonas CollectionSubject = "all_personas" // toda persona da empresa, inclusive as futuras
	CollectionSubjectUser        CollectionSubject = "user"         // um humano
)

// Valid informa se o sujeito é conhecido.
func (s CollectionSubject) Valid() bool {
	switch s {
	case CollectionSubjectPersona, CollectionSubjectPersonaTree, CollectionSubjectAllPersonas, CollectionSubjectUser:
		return true
	}
	return false
}

// CollectionPermission é o que a concessão permite.
type CollectionPermission string

const (
	CollectionUse    CollectionPermission = "use"    // persona resolve itens da coleção nas runs
	CollectionView   CollectionPermission = "view"   // humano vê nomes, campos visíveis e uso
	CollectionManage CollectionPermission = "manage" // humano também cria, rotaciona, revela, restaura e apaga
)

// Valid informa se a permissão é conhecida.
func (p CollectionPermission) Valid() bool {
	return p == CollectionUse || p == CollectionView || p == CollectionManage
}

// ValidateCollectionGrant confere a combinação sujeito × permissão (a mesma das constraints do banco): personas só
// recebem use, humanos só view ou manage, e all_personas é o único sujeito sem id.
func ValidateCollectionGrant(subject CollectionSubject, hasSubjectID bool, perm CollectionPermission) error {
	if !subject.Valid() {
		return Invalid("subject_type", fmt.Sprintf("sujeito desconhecido: %q", subject))
	}
	if !perm.Valid() {
		return Invalid("permission", fmt.Sprintf("permissão desconhecida: %q", perm))
	}
	if (subject == CollectionSubjectAllPersonas) == hasSubjectID {
		if hasSubjectID {
			return Invalid("subject_id", "all_personas não leva id")
		}
		return Invalid("subject_id", "informe a persona ou o usuário")
	}
	if subject == CollectionSubjectUser && perm == CollectionUse {
		return Invalid("permission", "humano recebe view ou manage; use é de persona")
	}
	if subject != CollectionSubjectUser && perm != CollectionUse {
		return Invalid("permission", "persona recebe use")
	}
	return nil
}

// DefaultCollectionName é a coleção criada em cada empresa na migração, concedida a all_personas (ADR-0068 §2.6).
const DefaultCollectionName = "padrão"

// SecretCollection são os metadados de uma coleção.
type SecretCollection struct {
	ID              ID        `json:"id"`
	CompanyID       ID        `json:"company_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	IsDefault       bool      `json:"is_default"`
	RotateAfterDays *int      `json:"rotate_after_days,omitempty"`
	CreatedBy       Actor     `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CollectionGrant é uma concessão de coleção.
type CollectionGrant struct {
	ID           ID                   `json:"id"`
	CollectionID ID                   `json:"collection_id"`
	SubjectType  CollectionSubject    `json:"subject_type"`
	SubjectID    *ID                  `json:"subject_id,omitempty"`
	Permission   CollectionPermission `json:"permission"`
	CreatedBy    Actor                `json:"created_by"`
	CreatedAt    time.Time            `json:"created_at"`
}

// ---------- delegação de segredo pessoal ----------

// DelegationTarget é o trabalho a cujas runs o dono delega um segredo pessoal (ADR-0068 §6).
type DelegationTarget string

const (
	DelegationTask         DelegationTarget = "task"
	DelegationConversation DelegationTarget = "conversation"
	DelegationRun          DelegationTarget = "run"
)

// Valid informa se o alvo é conhecido.
func (t DelegationTarget) Valid() bool {
	return t == DelegationTask || t == DelegationConversation || t == DelegationRun
}

const (
	// DefaultDelegationTTL e MaxDelegationTTL limitam o prazo de uma delegação.
	DefaultDelegationTTL = 24 * time.Hour
	MaxDelegationTTL     = 7 * 24 * time.Hour
)

// DelegationExpiry devolve o vencimento de uma delegação pedida com ttl (0 = padrão) ou o erro se passa do máximo.
func DelegationExpiry(now time.Time, ttl time.Duration) (time.Time, error) {
	switch {
	case ttl == 0:
		ttl = DefaultDelegationTTL
	case ttl < 0:
		return time.Time{}, Invalid("expires_in", "prazo negativo")
	case ttl > MaxDelegationTTL:
		return time.Time{}, Invalid("expires_in", "prazo máximo de uma delegação é 7 dias")
	}
	return now.Add(ttl), nil
}

// SecretDelegation é a concessão de um segredo pessoal às runs de um alvo.
type SecretDelegation struct {
	ID          ID               `json:"id"`
	CompanyID   ID               `json:"company_id"`
	OwnerUserID ID               `json:"owner_user_id"`
	SecretID    ID               `json:"secret_id"`
	TargetType  DelegationTarget `json:"target_type"`
	TargetID    ID               `json:"target_id"`
	ExpiresAt   time.Time        `json:"expires_at"`
	RevokedAt   *time.Time       `json:"revoked_at,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

// Active informa se a delegação vale no instante now.
func (d SecretDelegation) Active(now time.Time) bool {
	return d.RevokedAt == nil && now.Before(d.ExpiresAt)
}

// SecretAccessVia é por onde um valor chegou a quem o usou (registro em secret_access_log).
type SecretAccessVia string

const (
	SecretAccessDispatch SecretAccessVia = "dispatch" // ${secret:} resolvido no despacho
	SecretAccessTool     SecretAccessVia = "tool"     // hanko_secret_get
	SecretAccessFile     SecretAccessVia = "file"     // ${secretfile:}
	SecretAccessSend     SecretAccessVia = "send"     // hanko_send_create
)

// Valid informa se a via é conhecida.
func (v SecretAccessVia) Valid() bool {
	switch v {
	case SecretAccessDispatch, SecretAccessTool, SecretAccessFile, SecretAccessSend:
		return true
	}
	return false
}

// ---------- envio (ADR-0069) ----------

// SendKind é o conteúdo de um envio.
type SendKind string

const (
	SendText SendKind = "text"
	SendFile SendKind = "file"
)

// Valid informa se o tipo é conhecido.
func (k SendKind) Valid() bool { return k == SendText || k == SendFile }

const (
	MaxSendTextBytes   = 64 << 10
	MaxSendFileBytes   = 25 << 20
	DefaultSendTTL     = 7 * 24 * time.Hour
	MaxSendTTL         = 30 * 24 * time.Hour
	MaxSendAccesses    = 1000
	SendLockAfterFails = 5
	SendLockFor        = 15 * time.Minute
)

// ValidateSend confere tipo, prazo e limite de acessos de um envio novo.
func ValidateSend(kind SendKind, ttl time.Duration, maxAccesses *int) error {
	if !kind.Valid() {
		return Invalid("kind", fmt.Sprintf("tipo de envio desconhecido: %q", kind))
	}
	if ttl <= 0 || ttl > MaxSendTTL {
		return Invalid("expires_in", "prazo do envio: de 1 minuto a 30 dias")
	}
	if maxAccesses != nil && (*maxAccesses < 1 || *maxAccesses > MaxSendAccesses) {
		return Invalid("max_accesses", "limite de acessos: de 1 a 1000")
	}
	return nil
}

// SecretSend são os metadados de um envio (nunca chave, senha nem conteúdo).
type SecretSend struct {
	ID             ID         `json:"id"`
	CompanyID      ID         `json:"company_id"`
	PublicID       string     `json:"public_id"`
	Kind           SendKind   `json:"kind"`
	SizeBytes      int        `json:"size_bytes"`
	Label          string     `json:"label,omitempty"`
	HasPassword    bool       `json:"has_password"`
	MaxAccesses    *int       `json:"max_accesses,omitempty"`
	AccessCount    int        `json:"access_count"`
	ExpiresAt      time.Time  `json:"expires_at"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	PurgedAt       *time.Time `json:"purged_at,omitempty"`
	SourceSecretID *ID        `json:"source_secret_id,omitempty"`
	SourceField    string     `json:"source_field,omitempty"`
	RunID          *ID        `json:"run_id,omitempty"`
	CreatedBy      Actor      `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Openable informa se o envio ainda pode ser aberto no instante now.
func (s SecretSend) Openable(now time.Time) bool {
	if s.PurgedAt != nil || !now.Before(s.ExpiresAt) {
		return false
	}
	return s.MaxAccesses == nil || s.AccessCount < *s.MaxAccesses
}

// ---------- reautenticação e passkey (ADR-0070) ----------

// ReauthMethod é a prova usada na reautenticação recente.
type ReauthMethod string

const (
	ReauthPasskey  ReauthMethod = "passkey"
	ReauthPassword ReauthMethod = "password"
	ReauthFirebase ReauthMethod = "firebase"
)

// Valid informa se o método é conhecido.
func (m ReauthMethod) Valid() bool {
	return m == ReauthPasskey || m == ReauthPassword || m == ReauthFirebase
}

const (
	DefaultReauthWindow = 5 * time.Minute
	MaxReauthWindow     = 60 * time.Minute
	MaxPasskeysPerUser  = 10
)

// ReauthFresh informa se a prova feita em at ainda vale em now com a janela dada (<= 0 cai no padrão).
func ReauthFresh(at *time.Time, now time.Time, window time.Duration) bool {
	if at == nil {
		return false
	}
	if window <= 0 {
		window = DefaultReauthWindow
	}
	return !at.After(now.Add(time.Minute)) && now.Sub(*at) <= window
}

// Passkey são os metadados de uma credencial WebAuthn de um usuário (a chave pública não sai pela API).
type Passkey struct {
	ID         ID         `json:"id"`
	UserID     ID         `json:"user_id"`
	Name       string     `json:"name"`
	AAGUID     *ID        `json:"aaguid,omitempty"`
	Transports []string   `json:"transports,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}
