package updates

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBasics(t *testing.T) {
	got := RenderMarkdown("# Baslik\n\nMerhaba **kalin** ve *italik* `kod`.\n\n- bir\n- iki\n\n1. a\n2. b\n")
	for _, want := range []string{"<h2>Baslik</h2>", "<strong>kalin</strong>", "<em>italik</em>", "<code>kod</code>", "<ul>", "<li>iki</li>", "<ol>"} {
		if !strings.Contains(got, want) {
			t.Errorf("eksik %q:\n%s", want, got)
		}
	}
}

func TestRenderMarkdownXSS(t *testing.T) {
	cases := []string{
		"<script>alert(1)</script>",
		"[x](javascript:alert(1))",
		"[x](JaVaScRiPt:alert(1))",
		"[x](data:text/html;base64,AAAA)",
		"[x](vbscript:foo)",
		"**<img src=x onerror=alert(1)>**",
		"`<b>`",
		"```\n<script>x</script>\n```",
		"# <iframe src=//evil>",
		"[<img src=x onerror=1>](https://a.com)",
		"[x](https://a.com\" onclick=\"alert(1))",
		"> <svg onload=alert(1)>",
		"- <a href=\"javascript:1\">x</a>",
	}
	for _, c := range cases {
		got := RenderMarkdown(c)
		low := strings.ToLower(got)
		for _, bad := range []string{"<script", "<img", "<iframe", "<svg", "href=\"javascript", "data:text", "vbscript:", "onclick=\"", "<b>"} {
			if strings.Contains(low, bad) {
				t.Errorf("girdi %q: %q cikti icinde:\n%s", c, bad, got)
			}
		}
	}
}

func TestRenderMarkdownLinks(t *testing.T) {
	got := RenderMarkdown("[Zorven](https://zorven.app/x?a=1&b=2) [m](mailto:a@b.co)")
	if !strings.Contains(got, `href="https://zorven.app/x?a=1&amp;b=2"`) || !strings.Contains(got, `rel="noopener noreferrer nofollow"`) {
		t.Fatalf("baglanti hatali: %s", got)
	}
	if !strings.Contains(got, `href="mailto:a@b.co"`) {
		t.Fatalf("mailto hatali: %s", got)
	}
	if strings.Contains(RenderMarkdown("[x](ftp://a.com/f)"), "<a ") {
		t.Fatal("ftp baglantisi kabul edildi")
	}
}

func TestPlainText(t *testing.T) {
	if got := PlainText("## Hi\n**b** `c`"); got != "HI\nb c" {
		t.Fatalf("got %q", got)
	}
}
