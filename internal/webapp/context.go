package webapp

import (
	"context"

	"studbot/internal/storage"
)

type contextKey string

const userContextKey contextKey = "user"

func contextWithUser(ctx context.Context, user *storage.User) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

func userFromContext(ctx context.Context) *storage.User {
	if user, ok := ctx.Value(userContextKey).(*storage.User); ok {
		return user
	}
	return nil
}
