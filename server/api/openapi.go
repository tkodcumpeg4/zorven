package api

import (
	_ "embed"
	"net/http"
)

// openapiYAML / openapiEnYAML, public REST API'nin OpenAPI 3.1 sozlesmesi (TR/EN).
// Tek dogruluk kaynagi bunlardir; docs sayfalari (Scalar) ve istemci uretimi
// bunlari tuketir.
//
//go:embed openapi.yaml
var openapiYAML []byte

//go:embed openapi.en.yaml
var openapiEnYAML []byte

// openapiSpec, GET /api/v1/openapi.yaml — kimlik dogrulamasiz (TR).
func (s *Server) openapiSpec(w http.ResponseWriter, _ *http.Request) {
	writeOpenAPI(w, openapiYAML)
}

// openapiSpecEN, GET /api/v1/openapi.en.yaml — kimlik dogrulamasiz (EN).
func (s *Server) openapiSpecEN(w http.ResponseWriter, _ *http.Request) {
	writeOpenAPI(w, openapiEnYAML)
}

func writeOpenAPI(w http.ResponseWriter, spec []byte) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(spec)
}
