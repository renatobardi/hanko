package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/renatobardi/hanko/internal/aidlc"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// ClassifyExternalConversations classifica as conversas externas de todas as empresas (ADR-0079 itens 1 a 3 e 7). Cada
// passada refaz a classe de cada conversa pelos sinais da telemetria; só o que mudou grava e gera `conversation.classified`.
// Devolve quantas conversas mudaram.
func (s *Service) ClassifyExternalConversations(ctx context.Context) (int, error) {
	cos, err := s.read().ListCompaniesWithExternalConversations(ctx)
	if err != nil {
		return 0, fmt.Errorf("list companies with external conversations: %w", err)
	}
	total := 0
	for _, co := range cos {
		var n int
		err := s.withTx(ctx, func(t *txc) error {
			var err error
			n, err = t.classifyConversations(co)
			return err
		})
		if err != nil {
			return total, fmt.Errorf("classify conversations of company %s: %w", co, err)
		}
		total += n
	}
	return total, nil
}

// sessionFact são os fatos de uma sessão (phase.py `_facts`): a hora do último, o oute.task.id e os papéis da rodada.
type sessionFact struct {
	task                string
	round, worker, step bool
}

// openingFact é a abertura de um oute.task.id: a primeira fase válida, a origem dela, o repositório e o número da issue.
type openingFact struct {
	phase, origin, repo, number string
}

var slugNumber = regexp.MustCompile(`^([0-9]+)-`)

// classInputs são as leituras de uma empresa que a classificação de cada conversa usa.
type classInputs struct {
	facts    map[string]sessionFact
	openings map[string]openingFact
	labels   map[[2]string]map[string]int
	actions  map[string]map[string]int
	skills   map[string]map[string]aidlc.SkillUse
	manual   map[domain.ID]string
}

