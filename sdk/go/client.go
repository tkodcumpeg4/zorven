// Package zorven, Zorven REST API (/api/v1) icin Go istemcisidir.
// Yalnizca standart kutuphane kullanir.
//
//	c := zorven.NewClient(zorven.Options{Token: os.Getenv("ZORVEN_TOKEN")})
//	page, err := c.Tunnels.List(ctx, nil)
package zorven

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL uretim API adresidir.
const DefaultBaseURL = "https://zorven.app/api/v1"

// Version SDK surumudur (User-Agent'ta gonderilir).
const Version = "0.1.0"

// Options Client ayarlaridir.
type Options struct {
	// BaseURL varsayilan DefaultBaseURL. Sonundaki "/" yoksayilir.
	BaseURL string
	// Token API anahtari (zrv_api_...). Authorization: Bearer olarak gider.
	Token string
	// Project verilirse X-Zorven-Project basligi olarak gider (proje slug'i).
	Project string
	// HTTPClient varsayilan: 30 sn zaman asimli http.Client.
	HTTPClient *http.Client
	// UserAgent verilirse SDK User-Agent'inin sonuna eklenir.
	UserAgent string
	// MaxRetries 429/5xx icin yeniden deneme sayisi. 0 = varsayilan (3), negatif = kapali.
	MaxRetries int
	// RetryBaseDelay ilk bekleme (ussel artar, tavan 30 sn). Varsayilan 500 ms.
	RetryBaseDelay time.Duration
}

// Client Zorven API istemcisi; es zamanli kullanima uygundur.
type Client struct {
	baseURL string
	token   string
	project string
	hc      *http.Client
	ua      string
	retries int
	delay   time.Duration

	Clients     *ClientsService
	Tunnels     *TunnelsService
	Hostnames   *HostnamesService
	IPAllowlist *IPAllowlistService
	Analytics   *AnalyticsService
	Meta        *MetaService
}

// NewClient yeni bir Client olusturur.
func NewClient(o Options) *Client {
	c := &Client{
		baseURL: strings.TrimRight(o.BaseURL, "/"),
		token:   o.Token,
		project: o.Project,
		hc:      o.HTTPClient,
		retries: o.MaxRetries,
		delay:   o.RetryBaseDelay,
		ua:      "zorven-go/" + Version,
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.hc == nil {
		c.hc = &http.Client{Timeout: 30 * time.Second}
	}
	if o.UserAgent != "" {
		c.ua += " " + o.UserAgent
	}
	if c.retries == 0 {
		c.retries = 3
	} else if c.retries < 0 {
		c.retries = 0
	}
	if c.delay <= 0 {
		c.delay = 500 * time.Millisecond
	}
	c.Clients = &ClientsService{c}
	c.Tunnels = &TunnelsService{c}
	c.Hostnames = &HostnamesService{c}
	c.IPAllowlist = &IPAllowlistService{c}
	c.Analytics = &AnalyticsService{c}
	c.Meta = &MetaService{c}
	return c
}

// APIError sunucunun {"error":{"code","message"}} yanitidir.
type APIError struct {
	Status     int    // HTTP durum kodu
	Code       string // ornek: insufficient_scope
	Message    string
	RequestID  string        // X-Request-Id
	RetryAfter time.Duration // Retry-After (varsa)
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("zorven: %d", e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Message != "" {
		s += ": " + e.Message
	}
	if e.RequestID != "" {
		s += " (request id " + e.RequestID + ")"
	}
	return s
}

// IsNotFound 404 mu.
func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// AsAPIError err zincirinden *APIError cikarir.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

type response struct {
	header http.Header
	body   []byte
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, in any) (*response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("zorven: istek govdesi: %w", err)
		}
		payload = b
	}
	// POST/PATCH 5xx'te tekrarlanmaz (cift kayit riski); 429'da her zaman tekrarlanir.
	idempotent := method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete

	var lastErr error
	for attempt := 0; ; attempt++ {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.ua)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		if c.project != "" {
			req.Header.Set("X-Zorven-Project", c.project)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.hc.Do(req)
		var wait time.Duration
		retry := false
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			retry = idempotent
		} else {
			b, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			switch {
			case rerr != nil:
				lastErr = rerr
				retry = idempotent
			case resp.StatusCode >= 200 && resp.StatusCode < 300:
				return &response{header: resp.Header, body: b}, nil
			default:
				ae := decodeAPIError(resp, b)
				lastErr = ae
				wait = ae.RetryAfter
				retry = resp.StatusCode == http.StatusTooManyRequests ||
					(idempotent && resp.StatusCode >= 500)
			}
		}
		if !retry || attempt >= c.retries {
			return nil, lastErr
		}
		if wait <= 0 {
			wait = c.delay << uint(attempt)
			if wait > 30*time.Second || wait <= 0 {
				wait = 30 * time.Second
			}
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func decodeAPIError(resp *http.Response, b []byte) *APIError {
	ae := &APIError{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-Id")}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &env) == nil && (env.Error.Code != "" || env.Error.Message != "") {
		ae.Code, ae.Message = env.Error.Code, env.Error.Message
	} else if s := strings.TrimSpace(string(b)); s != "" {
		if len(s) > 200 {
			s = s[:200]
		}
		ae.Message = s
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if n, err := strconv.Atoi(ra); err == nil && n >= 0 {
			ae.RetryAfter = time.Duration(n) * time.Second
		} else if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				ae.RetryAfter = d
			}
		}
	}
	return ae
}

