package reqlog

import "testing"

// Kimlikler surecler/yeniden baslatmalar arasi cakismamali (eski sirali sayac
// her restart'ta sifirlanip Postgres'teki kayitlarla cakisiyordu).
func TestNewIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := NewID()
		if seen[id] {
			t.Fatalf("tekrar eden kimlik: %s", id)
		}
		seen[id] = true
	}
	a, b := New(10), New(10)
	if a.Add(Entry{}).ID == b.Add(Entry{}).ID {
		t.Fatal("iki ayri halka ayni ilk kimligi uretmemeli")
	}
}
