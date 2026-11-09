package cli

import (
	"context"
	"fmt"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/store"
)

// requireConnectedContext returns a NotInitialised error if the Notion context is not ready for use
func requireConnectedContext(ctx context.Context, s *store.Store, contextID string) error {
	connected, err := s.ContextConnected(ctx, contextID)
	if err != nil {
		return err
	}
	if !connected {
		return &clierr.Error{Code: clierr.NotInitialised, Message: fmt.Sprintf("context %q not connected to Notion, run 'tt init'", contextID)}
	}

	return nil
}