// call istegi yapar ve JSON govdeyi out'a cozer (out nil ve govde bos olabilir).
func (c *Client) call(ctx context.Context, method, path string, q url.Values, in, out any) error {
	r, err := c.do(ctx, method, path, q, in)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(r.body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		return fmt.Errorf("zorven: yanit cozulemedi: %w", err)
	}
	return nil
}

// ListOptions sayfalama parametreleridir. Sifir deger = sunucu varsayilani
// (limit/cursor verilmezse sunucu tum listeyi doner).
type ListOptions struct {
	Limit  int
	Cursor string
}

func (o *ListOptions) values() url.Values {
	q := url.Values{}
	if o == nil {
		return q
	}
	if o.Limit > 0 {
		q.Set("limit", strconv.Itoa(o.Limit))
	}
	if o.Cursor != "" {
		q.Set("cursor", o.Cursor)
	}
	return q
}

// Page tek bir liste sayfasidir.
type Page[T any] struct {
	Items      []T
	NextCursor string // X-Next-Cursor
	HasMore    bool   // X-Has-More
}

func listPage[T any](ctx context.Context, c *Client, path string, q url.Values) (*Page[T], error) {
	r, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return nil, err
	}
	p := &Page[T]{
		NextCursor: r.header.Get("X-Next-Cursor"),
		HasMore:    strings.EqualFold(r.header.Get("X-Has-More"), "true"),
	}
	if len(bytes.TrimSpace(r.body)) > 0 {
		if err := json.Unmarshal(r.body, &p.Items); err != nil {
			return nil, fmt.Errorf("zorven: yanit cozulemedi: %w", err)
		}
	}
	return p, nil
}

// iterate tum sayfalari tek tek dolasir (iter.Seq2; for ... range ile kullanilir).
// Hata olursa (zero, err) uretilir ve dongu biter.
func iterate[T any](ctx context.Context, opts *ListOptions, fetch func(ListOptions) (*Page[T], error)) func(yield func(T, error) bool) {
	return func(yield func(T, error) bool) {
		var o ListOptions
		if opts != nil {
			o = *opts
		}
		if o.Limit <= 0 {
			o.Limit = 200
		}
		for {
			p, err := fetch(o)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, it := range p.Items {
				if !yield(it, nil) {
					return
				}
			}
			if !p.HasMore || p.NextCursor == "" {
				return
			}
			if err := ctx.Err(); err != nil {
				var zero T
				yield(zero, err)
				return
			}
			o.Cursor = p.NextCursor
		}
	}
}

// collect iterate sonucunu diziye toplar.
func collect[T any](seq func(yield func(T, error) bool)) ([]T, error) {
	var all []T
	var ferr error
	seq(func(v T, err error) bool {
		if err != nil {
			ferr = err
			return false
		}
		all = append(all, v)
		return true
	})
	return all, ferr
}
