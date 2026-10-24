package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
)

// Contrato do cofre para agentes (ADR-0068), do envio (ADR-0069) e da reautenticação com passkey (ADR-0070).
// Cada operação já tem entrada e saída no OpenAPI e responde 501 até a fatia que a implementa ligar o backend; a
// descrição de cada uma diz qual fatia (issues #56 a #62). Nenhuma resposta leva valor de campo secreto, exceto as
// de revelar e de gerar, como no ADR-0050 §6.

// pendingOp registra uma operação do contrato que ainda responde 501.
func pendingOp[I, O any](api huma.API, o huma.Operation, slice string) {
	o.Description = "Contrato do cofre (ADR-0068/0069/0070). Responde 501 até a " + slice + "."
	o.Errors = append(o.Errors, http.StatusNotImplemented)
	huma.Register(api, o, func(context.Context, *I) (*O, error) {
		return nil, problem(http.StatusNotImplemented, "operação ainda não implementada")
	})
}

const (
	sliceFields      = "fatia 1 (#56)"
	sliceReauth      = "fatia 2 (#57)"
	sliceSends       = "fatia 4 (#59)"
	sliceDelegations = "fatia 5 (#60)"
	sliceHealth      = "fatia 6 (#61)"
	slicePortability = "fatia 7 (#62)"
)

// ---------- campos e histórico (fatia 1) ----------

