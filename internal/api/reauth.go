package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// ---------- reautenticação recente e passkey (fatia 2, ADR-0070) ----------

// registerReauth liga as rotas de /me: provas, passkeys e o desafio de cada uma. As rotas de senha e Firebase usam o
// mesmo Guard das demais; quem não tem sessão recebe 403 (só sessão prova).
func (s *server) registerReauth(api huma.API) {
	tag := "auth"
	huma.Register(api, op("reauth-options", http.MethodPost, "/me/reauth/options", tag,
		"Provas aceitas para reautenticar agora (e o desafio da passkey)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[ReauthOptions], error) {
			id, _ := auth.IdentityFrom(ctx)
			o, err := s.svc.ReauthOptionsFor(ctx, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := ReauthOptions{Methods: o.Methods, WindowMinutes: int(o.Window / time.Minute)}
			if o.Passkey != nil {
				out.Passkey = &WebauthnOptions{ChallengeID: o.Passkey.ID.String(), PublicKey: o.Passkey.Options}
			}
			return &ItemOut[ReauthOptions]{Body: out}, nil
		})

	huma.Register(api, op("reauthenticate", http.MethodPost, "/me/reauth", tag,
		"Prova recente de identidade para as ações sensíveis do cofre (ADR-0070)"),
		func(ctx context.Context, in *reauthIn) (*ItemOut[ReauthResult], error) {
			id, _ := auth.IdentityFrom(ctx)
			var proof service.ReauthProof
			var err error
			switch in.Body.Method {
			case "passkey":
				cid, perr := domain.ParseID(in.Body.ChallengeID)
				if perr != nil {
					return nil, s.fail(ctx, domain.Invalid("challenge_id", "id inválido"))
				}
				proof, err = s.svc.FinishPasskeyReauth(ctx, id, cid, in.Body.Credential)
			case "password":
				proof, err = s.svc.ReauthWithPassword(ctx, id, in.Body.Password)
			case "firebase":
				proof, err = s.svc.ReauthWithFirebase(ctx, id, in.Body.IDToken)
			default:
				return nil, s.fail(ctx, domain.Invalid("method", "prova desconhecida"))
			}
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[ReauthResult]{Body: ReauthResult{Method: proof.Method, ReauthAt: proof.At, ValidUntil: proof.ValidUntil}}, nil
		})

	huma.Register(api, op("begin-passkey-registration", http.MethodPost, "/me/passkeys/registration", tag,
		"Começa o cadastro de uma passkey (exige reautenticação recente)"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[WebauthnOptions], error) {
			id, err := s.passkeyCaller(ctx)
			if err != nil {
				return nil, err
			}
			ch, err := s.svc.BeginPasskeyRegistration(ctx, id)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[WebauthnOptions]{Body: WebauthnOptions{ChallengeID: ch.ID.String(), PublicKey: ch.Options}}, nil
		})

	huma.Register(api, created(op("finish-passkey-registration", http.MethodPost, "/me/passkeys", tag,
		"Conclui o cadastro de uma passkey")),
		func(ctx context.Context, in *finishPasskeyIn) (*ItemOut[Passkey], error) {
			id, err := s.passkeyCaller(ctx)
			if err != nil {
				return nil, err
			}
			cid, perr := domain.ParseID(in.Body.ChallengeID)
			if perr != nil {
				return nil, s.fail(ctx, domain.Invalid("challenge_id", "id inválido"))
			}
			p, err := s.svc.FinishPasskeyRegistration(ctx, id, cid, in.Body.Name, in.Body.Credential)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[Passkey]{Body: passkeyOut(p)}, nil
		})

	huma.Register(api, op("list-passkeys", http.MethodGet, "/me/passkeys", tag,
		"Lista as passkeys do usuário"),
		func(ctx context.Context, _ *struct{}) (*ListOut[Passkey], error) {
			id, err := s.passkeyCaller(ctx)
			if err != nil {
				return nil, err
			}
			ps, err := s.svc.ListPasskeys(ctx, id.User.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listAll(ps, Page{Limit: len(ps)}, passkeyOut), nil
		})

	huma.Register(api, noContent(op("delete-passkey", http.MethodDelete, "/me/passkeys/{id}", tag,
		"Remove uma passkey (exige reautenticação recente)")),
		func(ctx context.Context, in *passkeyPath) (*struct{}, error) {
			id, err := s.passkeyCaller(ctx)
			if err != nil {
				return nil, err
			}
			pid, perr := domain.ParseID(in.ID)
			if perr != nil {
				return nil, s.fail(ctx, domain.Invalid("id", "id inválido"))
			}
			if err := s.svc.DeletePasskey(ctx, id, pid); err != nil {
				return nil, s.fail(ctx, err)
			}
			return nil, nil
		})
}

// passkeyCaller devolve a identidade que faz a operação de passkey. Sem usuário (modo trusted, sem identidade do
// Guard), recusa: passkey é do usuário.
func (s *server) passkeyCaller(ctx context.Context) (auth.Identity, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id.User.ID == (domain.ID{}) {
		return auth.Identity{}, s.fail(ctx, domain.ErrUnauthenticated)
	}
	return id, nil
}

func passkeyOut(p domain.Passkey) Passkey {
	return Passkey{ID: p.ID.String(), Name: p.Name, Transports: p.Transports, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
}
