package agent

import (
	"bytes"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"image"
	"io"
	"regexp"
	"strings"
)

// Ekran yardimcisi (screen-helper) IPC protokolu.
//
// Windows servisi oturum 0'da calisir: etkilesimli masaustunu yakalayamaz ve
// girdi enjekte edemez. Servis, aktif konsol oturumunda kullanici hesabiyla bir
// "zorven screen-helper" sureci baslatir; ikisi named pipe uzerinden konusur.
// Bu dosya platformdan bagimsiz kismidir (cerceveleme, el sikisma, SDDL), boylece
// Linux'ta da test edilir.
//
// Cerceve: [uint32 LE uzunluk][1 bayt tur][yuk]; uzunluk tur+yuk toplamidir.
// Istek/yanit kilit adimlidir (servis ayni anda tek istek yollar).

const (
	hmHello         byte = 1  // yardimci -> servis: token, surum, pid
	hmHelloAck      byte = 2  // servis -> yardimci: 1 bayt (1=kabul)
	hmInfoReq       byte = 3  // servis -> yardimci
	hmInfoResp      byte = 4  // yardimci -> servis
	hmCapReq        byte = 5  // servis -> yardimci: int32 monitor
	hmCapResp       byte = 6  // yardimci -> servis
	hmInputReq      byte = 7  // servis -> yardimci: JSON
	hmInputResp     byte = 8  // yardimci -> servis: JSON
	hmClipReq       byte = 9  // servis -> yardimci
	hmClipResp      byte = 10 // yardimci -> servis
	hmShutdown      byte = 11 // servis -> yardimci (yanit yok)
	hmMaxFrame           = 1 << 29
	helperPipePf         = `\\.\pipe\zorven-screen-`
	helperTokenLen       = 64 // hex karakter
	helperMaxStrLen      = 1 << 16
)

// Capture yanit bayraklari.
const (
	capFlagHinted   byte = 1 << 0 // dikdortgenler yalniz degisen bolgeler (aksi halde tam kare)
	capFlagUnchang  byte = 1 << 1 // onceki kareden beri degisim yok (piksel yok)
	capFlagDegraded byte = 1 << 2 // guvenli masaustu/kilit ekrani: son kare tekrar ediliyor
)

var errHelperFrame = errors.New("ekran yardimcisi: gecersiz cerceve")

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload)+1 > hmMaxFrame {
		return errHelperFrame
	}
	var hdr [5]byte
	binary.LittleEndian.PutUint32(hdr[:4], uint32(len(payload)+1))
	hdr[4] = typ
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n < 1 || n > hmMaxFrame {
		return 0, nil, errHelperFrame
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return buf[0], buf[1:], nil
}

// --- ikili alan yardimcilari ---

type wbuf struct{ bytes.Buffer }

func (b *wbuf) u8(v byte)    { b.WriteByte(v) }
func (b *wbuf) u16(v uint16) { var t [2]byte; binary.LittleEndian.PutUint16(t[:], v); b.Write(t[:]) }
func (b *wbuf) u32(v uint32) { var t [4]byte; binary.LittleEndian.PutUint32(t[:], v); b.Write(t[:]) }
func (b *wbuf) i32(v int)    { b.u32(uint32(int32(v))) }
func (b *wbuf) str(s string) {
	if len(s) >= helperMaxStrLen {
		s = s[:helperMaxStrLen-1]
	}
	b.u16(uint16(len(s)))
	b.WriteString(s)
}
func (b *wbuf) rect(r image.Rectangle) {
	b.i32(r.Min.X)
	b.i32(r.Min.Y)
	b.i32(r.Max.X)
	b.i32(r.Max.Y)
}

type rbuf struct {
	b   []byte
	err error
}

