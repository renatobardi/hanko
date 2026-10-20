package api

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/service"
)

// Acesso (ADR-0057): login da CLI pelo navegador, whoami, link de entrada e pedidos, contas da instalação.

// CLIAuthStarted é a resposta da criação de um pedido de login da CLI.
type CLIAuthStarted struct {
	Code        string    `json:"code" doc:"Código curto da URL de autorização."`
	PollToken   string    `json:"poll_token" doc:"Segredo de consulta; só a CLI o conhece."`
	VerifyPath  string    `json:"verify_path" example:"/cli-auth/AbCdEf123456" doc:"Caminho (no servidor) que a pessoa abre no navegador para autorizar."`
	ExpiresAt   time.Time `json:"expires_at"`
	IntervalSec int       `json:"interval_seconds" doc:"Intervalo sugerido entre consultas."`
}

// APIKeyInfo descreve uma API key sem o segredo.
type APIKeyInfo struct {
	ID     string `json:"id" format:"uuid"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
	Scope  string `json:"scope" enum:"full,read"`
	Device string `json:"device,omitempty"`
}

// CLIAuthPolled é o estado do pedido. A chave só vem uma vez, quando aprovado.
type CLIAuthPolled struct {
	Status string      `json:"status" enum:"pending,approved,denied,consumed"`
	APIKey string      `json:"api_key,omitempty" doc:"API key hk_… (só na primeira consulta depois de aprovado)."`
	Key    *APIKeyInfo `json:"key,omitempty"`
	User   *Whoami     `json:"user,omitempty"`
}

// Whoami diz quem está chamando.
type Whoami struct {
	Authenticated bool        `json:"authenticated" doc:"false em modo trusted (sem login)."`
	ID            string      `json:"id,omitempty" format:"uuid"`
	Email         string      `json:"email,omitempty"`
	Name          string      `json:"name,omitempty"`
	Role          string      `json:"role,omitempty" enum:"owner,admin,member" doc:"Papel na instalação."`
	Via           string      `json:"via,omitempty" enum:"session,api_key"`
	Key           *APIKeyInfo `json:"key,omitempty" doc:"A chave usada, quando via=api_key."`
}

func whoamiOf(u domain.User) *Whoami {
	return &Whoami{Authenticated: true, ID: idStr(u.ID), Email: u.Email, Name: u.Name, Role: string(u.Role)}
}

func keyInfoOf(k domain.APIKey) *APIKeyInfo {
	return &APIKeyInfo{ID: idStr(k.ID), Name: k.Name, Prefix: k.Prefix, Scope: string(k.Scope), Device: k.Device}
}

// JoinLink é o estado do link público de entrada. O token só vem na criação.
type JoinLink struct {
	Enabled bool       `json:"enabled"`
	Created *time.Time `json:"created_at,omitempty"`
	Token   string     `json:"token,omitempty" doc:"Token do link (só na criação)."`
	Path    string     `json:"path,omitempty" example:"/pedir-acesso/<token>" doc:"Caminho da página pública (só na criação)."`
}

// JoinRequest é um pedido de entrada.
type JoinRequest struct {
	ID        string     `json:"id" format:"uuid"`
	UserID    string     `json:"user_id" format:"uuid"`
	Email     string     `json:"email"`
	Name      string     `json:"name"`
	Message   string     `json:"message,omitempty"`
	Status    string     `json:"status" enum:"pending,approved,denied"`
	Role      string     `json:"role,omitempty" enum:"owner,admin,member,viewer" doc:"Papel concedido, quando aprovado."`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
}

func joinRequestOut(r domain.JoinRequest) JoinRequest {
	out := JoinRequest{ID: idStr(r.ID), UserID: idStr(r.UserID), Email: r.Email, Name: r.Name, Message: r.Message,
		Status: string(r.Status), CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt}
	if r.Role != nil {
		out.Role = string(*r.Role)
	}
	return out
}

// InstanceUser é uma conta da instalação.
type InstanceUser struct {
	ID          string     `json:"id" format:"uuid"`
	Email       string     `json:"email"`
	Name        string     `json:"name"`
	Provider    string     `json:"provider" enum:"local,firebase"`
	Role        string     `json:"role" enum:"owner,admin,member"`
	Companies   int64      `json:"companies" doc:"Empresas de que participa."`
	ActiveKeys  int64      `json:"active_keys" doc:"API keys ativas."`
	CreatedAt   time.Time  `json:"created_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
}

