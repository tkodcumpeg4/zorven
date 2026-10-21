// Package mcptools, Zorven'in MCP (Model Context Protocol) arac kumesini tanimlar.
//
// Ayni arac tanimlari iki yerde kullanilir:
//   - sunucu (/mcp, uzak streamable HTTP): arka uc SUREC-ICI, API'nin kimlik
//     dogrulama zincirine sentetik istek atar; kapsam/kota/denetim mantigi REST
//     ile birebir ayni kalir.
//   - istemci (zorven mcp, stdio/yerel HTTP): arka uc REST API'ye HTTP ile gider.
//
// Araclar yalnizca REST API'nin token'a acik uclarina eslenir; silme araclari
// BILEREK yoktur (ilk surum karari). Arac ciktilari kullanici denetimli metin
// (tunel adi, istek yolu, baslik) icerir: model baglamina girdigi icin guvenilmez
// veri sayilir ve boyut/baslik kisaltmasi uygulanir.
package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Request, REST API'ye yapilacak tek bir cagri.
type Request struct {
	Method string
	Path   string // "/api/v1/..." ile baslar
	Query  url.Values
	Body   any // nil degilse JSON olarak gonderilir
	// Project, X-Zorven-Project basligi (proje slug'i veya kimligi); bos = varsayilan.
	Project string
}

// Response, REST API yaniti (govde ham).
type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// Backend, arac cagrilarini gercek API'ye ileten katman.
type Backend interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// maxResponseBytes, ust akistan okunan govde tavani (bellek korumasi).
const maxResponseBytes = 8 << 20

// HTTPBackend, Backend'in REST API'ye HTTP ile giden uygulamasi (zorven mcp).
type HTTPBackend struct {
	BaseURL   string // or. https://zorven.app
	Token     string // API token (zrv_api_...)
	Client    *http.Client
	UserAgent string
}

// Do, istegi API'ye gonderir.
func (b *HTTPBackend) Do(ctx context.Context, r Request) (Response, error) {
	u := strings.TrimRight(b.BaseURL, "/") + r.Path
	if len(r.Query) > 0 {
		u += "?" + r.Query.Encode()
	}
	var body io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return Response{}, fmt.Errorf("istek govdesi kodlanamadi: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, u, body)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Project != "" {
		req.Header.Set("X-Zorven-Project", r.Project)
	}
	if b.UserAgent != "" {
		req.Header.Set("User-Agent", b.UserAgent)
	}
	c := b.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := c.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Response{}, err
	}
	return Response{Status: resp.StatusCode, Body: raw, Header: resp.Header}, nil
}
