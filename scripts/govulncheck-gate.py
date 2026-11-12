#!/usr/bin/env python3
"""govulncheck JSON ciktisini bilinen-istisna listesine gore degerlendirir.

Kullanim: (cd server && govulncheck -format json ./...) | python3 scripts/govulncheck-gate.py server

Neden: govulncheck metin kipinde CAGRILAN her zafiyette cikis kodu 3 verir. Duzeltmesi henuz
yayimlanmamis ve riski degerlendirilmis bir bulgu (GO-2026-6909) haftalik isi her hafta kirmizi
yapar, gercek yeni bulgular gurultude kaybolur. Bu kapi:
  - CAGRILAN (izde fonksiyon olan) ve istisna listesinde OLMAYAN bulguda cikis 1 (kirmizi);
  - istisnadaki bulguyu uyari olarak yazar; istisnanin duzeltmesi yayimlandiysa ::warning ile
    "yukselt" der (C1 takibi);
  - yalniz "cagrilmiyorsa" istisna olan bir bulgu cagrilir hale gelirse kirmizi.
Yalniz standart kitaplik.
"""
import json
import sys

# id -> (cagrilsa da istisna mi, gerekce)
KNOWN = {
    "GO-2026-6909": (True, "crewjam/saml: duzeltme yayimlanmadi; panik korumasi + NameID dogrulamasi var, "
                           "SSO yalniz Enterprise (docs/plans/runtime-acik-kalanlar-plani.md C1)"),
    "GO-2026-5932": (False, "x/crypto/openpgp: kodumuz cagirmiyor; kararli surum cikinca yukselt (C2)"),
}


def stream(text):
    dec = json.JSONDecoder()
    i, n = 0, len(text)
    while i < n:
        while i < n and text[i].isspace():
            i += 1
        if i >= n:
            break
        obj, i = dec.raw_decode(text, i)
        yield obj


def main():
    module = sys.argv[1] if len(sys.argv) > 1 else "?"
    text = sys.stdin.read()
    if not text.strip():
        print("::error::%s: govulncheck ciktisi bos (tarama calismadi mi?)" % module)
        return 2
    called, uncalled, fixed = {}, set(), {}
    for obj in stream(text):
        f = obj.get("finding")
        if not f:
            continue
        osv = f.get("osv", "")
        trace = f.get("trace") or []
        if f.get("fixed_version"):
            fixed[osv] = f["fixed_version"]
        if trace and trace[0].get("function"):
            fr = trace[0]
            called.setdefault(osv, "%s.%s" % (fr.get("package", fr.get("module", "")), fr["function"]))
        else:
            uncalled.add(osv)
    rc = 0
    for osv, where in sorted(called.items()):
        known = KNOWN.get(osv)
        if known and known[0]:
            print("istisna (cagriliyor): %s %s -- %s" % (osv, where, known[1]))
        elif known:
            print("::error::%s: %s artik CAGRILIYOR (%s); istisna yalniz cagrilmazken gecerliydi" % (module, osv, where))
            rc = 1
        else:
            print("::error::%s: YENI cagrilan zafiyet %s (%s) duzeltme=%s" % (module, osv, where, fixed.get(osv, "yok")))
            rc = 1
    for osv in sorted(uncalled - set(called)):
        print("bilgi (cagrilmiyor): %s %s" % (osv, KNOWN.get(osv, (None, ""))[1]))
    for osv in sorted(set(KNOWN) & set(fixed)):
        if KNOWN[osv][0] and osv in called:
            print("::warning::%s: istisnadaki %s icin duzeltme yayimlandi (%s): bagimliligi yukseltip istisnayi kaldirin"
                  % (module, osv, fixed[osv]))
    print("%s: cagrilan=%d (istisna disi %s) cagrilmayan=%d" % (
        module, len(called), "VAR" if rc else "yok", len(uncalled - set(called))))
    return rc


if __name__ == "__main__":
    sys.exit(main())