func instanceUserOut(u domain.InstanceUser) InstanceUser {
	return InstanceUser{ID: idStr(u.User.ID), Email: u.User.Email, Name: u.User.Name, Provider: string(u.User.Provider),
		Role: string(u.User.Role), Companies: u.Companies, ActiveKeys: u.ActiveKeys, CreatedAt: u.User.CreatedAt, LastLoginAt: u.User.LastLoginAt}
}

type cliAuthStartIn struct {
	Body struct {
		Device string `json:"device" minLength:"1" maxLength:"120" example:"notebook-da-ana" doc:"Nome do dispositivo, mostrado a quem autoriza."`
		Scope  string `json:"scope,omitempty" enum:"full,read" doc:"Escopo da API key; padrão full."`
	}
}

type cliAuthPollIn struct {
	Body struct {
		PollToken string `json:"poll_token" minLength:"1" maxLength:"256"`
	}
}

type joinDecisionIn struct {
	CompanyPath
	Request string `path:"request" format:"uuid" doc:"Id do pedido."`
	Body    struct {
		Approve bool   `json:"approve" doc:"true aprova; false nega."`
		Role    string `json:"role,omitempty" enum:"owner,admin,member,viewer" doc:"Papel concedido ao aprovar (padrão member). Admin só concede member/viewer."`
	}
}

type listJoinIn struct {
	CompanyPath
	Page
	Pending bool `query:"pending" doc:"Só os pedidos abertos."`
}

// ipRate é um limitador por IP, em memória e por janela fixa, para os endpoints públicos do login da CLI.
type ipRate struct {
	mu   sync.Mutex
	hits map[string]*rateWindow
	max  int
	win  time.Duration
}

type rateWindow struct {
	n     int
	reset time.Time
}

func newIPRate(max int, win time.Duration) *ipRate {
	return &ipRate{hits: map[string]*rateWindow{}, max: max, win: win}
}

func (l *ipRate) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10000 { // varre o que expirou; nunca cresce sem limite
		for k, w := range l.hits {
			if !now.Before(w.reset) {
				delete(l.hits, k)
			}
		}
	}
	w := l.hits[ip]
	if w == nil || !now.Before(w.reset) {
		w = &rateWindow{reset: now.Add(l.win)}
		l.hits[ip] = w
	}
	w.n++
	return w.n <= l.max
}

func clientIP(ctx context.Context) string {
	if r, ok := ctx.Value(keyRemoteAddr).(string); ok {
		if h, _, err := net.SplitHostPort(r); err == nil {
			return h
		}
		return r
	}
	return ""
}