func (r *rbuf) take(n int) []byte {
	if r.err != nil || n < 0 || len(r.b) < n {
		r.err = errHelperFrame
		return nil // hata bayragi kalici; cagiranlar r.err kontrol eder
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}
func (r *rbuf) u8() byte {
	t := r.take(1)
	if r.err != nil {
		return 0
	}
	return t[0]
}
func (r *rbuf) u16() uint16 {
	t := r.take(2)
	if r.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint16(t)
}
func (r *rbuf) u32() uint32 {
	t := r.take(4)
	if r.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(t)
}
func (r *rbuf) i32() int { return int(int32(r.u32())) }
func (r *rbuf) str() string {
	n := int(r.u16())
	t := r.take(n)
	if r.err != nil {
		return ""
	}
	return string(t)
}
func (r *rbuf) rect() image.Rectangle {
	return image.Rect(r.i32(), r.i32(), r.i32(), r.i32())
}

// --- el sikisma ---

func encodeHello(token, version string, pid uint32) []byte {
	var b wbuf
	b.str(token)
	b.str(version)
	b.u32(pid)
	return b.Bytes()
}

func decodeHello(p []byte) (token, version string, pid uint32, err error) {
	r := rbuf{b: p}
	token, version, pid = r.str(), r.str(), r.u32()
	return token, version, pid, r.err
}

// tokenEqual, sabit zamanli karsilastirma.
func tokenEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// acceptHandshake (servis tarafi): ilk cerceve gecerli token tasiyan hello olmali.
func acceptHandshake(rw io.ReadWriter, token string) (version string, pid uint32, err error) {
	typ, p, err := readFrame(rw)
	if err != nil {
		return "", 0, err
	}
	if typ != hmHello {
		return "", 0, errors.New("ekran yardimcisi: ilk mesaj hello degil")
	}
	tok, version, pid, err := decodeHello(p)
	if err != nil {
		return "", 0, err
	}
	if !tokenEqual(tok, token) {
		_ = writeFrame(rw, hmHelloAck, []byte{0})
		return "", 0, errors.New("ekran yardimcisi: token gecersiz")
	}
	return version, pid, writeFrame(rw, hmHelloAck, []byte{1})
}

// dialHandshake (yardimci tarafi): token'i sunar ve kabulu bekler.
func dialHandshake(rw io.ReadWriter, token, version string, pid uint32) error {
	if err := writeFrame(rw, hmHello, encodeHello(token, version, pid)); err != nil {
		return err
	}
	typ, p, err := readFrame(rw)
	if err != nil {
		return err
	}
	if typ != hmHelloAck || len(p) != 1 || p[0] != 1 {
		return errors.New("ekran yardimcisi: servis el sikismayi reddetti")
	}
	return nil
}

// --- guvenlik ---

var (
	sidRe    = regexp.MustCompile(`^S-1-[0-9]+(-[0-9]+)+$`)
	pipeRe   = regexp.MustCompile(`^\\\\\.\\pipe\\zorven-screen-[0-9a-f]{16,64}$`)
	tokenRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errBadSD = errors.New("gecersiz SID")
)

// helperPipeSDDL, pipe'a yalniz SYSTEM ve verilen kullanici SID'ine tam erisim
// veren korumali (miras almayan) DACL. Digerleri (Administrators dahil) erisemez.
func helperPipeSDDL(userSID string) (string, error) {
	if !sidRe.MatchString(userSID) {
		return "", errBadSD
	}
	return "D:P(A;;GA;;;SY)(A;;GA;;;" + userSID + ")", nil
}

// validHelperArgs, yardimci komutunun bagimsiz degiskenlerini dogrular.
func validHelperArgs(pipe, token string) error {
	if !pipeRe.MatchString(pipe) || !tokenRe.MatchString(strings.ToLower(token)) || token != strings.ToLower(token) {
		return errors.New("invalid screen-helper arguments")
	}
	return nil
}

// --- capture yaniti ---

// capResp, capture yanitinin cozulmus hali.
type capResp struct {
	Flags    byte
	Bounds   image.Rectangle // monitor siniri
	Displays int
	Method   string
	W, H     int // tam goruntu boyutu
	Rects    []image.Rectangle
	Pix      [][]byte // her dikdortgen icin siki paketlenmis RGBA (Dx*Dy*4)
}

func encodeCapOK(c capResp) []byte {
	var b wbuf
	b.u8(0)
	b.u8(c.Flags)
	b.rect(c.Bounds)
	b.u16(uint16(c.Displays))
	b.str(c.Method)
	b.i32(c.W)
	b.i32(c.H)
	b.u32(uint32(len(c.Rects)))
	for i, r := range c.Rects {
		b.rect(r)
		b.Write(c.Pix[i])
	}
	return b.Bytes()
}

func encodeErr(msg string) []byte {
	var b wbuf
	b.u8(1)
	b.str(msg)
	return b.Bytes()
}

// decodeCapResp, yaniti cozer; yardimci hata dondurduysa remoteErr dolu gelir.
func decodeCapResp(p []byte) (c capResp, remoteErr string, err error) {
	r := rbuf{b: p}
	if r.u8() != 0 {
		return c, r.str(), r.err
	}
	c.Flags = r.u8()
	c.Bounds = r.rect()
	c.Displays = int(r.u16())
	c.Method = r.str()
	c.W, c.H = r.i32(), r.i32()
	n := int(r.u32())
	if r.err != nil || n < 0 || n > 4096 || c.W < 0 || c.H < 0 || c.W*c.H > 1<<28 {
		return c, "", errHelperFrame
	}
	for i := 0; i < n; i++ {
		rc := r.rect()
		if r.err != nil || rc.Dx() < 0 || rc.Dy() < 0 || rc.Dx()*rc.Dy() > c.W*c.H ||
			rc.Min.X < 0 || rc.Min.Y < 0 || rc.Max.X > c.W || rc.Max.Y > c.H {
			return c, "", errHelperFrame
		}
		px := r.take(rc.Dx() * rc.Dy() * 4)
		if r.err != nil {
			return c, "", r.err
		}
		c.Rects = append(c.Rects, rc)
		c.Pix = append(c.Pix, px)
	}
	return c, "", r.err
}

// copyRect, img'den rc bolgesini siki paketli RGBA olarak cikarir.
func copyRect(img *image.RGBA, rc image.Rectangle) []byte {
	out := make([]byte, rc.Dx()*rc.Dy()*4)
	for y := 0; y < rc.Dy(); y++ {
		so := (rc.Min.Y+y-img.Rect.Min.Y)*img.Stride + (rc.Min.X-img.Rect.Min.X)*4
		copy(out[y*rc.Dx()*4:(y+1)*rc.Dx()*4], img.Pix[so:so+rc.Dx()*4])
	}
	return out
}

// pasteRect, siki paketli px'i canvas'in rc bolgesine yazar.
func pasteRect(canvas *image.RGBA, rc image.Rectangle, px []byte) {
	for y := 0; y < rc.Dy(); y++ {
		do := (rc.Min.Y+y)*canvas.Stride + rc.Min.X*4
		copy(canvas.Pix[do:do+rc.Dx()*4], px[y*rc.Dx()*4:(y+1)*rc.Dx()*4])
	}
}
