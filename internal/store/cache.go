package store

import "sync"

// reconstructionCache is a tiny LRU keyed by edit_id, holding the full
// reconstructed content of that edit. A simple map+linked list is enough for
// our typical sizes (default 16 entries).
type reconstructionCache struct {
	mu       sync.Mutex
	capacity int
	order    []int64
	store    map[int64]string
}

// newReconstructionCache returns an LRU with the given capacity. capacity<=0
// disables caching; calls become no-ops.
func newReconstructionCache(capacity int) *reconstructionCache {
	if capacity < 0 {
		capacity = 0
	}
	return &reconstructionCache{
		capacity: capacity,
		store:    make(map[int64]string, capacity),
	}
}

func (c *reconstructionCache) get(id int64) (string, bool) {
	if c == nil || c.capacity == 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.store[id]
	if !ok {
		return "", false
	}
	c.bumpLocked(id)
	return v, true
}

func (c *reconstructionCache) put(id int64, value string) {
	if c == nil || c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.store[id]; ok {
		c.store[id] = value
		c.bumpLocked(id)
		return
	}
	c.store[id] = value
	c.order = append(c.order, id)
	for len(c.order) > c.capacity {
		victim := c.order[0]
		c.order = c.order[1:]
		delete(c.store, victim)
	}
}

func (c *reconstructionCache) evict(id int64) {
	if c == nil || c.capacity == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, id)
	c.removeFromOrderLocked(id)
}

func (c *reconstructionCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store = make(map[int64]string, c.capacity)
	c.order = c.order[:0]
}

func (c *reconstructionCache) bumpLocked(id int64) {
	c.removeFromOrderLocked(id)
	c.order = append(c.order, id)
}

func (c *reconstructionCache) removeFromOrderLocked(id int64) {
	for i, x := range c.order {
		if x == id {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// SetCacheSize changes the cache's capacity, evicting if necessary.
func (s *Store) SetCacheSize(n int) {
	if n < 0 {
		n = 0
	}
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	s.cache.capacity = n
	for len(s.cache.order) > n {
		victim := s.cache.order[0]
		s.cache.order = s.cache.order[1:]
		delete(s.cache.store, victim)
	}
}
