package protocol

import "testing"

// screen2_fps60: tel uzerindeki yetenek adi ve fps tavanlari eski/yeni uc noktalar
// arasinda sozlesmedir; yanlislikla degistirilmemeli.
func TestScreen2FPSContract(t *testing.T) {
	if FeatureScreen2FPS60 != "screen2_fps60" {
		t.Fatalf("yetenek adi degisti: %q", FeatureScreen2FPS60)
	}
	if Screen2FPSBase != 30 || Screen2FPSMax != 60 {
		t.Fatalf("fps tavanlari: %d / %d", Screen2FPSBase, Screen2FPSMax)
	}
	// Ajan istatistik/geri bildirim dogrulamasi 60'in uzerindeki degerleri de tasiyabilmeli
	// (hedef fps ve kare hizi sinirlari 1000'in altinda).
	st := Screen2Stats{Type: TypeScreen2Stats, SessionID: "s", WindowMs: 1000, FPS: 59.5, CaptureFPS: 60, TargetFPS: 60, MaxFPS: 60}
	if !st.Valid() {
		t.Fatal("60 fps'lik istatistik gecersiz sayildi")
	}
	fb := Screen2Feedback{Type: TypeScreen2Feedback, SessionID: "s", WindowMs: 500,
		Layers: []Screen2FeedbackLayer{{ID: 0, FPS: 60, Viewers: 1}}}
	if !fb.Valid() {
		t.Fatal("60 fps'lik geri bildirim gecersiz sayildi")
	}
}