// SecretField são os metadados de um campo do segredo; Value só nas respostas de revelar e de criar/rotacionar com
// valor gerado.
type SecretField struct {
	ID          string     `json:"id" format:"uuid"`
	Name        string     `json:"name" example:"password"`
	Reference   string     `json:"reference" doc:"Como citar o campo." example:"${secret:github-bot#password}"`
	Kind        string     `json:"kind" enum:"text,totp,file"`
	Sensitive   bool       `json:"sensitive" doc:"Secreto (só revelar uma vez por versão) ou visível (legível por member)."`
	Provider    string     `json:"provider" enum:"local,vault" doc:"local: cifrado no Hanko. vault: lido do provider externo a cada uso."`
	ExternalRef string     `json:"external_ref,omitempty" doc:"Provider vault: caminho#campo no provider externo."`
	FileName    string     `json:"file_name,omitempty"`
	ContentType string     `json:"content_type,omitempty"`
	SizeBytes   int        `json:"size_bytes,omitempty"`
	Position    int        `json:"position"`
	Version     int        `json:"version" doc:"Sobe a cada rotação."`
	Revealed    bool       `json:"revealed" doc:"O valor desta versão já foi mostrado."`
	VisibleText string     `json:"visible_text,omitempty" doc:"Só em campo visível: o valor, legível por member."`
	Value       string     `json:"value,omitempty" doc:"SÓ na resposta de revelar e de criar/rotacionar com valor gerado."`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	RotatedAt   time.Time  `json:"rotated_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SecretFieldVersion é uma versão anterior de um campo (sem valor).
type SecretFieldVersion struct {
	Version    int        `json:"version"`
	Provider   string     `json:"provider" enum:"local,vault"`
	Revealed   bool       `json:"revealed"`
	Value      string     `json:"value,omitempty" doc:"SÓ na resposta de revelar a versão."`
	ReplacedBy ActorRef   `json:"replaced_by"`
	CreatedAt  time.Time  `json:"created_at"`
	ReplacedAt time.Time  `json:"replaced_at"`
	RevealedAt *time.Time `json:"revealed_at,omitempty"`
}

// SecretFieldValue é o valor de um campo na criação ou rotação: texto, gerado, arquivo ou referência externa.
type SecretFieldValue struct {
	Provider      string `json:"provider,omitempty" enum:"local,vault" doc:"Padrão local."`
	Value         string `json:"value,omitempty" maxLength:"8192" doc:"Texto ou semente TOTP (base32 ou otpauth://). Cifrado; nunca devolvido."`
	Generate      bool   `json:"generate,omitempty" doc:"O servidor gera o valor (gerador da fatia 6) e o devolve uma vez."`
	ContentBase64 string `json:"content_base64,omitempty" maxLength:"1398104" doc:"Campo arquivo: conteúdo em base64 (até 1 MiB)."`
	FileName      string `json:"file_name,omitempty" maxLength:"255"`
	ContentType   string `json:"content_type,omitempty" maxLength:"255"`
	ExternalRef   string `json:"external_ref,omitempty" maxLength:"500" doc:"Provider vault: caminho#campo."`
}

type SecretFieldPath struct {
	SecretPath
	Field string `path:"field" doc:"Nome do campo." example:"password"`
}

type secretFieldVersionPath struct {
	SecretFieldPath
	Version int `path:"version" minimum:"1"`
}

type createSecretFieldIn struct {
	SecretPath
	Body struct {
		Name      string `json:"name" pattern:"^[A-Za-z][A-Za-z0-9_-]{0,63}$" example:"password"`
		Kind      string `json:"kind,omitempty" enum:"text,totp,file" doc:"Padrão text."`
		Sensitive *bool  `json:"sensitive,omitempty" doc:"Padrão true; totp e file são sempre secretos."`
		Position  int    `json:"position,omitempty"`
		SecretFieldValue
	}
}

type updateSecretFieldIn struct {
	SecretFieldPath
	Body struct {
		Sensitive *bool `json:"sensitive,omitempty"`
		Position  *int  `json:"position,omitempty"`
	}
}

type rotateSecretFieldIn struct {
	SecretFieldPath
	Body SecretFieldValue
}

type listSecretFieldsIn struct {
	SecretPath
	Page
}

// registerSecretFieldsScope registra os campos de um escopo. Criar, listar, mudar e trocar valor ligam o service
// (registerFieldWrites); revelar, apagar e o histórico exigem reautenticação recente e ficam em 501 até a fatia 2.
func (s *server) registerSecretFieldsScope(api huma.API, scope domain.SecretScope, seg, opPrefix, what string) {
	s.registerFieldWrites(api, scope, seg, opPrefix, what)
	base := "/companies/{company}/" + seg + "/{name}/fields"
	tag := "vault"
	pendingOp[SecretFieldPath, ItemOut[SecretField]](api, op("reveal-"+opPrefix+"-field", http.MethodPost, base+"/{field}/reveal", tag,
		"Mostra o valor de um campo UMA vez por versão (exige reautenticação recente)"), sliceReauth)
	pendingOp[SecretFieldPath, struct{}](api, noContent(op("delete-"+opPrefix+"-field", http.MethodDelete, base+"/{field}", tag,
		"Apaga um campo e o seu histórico (exige reautenticação recente)")), sliceReauth)
	pendingOp[SecretFieldPath, ListOut[SecretFieldVersion]](api, op("list-"+opPrefix+"-field-history", http.MethodGet, base+"/{field}/history", tag,
		"Lista as versões anteriores de um campo (exige reautenticação recente)"), sliceReauth)
	pendingOp[secretFieldVersionPath, ItemOut[SecretFieldVersion]](api, op("reveal-"+opPrefix+"-field-version", http.MethodPost,
		base+"/{field}/history/{version}/reveal", tag, "Mostra uma versão anterior UMA vez (exige reautenticação recente)"), sliceReauth)
	pendingOp[secretFieldVersionPath, ItemOut[SecretField]](api, op("restore-"+opPrefix+"-field-version", http.MethodPost,
		base+"/{field}/history/{version}/restore", tag, "Restaura uma versão anterior como versão nova (exige reautenticação recente)"), sliceReauth)
}

// ---------- coleções e concessões (fatia 1) ----------

// SecretCollection é uma coleção de segredos da empresa.
type SecretCollection struct {
	ID              string    `json:"id" format:"uuid"`
	Name            string    `json:"name" example:"infra"`
	Description     string    `json:"description,omitempty"`
	IsDefault       bool      `json:"is_default" doc:"A coleção padrão (concedida a todas as personas na migração)."`
	RotateAfterDays *int      `json:"rotate_after_days,omitempty" doc:"Prazo de rotação dos itens sem prazo próprio (relatório de saúde)."`
	Secrets         int       `json:"secrets" doc:"Quantos segredos a coleção tem."`
	CreatedBy       ActorRef  `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CollectionGrant é uma concessão de coleção.
type CollectionGrant struct {
	ID          string    `json:"id" format:"uuid"`
	SubjectType string    `json:"subject_type" enum:"persona,persona_tree,all_personas,user"`
	SubjectID   string    `json:"subject_id,omitempty" format:"uuid"`
	SubjectName string    `json:"subject_name,omitempty" doc:"Slug da persona ou e-mail do humano."`
	Permission  string    `json:"permission" enum:"use,view,manage" doc:"use: persona resolve nas runs. view/manage: humano."`
	CreatedBy   ActorRef  `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type CollectionPath struct {
	CompanyPath
	Collection string `path:"collection" doc:"Id ou nome da coleção."`
}

type collectionBody struct {
	Name            string `json:"name" minLength:"1" maxLength:"64" example:"infra"`
	Description     string `json:"description,omitempty" maxLength:"300"`
	RotateAfterDays *int   `json:"rotate_after_days,omitempty" minimum:"1" maximum:"3650"`
}

type createCollectionIn struct {
	CompanyPath
	Body collectionBody
}

type updateCollectionIn struct {
	CollectionPath
	Body collectionBody
}

type listCollectionsIn struct {
	CompanyPath
	Page
}

type collectionSecretPath struct {
	CollectionPath
	Name string `path:"name" doc:"Nome do segredo da empresa."`
}

type listCollectionPageIn struct {
	CollectionPath
	Page
}

type createGrantIn struct {
	CollectionPath
	Body struct {
		SubjectType string `json:"subject_type" enum:"persona,persona_tree,all_personas,user"`
		Subject     string `json:"subject,omitempty" doc:"Slug ou id da persona; e-mail ou id do humano. Vazio em all_personas."`
		Permission  string `json:"permission" enum:"use,view,manage"`
	}
}

type grantPath struct {
	CollectionPath
	Grant string `path:"grant" format:"uuid"`
}

// registerCollections registra as coleções: o CRUD e os segredos da coleção ligam o service (registerCollectionOps);
// conceder e revogar exigem reautenticação recente e ficam em 501 até a fatia 2.
func (s *server) registerCollections(api huma.API) {
	s.registerCollectionOps(api)
	base := "/companies/{company}/secret-collections"
	tag := "vault"
	pendingOp[createGrantIn, ItemOut[CollectionGrant]](api, created(op("create-collection-grant", http.MethodPost, base+"/{collection}/grants", tag,
		"Concede a coleção a persona, subárvore, todas as personas ou humano (admin; exige reautenticação recente)")), sliceReauth)
	pendingOp[grantPath, struct{}](api, noContent(op("delete-collection-grant", http.MethodDelete, base+"/{collection}/grants/{grant}", tag,
		"Revoga uma concessão (admin; exige reautenticação recente)")), sliceReauth)
}

// ---------- reautenticação e passkeys (fatia 2) ----------

// Passkey são os metadados de uma passkey do usuário.
type Passkey struct {
	ID         string     `json:"id" format:"uuid"`
	Name       string     `json:"name" example:"Notebook"`
	Transports []string   `json:"transports,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// WebauthnOptions são as opções que o navegador passa a navigator.credentials.create/get.
type WebauthnOptions struct {
	ChallengeID string          `json:"challenge_id" format:"uuid" doc:"Desafio de uso único, válido por 2 minutos."`
	PublicKey   json.RawMessage `json:"public_key" doc:"PublicKeyCredentialCreationOptions ou RequestOptions (JSON do WebAuthn)."`
}

// ReauthOptions diz quais provas o usuário pode usar agora.
type ReauthOptions struct {
	Methods       []string         `json:"methods" doc:"passkey, password (provider local) e/ou firebase."`
	WindowMinutes int              `json:"window_minutes" doc:"Quanto tempo a prova vale."`
	Passkey       *WebauthnOptions `json:"passkey,omitempty" doc:"Presente se o usuário tem passkey."`
}

// ReauthResult é a prova aceita.
type ReauthResult struct {
	Method     string    `json:"method" enum:"passkey,password,firebase"`
	ReauthAt   time.Time `json:"reauth_at"`
	ValidUntil time.Time `json:"valid_until"`
}

type finishPasskeyIn struct {
	Body struct {
		ChallengeID string          `json:"challenge_id" format:"uuid"`
		Name        string          `json:"name" maxLength:"80"`
		Credential  json.RawMessage `json:"credential" doc:"Resposta de navigator.credentials.create (JSON do WebAuthn)."`
	}
}

type passkeyPath struct {
	ID string `path:"id" format:"uuid"`
}

type reauthIn struct {
	Body struct {
		Method      string          `json:"method" enum:"passkey,password,firebase"`
		Password    string          `json:"password,omitempty" doc:"Provider local."`
		IDToken     string          `json:"id_token,omitempty" doc:"Provider firebase: ID token novo, com auth_time recente."`
		ChallengeID string          `json:"challenge_id,omitempty" format:"uuid" doc:"Passkey: o desafio de /me/reauth/options."`
		Credential  json.RawMessage `json:"credential,omitempty" doc:"Passkey: resposta de navigator.credentials.get."`
	}
}

func registerReauth(api huma.API) {
	tag := "auth"
	pendingOp[struct{}, ItemOut[WebauthnOptions]](api, op("begin-passkey-registration", http.MethodPost, "/me/passkeys/registration", tag,
		"Começa o cadastro de uma passkey (exige reautenticação recente)"), sliceReauth)
	pendingOp[finishPasskeyIn, ItemOut[Passkey]](api, created(op("finish-passkey-registration", http.MethodPost, "/me/passkeys", tag,
		"Conclui o cadastro de uma passkey")), sliceReauth)
	pendingOp[struct{}, ListOut[Passkey]](api, op("list-passkeys", http.MethodGet, "/me/passkeys", tag,
		"Lista as passkeys do usuário"), sliceReauth)
	pendingOp[passkeyPath, struct{}](api, noContent(op("delete-passkey", http.MethodDelete, "/me/passkeys/{id}", tag,
		"Remove uma passkey (exige reautenticação recente)")), sliceReauth)
	pendingOp[struct{}, ItemOut[ReauthOptions]](api, op("reauth-options", http.MethodPost, "/me/reauth/options", tag,
		"Provas aceitas para reautenticar agora (e o desafio da passkey)"), sliceReauth)
	pendingOp[reauthIn, ItemOut[ReauthResult]](api, op("reauthenticate", http.MethodPost, "/me/reauth", tag,
		"Prova recente de identidade para as ações sensíveis do cofre (ADR-0070)"), sliceReauth)
}

// ---------- envio (fatia 4) ----------

// SecretSend são os metadados de um envio (nunca chave, senha nem conteúdo).
type SecretSend struct {
	ID             string     `json:"id" format:"uuid"`
	PublicID       string     `json:"public_id"`
	Kind           string     `json:"kind" enum:"text,file"`
	SizeBytes      int        `json:"size_bytes"`
	Label          string     `json:"label,omitempty" doc:"Rótulo visível só para quem criou (não ponha o segredo aqui)."`
	HasPassword    bool       `json:"has_password"`
	MaxAccesses    *int       `json:"max_accesses,omitempty"`
	AccessCount    int        `json:"access_count"`
	ExpiresAt      time.Time  `json:"expires_at"`
	LastAccessedAt *time.Time `json:"last_accessed_at,omitempty"`
	PurgedAt       *time.Time `json:"purged_at,omitempty" doc:"Conteúdo apagado (prazo, limite ou pedido)."`
	SourceSecret   string     `json:"source_secret,omitempty" doc:"Envio criado por persona: o segredo de origem."`
	SourceField    string     `json:"source_field,omitempty"`
	CreatedBy      ActorRef   `json:"created_by"`
	CreatedAt      time.Time  `json:"created_at"`
}

// PublicSend é o que a página pública vê antes de abrir.
type PublicSend struct {
	Kind        string     `json:"kind" enum:"text,file"`
	HasPassword bool       `json:"has_password"`
	KdfSalt     string     `json:"kdf_salt,omitempty" doc:"Sal da senha (PBKDF2-SHA256, 600 000 iterações), em base64url."`
	SizeBytes   int        `json:"size_bytes"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LockedUntil *time.Time `json:"locked_until,omitempty"`
}

// OpenedSend é o conteúdo cifrado entregue a quem abriu.
type OpenedSend struct {
	Kind       string `json:"kind" enum:"text,file"`
	Ciphertext string `json:"ciphertext,omitempty" doc:"Texto: conteúdo cifrado (AES-256-GCM), base64url. Decifrado no navegador com a chave do fragmento."`
	ContentURL string `json:"content_url,omitempty" doc:"Arquivo: URL de uso único, válida por 60 s, do blob cifrado."`
}

type createSendIn struct {
	CompanyPath
	Body struct {
		Kind             string `json:"kind" enum:"text,file"`
		Ciphertext       string `json:"ciphertext,omitempty" maxLength:"90000" doc:"Texto: conteúdo já cifrado no cliente, base64url."`
		SizeBytes        int    `json:"size_bytes" minimum:"0" maximum:"26214400"`
		Label            string `json:"label,omitempty" maxLength:"120"`
		KdfSalt          string `json:"kdf_salt,omitempty" doc:"Com senha: sal usado no cliente."`
		PasswordVerifier string `json:"password_verifier,omitempty" doc:"Com senha: SHA-256 da prova derivada da senha, base64url."`
		ExpiresInSeconds int    `json:"expires_in_seconds" minimum:"60" maximum:"2592000"`
		MaxAccesses      *int   `json:"max_accesses,omitempty" minimum:"1" maximum:"1000"`
	}
}

type sendPath struct {
	CompanyPath
	ID string `path:"id" format:"uuid"`
}

type uploadSendIn struct {
	sendPath
	RawBody []byte `contentType:"application/octet-stream" doc:"Arquivo: o blob já cifrado no cliente (até 25 MiB)."`
}

type listSendsIn struct {
	CompanyPath
	Page
}

type publicSendPath struct {
	PublicID string `path:"public_id" pattern:"^[A-Za-z0-9_-]{22}$"`
}

type openSendIn struct {
	publicSendPath
	Body struct {
		PasswordProof string `json:"password_proof,omitempty" doc:"Com senha: a prova derivada no cliente, base64url."`
	}
}

func registerSends(api huma.API) {
	base := "/companies/{company}/sends"
	tag := "vault"
	pendingOp[createSendIn, ItemOut[SecretSend]](api, created(op("create-send", http.MethodPost, base, tag,
		"Cria um envio com conteúdo já cifrado no cliente (exige reautenticação recente)")), sliceSends)
	pendingOp[uploadSendIn, ItemOut[SecretSend]](api, op("upload-send-content", http.MethodPut, base+"/{id}/content", tag,
		"Envia o blob cifrado de um envio de arquivo"), sliceSends)
	pendingOp[listSendsIn, ListOut[SecretSend]](api, op("list-sends", http.MethodGet, base, tag,
		"Lista os envios da empresa (os próprios; admin vê todos)"), sliceSends)
	pendingOp[sendPath, ItemOut[SecretSend]](api, op("get-send", http.MethodGet, base+"/{id}", tag,
		"Obtém os metadados de um envio"), sliceSends)
	pendingOp[sendPath, struct{}](api, noContent(op("delete-send", http.MethodDelete, base+"/{id}", tag,
		"Apaga o conteúdo de um envio antes do prazo")), sliceSends)
	pendingOp[publicSendPath, ItemOut[PublicSend]](api, op("get-public-send", http.MethodGet, "/s/{public_id}", "public",
		"Página pública: o que o envio pede para abrir (sem conta)"), sliceSends)
	pendingOp[openSendIn, ItemOut[OpenedSend]](api, op("open-public-send", http.MethodPost, "/s/{public_id}/open", "public",
		"Página pública: abre o envio, conta o acesso e devolve o conteúdo cifrado (sem conta)"), sliceSends)
}

// ---------- delegação e uso (fatia 5) ----------

// SecretDelegation é a delegação de um segredo pessoal às runs de um alvo.
type SecretDelegation struct {
	ID         string     `json:"id" format:"uuid"`
	Secret     string     `json:"secret" doc:"Nome do segredo pessoal."`
	TargetType string     `json:"target_type" enum:"task,conversation,run"`
	TargetID   string     `json:"target_id" format:"uuid"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	Active     bool       `json:"active"`
	CreatedAt  time.Time  `json:"created_at"`
}

// SecretAccess é um uso registrado de um segredo (nunca o valor).
type SecretAccess struct {
	Field      string    `json:"field"`
	Via        string    `json:"via" enum:"dispatch,tool,file,send"`
	RunID      string    `json:"run_id,omitempty" format:"uuid"`
	Persona    string    `json:"persona,omitempty" doc:"Slug da persona."`
	Delegated  bool      `json:"delegated" doc:"Uso por delegação de segredo pessoal."`
	AccessedAt time.Time `json:"accessed_at"`
}

type createDelegationIn struct {
	SecretPath
	Body struct {
		TargetType       string `json:"target_type" enum:"task,conversation,run"`
		TargetID         string `json:"target_id" format:"uuid" doc:"Task ou conversa que você pediu, ou run em curso dela."`
		ExpiresInSeconds int    `json:"expires_in_seconds,omitempty" minimum:"0" maximum:"604800" doc:"Padrão 24 h; máximo 7 dias."`
	}
}

type delegationPath struct {
	CompanyPath
	ID string `path:"id" format:"uuid"`
}

type listSecretPageIn struct {
	SecretPath
	Page
}

func registerDelegations(api huma.API) {
	tag := "vault"
	pendingOp[createDelegationIn, ItemOut[SecretDelegation]](api, created(op("create-secret-delegation", http.MethodPost,
		"/companies/{company}/user-secrets/{name}/delegations", tag, "Delega um segredo pessoal às runs de uma task ou conversa sua")), sliceDelegations)
	pendingOp[listSecretsIn, ListOut[SecretDelegation]](api, op("list-my-delegations", http.MethodGet, "/companies/{company}/me/delegations", tag,
		"Lista as suas delegações"), sliceDelegations)
	pendingOp[delegationPath, struct{}](api, noContent(op("revoke-secret-delegation", http.MethodDelete, "/companies/{company}/me/delegations/{id}", tag,
		"Revoga uma delegação")), sliceDelegations)
	pendingOp[listSecretPageIn, ListOut[SecretAccess]](api, op("list-user-secret-usage", http.MethodGet, "/companies/{company}/user-secrets/{name}/usage",
		tag, "Onde e quando um segredo pessoal foi usado (só o dono)"), sliceDelegations)
	pendingOp[listSecretPageIn, ListOut[SecretAccess]](api, op("list-secret-usage", http.MethodGet, "/companies/{company}/secrets/{name}/usage",
		tag, "Onde e quando um segredo da empresa foi usado (admin, view ou manage)"), sliceDelegations)
}

// ---------- saúde e gerador (fatia 6) ----------

// GeneratedValue é um valor do gerador (não é gravado).
type GeneratedValue struct {
	Value       string `json:"value"`
	EntropyBits int    `json:"entropy_bits"`
}

// VaultHealthFinding é um achado do relatório de saúde.
type VaultHealthFinding struct {
	Kind    string   `json:"kind" enum:"duplicate,stale,unused,orphan,weak"`
	Secret  string   `json:"secret"`
	Field   string   `json:"field,omitempty"`
	Detail  string   `json:"detail" doc:"Explicação sem valor (ex.: \"igual a infra/prd#password\", \"sem rotação há 120 dias\")."`
	Related []string `json:"related,omitempty" doc:"Repetido: os outros segredo#campo com o mesmo valor."`
}

// VaultHealth é o relatório de saúde da empresa.
type VaultHealth struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Counts      map[string]int       `json:"counts" doc:"Achados por tipo."`
	Findings    []VaultHealthFinding `json:"findings"`
}

type generateIn struct {
	Body struct {
		Length          int      `json:"length,omitempty" minimum:"0" maximum:"128" doc:"Padrão: 32 bytes aleatórios em base64url."`
		Classes         []string `json:"classes,omitempty" enum:"lower,upper,digits,symbols"`
		PassphraseWords int      `json:"passphrase_words,omitempty" minimum:"0" maximum:"12" doc:"Frase-senha com 3 a 12 palavras."`
	}
}

type updateSecretMetaIn struct {
	SecretPath
	Body struct {
		Description     *string `json:"description,omitempty" maxLength:"300"`
		RotateAfterDays *int    `json:"rotate_after_days,omitempty" minimum:"0" maximum:"3650" doc:"0 tira o prazo."`
	}
}

func registerHealth(api huma.API) {
	tag := "vault"
	pendingOp[generateIn, ItemOut[GeneratedValue]](api, op("generate-secret-value", http.MethodPost, "/secrets/generate", tag,
		"Gera um valor aleatório ou frase-senha (não grava nada)"), sliceHealth)
	pendingOp[CompanyPath, ItemOut[VaultHealth]](api, op("get-vault-health", http.MethodGet, "/companies/{company}/vault/health", tag,
		"Relatório de saúde dos segredos: repetidos, velhos, sem uso, órfãos e fracos"), sliceHealth)
	pendingOp[updateSecretMetaIn, ItemOut[Secret]](api, op("update-secret", http.MethodPatch, "/companies/{company}/secrets/{name}", tag,
		"Muda descrição e prazo de rotação de um segredo da empresa"), sliceHealth)
}

// ---------- import de .env e export cifrado (fatia 7) ----------

// EnvImportPlan é a prévia do import de .env.
type EnvImportPlan struct {
	Items     []EnvImportItem `json:"items"`
	Conflicts []string        `json:"conflicts,omitempty" doc:"Nomes que já existem."`
	Skipped   []string        `json:"skipped,omitempty" doc:"Linhas ignoradas (comentário, vazia, inválida) com o número da linha."`
}

// EnvImportItem é um segredo que o import cria ou atualiza.
type EnvImportItem struct {
	Secret string   `json:"secret"`
	Fields []string `json:"fields"`
	Exists bool     `json:"exists"`
}

// VaultImportResult resume um import.
type VaultImportResult struct {
	Secrets     int `json:"secrets"`
	Fields      int `json:"fields"`
	Collections int `json:"collections"`
	Grants      int `json:"grants"`
	Skipped     int `json:"skipped" doc:"Concessões a persona ou humano que não existe no destino."`
}

// VaultExport é o arquivo cifrado do cofre.
type VaultExport struct {
	FileBase64 string `json:"file_base64" doc:"Arquivo .hkv: cabeçalho, sal, parâmetros e conteúdo cifrado (AES-256-GCM, PBKDF2-SHA256)."`
	SHA256     string `json:"sha256"`
	Secrets    int    `json:"secrets"`
}

type envImportIn struct {
	CompanyPath
	Body struct {
		Content    string `json:"content" maxLength:"262144" doc:"Conteúdo do .env."`
		Mode       string `json:"mode" enum:"single_item,items" doc:"single_item: um segredo com um campo por variável. items: um segredo por variável."`
		ItemName   string `json:"item_name,omitempty" doc:"single_item: nome do segredo."`
		Collection string `json:"collection,omitempty" doc:"Coleção de destino (padrão: a padrão)."`
		Overwrite  bool   `json:"overwrite,omitempty" doc:"Rotaciona o que já existe em vez de pular."`
	}
}

type exportVaultIn struct {
	CompanyPath
	Body struct {
		Password string `json:"password" minLength:"12" doc:"Senha do arquivo; não é guardada."`
	}
}

type importVaultIn struct {
	CompanyPath
	Body struct {
		FileBase64 string `json:"file_base64"`
		Password   string `json:"password"`
	}
}

func registerPortability(api huma.API) {
	tag := "vault"
	pendingOp[envImportIn, ItemOut[EnvImportPlan]](api, op("preview-env-import", http.MethodPost, "/companies/{company}/vault/import-env/preview", tag,
		"Prévia do import de um .env (não grava)"), slicePortability)
	pendingOp[envImportIn, ItemOut[VaultImportResult]](api, op("apply-env-import", http.MethodPost, "/companies/{company}/vault/import-env", tag,
		"Importa um .env para o cofre (admin; exige reautenticação recente)"), slicePortability)
	pendingOp[exportVaultIn, ItemOut[VaultExport]](api, op("export-vault", http.MethodPost, "/companies/{company}/vault/export", tag,
		"Exporta o cofre cifrado com senha (admin; exige reautenticação recente)"), slicePortability)
	pendingOp[importVaultIn, ItemOut[VaultImportResult]](api, op("import-vault", http.MethodPost, "/companies/{company}/vault/import", tag,
		"Importa um cofre exportado por outra instância (admin; exige reautenticação recente; tudo ou nada)"), slicePortability)
}

func (s *server) registerVault(api huma.API) {
	s.registerSecretFieldsScope(api, domain.SecretScopeCompany, "secrets", "secret", "segredo da empresa")
	s.registerSecretFieldsScope(api, domain.SecretScopeUser, "user-secrets", "user-secret", "segredo do usuário")
	s.registerCollections(api)
	registerReauth(api)
	registerSends(api)
	registerDelegations(api)
	registerHealth(api)
	registerPortability(api)
}
