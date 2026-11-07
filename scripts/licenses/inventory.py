#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Ucuncu taraf lisans envanteri -> THIRD_PARTY_LICENSES.md

Ag erisimi GEREKMEZ: Go modulleri yerel modul onbelleginden (go list), npm paketleri
web/package-lock.json + web/node_modules/*/package.json uzerinden okunur.

  python3 scripts/licenses/inventory.py                 # THIRD_PARTY_LICENSES.md yazar
  python3 scripts/licenses/inventory.py --out X.md
  python3 scripts/licenses/inventory.py --check         # sorunlu lisans varsa cikis kodu 1

Kapsam:
  * Go: shared, server, client, desktop. Uretimde derlenen modulleri `go list -deps`
    (linux/windows/darwin birlesimi, testler haric) ile bulur; modul grafiginde olup hic
    derlenmeyenleri (`go list -m all`) ayri, kisa bir bolumde sayar.
  * npm: yalniz URETIM bagimliliklari (package-lock.json'da "dev": true OLMAYANLAR).
    Lisans: node_modules/<ad>/package.json "license"; yoksa lock dosyasindaki alan;
    o da yoksa LICENSE dosyasi metninden.

Sinif:
  izinli       MIT, BSD-*, ISC, Apache-2.0, 0BSD, Unlicense, CC0, BlueOak, Zlib, Python-2.0, CC-BY-4.0 ...
  dikkat       MPL, LGPL, EPL, CDDL (zayif copyleft: dinamik baglama/degistirme kosullari incelenmeli)
  uyumsuz      GPL, AGPL, SSPL, BUSL, "proprietary", "UNLICENSED", lisanssiz
  bilinmiyor   metinden/alandan tespit edilemedi
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent
GO_MODULES = ["shared", "server", "client", "desktop"]
FIRST_PARTY = ("github.com/tkodcumpeg4/",)
LICENSE_FILES = ("LICENSE", "LICENSE.md", "LICENSE.txt", "LICENCE", "LICENCE.md", "COPYING",
                 "COPYING.md", "UNLICENSE", "LICENSE-MIT", "LICENSE-APACHE", "LICENSE.BSD")


def classify_text(t: str) -> str:
    """Lisans metninden SPDX benzeri kimlik; tespit edilemezse ''."""
    low = " ".join(t.lower().split())
    # MPL ve Apache metinleri "GNU ... GPL" ifadelerini (ikincil lisans / uyumluluk) anar:
    # once bunlari ele.
    if "mozilla public license" in low:
        return "MPL-2.0" if "2.0" in low else "MPL"
    if "apache license" in low and "version 2.0" in low:
        return "Apache-2.0"
    if "gnu affero general public license" in low:
        return "AGPL-3.0"
    if "gnu lesser general public license" in low:
        return "LGPL"
    if "gnu general public license" in low:
        return "GPL-3.0" if "version 3" in low else ("GPL-2.0" if "version 2" in low else "GPL")
    if "mozilla public license" in low:
        return "MPL-2.0" if "2.0" in low else "MPL"
    if "eclipse public license" in low:
        return "EPL"
    if "server side public license" in low:
        return "SSPL"
    if "apache license" in low and "version 2.0" in low:
        return "Apache-2.0"
    if "the unlicense" in low or "this is free and unencumbered software" in low:
        return "Unlicense"
    if "creative commons" in low and "cc0" in low:
        return "CC0-1.0"
    if "blue oak model license" in low:
        return "BlueOak-1.0.0"
    if "permission to use, copy, modify, and/or distribute" in low or "permission to use, copy, modify, and distribute this software for any purpose with or without fee" in low:
        return "ISC"
    if "redistribution and use in source and binary forms" in low:
        if "neither the name" in low or "may be used to endorse" in low:
            return "BSD-3-Clause"
        return "BSD-2-Clause"
    if "permission is hereby granted, free of charge" in low:
        return "MIT"
    if "altered source versions must be plainly marked" in low or ("zlib" in low and "altered source versions" in low):
        return "Zlib"
    return ""


def classify_spdx(expr: str) -> str:
    """Bir SPDX ifadesinin sinifi (OR: en gevsek secenek)."""
    e = expr.strip().strip("()")
    if not e:
        return "bilinmiyor"
    alts = [a.strip(" ()") for a in re.split(r"\s+OR\s+", e, flags=re.I)]
    best = None
    order = {"izinli": 0, "dikkat": 1, "bilinmiyor": 2, "uyumsuz": 3}
    for a in alts:
        parts = [p.strip(" ()") for p in re.split(r"\s+AND\s+", a, flags=re.I)]
        worst = "izinli"
        for p in parts:
            c = classify_one(p)
            if order[c] > order[worst]:
                worst = c
        if best is None or order[worst] < order[best]:
            best = worst
    return best or "bilinmiyor"


PERMISSIVE = re.compile(r"^(MIT|MIT-0|BSD(-[0-9]-Clause.*)?|ISC|Apache-2\.0|0BSD|Unlicense|CC0-1\.0|"
                        r"BlueOak-1\.0\.0|Zlib|Python-2\.0|CC-BY-[0-9.]+|WTFPL|MIT-CMU|Artistic-2\.0|Unicode.*|BSL-1\.0|X11|libpng.*|OFL-1\.1|HPND)$", re.I)
WEAK = re.compile(r"^(MPL.*|LGPL.*|EPL.*|CDDL.*|CPL.*|OSL.*)$", re.I)
BAD = re.compile(r"^(A?GPL.*|SSPL.*|BUSL.*|UNLICENSED|PROPRIETARY|SEE LICENSE.*|NOASSERTION|CC-BY-NC.*|CC-BY-ND.*)$", re.I)


def classify_one(lic: str) -> str:
    if not lic or lic.lower() in ("unknown", "none"):
        return "bilinmiyor"
    if BAD.match(lic):
        # LGPL ayri sinif; GPL deseni LGPL'yi de yakalayabilir
        return "dikkat" if lic.upper().startswith("LGPL") else "uyumsuz"
    if WEAK.match(lic):
        return "dikkat"
    if PERMISSIVE.match(lic):
        return "izinli"
    return "bilinmiyor"


def run(cmd, cwd, env_extra=None):
    env = dict(os.environ)
    env["GOFLAGS"] = env.get("GOFLAGS", "")
    env["GOPROXY"] = "off"   # ag yok: yalniz modul onbellegi
    if env_extra:
        env.update(env_extra)
    return subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True)


def read_license_dir(d: Path) -> tuple[str, str]:
    """(kimlik, dosya adi). Birden cok dosya varsa (cift lisans) kimlikler ' OR ' ile birlesir."""
    ids, names = [], []
    if not d.is_dir():
        return "", ""
    for f in sorted(d.iterdir()):
        if f.is_file() and (f.name.upper() in LICENSE_FILES or f.name.upper().startswith(("LICENSE", "LICENCE", "COPYING", "UNLICENSE"))):
            try:
                t = f.read_text(encoding="utf-8", errors="replace")
            except OSError:
                continue
            ident = classify_text(t[:20000])
            if ident and ident not in ids:
                ids.append(ident)
            names.append(f.name)
    if not ids:
        return "", ",".join(names)
    # Ayni dizinde birden cok kimlik: dual-license varsayilir (OR).
    return " OR ".join(ids), ",".join(names)


def go_inventory():
    mods = {}       # path -> {version, dir, used_by:set, in_build:bool}
    for m in GO_MODULES:
        d = ROOT / m
        if not (d / "go.mod").exists():
            continue
        r = run(["go", "list", "-m", "-json", "all"], d)
        if r.returncode != 0:
            print(f"UYARI: {m}: go list -m basarisiz: {r.stderr[:200]}", file=sys.stderr)
            continue
        dec = json.JSONDecoder()
        s, i = r.stdout, 0
        while i < len(s):
            while i < len(s) and s[i].isspace():
                i += 1
            if i >= len(s):
                break
            o, i = dec.raw_decode(s, i)
            p = o.get("Path", "")
            if o.get("Main") or p.startswith(FIRST_PARTY):
                continue
            rep = o.get("Replace") or {}
            ver = rep.get("Version") or o.get("Version", "")
            dirp = rep.get("Dir") or o.get("Dir") or ""
            e = mods.setdefault(p, {"version": ver, "dir": dirp, "used_by": set(), "in_build": False})
            e["used_by"].add(m)
            if not e["dir"] and dirp:
                e["dir"] = dirp
        # uretimde derlenenler (testler haric), uc isletim sistemi
        for goos in ("linux", "windows", "darwin"):
            r = run(["go", "list", "-deps", "-test=false", "-f", "{{with .Module}}{{.Path}}{{end}}", "./..."],
                    d, {"GOOS": goos, "CGO_ENABLED": "0" if goos != "linux" else "1"})
            for line in r.stdout.split():
                if line in mods:
                    mods[line]["in_build"] = True
    rows = []
    for p, e in sorted(mods.items()):
        lic, fn = read_license_dir(Path(e["dir"])) if e["dir"] else ("", "")
        if not lic and e["dir"] and Path(e["dir"]).is_dir():
            lic = ""   # LICENSE var ama tanimlanamadi
        cls = classify_spdx(lic) if lic else "bilinmiyor"
        note = ""
        if not e["dir"] or not Path(e["dir"]).is_dir():
            note = "modul onbellekte yok"
        elif not fn:
            note = "LICENSE dosyasi yok"
        elif not lic:
            note = f"{fn}: metin taninmadi"
        rows.append({"name": p, "version": e["version"], "license": lic or "?", "class": cls,
                     "used_by": ",".join(sorted(e["used_by"])), "in_build": e["in_build"], "note": note})
    return rows


def npm_inventory():
    web = ROOT / "web"
    lock = web / "package-lock.json"
    if not lock.exists():
        return [], "web/package-lock.json yok"
    data = json.loads(lock.read_text(encoding="utf-8"))
    pk = data.get("packages", {})
    nm_ok = (web / "node_modules").exists()
    rows = []
    for key, v in sorted(pk.items()):
        if not key or "node_modules/" not in key:
            continue
        if v.get("dev"):
            continue
        name = key.split("node_modules/")[-1]
        lic = ""
        src = "lock"
        pj = web / key / "package.json"
        if pj.exists():
            try:
                j = json.loads(pj.read_text(encoding="utf-8"))
                l = j.get("license") or j.get("licenses")
                if isinstance(l, dict):
                    l = l.get("type")
                if isinstance(l, list):
                    l = " OR ".join(x.get("type", "") if isinstance(x, dict) else str(x) for x in l)
                lic = l or ""
                src = "package.json"
            except (ValueError, OSError):
                pass
        if not lic:
            lic = v.get("license") if isinstance(v.get("license"), str) else ""
        if not lic and (web / key).is_dir():
            lic, _ = read_license_dir(web / key)
            src = "LICENSE metni"
        cls = classify_spdx(lic) if lic else "bilinmiyor"
        rows.append({"name": name, "version": v.get("version", ""), "license": lic or "?", "class": cls,
                     "used_by": "web", "in_build": True, "note": "" if lic else
                     ("node_modules yok" if not nm_ok else "lisans alani yok"),
                     "optional": bool(v.get("optional"))})
    return rows, ("" if nm_ok else "web/node_modules yok: lisanslar yalniz package-lock.json alanindan okundu")


def table(rows, cols):
    out = ["| " + " | ".join(h for h, _ in cols) + " |", "|" + "|".join("---" for _ in cols) + "|"]
    for r in rows:
        out.append("| " + " | ".join(str(f(r)).replace("|", "\\|") for _, f in cols) + " |")
    return "\n".join(out)


def summarize(rows):
    c = defaultdict(int)
    for r in rows:
        c[r["license"]] += 1
    return ", ".join(f"{k}: {v}" for k, v in sorted(c.items(), key=lambda kv: -kv[1]))


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--out", default=str(ROOT / "THIRD_PARTY_LICENSES.md"))
    ap.add_argument("--check", action="store_true", help="uyumsuz/bilinmeyen varsa cikis kodu 1")
    ap.add_argument("--no-go", action="store_true")
    ap.add_argument("--no-npm", action="store_true")
    a = ap.parse_args()

    go_rows = [] if a.no_go else go_inventory()
    npm_rows, npm_note = ([], "") if a.no_npm else npm_inventory()
    go_build = [r for r in go_rows if r["in_build"]]
    go_graph = [r for r in go_rows if not r["in_build"]]

    # Modul onbellekte hic yoksa (yalniz go.mod grafiginde) uretime girmez: tek tek listelenmez.
    uncached = [r for r in go_rows if not r["in_build"] and r["note"] == "modul onbellekte yok"]
    problems = [dict(r, eco="Go") for r in go_rows
                if r["class"] in ("uyumsuz", "bilinmiyor", "dikkat") and r not in uncached]
    problems += [dict(r, eco="npm") for r in npm_rows if r["class"] in ("uyumsuz", "bilinmiyor", "dikkat")]
    order = {"uyumsuz": 0, "bilinmiyor": 1, "dikkat": 2}
    problems.sort(key=lambda r: (order[r["class"]], r["eco"], r["name"]))

    L = []
    L.append("# Ucuncu Taraf Lisanslari\n")
    L.append("Bu dosya `python3 scripts/licenses/inventory.py` ile uretilir (ag erisimi gerektirmez). "
             "Elle duzenlemeyin; bagimlilik degisince yeniden uretin.\n")
    L.append(f"- Go: {len(go_build)} uretimde derlenen modul (+ {len(go_graph)} yalniz modul grafiginde)\n"
             f"- npm: {len(npm_rows)} uretim paketi (`dev` olmayanlar)\n")
    L.append("## Sorunlu / incelenecek lisanslar\n")
    nb = {c: sum(1 for p in problems if p["class"] == c) for c in ("uyumsuz", "bilinmiyor", "dikkat")}
    L.append(f"**Uyumsuz: {nb['uyumsuz']} - Bilinmeyen: {nb['bilinmiyor']} - Dikkat (zayif copyleft): {nb['dikkat']}**\n")
    if nb["uyumsuz"] == 0 and nb["bilinmiyor"] == 0:
        L.append("GPL/AGPL/SSPL ailesi ya da lisanssiz paket yok. `dikkat` satirlari dosya duzeyinde (MPL-2.0) "
                 "copyleft'tir: degistirilmeden kullanildiginda ticari/kapali dagitimi etkilemez; kaynak dosyalarina "
                 "yapilan degisiklikler yayinlanmalidir. `lightningcss*` panel derleme (Tailwind/Vite) aracidir, "
                 "calisma zamani paketine girmez.\n")
    if not problems:
        L.append("Sorunlu lisans yok.\n")
    else:
        L.append("Siniflar: **uyumsuz** (GPL/AGPL/SSPL/lisanssiz: kapali ticari dagitimla bagdasmaz), "
                 "**bilinmiyor** (otomatik tespit edilemedi: elle bakilmali), "
                 "**dikkat** (MPL/LGPL/EPL gibi zayif copyleft: degistirme/baglama kosullari).\n")
        L.append(table(problems, [("Sinif", lambda r: r["class"]), ("Ekosistem", lambda r: r["eco"]),
                                   ("Paket", lambda r: r["name"]), ("Surum", lambda r: r["version"]),
                                   ("Lisans", lambda r: r["license"]),
                                   ("Uretimde", lambda r: "evet" if r["in_build"] else "hayir (yalniz grafik)"),
                                   ("Not", lambda r: r["note"])]))
        L.append("")
    if uncached:
        L.append(f"> {len(uncached)} Go modulu yalniz go.mod grafiginde ve yerel modul onbelleginde yok "
                 "(testlerde/baska platformlarda gereken dolayli bagimliliklar; ikiliye girmez). "
                 "Lisanslari okunamadi; ilgili bolumde `?` olarak gorunur.\n")
    L.append("## Ozet\n")
    L.append(f"- Go (uretim): {summarize(go_build)}")
    L.append(f"- Go (yalniz grafik): {summarize(go_graph)}")
    L.append(f"- npm (uretim): {summarize(npm_rows)}\n")
    if npm_note:
        L.append(f"> npm notu: {npm_note}\n")
    L.append("## Go modulleri (uretimde derlenenler)\n")
    L.append(table(go_build, [("Modul", lambda r: r["name"]), ("Surum", lambda r: r["version"]),
                               ("Lisans", lambda r: r["license"]), ("Kullanan", lambda r: r["used_by"])]))
    L.append("\n## Go modulleri (yalniz modul grafiginde; uretim ikilisine girmez)\n")
    L.append(table(go_graph, [("Modul", lambda r: r["name"]), ("Surum", lambda r: r["version"]),
                               ("Lisans", lambda r: r["license"]), ("Kullanan", lambda r: r["used_by"])]))
    L.append("\n## npm (uretim bagimliliklari)\n")
    L.append(table(npm_rows, [("Paket", lambda r: r["name"]), ("Surum", lambda r: r["version"]),
                               ("Lisans", lambda r: r["license"])]))
    L.append("")
    Path(a.out).write_text("\n".join(L), encoding="utf-8")
    print(f"yazildi: {a.out}  go_uretim={len(go_build)} go_grafik={len(go_graph)} npm={len(npm_rows)} "
          f"sorunlu={len(problems)}")
    for p in problems:
        if p["class"] == "uyumsuz":
            print(f"  UYUMSUZ: {p['eco']} {p['name']}@{p['version']} {p['license']}")
    bad = [p for p in problems if p["class"] in ("uyumsuz", "bilinmiyor")]
    return 1 if (a.check and bad) else 0


if __name__ == "__main__":
    sys.exit(main())
