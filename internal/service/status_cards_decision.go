package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/renatobardi/hanko/internal/decision"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Cards de status com o decision model como motor, cron e resultado em artefato (ADR-0048).
//
// O motor decision (ADR-0056) escreve o resumo numa chamada só, sem run de persona: o Hanko monta o retrato das tasks
// observadas e pede o resumo à feature card_summary. A chamada HTTP não cabe numa transação, então a avaliação (dentro da
// transação do card) só abre a rodada e devolve um cardDecisionJob; quem chamou o executa depois do commit
// (runCardDecision) e a escrita do resumo é outra transação, que confere que a geração ainda é a mesma. O resultado
// de qualquer motor também vira uma versão de um artefato (publishCardArtifact) e o card pode aparecer no Painel.

const (
	// cardDecisionTimeout é quanto uma geração pelo decision model pode ficar em andamento (a chamada tem o próprio
	// timeout, no máximo 300 s): passou disso, o processo caiu no meio e a rodada vira falha.
	cardDecisionTimeout = 10 * time.Minute
	// maxDecisionCardContext é o teto do retrato das tasks enviado ao modelo.
	maxDecisionCardContext = 24 << 10
	// maxDecisionCardTokens limita a resposta do modelo (um resumo, não uma classificação).
	maxDecisionCardTokens = 1500
	// maxCardsPerDashboard é quantos cards o Painel mostra.
)

// cardDecisionJob é a geração aberta numa transação e executada depois do commit.
type cardDecisionJob struct {
	companyID, cardID, updateID domain.ID
	since                       time.Time // a marca de geração do card: a escrita só vale se ela ainda for a mesma
	prompt                      decision.Prompt
}

// ---------- cron ----------

// cronSchedule lê a agenda da política (a validação da expressão é feita ao salvar).
func cronSchedule(pol domain.CardPolicy) (interface{ Next(time.Time) time.Time }, bool) {
	sched, err := ParseCron(pol.Cron, pol.CronTZ)
	if err != nil {
		return nil, false
	}
	return sched, true
}

// cronDue diz se o horário do cron chegou desde a última geração (ou desde a criação do card). Nunca gerado = vencido.
func cronDue(pol domain.CardPolicy, last *time.Time, now time.Time) bool {
	if last == nil {
		return true
	}
	sched, ok := cronSchedule(pol)
	if !ok {
		return false
	}
	n := sched.Next(*last)
	return !n.IsZero() && !n.After(now)
}

// nextCronPoll é a próxima verificação de um card em modo cron: o próximo disparo depois da última geração (ou de agora,
// se ele já passou sem gerar, por exemplo por não haver mudança).
func nextCronPoll(pol domain.CardPolicy, last *time.Time, now time.Time) time.Time {
	sched, ok := cronSchedule(pol)
	if !ok {
		return now.Add(cardPollManual)
	}
	base := now
	if last != nil {
		base = *last
	}
	n := sched.Next(base)
	if n.IsZero() {
		return now.Add(cardPollManual)
	}
	if !n.After(now) {
		n = sched.Next(now)
	}
	return n.UTC()
}

// normalizeCardPolicy normaliza a política e confere a expressão cron do modo cron.
func normalizeCardPolicy(p *domain.CardPolicy) error {
	if err := p.Normalize(); err != nil {
		return err
	}
	if p.Mode == domain.CardModeCron {
		if _, err := ParseCron(p.Cron, p.CronTZ); err != nil {
			return domain.Invalid("refresh_policy.cron", err.Error())
		}
	}
	return nil
}

// ---------- geração ----------

