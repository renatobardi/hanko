// Package aidlc classifica a conversa externa na fase do AI-DLC (ADR-0079 itens 2 e 3). A regra é a de phase.py
// (oute-agent@845764f): o primeiro sinal forte decide, nesta ordem: troca manual, abertura, label, skill, papel, ação.
// O Jev (decision model) não entra aqui: quando a ação não é forte, `NeedsJev` avisa que ele seria chamado.
package aidlc

import (
	"regexp"
	"sort"
	"strings"
)

// Classifier é o nome do classificador gravado em external_conversation.classifier.
const Classifier = "aidlc"

// Origens (ADR-0079 item 3) e confianças.
const (
	OriginOpening  = "opening"
	OriginLabel    = "label"
	OriginSkill    = "skill"
	OriginRole     = "role"
	OriginAction   = "action"
	OriginManual   = "manual"
	ConfidenceHigh = "high"
	ConfidenceLow  = "low"
)

// Phases são as 12 fases do AI-DLC (ADR-0007 do oute-agent); DefaultPhase é a mais provável sem sinal.
var Phases = []string{"strat", "intent", "spec", "arch", "design", "plan", "build", "qa", "ship", "ops", "learn", "iter"}

// DefaultPhase é a fase sem sinal (a maior parte do trabalho é construção).
const DefaultPhase = "build"

// strongMin e strongRatio: uma ação só decide com pontos >= strongMin e >= strongRatio vezes os da segunda fase.
const (
	strongMin   = 4
	strongRatio = 2
)

// Signals são os sinais de uma conversa, já lidos da telemetria e da troca manual.
type Signals struct {
	Manual     string
	Opening    string
	Label      string
	Skills     map[string]SkillUse
	Reviewer   bool
	Dispatcher bool
	Actions    map[string]int
}

// SkillUse é o uso de uma skill de fase: quantas vezes e a hora da primeira.
type SkillUse struct {
	Uses  int
	First int64
}

// Result é a classe de uma conversa, a origem do sinal que a decidiu e a confiança. NeedsJev diz que a ação não foi forte.
type Result struct {
	Class      string
	Origin     string
	Confidence string
	NeedsJev   bool
}

// IsPhase informa se o texto é uma das 12 fases.
func IsPhase(s string) bool {
	for _, p := range Phases {
		if p == s {
			return true
		}
	}
	return false
}

// Decide devolve a fase da conversa pelo primeiro sinal forte (ADR-0079 itens 2 e 3).
func Decide(s Signals) Result {
	if IsPhase(s.Manual) {
		return Result{Class: s.Manual, Origin: OriginManual, Confidence: ConfidenceHigh}
	}
	if IsPhase(s.Opening) {
		return Result{Class: s.Opening, Origin: OriginOpening, Confidence: ConfidenceHigh}
	}
	if IsPhase(s.Label) {
		return Result{Class: s.Label, Origin: OriginLabel, Confidence: ConfidenceHigh}
	}
	if len(s.Skills) > 0 {
		return skillResult(s.Skills)
	}
	if s.Reviewer {
		return Result{Class: "qa", Origin: OriginRole, Confidence: ConfidenceHigh}
	}
	if s.Dispatcher {
		return Result{Class: "plan", Origin: OriginRole, Confidence: ConfidenceHigh}
	}
	return actionResult(s.Actions)
}

// skillResult: a fase mais usada; empate de usos vale a skill usada primeiro, com confiança baixa.
func skillResult(skills map[string]SkillUse) Result {
	best, tie := Top(counts(skills))
	if tie {
		var tied []string
		for p, u := range skills {
			if u.Uses == skills[best].Uses {
				tied = append(tied, p)
			}
		}
		sort.Slice(tied, func(a, b int) bool {
			if skills[tied[a]].First != skills[tied[b]].First {
				return skills[tied[a]].First < skills[tied[b]].First
			}
			return tied[a] < tied[b]
		})
		best = tied[0]
	}
	conf := ConfidenceHigh
	if tie {
		conf = ConfidenceLow
	}
	return Result{Class: best, Origin: OriginSkill, Confidence: conf}
}

