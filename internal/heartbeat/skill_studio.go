package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/riverqueue/river"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Skills (ADR-0035): a run de teste do estúdio e o tick que sincroniza as fontes git e encerra testes parados.

// skillStudioBackend é o que o service oferece ao estúdio e às fontes; opcional para que os fakes dos testes
// que não os usam continuem valendo.
type skillStudioBackend interface {
	SkillTestRunSpec(ctx context.Context, companyID, taskID domain.ID) (service.SkillTestSpec, error)
}

// connectionTickBackend é a renovação periódica dos tokens das conexões OAuth (ADR-0043).
type connectionTickBackend interface {
	RefreshDueConnections(ctx context.Context, now time.Time, limit int) (int, error)
}

type skillTickBackend interface {
	DueSkillSources(ctx context.Context, now time.Time, limit int) ([]service.SkillSourceRef, error)
	SyncDueSkillSource(ctx context.Context, ref service.SkillSourceRef) (service.SkillSyncResult, error)
	ResolveStaleSkillTests(ctx context.Context) (int, error)
}

// skillTestExtras devolve só a skill testada, na versão do teste, e a entrada; nenhum MCP da empresa. Uma falha
// deixa os extras vazios e a montagem da run falha com o motivo.
func (h *Heartbeat) skillTestExtras(ctx context.Context, rc service.RunContext, emit func(domain.RunEventKind, string)) service.RunExtras {
	sb, ok := h.svc.(skillStudioBackend)
	if !ok || rc.Task.ID == (domain.ID{}) {
		return service.RunExtras{}
	}
	spec, err := sb.SkillTestRunSpec(ctx, rc.Company.ID, rc.Task.ID)
	if err != nil {
		emit(domain.EventSystem, "teste de skill: não foi possível carregar a skill: "+err.Error())
		return service.RunExtras{}
	}
	emit(domain.EventSystem, fmt.Sprintf("teste de skill: só a skill %s (versão %d) nesta execução, sem servidores MCP da empresa nem memória",
		spec.Skill.Slug, spec.Skill.Version))
	return service.RunExtras{Skills: []domain.Skill{spec.Skill}, SkillTest: &spec}
}

// BuildSkillTestPrompt monta a run de teste: as instruções da persona, o aviso de que é um teste da skill e a
// entrada como prompt. A resposta final da persona é a saída do teste.
func BuildSkillTestPrompt(rc service.RunContext, spec service.SkillTestSpec) (system, prompt string) {
	var sb strings.Builder
	if s := strings.TrimSpace(rc.Persona.Instructions); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	sb.WriteString("## Teste de skill (estúdio do Hanko)\n\n")
	fmt.Fprintf(&sb, "Esta execução testa a skill `%s` (versão %d). Ela é a única skill disponível agora.\n", spec.Skill.Slug, spec.Skill.Version)
	sb.WriteString("- Trate a mensagem do usuário como o pedido real e responda a ela usando a skill quando ela se aplicar.\n")
	sb.WriteString("- Não crie tasks, não comente em tasks e não altere nada fora deste diretório temporário: é um teste.\n")
	sb.WriteString("- Sua resposta final é a saída do teste; escreva-a completa, em markdown.\n")
	system = strings.TrimSpace(sb.String())
	return system, spec.Input
}

// ---- tick das fontes ----

// SkillTickArgs é a varredura periódica das fontes de skills e dos testes parados (job "skill_tick").
type SkillTickArgs struct{}

func (SkillTickArgs) Kind() string { return "skill_tick" }

func (SkillTickArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{MaxAttempts: 1} }

// SkillTickInterval é o ritmo do tick das fontes.
const SkillTickInterval = 5 * time.Minute

type skillTickWorker struct {
	river.WorkerDefaults[SkillTickArgs]
	h *Heartbeat
}

// Timeout cobre várias sincronizações seguidas (cada clone tem o próprio teto).
func (w *skillTickWorker) Timeout(*river.Job[SkillTickArgs]) time.Duration { return 20 * time.Minute }

func (w *skillTickWorker) Work(ctx context.Context, _ *river.Job[SkillTickArgs]) error {
	return w.h.skillTick(ctx, time.Now().UTC())
}

// skillTick sincroniza as fontes vencidas, uma de cada vez, e falha os testes que nunca chegaram a rodar. Uma
// fonte falhar não impede as outras (a falha fica na fonte, com backoff).
func (h *Heartbeat) skillTick(ctx context.Context, now time.Time) error {
	if h.svc == nil {
		return errNotAttached
	}
	var errs []error
	// conexões OAuth (ADR-0043): renova os tokens que vencem em breve; independe das fontes de skills
	if cb, ok := h.svc.(connectionTickBackend); ok {
		if n, err := cb.RefreshDueConnections(ctx, now, 20); err != nil {
			errs = append(errs, fmt.Errorf("connection refresh: %w", err))
		} else if n > 0 {
			h.log.Info("skill_tick: tokens de conexão renovados", "count", n)
		}
	}
	sb, ok := h.svc.(skillTickBackend)
	if !ok {
		return errors.Join(errs...)
	}
	if n, err := sb.ResolveStaleSkillTests(ctx); err != nil {
		errs = append(errs, fmt.Errorf("stale skill tests: %w", err))
	} else if n > 0 {
		h.log.Info("skill_tick: testes de skill parados encerrados", "count", n)
	}
	due, err := sb.DueSkillSources(ctx, now, 20)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("skill_tick: %w", err))...)
	}
	for _, ref := range due {
		res, err := sb.SyncDueSkillSource(ctx, ref)
		switch {
		case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrNotFound):
		case err != nil:
			h.log.Warn("skill_tick: sincronização falhou", "company_id", ref.CompanyID, "source_id", ref.ID, "err", err)
			errs = append(errs, err)
		case res.Error != "":
			h.log.Warn("skill_tick: fonte com erro", "company_id", ref.CompanyID, "source_id", ref.ID, "err", res.Error)
		default:
			h.log.Info("skill_tick: fonte sincronizada", "company_id", ref.CompanyID, "source_id", ref.ID, "commit", res.Commit,
				"imported", res.Imported, "updated", res.Updated)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
}
