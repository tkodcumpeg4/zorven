"""Tray ikonlarini uretir.

Uygulamanin kendi markasiyla ayni sekil (uc dugum + baglantilar), arka plan
rengi baglanti durumunu tasir. Ikonlar repoda BINARY olarak duruyor ama bu
betik sayesinde yeniden uretilebilir.

Calistirmak icin:  python icons/generate.py
"""
from PIL import Image, ImageDraw

# Uygulama token'lariyla ayni renkler (frontend/dist/style.css)
DURUMLAR = {
    "connected": (0x22, 0xC5, 0x5E),   # --accent
    "idle":      (0x73, 0x73, 0x73),   # notr gri
    "error":     (0xEF, 0x44, 0x44),   # --danger
}

BOYUT = 256          # once buyuk ciz, sonra kucult (kenar yumusatma icin)
ICO_BOYUTLARI = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64)]


def ikon_ciz(renk):
    img = Image.new("RGBA", (BOYUT, BOYUT), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # Yuvarlak kose kare zemin
    d.rounded_rectangle([0, 0, BOYUT - 1, BOYUT - 1], radius=BOYUT // 4.5, fill=renk + (255,))

    beyaz = (255, 255, 255, 255)
    kalinlik = BOYUT // 22

    # Uc dugum: ust orta, sol alt, sag alt
    ust  = (BOYUT * 0.50, BOYUT * 0.30)
    sol  = (BOYUT * 0.30, BOYUT * 0.72)
    sag  = (BOYUT * 0.70, BOYUT * 0.72)

    # Baglanti cizgileri (dugumlerin altinda kalsin diye once cizilir)
    for a, b in ((ust, sol), (ust, sag), (sol, sag)):
        d.line([a, b], fill=beyaz, width=kalinlik)

    # Dugumler
    r = BOYUT * 0.085
    for cx, cy in (ust, sol, sag):
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=renk + (255,), outline=beyaz, width=kalinlik)

    return img


def main():
    for ad, renk in DURUMLAR.items():
        img = ikon_ciz(renk)
        img.save(f"icons/tray-{ad}.ico", format="ICO", sizes=ICO_BOYUTLARI)
        # Linux/macOS systray PNG bekler
        img.resize((64, 64), Image.LANCZOS).save(f"icons/tray-{ad}.png", format="PNG")
        print(f"  uretildi: tray-{ad}.ico + .png")


if __name__ == "__main__":
    main()
