package agent

import (
	"context"
	"time"
)

// Closing prevents new handlers before waiting, so no Add races Wait. The
// caller has already cancelled session request contexts and closed streams.
func (h *localInference) drain() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*usageReportTimeout+time.Second)
	defer cancel()
	settled := make(chan struct{})
	go func() { h.requests.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-ctx.Done():
		return
	}
	h.mu.Lock()
	done := h.usageDone
	h.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
}