// startDecisionGeneration abre a rodada do decision model (sempre do zero) e prepara o job. O card fica "atualizando".
func (t *txc) startDecisionGeneration(row store.StatusCard, trig domain.CardUpdateTrigger, changes []domain.CardChange,
	fp domain.CardFingerprint, m cardMatch) (store.StatusCard, error) {
	c := cardFromRow(row)
	names, err := loadCardNames(t.ctx, t.q, row.CompanyID)
	if err != nil {
		return store.StatusCard{}, err
	}
	stored := changes
	if len(stored) > maxCardChangesStored {
		stored = stored[:maxCardChangesStored]
	}
	if stored == nil {
		stored = []domain.CardChange{}
	}
	u, err := t.q.InsertStatusCardUpdate(t.ctx, store.InsertStatusCardUpdateParams{ID: domain.NewID(), CompanyID: row.CompanyID, CardID: row.ID,
		Kind: string(domain.CardUpdateFull), Trigger: string(trig), Changes: mustJSON(stored), Fingerprint: mustJSON(fp),
		QueryVersion: row.QueryVersion, Now: t.now})
	if err != nil {
		return store.StatusCard{}, mapErr(err, "insert status card update")
	}
	until := t.now.Add(cardDecisionTimeout)
	row.State, row.FailureReason = string(domain.CardActive), ""
	row.GeneratingTaskID, row.GeneratingSince, row.NextEvalAt = nil, &t.now, &until
	if row, err = t.saveCard(row); err != nil {
		return store.StatusCard{}, err
	}
	if err := t.activity(domain.SystemActor, row.CompanyID, "status_card.update_started", "status_card", row.ID, map[string]any{
		"kind": domain.CardUpdateFull, "trigger": trig, "changes": len(changes), "engine": domain.CardEngineDecision}, ""); err != nil {
		return store.StatusCard{}, err
	}
	t.cardJob = &cardDecisionJob{companyID: row.CompanyID, cardID: row.ID, updateID: u.ID, since: t.now,
		prompt: decision.CardSummaryPrompt(row.Interest, decisionCardContext(c, names, changes, m))}
	return row, nil
}

// decisionCardContext é o retrato das tasks observadas que o modelo recebe: filtros, mudanças e as tasks (com o último
// comentário humano). É dado, nunca instrução.
func decisionCardContext(c domain.StatusCard, names cardNames, changes []domain.CardChange, m cardMatch) string {
	var b strings.Builder
	b.WriteString("## Filtros do card\n\n")
	for i, q := range c.Queries {
		fmt.Fprintf(&b, "%d. %s\n", i+1, names.describe(q))
	}
	if len(changes) > 0 {
		fmt.Fprintf(&b, "\n## Mudanças desde a última versão (%d)\n\n", len(changes))
		for i, ch := range changes {
			if i >= 40 {
				fmt.Fprintf(&b, "- … e mais %d mudanças\n", len(changes)-i)
				break
			}
			b.WriteString("- " + describeChange(ch) + "\n")
		}
	}
	total := len(m.tasks)
	fmt.Fprintf(&b, "\n## Tasks observadas (%d)\n\n", total)
	if total == 0 {
		b.WriteString("(nenhuma task casa com os filtros hoje)\n")
	}
	budget := maxDecisionCardContext - b.Len()
	for i, tk := range m.tasks {
		var line strings.Builder
		who := tk.PersonaName
		if who == "" {
			who = "sem responsável"
		}
		fmt.Fprintf(&line, "- %s · %s · %s · %s · %s\n", tk.Key, tk.Status, tk.Priority, who, tk.Title)
		if tk.HumanCommentAt != nil {
			fmt.Fprintf(&line, "  - último comentário humano (%s): %s\n", tk.HumanCommentAt.UTC().Format("2006-01-02"),
				strings.Join(strings.Fields(truncateRunes(tk.HumanComment, maxCardCommentExcerpt)), " "))
		}
		if line.Len() > budget {
			fmt.Fprintf(&b, "- … e mais %d tasks\n", total-i)
			break
		}
		budget -= line.Len()
		b.WriteString(line.String())
	}
	return b.String()
}

