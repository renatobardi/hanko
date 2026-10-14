package views

import "context"

// UserRef is the signed-in user shown in the sidebar (authenticated mode).
type UserRef struct {
	Name  string
	Email string
}

type userKey struct{}

// WithUser attaches the signed-in user to the render context.
func WithUser(ctx context.Context, u UserRef) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

func userFrom(ctx context.Context) (UserRef, bool) {
	u, ok := ctx.Value(userKey{}).(UserRef)
	return u, ok
}
