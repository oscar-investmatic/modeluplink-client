package main

import (
	"sort"

	"github.com/oscar-investmatic/modeluplink-client/internal/localconfig"
	"github.com/oscar-investmatic/modeluplink-client/pkg/client"
)

const connectionLimitMessage = "Your account has reached its connection limit. Offline connections still count. Open your dashboard to manage your existing connections."

// Older pilot servers do not advertise their limit; they allow one permanent
// connection. An explicit zero from newer servers means unlimited.
func paidConnectionLimit(account client.Account, remote []client.Endpoint) *client.Endpoint {
	if !entitled(account) {
		return nil
	}
	limit := 1
	if account.MaxEndpoints != nil {
		limit = *account.MaxEndpoints
	}
	if limit <= 0 {
		return nil
	}
	var permanent []client.Endpoint
	for _, e := range remote {
		if !e.Trial {
			permanent = append(permanent, e)
		}
	}
	if len(permanent) < limit {
		return nil
	}
	// Stable selection prevents quiet refreshes from changing the shown address.
	sort.Slice(permanent, func(i, j int) bool { return permanent[i].ID < permanent[j].ID })
	return &permanent[0]
}

// A stopped connection still belongs to its original computer. Sign-in only
// discovers it; it never transfers ownership or creates another endpoint.
func otherComputerTrial(account client.Account, remote []client.Endpoint, cfg localconfig.Config) *client.Endpoint {
	if entitled(account) {
		return nil
	}
	for _, e := range remote {
		if local, ok := cfg.Endpoints[e.Slug]; e.Trial && (!ok || local.ID != e.ID) {
			return &e
		}
	}
	return nil
}