// runCardDecision executa a geração: chama o decision model (fora de qualquer transação) e grava o resumo; em qualquer
// falha a rodada fica failed e o card em erro com o motivo (o último resumo bom continua à vista).
func (s *Service) runCardDecision(ctx context.Context, job *cardDecisionJob) error {
	ctx, endSpan := coordSpan(ctx, "service.status_card_decision", attribute.String("card_id", job.cardID.String()))
	defer endSpan()
	var inv domain.DecisionInvocation
	applied := false
	got, err := s.runFeature(ctx, job.companyID, featureRun{feature: domain.FeatureCardSummary, prompt: job.prompt, maxTokens: maxDecisionCardTokens,
		onInvoked: func(i domain.DecisionInvocation) { inv = i },
		handle: func(content string) (func(t *txc) error, error) {
			r, err := decision.ParseCardSummary(content)
			if err != nil {
				return nil, err
			}
			return func(t *txc) error {
				var aerr error
				applied, aerr = t.applyDecisionSummary(job, r, inv)
				return aerr
			}, nil
		}})
	if err != nil {
		countCoord(ctx, "status_card", "decision_error")
		if got.ID != (domain.ID{}) {
			inv = got
		}
		if ferr := s.failDecisionJob(ctx, job, inv, err); ferr != nil {
			return fmt.Errorf("%w (e ao registrar a falha: %v)", err, ferr)
		}
		return err
	}
	countCoord(ctx, "status_card", "decision_ok")
	if applied {
		s.publishCardArtifact(ctx, job.companyID, job.cardID)
	}
	return nil
}

// applyDecisionSummary grava o resumo do decision model (nova revisão, retrato, pendências zeradas), se a geração ainda
// for a que abriu o job. Devolve se gravou.
func (t *txc) applyDecisionSummary(job *cardDecisionJob, r decision.CardSummaryResult, inv domain.DecisionInvocation) (bool, error) {
	card, err := t.q.GetStatusCardForUpdate(t.ctx, store.GetStatusCardForUpdateParams{CompanyID: job.companyID, ID: job.cardID})
	if err != nil {
		return false, mapErr(err, "status card")
	}
	if card.GeneratingSince == nil || !card.GeneratingSince.Equal(job.since) || card.ArchivedAt != nil {
		return false, nil // substituída (arquivada, remontada) ou já encerrada: o ledger guarda a chamada, o resumo não vale
	}
	u, err := t.q.GetStatusCardUpdateForUpdate(t.ctx, job.updateID)
	if err != nil {
		return false, mapErr(err, "status card update")
	}
	if u.Status != string(domain.CardUpdateRunning) {
		return false, nil
	}
	if !card.TitlePinned && strings.TrimSpace(r.Title) != "" {
		if title, err := checkCardTitle(r.Title); err == nil {
			card.Title = title
		}
	}
	mentioned, _, err := t.resolveTaskKeys(job.companyID, r.Mentioned)
	if err != nil {
		return false, err
	}
	rev := card.SummaryRevision + 1
	if _, err := t.q.InsertStatusCardRevision(t.ctx, store.InsertStatusCardRevisionParams{ID: domain.NewID(), CompanyID: job.companyID, CardID: card.ID,
		Revision: rev, Markdown: r.Summary, Kind: string(domain.CardUpdateFull), Model: inv.Model, MentionedTaskIds: mentioned, Now: t.now}); err != nil {
		return false, mapErr(err, "insert status card revision")
	}
	if err := t.q.PruneStatusCardRevisions(t.ctx, store.PruneStatusCardRevisionsParams{CardID: card.ID, Below: rev - domain.MaxCardRevisions}); err != nil {
		return false, fmt.Errorf("prune status card revisions: %w", err)
	}
	cost := inv.CostUSD
	if err := t.q.SetStatusCardUpdateRun(t.ctx, store.SetStatusCardUpdateRunParams{ID: u.ID, InputTokens: inv.InputTokens,
		OutputTokens: inv.OutputTokens, CostUsd: &cost, Model: inv.Model}); err != nil {
		return false, fmt.Errorf("set status card update tokens: %w", err)
	}
	if _, err := t.q.FinishStatusCardUpdate(t.ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateOK), Now: &t.now}); err != nil {
		return false, mapErr(err, "finish status card update")
	}
	kind := string(domain.CardUpdateFull)
	card.Summary, card.SummaryRevision, card.SummaryQueryVersion = r.Summary, rev, u.QueryVersion
	card.IncrementalsSinceFull = 0
	card.LastUpdateKind, card.LastGeneratedAt, card.LastModel = &kind, &t.now, inv.Model
	card.Fingerprint, card.FingerprintAt, card.MentionedTaskIds = u.Fingerprint, &t.now, mentioned
	card.PendingChangeCount, card.PendingChangeHash = 0, ""
	card.State, card.FailureReason = string(domain.CardActive), ""
	card.GeneratingTaskID, card.GeneratingSince = nil, nil
	next := nextCardPoll(policyFromRaw(card.RefreshPolicy), card, t.now)
	card.NextEvalAt = &next
	if card, err = t.saveCard(card); err != nil {
		return false, err
	}
	return true, t.activity(domain.SystemActor, job.companyID, "status_card.summary_updated", "status_card", card.ID, map[string]any{
		"revision": rev, "kind": kind, "engine": domain.CardEngineDecision, "mentioned": len(mentioned), "invocation_id": inv.ID}, "")
}

