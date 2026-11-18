#!/usr/bin/env python3
"""Arama/yapay zeka arama botlarinin YAYIMLADIGI IP araliklarini indirir ve
server/botguard/botranges.json dosyasini yeniden uretir.

Yalniz ters DNS ile dogrulanamayan botlar icindir (DuckDuckGo, OpenAI, Perplexity). Liste eskirse
tek sonuc ilgili botun dogrulanamayip sorgulanmasidir (guvenli taraf); birkac ayda bir calistirin:
    python3 scripts/update-bot-ranges.py && (cd server && go test ./botguard)
"""
import datetime, ipaddress, json, pathlib, sys, urllib.request

SOURCES = {
    "duckduckbot": ["https://duckduckgo.com/duckduckbot.json"],
    "oai-searchbot": ["https://openai.com/searchbot.json"],
    "chatgpt-user": ["https://openai.com/chatgpt-user.json"],
    "perplexitybot": ["https://www.perplexity.com/perplexitybot.json"],
    "perplexity-user": ["https://www.perplexity.com/perplexity-user.json"],
}
OUT = pathlib.Path(__file__).resolve().parent.parent / "server" / "botguard" / "botranges.json"


def fetch(url):
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0 (compatible; bot-range-update)"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode("utf-8"))


def main():
    bots = {}
    for name, urls in SOURCES.items():
        cidrs = set()
        for u in urls:
            for p in fetch(u).get("prefixes", []):
                v = p.get("ipv4Prefix") or p.get("ipv6Prefix")
                if not v:
                    continue
                net = ipaddress.ip_network(v, strict=False)
                # Asiri genis bir aralik (yanlislikla ya da kotu niyetle) korumayi delmesin.
                if (net.version == 4 and net.prefixlen < 16) or (net.version == 6 and net.prefixlen < 32):
                    print(f"UYARI: {name}: cok genis aralik atlandi: {v}", file=sys.stderr)
                    continue
                cidrs.add(str(net))
        if not cidrs:
            print(f"HATA: {name}: aralik bulunamadi", file=sys.stderr)
            return 1
        bots[name] = sorted(cidrs, key=lambda c: (ipaddress.ip_network(c).version, ipaddress.ip_network(c)))
    doc = {"snapshot": datetime.date.today().isoformat(), "sources": SOURCES, "bots": bots}
    OUT.write_text(json.dumps(doc, indent=1) + "\n", encoding="utf-8")
    print("yazildi:", OUT, {k: len(v) for k, v in bots.items()})
    return 0


if __name__ == "__main__":
    sys.exit(main())
