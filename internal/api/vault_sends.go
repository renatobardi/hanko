package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Operações da fatia 4 do cofre (#59) que ligam o service: criar um envio por humano (com o conteúdo já cifrado
// no cliente), a página pública (o que pedir antes de abrir) e abrir o envio. A chave do link nunca chega aqui: ela
// fica no fragmento da URL (ADR-0069 §2).

// sendBackend é o subconjunto do service dos envios; opcional como vaultBackend.
type sendBackend interface {
	CreateSend(ctx context.Context, actor domain.Actor, companyID domain.ID, in service.CreateSendInput) (service.SendView, error)
	PublicSendInfo(ctx context.Context, publicID string) (service.PublicSendView, error)
	OpenPublicSend(ctx context.Context, publicID, passwordProof string) (service.OpenedSendView, error)
}

func (s *server) sends() (sendBackend, error) {
	b, ok := s.svc.(sendBackend)
	if !ok {
		return nil, problem(http.StatusNotImplemented, "envios indisponíveis neste servidor")
	}
	return b, nil
}

// sendCall faz o que criar um envio precisa antes do service: barra run token e persona, acha o backend e resolve a
// empresa e o ator.
func (s *server) sendCall(ctx context.Context, company string) (sendBackend, scope, error) {
	if err := forbidPersona(ctx); err != nil {
		return nil, scope{}, err
	}
	sb, err := s.sends()
	if err != nil {
		return nil, scope{}, err
	}
	sc, err := s.scope(ctx, company)
	return sb, sc, err
}

// sendOut mostra um envio sem conteúdo nem chave.
func sendOut(v service.SendView) SecretSend {
	x := v.SecretSend
	return SecretSend{ID: idStr(x.ID), PublicID: x.PublicID, Kind: string(x.Kind), SizeBytes: x.SizeBytes, Label: x.Label,
		HasPassword: x.HasPassword, MaxAccesses: x.MaxAccesses, AccessCount: x.AccessCount, ExpiresAt: x.ExpiresAt,
		LastAccessedAt: x.LastAccessedAt, PurgedAt: x.PurgedAt, SourceField: x.SourceField, CreatedBy: actorRef(x.CreatedBy),
		CreatedAt: x.CreatedAt}
}

// registerSendOps registra criar, página pública e abrir, todas ligadas ao service.
func (s *server) registerSendOps(api huma.API) {
	base := "/companies/{company}/sends"
	huma.Register(api, created(op("create-send", http.MethodPost, base, "vault",
		"Cria um envio com conteúdo já cifrado no cliente (exige reautenticação recente)")),
		func(ctx context.Context, in *createSendIn) (*ItemOut[SecretSend], error) {
			sb, sc, err := s.sendCall(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			b := in.Body
			v, err := sb.CreateSend(ctx, sc.Actor, sc.Company.ID, service.CreateSendInput{
				PublicID: b.PublicID, Kind: domain.SendKind(b.Kind), Ciphertext: b.Ciphertext, Label: b.Label,
				KdfSalt: b.KdfSalt, PasswordVerifier: b.PasswordVerifier,
				ExpiresIn: time.Duration(b.ExpiresInSeconds) * time.Second, MaxAccesses: b.MaxAccesses})
			return vaultItem(ctx, s, sendOut(v), err)
		})

	huma.Register(api, op("get-public-send", http.MethodGet, "/s/{public_id}", "public",
		"Página pública: o que o envio pede para abrir (sem conta)"),
		func(ctx context.Context, in *publicSendPath) (*ItemOut[PublicSend], error) {
			sb, err := s.sends()
			if err != nil {
				return nil, err
			}
			v, err := sb.PublicSendInfo(ctx, in.PublicID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[PublicSend]{Body: PublicSend{Kind: string(v.Kind), HasPassword: v.HasPassword, KdfSalt: v.KdfSalt,
				ExpiresAt: v.ExpiresAt, LockedUntil: v.LockedUntil}}, nil
		})

	huma.Register(api, op("open-public-send", http.MethodPost, "/s/{public_id}/open", "public",
		"Página pública: abre o envio, conta o acesso e devolve o conteúdo cifrado (sem conta)"),
		func(ctx context.Context, in *openSendIn) (*ItemOut[OpenedSend], error) {
			sb, err := s.sends()
			if err != nil {
				return nil, err
			}
			v, err := sb.OpenPublicSend(ctx, in.PublicID, in.Body.PasswordProof)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[OpenedSend]{Body: OpenedSend{Kind: string(v.Kind), Ciphertext: v.Ciphertext}}, nil
		})
}
