package api

import "net/http"

// ACIK SURUM: webmail'de plan siniri yoktur (harici gonderim serbest; sahip
// karari, docs/open-source-guideline.md). Ticari surumdeki plan kontrolleri
// ve Pro deneme sablonu burada yer almaz.

// canSendExternalMail: acikta her kiraci harici alicilara gonderebilir.
func (s *Server) canSendExternalMail(_ *http.Request, _ string) bool { return true }

// externalMailDeniedMessage: acikta kullanilmaz (gonderim reddedilmez).
func (s *Server) externalMailDeniedMessage() string { return "harici e-posta gonderimi kapali" }

// mailTemplateHTML: acikta hazir (ticari) sablon yoktur.
func mailTemplateHTML(_ string) string { return "" }
