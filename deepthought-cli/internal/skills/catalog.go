package skills

import "sync"

// Catalog swaps complete snapshots; active requests retain their loaded body.
type Catalog struct {
	mu   sync.RWMutex
	cwd  string
	list []*Skill
}

func NewCatalog(cwd string) (*Catalog, error) { c := &Catalog{cwd: cwd}; return c, c.Reload() }
func (c *Catalog) Reload() error {
	list, err := NewLoader().Load(c.cwd)
	c.mu.Lock()
	c.list = list
	c.mu.Unlock()
	return err
}
func (c *Catalog) Listing() string { c.mu.RLock(); defer c.mu.RUnlock(); return Listing(c.list) }
func (c *Catalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var names []string
	for _, s := range c.list {
		names = append(names, s.Name)
	}
	return names
}
func (c *Catalog) Lookup(name string) (string, []string, bool, error) {
	c.mu.RLock()
	var found *Skill
	for _, s := range c.list {
		if s.Name == name {
			found = s
			break
		}
	}
	c.mu.RUnlock()
	if found == nil {
		return "", nil, false, nil
	}
	body, err := found.Body()
	return body, append([]string(nil), found.AllowedTools...), true, err
}
