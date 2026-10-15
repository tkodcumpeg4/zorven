package updates

import (
	"strings"
	"testing"
)

func TestRenderMarkdownLinkTable(t *testing.T) {
	const rel = ` rel="noopener noreferrer nofollow" target="_blank"`
	cases := []struct {
		name, in string
		want     []string // cikti bunlari icermeli
		not      []string // cikti bunlari icermemeli
	}{
		{"reddedilen sonra gecerli", "<script>alert(1)</script> [bagl](javascript:alert(1)) [site](https://zorven.app)",
			[]string{"&lt;script&gt;", `<a href="https://zorven.app"` + rel + `>site</a>`, "bagl "}, []string{"<script", "javascript", "bagl)"}},
		{"ic ice parantez url", "[w](https://en.wikipedia.org/wiki/Go_(language)) son",
			[]string{`href="https://en.wikipedia.org/wiki/Go_(language)"`, "</a> son"}, nil},
		{"etikette kose parantez", "[a [b] c](https://x.io)", []string{`href="https://x.io"`, ">a [b] c</a>"}, nil},
		{"etikette yuvarlak parantez", "[f(x)](https://x.io)", []string{">f(x)</a>"}, nil},
		{"birden fazla", "[a](https://a.io) ve [b](http://b.io) ve [c](mailto:c@d.io)",
			[]string{`href="https://a.io"`, `href="http://b.io"`, `href="mailto:c@d.io"`}, nil},
		{"kalin icinde", "**[a](https://a.io)**", []string{"<strong><a href=\"https://a.io\"", "</a></strong>"}, nil},
		{"italik icinde", "*[a](https://a.io)*", []string{"<em><a href=\"https://a.io\""}, nil},
		{"etiket kalin", "[**a**](https://a.io)", []string{"<strong>a</strong></a>"}, nil},
		{"unicode", "[Sürüm notları ✓ 日本語](https://zorven.app/ğ)", []string{"Sürüm notları ✓ 日本語</a>", `href="https://zorven.app/%C4%9F"`}, nil},
		{"kapanmayan", "[a](https://a.io ve [b", []string{"[a](https://a.io ve [b"}, []string{"<a "}},
		{"etiket ici baglanti ic ice olmaz", "[x [y](https://i.io)](https://o.io)", []string{`href="https://o.io"`}, []string{`href="https://i.io"`}},
		{"nitelik kacirma", `[a](https://a.io/"onmouseover="x)`, nil, []string{"<a ", `onmouseover="x`}},
		{"buyuk kucuk harf", "[a](JaVaScRiPt:alert(1))", nil, []string{"<a ", "href"}},
		{"tab", "[a](java\tscript:alert(1))", nil, []string{"<a ", "href"}},
		{"bosluk", "[a]( javascript:alert(1))", nil, []string{"<a ", "href"}},
		{"varlik", "[a](&#106;avascript:alert(1))", nil, []string{"<a ", "href"}},
		{"varlik hex", "[a](&#x6A;avascript:alert(1))", nil, []string{"<a ", "href"}},
		{"data", "[a](data:text/html;base64,PHNjcmlwdD4=)", nil, []string{"<a ", "href"}},
		{"vbscript", "[a](vbscript:msgbox(1))", nil, []string{"<a ", "href"}},
		{"protokolsuz", "[a](//evil.io/x)", nil, []string{"<a "}},
		{"etiket html", "[<img src=x onerror=alert(1)>](https://a.io)", []string{"&lt;img"}, []string{"<img"}},
		{"otomatik baglanti yok", "<https://a.io> ve https://b.io", []string{"&lt;https://a.io&gt;"}, []string{"<a "}},
	}
	for _, c := range cases {
		got := RenderMarkdown(c.in)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q yok: %s", c.name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %q olmamali: %s", c.name, n, got)
			}
		}
	}
}
