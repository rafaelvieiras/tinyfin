package metadata

import "context"

// NoopProvider is the zero-configuration Provider — every Lookup misses.
// Used whenever no direct provider is configured (e.g. TMDBAPIKey unset),
// so internal/scan's provider field is never nil and every call site gets
// the same ErrNotFound path a real provider's genuine miss would take,
// rather than needing its own separate "is a provider even configured"
// branch.
type NoopProvider struct{}

func (NoopProvider) Lookup(ctx context.Context, q Query) (Result, error) {
	return Result{}, ErrNotFound
}
