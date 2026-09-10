package agent

import (
	"bufio"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kbinani/screenshot"
	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// Donanim H.264 encoder'i tespiti. ffmpeg PATH'te ve bir donanim encoder'i
// destekliyorsa (NVENC / QuickSync / AMF) H.264 akisi kullanilir; yoksa JPEG.
//
// ffmpeg BUILD bagimliligi DEGIL, RUNTIME bagimliligidir: exec ile calistirilir,
// yani istemci hala cgo'suz derlenir. ffmpeg yoksa sistem sessizce JPEG'e duser.

var (
	hwEncoderOnce sync.Once
	hwEncoder     string // "h264_nvenc" | "h264_qsv" | "h264_amf" | ""
)

// tercih sirasi: NVENC (en yaygin/iyi) > QuickSync > AMF.
var hwEncoderPref = []string{"h264_nvenc", "h264_qsv", "h264_amf"}

// detectHWEncoder, kullanilabilir ilk donanim encoder'ini doner (bir kez tespit).
func detectHWEncoder() string {
	hwEncoderOnce.Do(func() {
		// MVP: ekran yakalama girdisi (gdigrab) yalnizca Windows'ta hazir.
		// Linux (x11grab) / macOS (avfoundation) sonra eklenebilir.
		if runtime.GOOS != "windows" {
			return
		}
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders").Output()
		if err != nil {
			return
		}
		listed := string(out)
		for _, enc := range hwEncoderPref {
			if strings.Contains(listed, enc) {
				hwEncoder = enc
				return
			}
		}
	})
	return hwEncoder
}

// captureH264, ffmpeg ile ekrani donanimda H.264'e kodlayip fragmented-MP4
// akisini parca parca sunucuya gonderir. Basariyla basladiysa true doner ve
// oturumu bir goroutine'de surdurur; ffmpeg hemen coktuyse false doner (cagiran
// JPEG'e duser).
func (sm *screenManager) captureH264(ctx context.Context, ss *screenSession, display, fps, maxWidth int, encoder string) bool {
	if fps <= 0 || fps > 60 {
		fps = 30 // H.264'te daha yuksek FPS mantikli
	}

	// Native ekran boyutu: dashboard'un cozunurluk menusu buna gore secenek
	// uretir. Olceklenmis kare boyutundan cikarilamaz (bkz. protocol.ScreenFrame).
	bounds := screenshot.GetDisplayBounds(display)
	screenW, screenH := bounds.Dx(), bounds.Dy()

	// gdigrab "desktop": tum sanal masaustunu yakalar. Tek monitor secimi icin
	// offset/boyut gerekir; MVP'de tum masaustu — dashboard tek goruntu gosterir.
	//
	// Dusuk gecikme ayarlari:
	//   -g fps  : saniyede bir anahtar kare (MSE'nin hizli baslamasi icin)
	//   fragmented mp4 (frag_keyframe+empty_moov+default_base_moof): tarayici
	//   MSE'nin akisi parca parca oynatabilmesi icin sart.
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "gdigrab", "-framerate", itoa(fps), "-i", "desktop",
		"-c:v", encoder,
		"-pix_fmt", "yuv420p",
		"-profile:v", "baseline", "-level", "3.1",
		"-g", itoa(fps),
		"-b:v", "6M", "-maxrate", "8M", "-bufsize", "8M",
		"-f", "mp4",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"pipe:1",
	}
	if encoder == "h264_nvenc" {
		// NVENC dusuk gecikme ayari: -c:v'den hemen sonra ekle.
		args = insertAfter(args, encoder, "-preset", "p1", "-tune", "ll")
	}
	// Cozunurluk secimi H.264'te de gecerli olmali. -2: yuksekligi orana gore
	// hesapla ve CIFT sayiya yuvarla (yuv420p tek boyut kabul etmez).
	if maxWidth > 0 && maxWidth < screenW {
		args = insertAfter(args, "desktop", "-vf", "scale="+itoa(maxWidth)+":-2")
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	if err := cmd.Start(); err != nil {
		return false
	}

	go func() {
		defer sm.finish(ss)
		defer cmd.Wait()

		reader := bufio.NewReaderSize(stdout, 64*1024)
		buf := make([]byte, 32*1024)
		var seq uint64

		for {
			n, err := reader.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				seq++
				if serr := sm.cs.sendControl(ctx, protocol.ScreenFrame{
					Type:      protocol.TypeScreenFrame,
					SessionID: ss.id,
					Data:      chunk,
					Seq:       seq,
					Codec:     "h264",
					ScreenW:   screenW,
					ScreenH:   screenH,
				}, protocol.TypeScreenFrame); serr != nil {
					cmd.Process.Kill()
					return
				}
			}
			if err != nil {
				// ffmpeg kapandi. Ilk karelerden once coktuyse dashboard zaten
				// bir sey gormedi; hata bildir.
				if seq == 0 {
					sm.sendError(ctx, ss.id, "H.264 encoder baslatilamadi (ffmpeg): "+err.Error())
				}
				return
			}
		}
	}()
	return true
}

// itoa, kucuk yardimci (strconv importunu tek kullanim icin getirmemek adina).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// insertAfter, args diliminde target'tan hemen sonra extra'lari ekler.
func insertAfter(args []string, target string, extra ...string) []string {
	for i, a := range args {
		if a == target {
			out := make([]string, 0, len(args)+len(extra))
			out = append(out, args[:i+1]...)
			out = append(out, extra...)
			out = append(out, args[i+1:]...)
			return out
		}
	}
	return args
}
