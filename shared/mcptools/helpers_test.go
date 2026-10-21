package mcptools

import (
	"net/http"
	"net/http/httptest"
)

func newTestServer(h func(http.ResponseWriter, *http.Request)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(h))
}
