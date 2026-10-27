package domain

import (
	"regexp"
	"slices"
)

// Rodada e aprovação externas (ADR-0080 e 0081). O Hanko só observa: nada aqui marca nem aprova algo no host. Os valores
// são os da migration 00210, em inglês (ADR-0078 item 4).

// ---------- rodada ----------

// RoundStepKind é o tipo de uma etapa da rodada (ADR-0080 item 1).
type RoundStepKind string

const (
	RoundStepTriage       RoundStepKind = "triage"        // triagem da rodada
	RoundStepMerge        RoundStepKind = "merge"         // um por PR; a chave é o número do PR
	RoundStepKaizen       RoundStepKind = "kaizen"        // melhoria do processo
	RoundStepClosing      RoundStepKind = "closing"       // fechamento da rodada
	RoundStepCycleSummary RoundStepKind = "cycle_summary" // resumo do ciclo; pertence ao ciclo, não a uma rodada
)

// RoundStepKinds lista os tipos na ordem em que a API os documenta.
var RoundStepKinds = []RoundStepKind{RoundStepTriage, RoundStepMerge, RoundStepKaizen, RoundStepClosing, RoundStepCycleSummary}

// Valid informa se o tipo é conhecido.
func (k RoundStepKind) Valid() bool { return slices.Contains(RoundStepKinds, k) }

// RoundStepReview é o veredito do revisor de outro modelo sobre uma etapa (ADR-0080 item 3).
type RoundStepReview string

const (
	RoundStepApproved   RoundStepReview = "approved"
	RoundStepRejected   RoundStepReview = "rejected"
	RoundStepNoReviewer RoundStepReview = "no_reviewer"
)

// Valid informa se o veredito é conhecido.
func (r RoundStepReview) Valid() bool {
	return r == RoundStepApproved || r == RoundStepRejected || r == RoundStepNoReviewer
}

// RoundStepReferenceCheck é a conferência das referências do texto da etapa (ADR-0080 item 3).
type RoundStepReferenceCheck string

const (
	RefCheckOK      RoundStepReferenceCheck = "ok"
	RefCheckFailed  RoundStepReferenceCheck = "failed"
	RefCheckMissing RoundStepReferenceCheck = "missing"
)

// Valid informa se a conferência é conhecida.
func (c RoundStepReferenceCheck) Valid() bool {
	return c == RefCheckOK || c == RefCheckFailed || c == RefCheckMissing
}

// ActionMarkState é o estado que o Bardi dá a uma ação da etapa (ADR-0080 item 4).
type ActionMarkState string

const (
	ActionDone    ActionMarkState = "done"
	ActionPending ActionMarkState = "pending"
)

// Valid informa se o estado é conhecido.
func (s ActionMarkState) Valid() bool { return s == ActionDone || s == ActionPending }

// roundStepKeyRe é a chave da etapa `merge`: o número do PR, sem zero à esquerda (até nove dígitos).
var roundStepKeyRe = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

// actionIDRe é o id de uma ação: uma letra e até quinze letras ou dígitos minúsculos (ADR-0080 item 4).
var actionIDRe = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

// roundIssueRe é a issue `<dono>/<repo>#<n>` que agrupa as rodadas de um ciclo (ADR-0080 item 7).
var roundIssueRe = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+#[0-9]{1,9}$`)

// RoundKeyMax é o tamanho máximo do id da rodada no agent-studio (o mesmo da constraint do banco).
const RoundKeyMax = 200

// ValidRoundKey confere o id da rodada: de 1 a RoundKeyMax caracteres.
func ValidRoundKey(key string) bool { return len(key) >= 1 && len(key) <= RoundKeyMax }

// ValidRoundStepKey confere a chave de uma etapa: a `merge` leva o número do PR, as outras não levam chave.
func ValidRoundStepKey(kind RoundStepKind, key string) bool {
	if kind == RoundStepMerge {
		return roundStepKeyRe.MatchString(key)
	}
	return key == ""
}

// ValidActionID confere o id de uma ação (ADR-0080 item 4).
func ValidActionID(id string) bool { return actionIDRe.MatchString(id) }

// ValidCycleIssueRef confere a issue do ciclo, `<dono>/<repo>#<n>`.
func ValidCycleIssueRef(ref string) bool { return len(ref) <= 200 && roundIssueRe.MatchString(ref) }

// ValidRoundName confere o nome amigável da rodada: o gerador único do ADR-0084, com o mesmo formato da conversa.
func ValidRoundName(name string) bool { return ValidExternalConversationName(name) }

// ---------- aprovação ----------

// ApprovalState diz se o pedido do canal ainda espera decisão (ADR-0081 item 1).
type ApprovalState string

const (
	ApprovalPending ApprovalState = "pending"
	ApprovalDecided ApprovalState = "decided"
)

// Valid informa se o estado é conhecido.
func (s ApprovalState) Valid() bool { return s == ApprovalPending || s == ApprovalDecided }

// ApprovalDecision é a decisão no host: recusado ou executado (ADR-0081 item 1).
type ApprovalDecision string

const (
	DecisionRejected ApprovalDecision = "rejected"
	DecisionExecuted ApprovalDecision = "executed"
)

// Valid informa se a decisão é conhecida.
func (d ApprovalDecision) Valid() bool { return d == DecisionRejected || d == DecisionExecuted }

// ApprovalApprover diz quem decidiu no host: a pessoa, ou o modo escuro (ADR-0081 item 1).
type ApprovalApprover string

const (
	ApproverHuman     ApprovalApprover = "human"
	ApproverHumanDark ApprovalApprover = "human_dark"
)

// Valid informa se o aprovador é conhecido.
func (a ApprovalApprover) Valid() bool { return a == ApproverHuman || a == ApproverHumanDark }

// approvalKeyRe é o id do pedido do canal, `oute.canal.id`: a data, a hora e um sufixo (ADR-0081).
var approvalKeyRe = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z0-9-]+$`)

// ValidApprovalKey confere o id do pedido do canal.
func ValidApprovalKey(id string) bool { return len(id) <= 200 && approvalKeyRe.MatchString(id) }

// sha256Re é o resumo de 64 dígitos hexadecimais minúsculos.
var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA256 confere um resumo sha256 no formato do `oute approve`.
func ValidSHA256(s string) bool { return sha256Re.MatchString(s) }

// ---------- alerta de disco do Postgres ----------

// AlertDBDiskLow é o alerta de disco do Postgres (comentário da #66, decisão do Bardi de 2026-10-25). O sujeito é a
// empresa, como os demais alertas (`alerts.company_id` é obrigatório).
const AlertDBDiskLow AlertKind = "db_disk_low"

// Limites de disco por empresa (chaves de `company_alert_settings`, ADR-0083 item 3). Só guardam o que a empresa mudou;
// o padrão vale pelo código. A unidade é GiB, no nome da chave.
const (
	LimitDBDiskWarnGiB     = "limit.db_disk_warn_gib"
	LimitDBDiskCriticalGiB = "limit.db_disk_critical_gib"
)

// Padrões do alerta de disco: os mesmos avisos do `hanko doctor` (ADR-0062: aviso abaixo de 5 GiB livres, falha abaixo
// de 512 MiB). Recomendação do autor, a confirmar com o Bardi antes da fatia do alerta (#126).
const (
	DefaultDBDiskWarnGiB     = 5.0
	DefaultDBDiskCriticalGiB = 0.5
)
