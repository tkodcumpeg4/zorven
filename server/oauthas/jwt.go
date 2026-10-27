package oauthas

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// AccessTokenTTL, access token omru.
const AccessTokenTTL = time.Hour

var b64 = base64.RawURLEncoding

// claims, access token (RFC 9068) govdesi.
type claims struct {
	Iss   string `json:"iss"`
	Sub   string `json:"sub"`
	Aud   string `json:"aud"`
	Scope string `json:"scope"`
	Tid   string `json:"tid"`
	Gid   string `json:"gid"`
	Cid   string `json:"client_id"`
	Exp   int64  `json:"exp"`
	Iat   int64  `json:"iat"`
	Jti   string `json:"jti"`
}

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// keyID, genel anahtardan kid turetir.
func keyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

func signJWT(priv ed25519.PrivateKey, c claims) (string, error) {
	pub := priv.Public().(ed25519.PublicKey)
	h, _ := json.Marshal(jwtHeader{Alg: "EdDSA", Typ: "at+jwt", Kid: keyID(pub)})
	p, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	return in + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(in))), nil
}

// ErrInvalidToken, imza/bicim/sure/audience hatalarinin ortak hatasi.
var ErrInvalidToken = errors.New("oauthas: gecersiz access token")

// parseJWT imzayi dogrular ve govdeyi doner (iss/aud/exp denetimi cagiranda).
func parseJWT(pub ed25519.PublicKey, tok string) (claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return claims{}, ErrInvalidToken
	}
	hb, err := b64.DecodeString(parts[0])
	if err != nil {
		return claims{}, ErrInvalidToken
	}
	var h jwtHeader
	if json.Unmarshal(hb, &h) != nil || h.Alg != "EdDSA" || h.Typ != "at+jwt" || h.Kid != keyID(pub) {
		return claims{}, ErrInvalidToken
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return claims{}, ErrInvalidToken
	}
	pb, err := b64.DecodeString(parts[1])
	if err != nil {
		return claims{}, ErrInvalidToken
	}
	var c claims
	if json.Unmarshal(pb, &c) != nil {
		return claims{}, ErrInvalidToken
	}
	return c, nil
}

// UpstreamIdentity, MCP tunelinin upstream sunucusuna X-Zorven-Identity ile
// iletilen kisa omurlu (60 sn) imzali kimligin icerigi. typ="upstream+jwt" oldugu
// icin access token olarak ASLA kabul edilmez.
type UpstreamIdentity struct {
	Subject    string
	Email      string
	TenantID   string
	TunnelID   string
	ClientID   string
	Scope      string
	AuthMethod string
}

// UpstreamTokenTTL, upstream kimliginin omru.
const UpstreamTokenTTL = 60 * time.Second

// UpstreamAudience, upstream kimliginin aud degeri.
const UpstreamAudience = "zorven-upstream"

func signUpstream(priv ed25519.PrivateKey, iss string, id UpstreamIdentity, now time.Time, jti string) (string, error) {
	pub := priv.Public().(ed25519.PublicKey)
	h, _ := json.Marshal(jwtHeader{Alg: "EdDSA", Typ: "upstream+jwt", Kid: keyID(pub)})
	p, err := json.Marshal(map[string]any{
		"iss": iss, "aud": UpstreamAudience, "sub": id.Subject, "email": id.Email, "tid": id.TenantID,
		"tunnel": id.TunnelID, "client_id": id.ClientID, "scope": id.Scope, "amr": id.AuthMethod,
		"iat": now.Unix(), "exp": now.Add(UpstreamTokenTTL).Unix(), "jti": jti,
	})
	if err != nil {
		return "", err
	}
	in := b64.EncodeToString(h) + "." + b64.EncodeToString(p)
	return in + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(in))), nil
}
