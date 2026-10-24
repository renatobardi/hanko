package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// HealthKind é a categoria de um achado do relatório de saúde (ADR-0068 §7.2).
type HealthKind string

const (
	HealthDuplicate HealthKind = "duplicate"
	HealthStale     HealthKind = "stale"
	HealthUnused    HealthKind = "unused"
	HealthOrphan    HealthKind = "orphan"
	HealthWeak      HealthKind = "weak"
)

// HealthKinds lista as categorias na ordem em que o relatório as mostra.
var HealthKinds = []HealthKind{HealthDuplicate, HealthStale, HealthUnused, HealthOrphan, HealthWeak}

// HealthUnusedAfter é o prazo sem uso que faz um campo entrar em "sem uso" (ADR-0068 §7.2).
const HealthUnusedAfter = 90 * 24 * time.Hour

// HealthFinding é um achado. Nunca leva valor de campo nem o HMAC que o comparou.
type HealthFinding struct {
	Kind    HealthKind
	Secret  string
	Field   string
	Detail  string
	Related []string
}

// HMACRef é um campo com o HMAC do valor, lido sem decifrar. Só serve para agrupar repetidos.
type HMACRef struct {
	Secret string
	Field  string
	MAC    string
}

// DuplicateFindings acha os campos que compartilham o HMAC do valor. Cada um sai com os outros como Related.
func DuplicateFindings(refs []HMACRef) []HealthFinding {
	byMAC := map[string][]string{}
	for _, r := range refs {
		if r.MAC == "" {
			continue
		}
		byMAC[r.MAC] = append(byMAC[r.MAC], r.Secret+"#"+r.Field)
	}
	var out []HealthFinding
	for _, r := range refs {
		group := byMAC[r.MAC]
		if r.MAC == "" || len(group) < 2 {
			continue
		}
		self := r.Secret + "#" + r.Field
		var related []string
		for _, g := range group {
			if g != self {
				related = append(related, g)
			}
		}
		slices.Sort(related)
		out = append(out, HealthFinding{Kind: HealthDuplicate, Secret: r.Secret, Field: r.Field,
			Detail: "mesmo valor de " + strings.Join(related, ", "), Related: related})
	}
	return sortFindings(out)
}

// RotationFact é um campo com a data da última rotação e o prazo que vale para ele (do item ou da coleção).
type RotationFact struct {
	Secret    string
	Field     string
	RotatedAt time.Time
	LimitDays int
}

// StaleFindings acha os campos que passaram do prazo de rotação.
func StaleFindings(facts []RotationFact, now time.Time) []HealthFinding {
	var out []HealthFinding
	for _, f := range facts {
		if f.LimitDays <= 0 {
			continue
		}
		days := int(now.Sub(f.RotatedAt).Hours() / 24)
		if days <= f.LimitDays {
			continue
		}
		out = append(out, HealthFinding{Kind: HealthStale, Secret: f.Secret, Field: f.Field,
			Detail: fmt.Sprintf("sem rotação há %d dias (prazo %d)", days, f.LimitDays)})
	}
	return sortFindings(out)
}

// UseFact é um campo com o instante do último uso no despacho (nil = nunca usado).
type UseFact struct {
	Secret     string
	Field      string
	LastUsedAt *time.Time
}

// UnusedFindings acha os campos sem uso há mais de HealthUnusedAfter, ou que nunca foram usados.
func UnusedFindings(facts []UseFact, now time.Time) []HealthFinding {
	var out []HealthFinding
	for _, f := range facts {
		if f.LastUsedAt != nil && now.Sub(*f.LastUsedAt) <= HealthUnusedAfter {
			continue
		}
		detail := "nunca usado"
		if f.LastUsedAt != nil {
			detail = fmt.Sprintf("sem uso há %d dias", int(now.Sub(*f.LastUsedAt).Hours()/24))
		}
		out = append(out, HealthFinding{Kind: HealthUnused, Secret: f.Secret, Field: f.Field, Detail: detail})
	}
	return sortFindings(out)
}

// OrphanFindings acha os segredos da empresa que nenhum uso referencia (persona, integração, ambiente, plugin,
// canal ou gatilho). used é o conjunto de nomes que alguma referência cita.
func OrphanFindings(secrets []string, used map[string]bool) []HealthFinding {
	var out []HealthFinding
	for _, name := range secrets {
		if used[name] {
			continue
		}
		out = append(out, HealthFinding{Kind: HealthOrphan, Secret: name, Detail: "nenhuma persona, integração, ambiente, plugin, canal ou gatilho usa este segredo"})
	}
	return sortFindings(out)
}

// WeakFinding diz se um campo de senha é fraco (entropia estimada abaixo de WeakEntropyBits). O valor entra só para
// estimar e não sai do achado: quem chama descarta o valor logo depois.
func WeakFinding(secret, field, value string) (HealthFinding, bool) {
	if !IsPasswordField(field) {
		return HealthFinding{}, false
	}
	bits := EstimateEntropy(value)
	if bits >= WeakEntropyBits {
		return HealthFinding{}, false
	}
	return HealthFinding{Kind: HealthWeak, Secret: secret, Field: field,
		Detail: fmt.Sprintf("entropia estimada de %d bits (mínimo %d)", bits, WeakEntropyBits)}, true
}

// sortFindings ordena por segredo e campo, para o relatório sair igual em toda consulta.
func sortFindings(f []HealthFinding) []HealthFinding {
	slices.SortFunc(f, func(a, b HealthFinding) int {
		if c := strings.Compare(a.Secret, b.Secret); c != 0 {
			return c
		}
		return strings.Compare(a.Field, b.Field)
	})
	return f
}
