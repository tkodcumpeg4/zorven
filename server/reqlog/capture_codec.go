package reqlog

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Kalici yakalama kodlayicisi: govde gzip (>= minCompress bayt) + AES-256-GCM.
// Basliklar ve meta (host, istemci IP, yontem, yol, sorgu) de sifrelenir: Authorization,
// cerez ve yol icindeki jetonlar DB'de duz metin durmaz. Her alan
// nonce(12)||sifreli metin bicimindedir; AAD = "tenant|id|alan" (satir baska
// satira/kiraciya kopyalanirsa cozulemez).

// CaptureKeyVersion, su an kullanilan sifreleme anahtari surumu.
const CaptureKeyVersion = 1

const minCompress = 128

// ErrCaptureNoKey, kalici yakalama icin sifreleme anahtari yok.
var ErrCaptureNoKey = errors.New("yakalama sifreleme anahtari yok")

// CaptureRecord, DB'ye yazilan (sifreli) satirin bellek gosterimi.
type CaptureRecord struct {
	ID, TenantID, TunnelID string
	TS                     time.Time
	Meta, ReqHeaders       []byte // sifreli
	RespHeaders            []byte // sifreli
	ReqBody, RespBody      []byte // sifreli (nil: govde yok)
	EncKeyVersion          int
	Compressed             bool
	ReqTruncated           bool
	RespTruncated          bool
	ReqSize, RespSize      int // ham (sikistirilmamis) boyut
	StoredBytes            int64
}

// CaptureCodec, Capture <-> CaptureRecord donusturucusu.
type CaptureCodec struct{ aead cipher.AEAD }

// NewCaptureCodec, 32 baytlik anahtardan kodlayici olusturur.
func NewCaptureCodec(key []byte) (*CaptureCodec, error) {
	if len(key) != 32 {
		return nil, ErrCaptureNoKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CaptureCodec{aead: g}, nil
}

type captureMeta struct {
	Hostname   string `json:"h,omitempty"`
	ClientIP   string `json:"ip,omitempty"`
	Method     string `json:"m,omitempty"`
	Path       string `json:"p,omitempty"`
	Query      string `json:"q,omitempty"`
	Status     int    `json:"s,omitempty"`
	DurationMS int64  `json:"d,omitempty"`
}

func (k *CaptureCodec) seal(plain []byte, aad string) ([]byte, error) {
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, plain, []byte(aad)), nil
}

func (k *CaptureCodec) open(enc []byte, aad string) ([]byte, error) {
	ns := k.aead.NonceSize()
	if len(enc) < ns {
		return nil, errors.New("sifreli alan kisa")
	}
	return k.aead.Open(nil, enc[:ns], enc[ns:], []byte(aad))
}

func aadFor(tenant, id, field string) string { return tenant + "|" + id + "|" + field }

// sealBody: 1 bayt etiket (0 ham, 1 gzip) + veri, sonra GCM.
func (k *CaptureCodec) sealBody(body []byte, aad string) (out []byte, gz bool, err error) {
	if len(body) == 0 {
		return nil, false, nil
	}
	payload := append([]byte{0}, body...)
	if len(body) >= minCompress {
		var buf bytes.Buffer
		buf.WriteByte(1)
		zw := gzip.NewWriter(&buf)
		if _, err := zw.Write(body); err == nil && zw.Close() == nil && buf.Len() < len(payload) {
			payload, gz = buf.Bytes(), true
		}
	}
	out, err = k.seal(payload, aad)
	return out, gz, err
}

func (k *CaptureCodec) openBody(enc []byte, aad string) ([]byte, error) {
	if len(enc) == 0 {
		return nil, nil
	}
	p, err := k.open(enc, aad)
	if err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, errors.New("bos govde yuku")
	}
	if p[0] == 0 {
		return p[1:], nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(p[1:]))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(io.LimitReader(zr, 64<<20))
}

func (k *CaptureCodec) sealJSON(v any, aad string) ([]byte, error) {
	j, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return k.seal(j, aad)
}

// Encode, yakalamayi sifreli satira cevirir.
func (k *CaptureCodec) Encode(c *Capture) (*CaptureRecord, error) {
	r := &CaptureRecord{
		ID: c.ID, TenantID: c.TenantID, TunnelID: c.TunnelID, TS: c.TS,
		EncKeyVersion: CaptureKeyVersion,
		ReqTruncated:  c.ReqBodyTruncated, RespTruncated: c.RespBodyTruncated,
		ReqSize: len(c.ReqBody), RespSize: len(c.RespBody),
	}
	var err error
	if r.Meta, err = k.sealJSON(captureMeta{c.Hostname, c.ClientIP, c.Method, c.Path, c.Query, c.Status, c.DurationMS},
		aadFor(c.TenantID, c.ID, "meta")); err != nil {
		return nil, err
	}
	if r.ReqHeaders, err = k.sealJSON(c.ReqHeaders, aadFor(c.TenantID, c.ID, "reqh")); err != nil {
		return nil, err
	}
	if r.RespHeaders, err = k.sealJSON(c.RespHeaders, aadFor(c.TenantID, c.ID, "resph")); err != nil {
		return nil, err
	}
	var gz1, gz2 bool
	if r.ReqBody, gz1, err = k.sealBody(c.ReqBody, aadFor(c.TenantID, c.ID, "reqb")); err != nil {
		return nil, err
	}
	if r.RespBody, gz2, err = k.sealBody(c.RespBody, aadFor(c.TenantID, c.ID, "respb")); err != nil {
		return nil, err
	}
	r.Compressed = gz1 || gz2
	r.StoredBytes = int64(len(r.Meta) + len(r.ReqHeaders) + len(r.RespHeaders) + len(r.ReqBody) + len(r.RespBody))
	return r, nil
}

// Decode, satiri Capture'a cozer.
func (k *CaptureCodec) Decode(r *CaptureRecord) (*Capture, error) {
	if r.EncKeyVersion != CaptureKeyVersion {
		return nil, fmt.Errorf("bilinmeyen yakalama anahtar surumu %d", r.EncKeyVersion)
	}
	c := &Capture{ID: r.ID, TenantID: r.TenantID, TunnelID: r.TunnelID, TS: r.TS,
		ReqBodyTruncated: r.ReqTruncated, RespBodyTruncated: r.RespTruncated}
	var m captureMeta
	b, err := k.open(r.Meta, aadFor(r.TenantID, r.ID, "meta"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	c.Hostname, c.ClientIP, c.Method, c.Path, c.Query, c.Status, c.DurationMS =
		m.Hostname, m.ClientIP, m.Method, m.Path, m.Query, m.Status, m.DurationMS
	if b, err = k.open(r.ReqHeaders, aadFor(r.TenantID, r.ID, "reqh")); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.ReqHeaders); err != nil {
		return nil, err
	}
	if b, err = k.open(r.RespHeaders, aadFor(r.TenantID, r.ID, "resph")); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &c.RespHeaders); err != nil {
		return nil, err
	}
	if c.ReqBody, err = k.openBody(r.ReqBody, aadFor(r.TenantID, r.ID, "reqb")); err != nil {
		return nil, err
	}
	if c.RespBody, err = k.openBody(r.RespBody, aadFor(r.TenantID, r.ID, "respb")); err != nil {
		return nil, err
	}
	return c, nil
}
