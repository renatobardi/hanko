package domain

import (
	"errors"
	"fmt"
)

// ReauthError: a ação sensível pede uma prova recente de identidade (ADR-0070). Vale como ErrUnauthenticated para
// quem não olha o tipo; a API responde 401 com o código reauth_required e a janela exigida, e a UI abre o pedido de
// prova e repete a ação.
type ReauthError struct {
	WindowMinutes int
}

func (e *ReauthError) Error() string {
	return fmt.Sprintf("reauth required: prova com até %d min", e.WindowMinutes)
}

// Is faz o erro valer como ErrUnauthenticated.
func (e *ReauthError) Is(target error) bool { return target == ErrUnauthenticated }

// ErrTooManyAttempts: provas erradas demais para este usuário agora (o mesmo limitador do login). Vale como 429.
var ErrTooManyAttempts = errors.New("too many reauthentication attempts")
