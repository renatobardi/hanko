package service

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/renatobardi/hanko/internal/auth"
	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/store"
)

// Preferências da pessoa no servidor (ADR-0061): perfil próprio, favoritos e recentes da sidebar e avisos dispensados.
// São estado de navegação pessoal: favoritos geram fato de atividade (baixa frequência, e a empresa é quem os guarda);
// recentes e dispensas não (como o lido/não lido da Caixa, ADR-0042).

var prefsCounter metric.Int64Counter

func init() {
	c, err := otel.Meter("hanko/service").Int64Counter("hanko.preferences",
		metric.WithDescription("Escritas de preferências pessoais por tipo (favorito, recente, perfil, dispensa)."))
	if err == nil {
		prefsCounter = c
	}
}

func countPref(ctx context.Context, kind string) {
	if prefsCounter != nil {
		prefsCounter.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind)))
	}
}

// prefSubject é o dono das preferências: o usuário autenticado ou, no modo trusted, a única pessoa da instalação.
func prefSubject(ctx context.Context) string {
	if id, ok := auth.IdentityFrom(ctx); ok && id.User.ID != (domain.ID{}) {
		return id.User.ID.String()
	}
	return domain.LocalSubject
}

// ---------- favoritos e recentes ----------

// NavItems devolve os favoritos e os recentes da pessoa na empresa (favoritos pela ordem em que foram marcados, recentes
// do mais novo ao mais antigo).
func (s *Service) NavItems(ctx context.Context, companyID domain.ID) (favorites, recents []domain.NavItem, err error) {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return nil, nil, err
	}
	rows, err := s.read().ListNavItems(ctx, store.ListNavItemsParams{CompanyID: companyID, Subject: prefSubject(ctx)})
	if err != nil {
		return nil, nil, fmt.Errorf("list nav items: %w", err)
	}
	favorites, recents = []domain.NavItem{}, []domain.NavItem{}
	for _, r := range rows {
		it := domain.NavItem{Kind: domain.NavKind(r.Kind), Path: r.Path, Title: r.Title, TouchedAt: r.TouchedAt}
		if it.Kind == domain.NavFavorite {
			favorites = append(favorites, it)
		} else {
			recents = append(recents, it)
		}
	}
	// favoritos: o mais antigo primeiro (ordem de marcação estável); recentes já vêm do mais novo ao mais antigo
	for i, j := 0, len(favorites)-1; i < j; i, j = i+1, j-1 {
		favorites[i], favorites[j] = favorites[j], favorites[i]
	}
	return favorites, recents, nil
}

// SetFavorite marca (on) ou desmarca uma página como favorita da pessoa. Idempotente.
func (s *Service) SetFavorite(ctx context.Context, actor domain.Actor, companyID domain.ID, path, title string, on bool) error {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return err
	}
	p, err := domain.NormalizeNavPath(path)
	if err != nil {
		return err
	}
	subject := prefSubject(ctx)
	title = domain.CleanNavTitle(title)
	err = s.withTx(ctx, func(t *txc) error {
		if !on {
			n, err := t.q.DeleteNavItem(ctx, store.DeleteNavItemParams{CompanyID: companyID, Subject: subject, Kind: string(domain.NavFavorite), Path: p})
			if err != nil {
				return fmt.Errorf("delete favorite: %w", err)
			}
			if n == 0 {
				return nil
			}
			return t.activity(actor, companyID, "nav.favorite_removed", "company", companyID, map[string]any{"path": p}, "")
		}
		cnt, err := t.q.CountNavItems(ctx, store.CountNavItemsParams{CompanyID: companyID, Subject: subject, Kind: string(domain.NavFavorite)})
		if err != nil {
			return fmt.Errorf("count favorites: %w", err)
		}
		exists := false
		if cnt > 0 {
			favs, e := t.q.ListNavItems(ctx, store.ListNavItemsParams{CompanyID: companyID, Subject: subject})
			if e != nil {
				return fmt.Errorf("list nav items: %w", e)
			}
			for _, f := range favs {
				if f.Kind == string(domain.NavFavorite) && f.Path == p {
					exists = true
				}
			}
		}
		if exists {
			return nil // já é favorito: não há fato novo
		}
		if cnt >= domain.MaxFavorites {
			return domain.Invalid("path", fmt.Sprintf("no máximo %d favoritos por empresa: remova algum antes", domain.MaxFavorites))
		}
		if err := t.q.UpsertNavItem(ctx, store.UpsertNavItemParams{CompanyID: companyID, Subject: subject, Kind: string(domain.NavFavorite), Path: p, Title: title, Now: t.now}); err != nil {
			return fmt.Errorf("upsert favorite: %w", err)
		}
		return t.activity(actor, companyID, "nav.favorite_added", "company", companyID, map[string]any{"path": p, "title": title}, "")
	})
	if err == nil {
		countPref(ctx, "favorite")
	}
	return err
}

