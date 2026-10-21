package domain

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

// Workspace de execução com Git (ADR-0054, revisa o ADR-0025): o repositório do workspace vira um clone espelho
// da empresa e cada task ganha um worktree com branch própria. Aqui só há regra pura (estados, nomes, validações);
// quem fala com o git é internal/gitrepo e quem orquestra é o service.

// MirrorState é o estado do clone espelho de um workspace.
type MirrorState string

const (
	MirrorIdle    MirrorState = "idle"    // configurado, ainda não clonado
	MirrorSyncing MirrorState = "syncing" // clone ou busca em andamento
	MirrorReady   MirrorState = "ready"   // espelho atualizado
	MirrorFailed  MirrorState = "failed"  // a última busca falhou (last_error diz por quê)
)

// Valid diz se o estado é conhecido.
func (m MirrorState) Valid() bool {
	switch m {
	case MirrorIdle, MirrorSyncing, MirrorReady, MirrorFailed:
		return true
	}
	return false
}

// WorktreeState é o estado do worktree de uma task.
type WorktreeState string

const (
	WorktreeProvisioning WorktreeState = "provisioning" // criando (clone/busca + git worktree add)
	WorktreeReady        WorktreeState = "ready"        // pronto para a run
	WorktreeFailed       WorktreeState = "failed"       // a criação falhou; a próxima run tenta de novo
	WorktreeDiverged     WorktreeState = "diverged"     // o disco não bate com o registro (pasta sumiu, branch trocada)
	WorktreeRemoved      WorktreeState = "removed"      // limpo: a pasta saiu do disco, a branch fica no espelho
)

// Valid diz se o estado é conhecido.
func (w WorktreeState) Valid() bool {
	switch w {
	case WorktreeProvisioning, WorktreeReady, WorktreeFailed, WorktreeDiverged, WorktreeRemoved:
		return true
	}
	return false
}

// Live diz se o worktree ainda ocupa disco (ou está a caminho disso): precisa de limpeza no fim da task.
func (w WorktreeState) Live() bool { return w != WorktreeRemoved }

// CanTransition diz se o estado pode ir de w para to. A máquina é pequena de propósito: provisionando termina em
// pronto ou falhou; pronto pode divergir ou ser removido; falhou e divergente voltam a provisionar (nova tentativa)
// ou são removidos; removido volta a provisionar quando a task é retomada.
func (w WorktreeState) CanTransition(to WorktreeState) bool {
	switch w {
	case WorktreeProvisioning:
		return to == WorktreeReady || to == WorktreeFailed || to == WorktreeRemoved
	case WorktreeReady:
		return to == WorktreeDiverged || to == WorktreeRemoved || to == WorktreeProvisioning
	case WorktreeFailed, WorktreeDiverged:
		return to == WorktreeProvisioning || to == WorktreeReady || to == WorktreeRemoved
	case WorktreeRemoved:
		return to == WorktreeProvisioning
	}
	return false
}

// RemoveReason diz por que um worktree saiu do disco.
type RemoveReason string

const (
	RemovedDone      RemoveReason = "done"
	RemovedCancelled RemoveReason = "cancelled"
	RemovedMerged    RemoveReason = "merged"
	RemovedManual    RemoveReason = "manual"
	RemovedWorkspace RemoveReason = "workspace_deleted"
)

// Valid diz se o motivo é conhecido.
func (r RemoveReason) Valid() bool {
	switch r {
	case RemovedDone, RemovedCancelled, RemovedMerged, RemovedManual, RemovedWorkspace:
		return true
	}
	return false
}

