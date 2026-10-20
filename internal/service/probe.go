package service

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/renatobardi/hanko/internal/adapter"
	"github.com/renatobardi/hanko/internal/domain"
)

// personaProbeTimeout limita um probe inteiro (processo, handshake e rede).
const personaProbeTimeout = 60 * time.Second

// ProbePersona confere se o harness da persona está pronto para rodar (binário, protocolo, autenticação, endpoint e
// modelo, conforme o harness) sem enviar prompt nem gastar modelo (ADR-0056). Admin da empresa: o probe inicia o
// processo do harness com a configuração da persona (segredos resolvidos em memória). Harness sem probe devolve um
// único passo reprovado dizendo isso.
func (s *Service) ProbePersona(ctx context.Context, companyID domain.ID, ref string) (adapter.ProbeResult, error) {
	if err := s.require(ctx, companyID, domain.MemberAdmin); err != nil {
		return adapter.ProbeResult{}, err
	}
	p, err := s.GetPersona(ctx, companyID, ref)
	if err != nil {
		return adapter.ProbeResult{}, err
	}
	ctx, span := otel.Tracer("hanko/readiness").Start(ctx, "hanko.persona.probe",
		trace.WithAttributes(attribute.String("hanko.company_id", companyID.String()), attribute.String("hanko.agent", p.Agent)))
	defer span.End()

	pr, ok := s.Probers[p.Agent]
	if !ok {
		var out adapter.ProbeResult
		out.Add("probe", false, "o harness "+p.Agent+" ainda não tem verificação de prontidão; rode uma task simples para testar")
		return out, nil
	}
	cfg, secrets, err := s.ResolveAdapterConfig(ctx, companyID, p.AdapterConfig)
	if err != nil {
		var out adapter.ProbeResult
		out.Add("segredos", false, err.Error())
		return out, nil
	}
	pctx, cancel := context.WithTimeout(ctx, personaProbeTimeout)
	defer cancel()
	res := pr.Probe(pctx, adapter.Spec{CompanyID: companyID, PersonaID: p.ID, Agent: p.Agent, Model: p.Model, Config: cfg, Secrets: secrets,
		ReadOnly: true})
	if !res.OK {
		span.SetStatus(codes.Error, "probe failed")
	}
	return res, nil
}
