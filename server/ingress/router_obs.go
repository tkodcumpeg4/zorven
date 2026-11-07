package ingress

// RouteCount, bellekteki yonlendirme kayit sayisi (tam + joker host); gozlemlenebilirlik icin.
func (r *Router) RouteCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byHost) + len(r.wild)
}
