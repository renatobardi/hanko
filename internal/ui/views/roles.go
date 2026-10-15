package views

import (
	"context"
	"strings"

	"github.com/renatobardi/hanko/internal/domain"
	"github.com/renatobardi/hanko/internal/i18n"
)

// Papéis na UI (ADR-0021). Isto é só UX: esconde ou desabilita controles que o service recusaria.
// A barreira continua sendo o service, que devolve 403 se alguém forjar o POST.

type roleKey struct{}

// WithRole anexa ao contexto de render o papel da pessoa na empresa atual.
func WithRole(ctx context.Context, r domain.MemberRole) context.Context {
	return context.WithValue(ctx, roleKey{}, r)
}

// RoleFrom é o papel no contexto; vazio (sem papel conhecido) não habilita nada.
func RoleFrom(ctx context.Context) domain.MemberRole {
	r, _ := ctx.Value(roleKey{}).(domain.MemberRole)
	return r
}

// CanWrite: member ou mais (tasks, comentários, acordar, cancelar, gates approval/task_review).
func (s Shell) CanWrite() bool { return s.Role.AtLeast(domain.MemberMember) }

// CanAdmin: admin ou owner (personas, orçamentos, assinaturas, fluxos, gates hire).
func (s Shell) CanAdmin() bool { return s.Role.AtLeast(domain.MemberAdmin) }

// CanOwner: só owner.
func (s Shell) CanOwner() bool { return s.Role == domain.MemberOwner }

func canWrite(ctx context.Context) bool { return RoleFrom(ctx).AtLeast(domain.MemberMember) }
func canAdmin(ctx context.Context) bool { return RoleFrom(ctx).AtLeast(domain.MemberAdmin) }

// canDecide: o gate hire é de admin; os demais, de member.
func canDecide(ctx context.Context, kind string) bool {
	if domain.GateKind(kind) == domain.GateHire {
		return canAdmin(ctx)
	}
	return canWrite(ctx)
}

func decideNeed(kind string) domain.MemberRole {
	if domain.GateKind(kind) == domain.GateHire {
		return domain.MemberAdmin
	}
	return domain.MemberMember
}

func roleName(ctx context.Context, r domain.MemberRole) string {
	switch r {
	case domain.MemberOwner:
		return i18n.T(ctx, "Dono")
	case domain.MemberAdmin:
		return "Admin"
	case domain.MemberMember:
		return i18n.T(ctx, "Membro")
	case domain.MemberViewer:
		return i18n.T(ctx, "Leitor")
	}
	return string(r)
}

// roleBadge é o texto da sidebar: "você: leitor".
func roleBadge(ctx context.Context, r domain.MemberRole) string {
	return i18n.Tf(ctx, "você: %s", strings.ToLower(roleName(ctx, r)))
}

func needText(ctx context.Context, min domain.MemberRole) string {
	if min.AtLeast(domain.MemberAdmin) {
		return i18n.T(ctx, "requer admin")
	}
	return i18n.T(ctx, "somente leitura")
}
