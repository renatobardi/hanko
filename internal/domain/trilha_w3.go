package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Trilha contínua, onda 3 (ADR-0060): perfil próprio, favoritos e recentes da sidebar, itens dispensáveis e onboarding.

// ---------- perfil próprio (#69) ----------

// LocalSubject é o "sujeito" das preferências no modo trusted, onde não há usuário: uma pessoa só usa a instalação.
const LocalSubject = "local"

// ProfileLocales são os idiomas aceitos no perfil ("" = seguir o navegador).
var ProfileLocales = []string{"pt-BR", "en"}

// AvatarTones são as cores (tokens do Kubo DS) que o avatar aceita ("" = a padrão).
var AvatarTones = []string{"accent", "green", "amber", "red", "blue", "violet", "gray"}

// MaxAvatarGlyphRunes é o tamanho máximo do glifo do avatar (uma letra, sigla ou emoji).
const MaxAvatarGlyphRunes = 3

// MaxProfileNameRunes limita o nome de exibição.
const MaxProfileNameRunes = 80

// UserProfile é o perfil editável da própria pessoa. Name vive em users.name; o resto, em user_profiles.
type UserProfile struct {
	UserID      ID     `json:"user_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	AvatarGlyph string `json:"avatar_glyph"`
	AvatarTone  string `json:"avatar_tone"`
	Timezone    string `json:"timezone"`
	Locale      string `json:"locale"`
}

// ProfileInput são os campos que a pessoa pode mudar; nil não altera.
type ProfileInput struct {
	Name        *string
	AvatarGlyph *string
	AvatarTone  *string
	Timezone    *string
	Locale      *string
}

// ValidTimezone informa se o nome é um fuso IANA conhecido ("" vale: segue o da empresa).
func ValidTimezone(tz string) bool {
	if tz == "" {
		return true
	}
	if len(tz) > 64 || strings.ContainsAny(tz, " \t\r\n\\") || strings.Contains(tz, "..") {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// Apply aplica a entrada sobre o perfil atual e devolve o resultado validado (o original não muda).
func (p UserProfile) Apply(in ProfileInput) (UserProfile, error) {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || utf8.RuneCountInString(n) > MaxProfileNameRunes {
			return p, Invalid("name", fmt.Sprintf("use de 1 a %d caracteres", MaxProfileNameRunes))
		}
		p.Name = n
	}
	if in.AvatarGlyph != nil {
		g := strings.TrimSpace(*in.AvatarGlyph)
		if utf8.RuneCountInString(g) > MaxAvatarGlyphRunes || strings.ContainsAny(g, "<>&\"'\n\r\t") {
			return p, Invalid("avatar_glyph", fmt.Sprintf("use até %d caracteres, sem marcação", MaxAvatarGlyphRunes))
		}
		p.AvatarGlyph = g
	}
	if in.AvatarTone != nil {
		t := strings.TrimSpace(*in.AvatarTone)
		if t != "" && !containsStr(AvatarTones, t) {
			return p, Invalid("avatar_tone", fmt.Sprintf("use uma de %v", AvatarTones))
		}
		p.AvatarTone = t
	}
	if in.Timezone != nil {
		tz := strings.TrimSpace(*in.Timezone)
		if !ValidTimezone(tz) {
			return p, Invalid("timezone", "fuso IANA desconhecido (ex.: America/Sao_Paulo)")
		}
		p.Timezone = tz
	}
	if in.Locale != nil {
		l := strings.TrimSpace(*in.Locale)
		if l != "" && !containsStr(ProfileLocales, l) {
			return p, Invalid("locale", "use pt-BR ou en")
		}
		p.Locale = l
	}
	return p, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- favoritos e recentes (#69) ----------

// NavKind distingue favorito (marcado pela pessoa) de recente (registrado ao visitar).
type NavKind string

const (
	NavFavorite NavKind = "favorite"
	NavRecent   NavKind = "recent"
)

// Valid informa se o tipo é conhecido.
func (k NavKind) Valid() bool { return k == NavFavorite || k == NavRecent }

// Limites por pessoa e empresa.
const (
	MaxFavorites = 30
	MaxRecents   = 8
	maxNavPath   = 300
	maxNavTitle  = 200
)

// NavSections são os primeiros segmentos de caminho (relativos à empresa) que podem virar favorito ou recente.
var NavSections = map[string]bool{
	"tasks": true, "projetos": true, "personas": true, "fluxos": true, "pipelines": true, "cases": true, "metas": true,
	"artefatos": true, "workspaces": true, "skills": true, "integracoes": true, "gates": true, "cards-de-status": true,
	"execucoes": true, "custos": true, "timeline": true, "atividade": true, "aprendizados": true, "caixa": true,
	"chat": true, "sala": true, "decisoes": true, "revisao": true, "organograma": true, "equipes": true,
}

// NavItem é uma entrada da sidebar.
type NavItem struct {
	Kind      NavKind   `json:"kind"`
	Path      string    `json:"path"`
	Title     string    `json:"title"`
	TouchedAt time.Time `json:"touched_at"`
}

// NormalizeNavPath valida e limpa um caminho relativo à empresa ("/tasks/HNK-1"): sem query nem fragmento, sem esquema nem
// host, sem "..", só seções conhecidas. Devolve o caminho canônico (sem barra final).
func NormalizeNavPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxNavPath || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, "\\\r\n\t ") {
		return "", Invalid("path", "caminho inválido")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" {
		return "", Invalid("path", "caminho inválido")
	}
	p := strings.TrimRight(u.Path, "/")
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return "", Invalid("path", "caminho inválido")
		}
	}
	if !NavSections[segs[0]] {
		return "", Invalid("path", "esta página não pode ser favoritada")
	}
	return p, nil
}

// IsNavDetail informa se o caminho é de uma página de detalhe (mais de um segmento): só ela vira "recente".
func IsNavDetail(path string) bool { return strings.Count(strings.Trim(path, "/"), "/") >= 1 }

// CleanNavTitle apara e limita o título guardado.
func CleanNavTitle(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	if utf8.RuneCountInString(t) > maxNavTitle {
		r := []rune(t)
		t = string(r[:maxNavTitle-1]) + "…"
	}
	return t
}

// ---------- onboarding (#67) ----------

// OnboardingStepKey identifica um passo do assistente.
type OnboardingStepKey string

// Passos do assistente de primeiros passos.
const (
	StepCompany OnboardingStepKey = "company"
	StepHarness OnboardingStepKey = "harness"
	StepPersona OnboardingStepKey = "persona"
	StepTask    OnboardingStepKey = "task"
)

// OnboardingSteps é a ordem do assistente.
var OnboardingSteps = []OnboardingStepKey{StepCompany, StepHarness, StepPersona, StepTask}

// Valid informa se o passo existe.
func (k OnboardingStepKey) Valid() bool {
	for _, s := range OnboardingSteps {
		if s == k {
			return true
		}
	}
	return false
}

// Skippable informa se o passo pode ser pulado: só o do harness (quem já tem o login do harness na máquina ou usa o
// adapter de teste não precisa do probe); os outros são o que o assistente existe para fazer.
func (k OnboardingStepKey) Skippable() bool { return k == StepHarness }

// OnboardingFacts é o que a empresa já tem: o assistente deriva os passos daqui, então é retomável sem estado próprio.
type OnboardingFacts struct {
	HarnessAgent  string
	HarnessStatus string // "", ready, not_ready
	Personas      int
	HasRun        bool
	FirstTask     bool
	Skipped       []string
	Dismissed     bool
}

// OnboardingStep é um passo com o seu estado.
type OnboardingStep struct {
	Key     OnboardingStepKey `json:"key"`
	Done    bool              `json:"done"`
	Skipped bool              `json:"skipped,omitempty"`
}

// OnboardingStatus é o estado do assistente de uma empresa.
type OnboardingStatus struct {
	Steps []OnboardingStep `json:"steps"`
	// Current é o primeiro passo ainda aberto ("" quando acabou).
	Current   OnboardingStepKey `json:"current"`
	Complete  bool              `json:"complete"`
	Dismissed bool              `json:"dismissed"`
	// HarnessAgent e HarnessStatus repetem o último probe, para a tela e a CLI.
	HarnessAgent  string `json:"harness_agent,omitempty"`
	HarnessStatus string `json:"harness_status,omitempty"`
}

// Done conta os passos resolvidos (pulado conta como resolvido).
func (s OnboardingStatus) Done() int {
	n := 0
	for _, st := range s.Steps {
		if st.Done || st.Skipped {
			n++
		}
	}
	return n
}

// Active informa se o assistente ainda deve ser oferecido (não terminou nem foi dispensado).
func (s OnboardingStatus) Active() bool { return !s.Complete && !s.Dismissed }

// ComputeOnboarding deriva o estado do assistente dos fatos da empresa. O passo "empresa" está sempre feito (o assistente só
// existe dentro de uma empresa); "harness" fecha com um probe pronto ou ao ser pulado; "persona" com a primeira persona
// (contratada ou vinda de um pacote de time); "task" com a primeira task criada pelo assistente ou qualquer run.
func ComputeOnboarding(f OnboardingFacts) OnboardingStatus {
	skipped := map[OnboardingStepKey]bool{}
	for _, s := range f.Skipped {
		if k := OnboardingStepKey(s); k.Skippable() {
			skipped[k] = true
		}
	}
	out := OnboardingStatus{Dismissed: f.Dismissed, HarnessAgent: f.HarnessAgent, HarnessStatus: f.HarnessStatus}
	for _, k := range OnboardingSteps {
		st := OnboardingStep{Key: k}
		switch k {
		case StepCompany:
			st.Done = true
		case StepHarness:
			st.Done = f.HarnessStatus == "ready"
			st.Skipped = !st.Done && skipped[k]
		case StepPersona:
			st.Done = f.Personas > 0
		case StepTask:
			st.Done = f.FirstTask || f.HasRun
		}
		if out.Current == "" && !st.Done && !st.Skipped {
			out.Current = k
		}
		out.Steps = append(out.Steps, st)
	}
	out.Complete = out.Current == ""
	return out
}
