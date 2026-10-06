"""Zorven Python SDK.

Zorven CLI'sini ("zorven") saran ince bir kütüphane; yerel bir portu programatik
olarak tünelller ve herkese açık URL'i döner.

Gereksinim: `zorven` CLI kurulu olmalı (https://zorven.app/install.sh) veya
``bin`` parametresiyle tam yol verilmeli.

Örnek::

    import os, zorven
    t = zorven.connect(token=os.environ["ZORVEN_TOKEN"], port=8080)
    print(t.url)          # https://...zorven.app
    # ... iş bitince:
    t.close()
"""

from __future__ import annotations

import re
import subprocess
import threading
import time
from dataclasses import dataclass, field
from typing import List, Optional, Union

__all__ = ["connect", "Tunnel", "ZorvenError"]

_URL_RE = re.compile(r"^zorven-url:\s*(\S+)")


class ZorvenError(RuntimeError):
    """Zorven SDK hataları."""


@dataclass
class Tunnel:
    """Canlı bir tünel oturumu."""

    url: str
    urls: List[str] = field(default_factory=list)
    process: Optional[subprocess.Popen] = None

    def close(self) -> None:
        """Tüneli kapatır (CLI sürecini sonlandırır)."""
        if self.process and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()


def connect(
    token: str,
    port: Optional[Union[int, str]] = None,
    target: Optional[str] = None,
    server: Optional[str] = None,
    bin: str = "zorven",
    timeout: float = 30.0,
) -> Tunnel:
    """Yerel bir hedefi tünelller ve bağlantı kurulduğunda :class:`Tunnel` döner.

    ``port`` veya ``target`` verilmelidir. Bağlantı ``timeout`` saniye içinde
    kurulamazsa :class:`ZorvenError` yükseltilir.
    """
    if not token:
        raise ZorvenError("token zorunlu")
    tgt = str(target) if target is not None else (str(port) if port is not None else None)
    if not tgt:
        raise ZorvenError("port veya target zorunlu")

    args = [bin, tgt, "--token", token, "--print-url", "--no-auto-update"]
    if server:
        args += ["--server", server]

    try:
        proc = subprocess.Popen(
            args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, bufsize=1
        )
    except FileNotFoundError as exc:  # CLI yok
        raise ZorvenError(
            f"zorven CLI bulunamadı ({bin}). Kurulum: https://zorven.app/install.sh"
        ) from exc

    urls: List[str] = []
    result: dict = {}
    done = threading.Event()

    def _reader() -> None:
        assert proc.stdout is not None
        for line in proc.stdout:
            m = _URL_RE.match(line.strip())
            if m:
                urls.append(m.group(1))
                if "url" not in result:
                    result["url"] = m.group(1)
                    done.set()
        # Süreç URL vermeden bittiyse bekleyeni serbest bırak.
        done.set()

    threading.Thread(target=_reader, daemon=True).start()

    if not done.wait(timeout):
        proc.terminate()
        raise ZorvenError("bağlantı zaman aşımına uğradı")
    if "url" not in result:
        code = proc.poll()
        raise ZorvenError(f"zorven CLI URL vermeden çıktı (kod {code})")

    return Tunnel(url=result["url"], urls=urls, process=proc)
