package tunnel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/auth"
	"github.com/tkodcumpeg4/zorven/server/store"
)

type authErrStore struct {
	store.Store
	err error
}

func (a authErrStore) GetClientByTokenID(context.Context, string) (store.Client, error) {
	return store.Client{}, a.err
}

// DB hatasi 401 degil 503 olmali: 401 ajani kalici durdurur (FatalSessionError).
func TestAuthenticateDBHatasi503(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{errors.New("connection refused"), http.StatusServiceUnavailable},
		{store.ErrNotFound, http.StatusUnauthorized},
	}
	for _, c := range cases {
		h := &Handler{Store: authErrStore{err: c.err}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		req := httptest.NewRequest("GET", "/_tunnel/ws", nil)
		tok, _, _, err := auth.GenerateClient()
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		if _, ok := h.authenticate(rr, req); ok {
			t.Fatal("dogrulama basarili olmamali")
		}
		if rr.Code != c.want {
			t.Fatalf("hata %v: kod %d, beklenen %d", c.err, rr.Code, c.want)
		}
	}
}