// failDecisionJob fecha a rodada como falha e põe o card em erro, se a geração ainda for a do job.
func (s *Service) failDecisionJob(ctx context.Context, job *cardDecisionJob, inv domain.DecisionInvocation, cause error) error {
	return s.withTx(ctx, func(t *txc) error {
		card, err := t.q.GetStatusCardForUpdate(t.ctx, store.GetStatusCardForUpdateParams{CompanyID: job.companyID, ID: job.cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if card.GeneratingSince == nil || !card.GeneratingSince.Equal(job.since) {
			return nil
		}
		reason := "A atualização pelo decision model falhou: " + cardClip(cause.Error(), 400) + " Rode de novo para tentar outra vez."
		if err := t.finishDecisionUpdate(job.updateID, inv, reason); err != nil {
			return err
		}
		_, err = t.markDecisionCardFailed(card, reason)
		return err
	})
}

// finishDecisionUpdate fecha a rodada como falha, com os tokens da chamada (quando ela chegou a existir).
func (t *txc) finishDecisionUpdate(updateID domain.ID, inv domain.DecisionInvocation, reason string) error {
	u, err := t.q.GetStatusCardUpdateForUpdate(t.ctx, updateID)
	if err != nil {
		return mapErr(err, "status card update")
	}
	if u.Status != string(domain.CardUpdateRunning) {
		return nil
	}
	if inv.ID != (domain.ID{}) {
		cost := inv.CostUSD
		if err := t.q.SetStatusCardUpdateRun(t.ctx, store.SetStatusCardUpdateRunParams{ID: u.ID, InputTokens: inv.InputTokens,
			OutputTokens: inv.OutputTokens, CostUsd: &cost, Model: inv.Model}); err != nil {
			return fmt.Errorf("set status card update tokens: %w", err)
		}
	}
	if _, err := t.q.FinishStatusCardUpdate(t.ctx, store.FinishStatusCardUpdateParams{ID: u.ID, Status: string(domain.CardUpdateFailed),
		Error: reason, ChangeSummary: u.ChangeSummary, Now: &t.now}); err != nil {
		return mapErr(err, "finish status card update")
	}
	return nil
}

func (t *txc) markDecisionCardFailed(card store.StatusCard, reason string) (store.StatusCard, error) {
	card.State, card.FailureReason, card.NextEvalAt = string(domain.CardError), reason, nil
	card.GeneratingSince = nil
	card, err := t.saveCard(card)
	if err != nil {
		return store.StatusCard{}, err
	}
	return card, t.activity(domain.SystemActor, card.CompanyID, "status_card.failed", "status_card", card.ID,
		map[string]any{"reason": reason, "engine": domain.CardEngineDecision}, "")
}

// failDecisionGeneration encerra uma geração que não terminou (processo reiniciado no meio da chamada).
func (t *txc) failDecisionGeneration(row store.StatusCard, reason string) (store.StatusCard, error) {
	if u, err := t.q.GetRunningDecisionCardUpdateForUpdate(t.ctx, row.ID); err == nil {
		if err := t.finishDecisionUpdate(u.ID, domain.DecisionInvocation{}, reason); err != nil {
			return store.StatusCard{}, err
		}
	} else if !isNoRows(err) {
		return store.StatusCard{}, fmt.Errorf("running decision update: %w", err)
	}
	return t.markDecisionCardFailed(row, reason)
}

// ---------- artefato ----------

// cardArtifactName é o nome estável da cadeia de versões do card: derivado do título na primeira publicação.
func cardArtifactName(title string, id domain.ID) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = id.String()[:8]
	}
	return "card-" + slug + ".md"
}