// WorkspaceGit é a configuração Git de um workspace e o estado do clone espelho.
type WorkspaceGit struct {
	WorkspaceID ID `json:"workspace_id"`
	CompanyID   ID `json:"company_id"`
	// CredentialRef é a referência `${secret:NOME}` da credencial do repositório; vazia = repositório público.
	// O valor do segredo nunca é guardado nem devolvido.
	CredentialRef string      `json:"credential_ref,omitempty"`
	MirrorState   MirrorState `json:"mirror_state"`
	// BaseBranch é o branch de que os worktrees partem (o padrão do workspace ou o HEAD do remoto).
	BaseBranch  string     `json:"base_branch,omitempty"`
	HeadSHA     string     `json:"head_sha,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
	LastFetchAt *time.Time `json:"last_fetch_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TaskWorktree é o worktree (e a branch) de uma task.
type TaskWorktree struct {
	ID          ID            `json:"id"`
	CompanyID   ID            `json:"company_id"`
	TaskID      ID            `json:"task_id"`
	WorkspaceID ID            `json:"workspace_id"`
	Branch      string        `json:"branch"`
	BaseBranch  string        `json:"base_branch"`
	BaseSHA     string        `json:"base_sha,omitempty"`
	HeadSHA     string        `json:"head_sha,omitempty"` // gravado quando o worktree é removido
	Path        string        `json:"path,omitempty"`
	State       WorktreeState `json:"state"`
	Error       string        `json:"error,omitempty"`
	RemovedWhy  RemoveReason  `json:"removed_reason,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	RemovedAt   *time.Time    `json:"removed_at,omitempty"`
}

// MaxGitOutput é o teto de bytes de diff/arquivo que a UI e a API devolvem de uma vez.
const MaxGitOutput = 512 << 10

// MaxBrowseFile é o maior arquivo que o browser read-only mostra.
const MaxBrowseFile = 256 << 10

var (
	branchTokenRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	shaRe         = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// WorktreeBranch devolve o nome da branch de uma task a partir da chave: HNK-123 -> hnk-123. Chave inválida
// devolve erro (nunca um nome que o git recusaria ou que sairia da pasta).
func WorktreeBranch(taskKey string) (string, error) {
	prefix, n, ok := ParseTaskKey(taskKey)
	if !ok {
		return "", Invalid("task_key", "chave de task inválida")
	}
	b := strings.ToLower(fmt.Sprintf("%s-%d", prefix, n))
	if !branchTokenRe.MatchString(b) {
		return "", Invalid("task_key", "chave de task inválida")
	}
	return b, nil
}

// ValidSHA diz se s é um commit completo em hexadecimal minúsculo.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// CleanRepoPath normaliza um caminho relativo dentro do worktree (browser de arquivos): sem "..", sem raiz, sem
// NUL, sem a pasta .git. "" é a raiz.
func CleanRepoPath(p string) (string, error) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if strings.ContainsRune(p, 0) || len(p) > 500 {
		return "", Invalid("path", "caminho inválido")
	}
	if p == "" || p == "." || p == "/" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") {
		return "", Invalid("path", "use um caminho relativo à raiz do repositório")
	}
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", Invalid("path", "caminho inválido")
	}
	for _, seg := range strings.Split(c, "/") {
		if strings.EqualFold(seg, ".git") {
			return "", Invalid("path", "a pasta .git não é navegável")
		}
	}
	return c, nil
}

// TaskSessionReset é o pedido "Nova sessão" de uma task (#16): o próximo run não retoma a sessão do harness e
// vive só do briefing e da memória.
type TaskSessionReset struct {
	ID        ID        `json:"id"`
	TaskID    ID        `json:"task_id"`
	Reason    string    `json:"reason,omitempty"`
	By        Actor     `json:"by"`
	CreatedAt time.Time `json:"created_at"`
	// ConsumedRunID é o run que aplicou o pedido (nil enquanto pendente).
	ConsumedRunID *ID        `json:"consumed_run_id,omitempty"`
	ConsumedAt    *time.Time `json:"consumed_at,omitempty"`
}

// MaxResetReason limita o motivo digitado.
const MaxResetReason = 500

// SessionUsable diz se a sessão de um run anterior (criado em prevCreated) ainda pode ser retomada, dado o último
// pedido de nova sessão da task (nil = nunca houve): só vale uma sessão iniciada depois do pedido.
func SessionUsable(prevCreated time.Time, lastReset *time.Time) bool {
	return lastReset == nil || prevCreated.After(*lastReset)
}

// ProjectDefaults são os padrões de um projeto (#34).
type ProjectDefaults struct {
	ProjectID ID `json:"project_id"`
	// Instructions é o briefing do projeto: entra no system prompt depois das instruções da persona.
	Instructions string `json:"instructions,omitempty"`
	// GoalID é a meta padrão das tasks novas do projeto (nil = nenhuma).
	GoalID *ID `json:"goal_id,omitempty"`
	// ImportRepoSkills liga o import automático das skills do repositório clonado do workspace do projeto.
	ImportRepoSkills bool `json:"import_repo_skills"`
	// SkillsPath restringe a busca de skills a um caminho do repositório ("" = o repositório todo).
	SkillsPath string    `json:"skills_path,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// MaxProjectInstructions é o teto do briefing do projeto, em bytes.
const MaxProjectInstructions = 16 << 10

// Validate confere os padrões. O caminho de skills é relativo ao repositório.
func (d ProjectDefaults) Validate() error {
	if len(d.Instructions) > MaxProjectInstructions {
		return Invalid("instructions", fmt.Sprintf("muito longas (máx %d KiB)", MaxProjectInstructions>>10))
	}
	if strings.ContainsRune(d.Instructions, 0) {
		return Invalid("instructions", "caractere inválido")
	}
	if _, err := CleanRepoPath(d.SkillsPath); err != nil {
		return Invalid("skills_path", "caminho inválido")
	}
	return nil
}
