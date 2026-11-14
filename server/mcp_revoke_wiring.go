package main

import (
	"sync/atomic"

	"github.com/tkodcumpeg4/zorven/server/mcpserver"
)

// mcpIdentityInvalidator, api.Server.OnAPITokenRevoked kancasi: API token iptal/rotate
// edildiginde MCP kimlik onbellegini hemen bosaltir. Isleyici sonradan (mcpIdent.Store)
// baglandigi icin gosterici cagri aninda okunur; henuz yoksa no-op.
func mcpIdentityInvalidator(p *atomic.Pointer[mcpserver.Handler]) func() {
	return func() {
		if h := p.Load(); h != nil {
			h.InvalidateIdentities()
		}
	}
}
