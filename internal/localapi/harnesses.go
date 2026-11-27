package localapi

import (
	"context"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/config"
	"github.com/romanvolkov/flows/internal/harness"
	"github.com/romanvolkov/flows/internal/paths"
)

const harnessCacheTTL = 10 * time.Minute

type availableHarness struct {
	ID      string          `json:"id"`
	Version string          `json:"version"`
	Profile harness.Profile `json:"profile"`
}

type harnessCache struct {
	mu         sync.Mutex
	items      []availableHarness
	expires    time.Time
	refreshing bool
	ready      chan struct{}
	lastErr    error
}

func (c *harnessCache) list(ctx context.Context, home paths.Home) ([]availableHarness, error) {
	c.mu.Lock()
	if c.items != nil {
		items := append([]availableHarness(nil), c.items...)
		if time.Now().Before(c.expires) {
			c.mu.Unlock()
			return items, nil
		}
		if !c.refreshing {
			c.refreshing = true
			go c.refresh(home)
		}
		c.mu.Unlock()
		return items, nil
	}
	if c.refreshing {
		ready := c.ready
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ready:
		}
		c.mu.Lock()
		items := append([]availableHarness(nil), c.items...)
		err := c.lastErr
		c.mu.Unlock()
		return items, err
	}
	c.refreshing = true
	c.ready = make(chan struct{})
	c.mu.Unlock()
	return c.refreshNow(ctx, home)
}

func (c *harnessCache) refresh(home paths.Home) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, _ = c.refreshNow(ctx, home)
}

func (c *harnessCache) refreshNow(ctx context.Context, home paths.Home) ([]availableHarness, error) {
	items, err := detectHarnesses(ctx, home)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	c.lastErr = err
	if err != nil {
		c.expires = time.Now().Add(time.Minute)
		if c.ready != nil {
			close(c.ready)
			c.ready = nil
		}
		return nil, err
	}
	c.items = items
	c.expires = time.Now().Add(harnessCacheTTL)
	if c.ready != nil {
		close(c.ready)
		c.ready = nil
	}
	return append([]availableHarness(nil), items...), nil
}

func (c *harnessCache) invalidate() {
	c.mu.Lock()
	c.expires = time.Time{}
	c.mu.Unlock()
}

func detectHarnesses(ctx context.Context, home paths.Home) ([]availableHarness, error) {
	settings, err := config.LoadSettings(home.Settings())
	if err != nil {
		return nil, err
	}
	result := make([]availableHarness, 0, 3)
	for _, kind := range []harness.Kind{harness.KindClaude, harness.KindCodex, harness.KindAgy} {
		adapter, err := harness.ForKind(kind)
		if err != nil {
			continue
		}
		profile := config.ProfileFromSettings(kind, settings)
		caps, err := adapter.Probe(ctx, profile.Executable)
		if err == nil {
			result = append(result, availableHarness{ID: string(kind), Version: caps.Version, Profile: profile})
		}
	}
	return result, nil
}