// publishCardArtifact publica o resumo atual do card como uma versão do artefato dele (uma por atualização; o mesmo
// conteúdo não cria versão). É feito depois do commit do resumo: se falhar, o resumo continua valendo e a próxima
// atualização publica de novo. Nunca derruba quem chamou.
func (s *Service) publishCardArtifact(ctx context.Context, companyID, cardID domain.ID) {
	if err := s.publishCardArtifactErr(ctx, companyID, cardID); err != nil && s.Log != nil {
		s.Log.Warn("card de status: não foi possível publicar o artefato", "card_id", cardID, "err", err.Error())
	}
}

func (s *Service) publishCardArtifactErr(ctx context.Context, companyID, cardID domain.ID) error {
	row, err := s.read().GetStatusCard(ctx, store.GetStatusCardParams{CompanyID: companyID, ID: cardID})
	if err != nil {
		return mapErr(err, "status card")
	}
	if row.SummaryRevision == 0 || strings.TrimSpace(row.Summary) == "" {
		return nil
	}
	name := row.ArtifactName
	if name == "" {
		name = cardArtifactName(row.Title, row.ID)
	}
	title := cardTitleOrDefault(row.Title)
	content := []byte("# " + title + "\n\n" + strings.TrimSpace(row.Summary) + "\n")
	av, err := s.putArtifact(ctx, domain.SystemActor, companyID, PutArtifactInput{Name: name, Mime: "text/markdown", Content: content})
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetStatusCardForUpdate(t.ctx, store.GetStatusCardForUpdateParams{CompanyID: companyID, ID: cardID})
		if err != nil {
			return mapErr(err, "status card")
		}
		if cur.ArtifactName == "" {
			if err := t.q.SetStatusCardDelivery(t.ctx, store.SetStatusCardDeliveryParams{CompanyID: companyID, ID: cardID,
				Engine: cur.Engine, OnDashboard: cur.OnDashboard, ArtifactName: name, Now: t.now}); err != nil {
				return mapErr(err, "set status card delivery")
			}
		}
		if cur.ArtifactID != nil && *cur.ArtifactID == av.ID {
			return nil
		}
		if err := t.q.SetStatusCardArtifact(t.ctx, store.SetStatusCardArtifactParams{CompanyID: companyID, ID: cardID, ArtifactID: &av.ID}); err != nil {
			return mapErr(err, "set status card artifact")
		}
		return t.activity(domain.SystemActor, companyID, "status_card.artifact_published", "status_card", cardID,
			map[string]any{"artifact_id": av.ID, "name": name, "version": av.Version, "revision": row.SummaryRevision}, "")
	})
}

// ---------- Painel ----------

// DashboardCard é um card de status no Painel: o resumo mais recente e o artefato versionado que o guarda.
type DashboardCard struct {
	ID           domain.ID  `json:"id"`
	Title        string     `json:"title"`
	Summary      string     `json:"summary"`
	Revision     int        `json:"revision"`
	Engine       string     `json:"engine"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	NextAt       *time.Time `json:"next_at,omitempty"`
	ArtifactID   *domain.ID `json:"artifact_id,omitempty"`
	ArtifactName string     `json:"artifact_name,omitempty"`
	State        string     `json:"state"`
}

// DashboardStatusCards devolve os cards marcados para o Painel (até 6, os atualizados mais recentemente primeiro). Viewer.
func (s *Service) DashboardStatusCards(ctx context.Context, companyID domain.ID) ([]DashboardCard, error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, err
	}
	rows, err := s.read().ListDashboardStatusCards(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list dashboard status cards: %w", err)
	}
	out := make([]DashboardCard, 0, len(rows))
	for _, r := range rows {
		out = append(out, DashboardCard{ID: r.ID, Title: cardTitleOrDefault(r.Title), Summary: r.Summary, Revision: int(r.SummaryRevision),
			Engine: r.Engine, UpdatedAt: r.LastGeneratedAt, NextAt: r.NextEvalAt, ArtifactID: r.ArtifactID, ArtifactName: r.ArtifactName, State: r.State})
	}
	return out, nil
}