// TouchRecent registra a visita a uma página de detalhe (best-effort: quem chama ignora o erro). Mantém só os
// domain.MaxRecents mais novos. Páginas que não são de detalhe e caminhos inválidos são ignorados em silêncio.
func (s *Service) TouchRecent(ctx context.Context, companyID domain.ID, path, title string) error {
	if err := s.require(ctx, companyID, domain.MemberViewer); err != nil {
		return err
	}
	p, err := domain.NormalizeNavPath(path)
	if err != nil || !domain.IsNavDetail(p) {
		return nil
	}
	subject := prefSubject(ctx)
	err = s.withTx(ctx, func(t *txc) error {
		if err := t.q.UpsertNavItem(ctx, store.UpsertNavItemParams{CompanyID: companyID, Subject: subject, Kind: string(domain.NavRecent), Path: p,
			Title: domain.CleanNavTitle(title), Now: t.now}); err != nil {
			return fmt.Errorf("upsert recent: %w", err)
		}
		return t.q.PruneRecentNavItems(ctx, store.PruneRecentNavItemsParams{CompanyID: companyID, Subject: subject, Keep: domain.MaxRecents})
	})
	if err == nil {
		countPref(ctx, "recent")
	}
	return err
}

// ---------- perfil próprio ----------

func (s *Service) callerUserRow(ctx context.Context, q *store.Queries) (store.User, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id.User.ID == (domain.ID{}) {
		return store.User{}, domain.Invalid("user", "o perfil é de uma pessoa autenticada; no modo trusted não há conta para editar")
	}
	row, err := q.GetUserByID(ctx, id.User.ID)
	if err != nil {
		return store.User{}, mapErr(err, "user")
	}
	return row, nil
}

func profileOf(u store.User, p *store.UserProfile) domain.UserProfile {
	out := domain.UserProfile{UserID: u.ID, Email: u.Email, Name: u.Name}
	if p != nil {
		out.AvatarGlyph, out.AvatarTone, out.Timezone, out.Locale = p.AvatarGlyph, p.AvatarTone, p.Timezone, p.Locale
	}
	return out
}

// GetProfile devolve o perfil de quem chama.
func (s *Service) GetProfile(ctx context.Context) (domain.UserProfile, error) {
	q := s.read()
	u, err := s.callerUserRow(ctx, q)
	if err != nil {
		return domain.UserProfile{}, err
	}
	p, err := q.GetUserProfile(ctx, u.ID)
	if isNoRows(err) {
		return profileOf(u, nil), nil
	}
	if err != nil {
		return domain.UserProfile{}, fmt.Errorf("get user profile: %w", err)
	}
	return profileOf(u, &p), nil
}

// UpdateProfile altera nome, avatar, fuso e idioma da própria pessoa. Não é fato de empresa (o perfil vale em todas),
// então não gera atividade; fica no log e na métrica.
func (s *Service) UpdateProfile(ctx context.Context, in domain.ProfileInput) (domain.UserProfile, error) {
	var out domain.UserProfile
	err := s.withTx(ctx, func(t *txc) error {
		u, err := s.callerUserRow(ctx, t.q)
		if err != nil {
			return err
		}
		cur := profileOf(u, nil)
		if p, e := t.q.GetUserProfile(ctx, u.ID); e == nil {
			cur = profileOf(u, &p)
		} else if !isNoRows(e) {
			return fmt.Errorf("get user profile: %w", e)
		}
		next, err := cur.Apply(in)
		if err != nil {
			return err
		}
		if next.Name != u.Name {
			if u, err = t.q.SetUserName(ctx, store.SetUserNameParams{ID: u.ID, Name: next.Name}); err != nil {
				return mapErr(err, "set user name")
			}
		}
		p, err := t.q.UpsertUserProfile(ctx, store.UpsertUserProfileParams{UserID: u.ID, AvatarGlyph: next.AvatarGlyph, AvatarTone: next.AvatarTone,
			Timezone: next.Timezone, Locale: next.Locale, Now: t.now})
		if err != nil {
			return mapErr(err, "save user profile")
		}
		out = profileOf(u, &p)
		return nil
	})
	if err == nil {
		countPref(ctx, "profile")
	}
	return out, err
}

// ProfileOf devolve o perfil de um usuário pelo id, para a UI aplicar idioma e fuso na requisição (sem exigir empresa).
// Erro de leitura volta como perfil vazio: o perfil nunca derruba a página.
func (s *Service) ProfileOf(ctx context.Context, userID domain.ID) domain.UserProfile {
	q := s.read()
	u, err := q.GetUserByID(ctx, userID)
	if err != nil {
		return domain.UserProfile{}
	}
	p, err := q.GetUserProfile(ctx, userID)
	if err != nil {
		return profileOf(u, nil)
	}
	return profileOf(u, &p)
}

// ---------- avisos dispensados (O que há de novo) ----------

const dismissWhatsNew = "whats_new"

// WhatsNewSeen devolve a versão cujas novidades a pessoa já dispensou ("" = nenhuma).
func (s *Service) WhatsNewSeen(ctx context.Context) (string, error) {
	v, err := s.read().GetDismissal(ctx, store.GetDismissalParams{Subject: prefSubject(ctx), Key: dismissWhatsNew})
	if isNoRows(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get dismissal: %w", err)
	}
	return v, nil
}

// DismissWhatsNew registra que a pessoa viu as novidades da versão.
func (s *Service) DismissWhatsNew(ctx context.Context, version string) error {
	version = strings.TrimSpace(version)
	if version == "" || len(version) > 100 || strings.ContainsAny(version, "\r\n\t <>\"'") {
		return domain.Invalid("version", "versão inválida")
	}
	subject := prefSubject(ctx)
	err := s.withTx(ctx, func(t *txc) error {
		return t.q.UpsertDismissal(ctx, store.UpsertDismissalParams{Subject: subject, Key: dismissWhatsNew, Value: version, Now: t.now})
	})
	if err == nil {
		countPref(ctx, "dismissal")
	}
	return err
}
