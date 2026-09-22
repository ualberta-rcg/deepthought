package app

import (
	"context"
	"crypto/sha256"
	"deepthought-cli/internal/babel"
	"fmt"
	"time"
)

type catalogCache struct {
	Entries   []babel.CatalogEntry
	FetchedAt time.Time
}

func (s *Settings) DiscoverCatalog(ctx context.Context, name string, refresh bool) ([]babel.CatalogEntry, string, error) {
	s.mu.RLock()
	p, ok := s.cfg.FindProvider(name)
	s.mu.RUnlock()
	if !ok {
		return nil, "", fmt.Errorf("provider no longer exists")
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(p.Name+"|"+p.BaseURL+"|"+p.Wire+"|"+p.APIKey)))
	var cached catalogCache
	if s.local != nil {
		_ = s.local.ReadRecord("catalog", id, &cached)
	}
	if !refresh && !cached.FetchedAt.IsZero() && time.Since(cached.FetchedAt) < 24*time.Hour {
		return cached.Entries, "Catalog cached " + cached.FetchedAt.Format(time.RFC3339), nil
	}
	client, err := s.ProviderClient(name)
	if err != nil {
		return nil, "", err
	}
	entries, err := client.ListCatalog(ctx)
	if err != nil {
		if !cached.FetchedAt.IsZero() {
			return cached.Entries, "Provider unavailable; stale catalog from " + cached.FetchedAt.Format(time.RFC3339), nil
		}
		return nil, "", err
	}
	if s.local != nil {
		if err = s.local.WriteRecord("catalog", id, catalogCache{entries, time.Now()}); err != nil {
			return entries, "Catalog fetched but could not be cached", nil
		}
	}
	return entries, "Catalog refreshed; select a model to add and use", nil
}