func (s *server) registerAccess(api huma.API) {
	s.cliStartRate = newIPRate(20, time.Minute)

	start := created(op("start-cli-auth", http.MethodPost, "/cli-auth", "auth",
		"Abre um pedido de login da CLI pelo navegador (público)"))
	start.Security = []map[string][]string{} // público: o guard deixa passar
	huma.Register(api, start, func(ctx context.Context, in *cliAuthStartIn) (*ItemOut[CLIAuthStarted], error) {
		if !s.cliStartRate.allow(clientIP(ctx), s.now()) {
			return nil, problem(http.StatusTooManyRequests, "pedidos de login demais; espere um minuto")
		}
		st, err := s.svc.StartCLIAuth(ctx, in.Body.Device, domain.KeyScope(in.Body.Scope))
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		return &ItemOut[CLIAuthStarted]{Body: CLIAuthStarted{Code: st.Code, PollToken: st.PollToken, VerifyPath: auth.CLIAuthPath + st.Code,
			ExpiresAt: st.ExpiresAt, IntervalSec: int(service.CLIAuthPollEvery / time.Second)}}, nil
	})

	poll := op("poll-cli-auth", http.MethodPost, "/cli-auth/poll", "auth",
		"Consulta o pedido de login; aprovado, devolve a API key uma única vez (público)")
	poll.Security = []map[string][]string{}
	huma.Register(api, poll, func(ctx context.Context, in *cliAuthPollIn) (*ItemOut[CLIAuthPolled], error) {
		p, err := s.svc.PollCLIAuth(ctx, in.Body.PollToken)
		if err != nil {
			return nil, s.fail(ctx, err)
		}
		out := CLIAuthPolled{Status: string(p.Status), APIKey: p.APIKey}
		if p.APIKey != "" {
			out.Key, out.User = keyInfoOf(p.Key), whoamiOf(p.User)
		}
		return &ItemOut[CLIAuthPolled]{Body: out}, nil
	})

	huma.Register(api, op("whoami", http.MethodGet, "/whoami", "auth", "Quem está chamando: usuário, papel e, com API key, a chave"),
		func(ctx context.Context, _ *struct{}) (*ItemOut[Whoami], error) {
			if p := principalFrom(ctx); p.Claims != nil {
				return &ItemOut[Whoami]{Body: Whoami{Authenticated: true, Via: "run_token"}}, nil
			}
			w, err := s.svc.WhoamiOf(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			if !w.Authenticated {
				return &ItemOut[Whoami]{Body: Whoami{}}, nil
			}
			out := whoamiOf(w.User)
			out.Via = string(w.Via)
			if w.Key != nil {
				out.Key = keyInfoOf(*w.Key)
			}
			return &ItemOut[Whoami]{Body: *out}, nil
		})

	huma.Register(api, op("get-join-link", http.MethodGet, "/companies/{company}/join-link", "members", "Estado do link público de entrada (admin)"),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[JoinLink], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			st, err := s.svc.JoinLinkState(ctx, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[JoinLink]{Body: joinLinkOut(st)}, nil
		})

	huma.Register(api, created(op("create-join-link", http.MethodPost, "/companies/{company}/join-link", "members",
		"Gera (ou troca) o link público de entrada; devolve o link uma única vez (admin)")),
		func(ctx context.Context, in *CompanyPath) (*ItemOut[JoinLink], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			tok, st, err := s.svc.CreateJoinLink(ctx, sc.Actor, sc.Company.ID)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			out := joinLinkOut(st)
			out.Token, out.Path = tok, auth.JoinPath+tok
			return &ItemOut[JoinLink]{Body: out}, nil
		})

	huma.Register(api, func() huma.Operation {
		o := op("revoke-join-link", http.MethodDelete, "/companies/{company}/join-link", "members", "Desliga o link público de entrada (admin)")
		o.DefaultStatus = http.StatusNoContent
		return o
	}(), func(ctx context.Context, in *CompanyPath) (*struct{}, error) {
		if err := forbidPersona(ctx); err != nil {
			return nil, err
		}
		sc, err := s.scope(ctx, in.Company)
		if err != nil {
			return nil, err
		}
		if err := s.svc.RevokeJoinLink(ctx, sc.Actor, sc.Company.ID); err != nil {
			return nil, s.fail(ctx, err)
		}
		return nil, nil
	})

	huma.Register(api, op("list-join-requests", http.MethodGet, "/companies/{company}/join-requests", "members", "Lista os pedidos de entrada (admin)"),
		func(ctx context.Context, in *listJoinIn) (*ListOut[JoinRequest], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			rs, err := s.svc.ListJoinRequests(ctx, sc.Company.ID, in.Pending)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(rs, in.Page, joinRequestOut), nil
		})

	huma.Register(api, op("decide-join-request", http.MethodPost, "/companies/{company}/join-requests/{request}/decision", "members",
		"Aprova (escolhendo o papel) ou nega um pedido de entrada (admin)"),
		func(ctx context.Context, in *joinDecisionIn) (*ItemOut[JoinRequest], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			sc, err := s.scope(ctx, in.Company)
			if err != nil {
				return nil, err
			}
			id, err := domain.ParseID(in.Request)
			if err != nil {
				return nil, s.fail(ctx, domain.ErrNotFound)
			}
			r, err := s.svc.DecideJoinRequest(ctx, sc.Actor, sc.Company.ID, id, in.Body.Approve, domain.MemberRole(in.Body.Role))
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return &ItemOut[JoinRequest]{Body: joinRequestOut(r)}, nil
		})

	huma.Register(api, op("list-users", http.MethodGet, "/users", "auth", "Lista as contas da instalação (dono ou admin da instalação)"),
		func(ctx context.Context, in *struct{ Page }) (*ListOut[InstanceUser], error) {
			if err := forbidPersona(ctx); err != nil {
				return nil, err
			}
			us, err := s.svc.ListInstanceUsers(ctx)
			if err != nil {
				return nil, s.fail(ctx, err)
			}
			return listOf(us, in.Page, instanceUserOut), nil
		})
}

func joinLinkOut(l domain.JoinLink) JoinLink {
	out := JoinLink{Enabled: l.Enabled}
	if !l.CreatedAt.IsZero() {
		t := l.CreatedAt
		out.Created = &t
	}
	return out
}