// classifyConversations lê os sinais de uma empresa e grava a classe das conversas que mudaram.
func (t *txc) classifyConversations(companyID domain.ID) (int, error) {
	convs, err := t.q.ListConversationsToClassify(t.ctx, companyID)
	if err != nil {
		return 0, fmt.Errorf("list conversations to classify: %w", err)
	}
	if len(convs) == 0 {
		return 0, nil
	}
	in, err := t.loadClassInputs(companyID)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, c := range convs {
		res := aidlc.Decide(in.signalsFor(c))
		tags := conversationTags(in.facts[c.SessionID].task, c.OuteRepo)
		if sameClass(c, res, tags) {
			continue
		}
		if err := t.writeClass(companyID, c, res, tags); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// loadClassInputs lê, de uma vez, os sinais de todas as conversas da empresa.
func (t *txc) loadClassInputs(companyID domain.ID) (classInputs, error) {
	var in classInputs
	var err error
	if in.facts, err = t.sessionFacts(companyID); err != nil {
		return in, err
	}
	if in.openings, err = t.openingFacts(companyID); err != nil {
		return in, err
	}
	in.labels = labelVotes(in.openings)
	if in.actions, err = t.actionPoints(companyID); err != nil {
		return in, err
	}
	if in.skills, err = t.skillUses(companyID); err != nil {
		return in, err
	}
	manual, err := t.q.ListManualClasses(t.ctx, companyID)
	if err != nil {
		return in, fmt.Errorf("list manual classes: %w", err)
	}
	in.manual = map[domain.ID]string{}
	for _, m := range manual {
		in.manual[m.ConversationID] = m.Class
	}
	return in, nil
}

// signalsFor monta os sinais de uma conversa a partir das leituras da empresa.
func (in classInputs) signalsFor(c store.ListConversationsToClassifyRow) aidlc.Signals {
	f := in.facts[c.SessionID]
	o := in.openings[f.task]
	return aidlc.Signals{
		Manual:     in.manual[c.ID],
		Opening:    o.phase,
		Label:      labelOf(in.labels, o),
		Skills:     in.skills[c.SessionID],
		Reviewer:   f.step && f.round && !f.worker,
		Dispatcher: f.round && !f.worker && !f.step,
		Actions:    in.actions[c.SessionID],
	}
}

// sameClass informa se a conversa já tem essa classe, origem, confiança, classificador e marcas.
func sameClass(c store.ListConversationsToClassifyRow, res aidlc.Result, tags []string) bool {
	return c.Class == res.Class && c.ClassOrigin == res.Origin && c.ClassConfidence == res.Confidence &&
		c.Classifier == aidlc.Classifier && equalStrings(c.Tags, tags)
}

// writeClass grava a classe da conversa e o evento conversation.classified.
func (t *txc) writeClass(companyID domain.ID, c store.ListConversationsToClassifyRow, res aidlc.Result, tags []string) error {
	if err := t.q.SetConversationClass(t.ctx, store.SetConversationClassParams{ID: c.ID, Class: res.Class, ClassOrigin: res.Origin,
		ClassConfidence: res.Confidence, Classifier: aidlc.Classifier, Tags: tags, Now: t.now}); err != nil {
		return mapErr(err, "set conversation class")
	}
	return t.activity(domain.SystemActor, companyID, "conversation.classified", "external_conversation", c.ID,
		map[string]any{"from": c.Class, "to": res.Class, "origin": res.Origin, "confidence": res.Confidence}, "")
}

// sessionFacts lê os fatos de cada sessão da empresa.
func (t *txc) sessionFacts(companyID domain.ID) (map[string]sessionFact, error) {
	rows, err := t.q.ClassifySessionFacts(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("classify session facts: %w", err)
	}
	out := make(map[string]sessionFact, len(rows))
	for _, r := range rows {
		if r.SessionID == nil {
			continue
		}
		out[*r.SessionID] = sessionFact{task: r.TaskID, round: r.HasRound, worker: r.HasWorker, step: r.HasStep}
	}
	return out, nil
}

// openingFacts lê as aberturas por oute.task.id, na ordem da hora do fato (phase.py `_openings`).
func (t *txc) openingFacts(companyID domain.ID) (map[string]openingFact, error) {
	rows, err := t.q.ClassifyOpenings(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("classify openings: %w", err)
	}
	out := map[string]openingFact{}
	for _, r := range rows {
		if r.OuteTaskID == nil {
			continue
		}
		task := *r.OuteTaskID
		o := out[task]
		if aidlc.IsPhase(r.Phase) && o.phase == "" {
			o.phase, o.origin = r.Phase, r.Origin
		}
		if o.repo == "" {
			o.repo = r.Repo
		}
		if o.number == "" {
			if m := slugNumber.FindStringSubmatch(r.Slug); m != nil {
				o.number = m[1]
			}
		}
		out[task] = o
	}
	return out, nil
}

// labelVotes conta, por (repositório, número da issue), as fases das sessões abertas com origem `label` (phase.py `_labels`).
func labelVotes(openings map[string]openingFact) map[[2]string]map[string]int {
	votes := map[[2]string]map[string]int{}
	for _, o := range openings {
		if o.origin != aidlc.OriginLabel || !aidlc.IsPhase(o.phase) || o.repo == "" || o.number == "" {
			continue
		}
		k := [2]string{o.repo, o.number}
		if votes[k] == nil {
			votes[k] = map[string]int{}
		}
		votes[k][o.phase]++
	}
	return votes
}

// labelOf é a fase de label da abertura, quando o repositório e o número da issue são conhecidos.
func labelOf(votes map[[2]string]map[string]int, o openingFact) string {
	if o.repo == "" || o.number == "" {
		return ""
	}
	tally := votes[[2]string{o.repo, o.number}]
	if len(tally) == 0 {
		return ""
	}
	phase, _ := aidlc.Top(tally)
	return phase
}

// actionPoints soma os pontos das ações de cada sessão, pela tabela de regras (phase.py `_actions`).
func (t *txc) actionPoints(companyID domain.ID) (map[string]map[string]int, error) {
	rows, err := t.q.ClassifyToolActions(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("classify tool actions: %w", err)
	}
	out := map[string]map[string]int{}
	for _, r := range rows {
		if r.SessionID == nil {
			continue
		}
		phase, pts, ok := aidlc.ActionPoints(aidlc.Action{Tool: r.Tool, Command: r.Command, Path: r.Path})
		if !ok {
			continue
		}
		if out[*r.SessionID] == nil {
			out[*r.SessionID] = map[string]int{}
		}
		out[*r.SessionID][phase] += pts * int(r.N)
	}
	return out, nil
}

// skillUses conta os usos das skills de fase por sessão (phase.py `_skills`): a skill do `Skill` ou o caminho lido.
func (t *txc) skillUses(companyID domain.ID) (map[string]map[string]aidlc.SkillUse, error) {
	rows, err := t.q.ClassifySkillRows(t.ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("classify skill rows: %w", err)
	}
	out := map[string]map[string]aidlc.SkillUse{}
	for _, r := range rows {
		if r.SessionID == nil {
			continue
		}
		expr := r.Path
		if r.Tool == "Skill" {
			if s := firstNonEmpty(r.Skill, nestedSkillName(r.ToolParameters)); s != "" {
				expr = s
			}
		}
		phase := aidlc.SkillPhase(expr)
		if phase == "" {
			continue
		}
		if out[*r.SessionID] == nil {
			out[*r.SessionID] = map[string]aidlc.SkillUse{}
		}
		u := out[*r.SessionID][phase]
		u.Uses++
		if u.First == 0 || r.TimeUnixNano < u.First {
			u.First = r.TimeUnixNano
		}
		out[*r.SessionID][phase] = u
	}
	return out, nil
}

// nestedSkillName lê `skill_name` de `tool_parameters`, que chega como texto JSON. Texto que não é JSON não conta.
func nestedSkillName(params string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(params), &m); err != nil {
		return ""
	}
	s, _ := m["skill_name"].(string)
	return s
}

// firstNonEmpty devolve o primeiro texto não vazio.
func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// conversationTags são as marcas da conversa (ADR-0079 item 7): `regression` quando o oute.task.id começa com
// `regression-` ou o repositório é `regression`.
func conversationTags(task, repo string) []string {
	if strings.HasPrefix(task, "regression-") || repo == "regression" {
		return []string{"regression"}
	}
	return []string{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
