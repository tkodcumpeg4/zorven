package store

// Page, liste uclarinin sayfalama parametresi (LIMIT/OFFSET). Limit<=0 =
// SINIRSIZ (yalniz dahili cagrilar icin; HTTP katmani her zaman 1..500 verir).
type Page struct {
	Limit  int
	Offset int
}

// Unbounded, tum satirlari ister.
func (p Page) Unbounded() bool { return p.Limit <= 0 }
