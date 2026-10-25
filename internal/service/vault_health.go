package service

import (
	"context"
	"fmt"
	"time"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// GenerateValue gera um valor com o gerador (ADR-0068 §7.1). Não grava nada e não usa a empresa: sem opções, é o
// padrão de sempre (hk_ + 32 bytes); com opções, o gerador configurável.
func (s *Service) GenerateValue(_ context.Context, o domain.GenOptions) (domain.Generated, error) {
	if o.IsZero() {
		v, err := newGeneratedSecret()
		if err != nil {
			return domain.Generated{}, err
		}
		return domain.Generated{Value: v, EntropyBits: domain.GenDefaultBytes * 8}, nil
	}
	return domain.Generate(o)
}

// HealthReport é o relatório de saúde de uma empresa (ADR-0068 §7.2). Nenhum achado leva valor de campo nem HMAC.
type HealthReport struct {
	GeneratedAt time.Time
	Findings    []domain.HealthFinding
}

// VaultHealth monta o relatório de saúde dos segredos da empresa, sem rede. Só admin: o relatório diz quem usa cada item.
// Repetidos saem da comparação de HMAC, sem decifrar; o único campo decifrado é o de senha, em memória, para medir a
// entropia, e o valor não sai do achado.
func (s *Service) VaultHealth(ctx context.Context, companyID domain.ID) (HealthReport, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return HealthReport{}, err
	}
	q := s.read()
	now := s.Now().UTC()

	dupRows, err := q.ListDuplicateSecretFieldHmacs(ctx, companyID)
	if err != nil {
		return HealthReport{}, fmt.Errorf("list duplicate fields: %w", err)
	}
	refs := make([]domain.HMACRef, 0, len(dupRows))
	for _, r := range dupRows {
		refs = append(refs, domain.HMACRef{Secret: r.SecretName, Field: r.FieldName, MAC: r.ValueHmac})
	}

	dueRows, err := q.ListSecretsDueRotation(ctx, now)
	if err != nil {
		return HealthReport{}, fmt.Errorf("list rotation due: %w", err)
	}
	var facts []domain.RotationFact
	for _, r := range dueRows {
		if r.CompanyID != companyID {
			continue
		}
		facts = append(facts, domain.RotationFact{Secret: r.SecretName, Field: r.FieldName, RotatedAt: r.RotatedAt, LimitDays: int(r.LimitDays)})
	}

	fieldRows, err := q.ListHealthSecretFields(ctx, companyID)
	if err != nil {
		return HealthReport{}, fmt.Errorf("list health fields: %w", err)
	}
	var uses []domain.UseFact
	var weak []domain.HealthFinding
	for _, f := range fieldRows {
		uses = append(uses, domain.UseFact{Secret: f.SecretName, Field: f.FieldName, LastUsedAt: f.LastUsedAt})
		password := domain.IsPasswordMark(f.FieldName, f.Password)
		if !password {
			continue
		}
		plain, err := s.secretBox().Open(f.ValueEnc, fieldAADFor(f.AadVersion, f.SecretID, f.ID))
		if err != nil {
			return HealthReport{}, fmt.Errorf("abrir campo %s#%s: %w", f.SecretName, f.FieldName, err)
		}
		if finding, ok := domain.WeakFinding(f.SecretName, f.FieldName, password, string(plain)); ok {
			weak = append(weak, finding)
		}
	}

	secrets, err := q.ListCompanySecrets(ctx, companyID)
	if err != nil {
		return HealthReport{}, fmt.Errorf("list secrets: %w", err)
	}
	names := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		names = append(names, sec.Name)
	}
	usage, err := s.secretUsage(ctx, q, companyID)
	if err != nil {
		return HealthReport{}, err
	}
	used := make(map[string]bool, len(usage))
	for name := range usage {
		used[name] = true
	}

	var findings []domain.HealthFinding
	findings = append(findings, domain.DuplicateFindings(refs)...)
	findings = append(findings, domain.StaleFindings(facts, now)...)
	findings = append(findings, domain.UnusedFindings(uses, now)...)
	findings = append(findings, domain.OrphanFindings(names, used)...)
	findings = append(findings, weak...)
	return HealthReport{GeneratedAt: now, Findings: findings}, nil
}

// SecretMetaInput muda a descrição e o prazo de rotação de um segredo da empresa. Nil mantém o valor atual;
// RotateAfterDays 0 tira o prazo do item (volta a valer o da coleção, se houver).
type SecretMetaInput struct {
	Description     *string
	RotateAfterDays *int
}

// UpdateSecretMeta muda descrição e prazo de rotação do item, sem tocar em valor nem em versão (ADR-0068 §7.3).
// Só admin, como a escrita de segredo da empresa.
func (s *Service) UpdateSecretMeta(ctx context.Context, actor domain.Actor, companyID domain.ID, name string, in SecretMetaInput) (SecretView, error) {
	if _, err := s.secretCaller(ctx, actor, companyID, domain.SecretScopeCompany, true); err != nil {
		return SecretView{}, err
	}
	name, err := s.validateSecretName(name)
	if err != nil {
		return SecretView{}, err
	}
	var out SecretView
	err = s.withTx(ctx, func(t *txc) error {
		cur, err := t.q.GetCompanySecretByNameForUpdate(t.ctx, store.GetCompanySecretByNameForUpdateParams{CompanyID: companyID, Name: name})
		if err != nil {
			if isNoRows(err) {
				return fmt.Errorf("segredo %q: %w", name, domain.ErrNotFound)
			}
			return fmt.Errorf("lock secret: %w", err)
		}
		desc := cur.Description
		if in.Description != nil {
			if desc, err = validateSecretDescription(*in.Description); err != nil {
				return err
			}
		}
		rot := cur.RotateAfterDays
		if in.RotateAfterDays != nil {
			switch d := *in.RotateAfterDays; {
			case d == 0:
				rot = nil
			case d < 1 || d > 3650:
				return domain.Invalid("rotate_after_days", "prazo de rotação: 1 a 3650 dias (0 tira o prazo)")
			default:
				rot = ptr(i32(d))
			}
		}
		n, err := t.q.UpdateSecretMeta(t.ctx, store.UpdateSecretMetaParams{ID: cur.ID, CompanyID: companyID,
			Description: desc, RotateAfterDays: rot, Now: t.now})
		if err != nil {
			return mapErr(err, "update secret meta")
		}
		if n == 0 {
			return fmt.Errorf("segredo %q: %w", name, domain.ErrNotFound)
		}
		row, err := t.q.GetCompanySecretByName(t.ctx, store.GetCompanySecretByNameParams{CompanyID: companyID, Name: name})
		if err != nil {
			return fmt.Errorf("reload secret: %w", err)
		}
		out = SecretView{Secret: secretFromRow(row)}
		return t.activity(actor, companyID, "secret.updated", "secret", row.ID, secretEventData(row), "")
	})
	return out, err
}
