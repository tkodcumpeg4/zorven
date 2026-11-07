package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

func TestAbuseReporterIdentityAndCounts(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	in := store.AbuseReport{FQDN: "evil.example.test", Reason: "phishing", Category: "phishing",
		ReporterIP: "203.0.113.9", ReporterID: "0123456789abcdef0123456789abcdef",
		ReporterUA: "UA/1", ReporterTLS: "t13d", ReporterLang: "tr"}
	if _, err := s.CreateAbuseReportFull(ctx, in); err != nil {
		t.Fatal(err)
	}
	list, n, err := s.ListAbuseReportsPage(ctx, store.Page{Limit: 10})
	if err != nil || n < 1 {
		t.Fatalf("liste: %v %d", err, n)
	}
	got := list[0]
	if got.ReporterID != in.ReporterID || got.ReporterUA != "UA/1" || got.ReporterTLS != "t13d" || got.Category != "phishing" || got.ReporterLang != "tr" {
		t.Fatalf("alanlar okunmadi: %+v", got)
	}
	byIP, byID, same, err := s.AbuseReportCounts(ctx, time.Now().Add(-time.Hour), in.ReporterIP, in.ReporterID, in.FQDN)
	if err != nil || byIP != 1 || byID != 1 || same != 1 {
		t.Fatalf("sayilar: %d %d %d %v", byIP, byID, same, err)
	}
}