// actionResult: a ação forte decide; sem ela, a fase mais provável com confiança baixa e o aviso do Jev.
func actionResult(actions map[string]int) Result {
	if len(actions) == 0 {
		return Result{Class: DefaultPhase, Origin: OriginAction, Confidence: ConfidenceLow, NeedsJev: true}
	}
	type scored struct {
		phase string
		pts   int
	}
	ranked := make([]scored, 0, len(actions))
	for p, n := range actions {
		ranked = append(ranked, scored{p, n})
	}
	sort.Slice(ranked, func(a, b int) bool {
		if ranked[a].pts != ranked[b].pts {
			return ranked[a].pts > ranked[b].pts
		}
		return ranked[a].phase < ranked[b].phase
	})
	first := ranked[0]
	second := 0
	if len(ranked) > 1 {
		second = ranked[1].pts
	}
	if first.pts >= strongMin && first.pts >= strongRatio*second {
		return Result{Class: first.phase, Origin: OriginAction, Confidence: ConfidenceHigh}
	}
	return Result{Class: first.phase, Origin: OriginAction, Confidence: ConfidenceLow, NeedsJev: true}
}

// counts converte skills em {fase: usos}.
func counts(skills map[string]SkillUse) map[string]int {
	out := make(map[string]int, len(skills))
	for p, u := range skills {
		out[p] = u.Uses
	}
	return out
}

// Top devolve a menor chave de maior valor e se houve empate (mais de uma chave com esse valor), como _top do phase.py.
func Top(tally map[string]int) (string, bool) {
	best := 0
	first := true
	for _, v := range tally {
		if first || v > best {
			best, first = v, false
		}
	}
	var keys []string
	for k, v := range tally {
		if v == best {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys[0], len(keys) > 1
}

// Action é o que uma ferramenta fez: o nome da ferramenta, o comando (só testado) e o caminho do arquivo.
type Action struct {
	Tool    string
	Command string
	Path    string
}

// actionRule é uma linha da tabela de ações (phase.py ACTION_RULES): id, fase, pontos e a condição.
type actionRule struct {
	id, phase string
	pts       int
	match     func(a Action) bool
}

var (
	reCmd = func(p string) func(Action) bool {
		re := regexp.MustCompile(p)
		return func(a Action) bool { return re.MatchString(a.Command) }
	}
	edits = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}
)

// ActionRules é a tabela de ações, na ordem de phase.py: a primeira que casa vale.
var ActionRules = []actionRule{
	{"abriu-pr", "build", 4, reCmd(`\bgh pr (create|merge)\b`)},
	{"commit", "build", 2, reCmd(`\bgit commit\b`)},
	{"editou-adr", "arch", 3, func(a Action) bool { return edits[a.Tool] && strings.Contains(a.Path, "/docs/adr/") }},
	{"editou", "build", 1, func(a Action) bool { return edits[a.Tool] }},
	{"auditou-pr", "qa", 3, reCmd(`\bgh pr (review|comment|diff|checks)\b|\boute-sonar\b`)},
	{"release-deploy", "ship", 4, reCmd(`scripts/release|\bgh release\b|\boute (up|down|update)\b|\boute-aidlc-ship`)},
	{"telemetria", "ops", 2, reCmd(`/v1/(usage|alerts|tray)|\boute-inbox\b|\boute-quota\b|journalctl|ssh oute-server`)},
	{"criou-issue", "spec", 3, reCmd(`\bgh issue (create|edit)\b`)},
	{"pesquisou", "strat", 1, func(a Action) bool { return a.Tool == "WebSearch" || a.Tool == "WebFetch" }},
}

// ActionPoints devolve a fase e os pontos da primeira regra que casa com a ação; ok=false se nenhuma casa.
func ActionPoints(a Action) (phase string, pts int, ok bool) {
	for _, r := range ActionRules {
		if r.match(a) {
			return r.phase, r.pts, true
		}
	}
	return "", 0, false
}

// SkillPhase devolve a fase de uma skill `oute-aidlc-<fase>-…` (o nome da skill ou o caminho do SKILL.md), ou "".
var skillRe = regexp.MustCompile(`oute-aidlc-(strat|intent|spec|arch|design|plan|build|qa|ship|ops|learn|iter)-`)

func SkillPhase(s string) string {
	if m := skillRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}
