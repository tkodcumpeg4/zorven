# zorven (Python)

Open a public [Zorven](https://zorven.app) tunnel to a local port from Python.
Wraps the `zorven` CLI (install: `curl -fsSL https://zorven.app/install.sh | sh`).

```python
import os, zorven

t = zorven.connect(token=os.environ["ZORVEN_TOKEN"], port=8080)
print("Public URL:", t.url)   # https://...zorven.app
# t.urls  -> all public URLs
# t.close()  -> stop the tunnel
```

## API

`connect(token, port=None, target=None, server=None, bin="zorven", timeout=30.0) -> Tunnel`

Either `port` or `target` is required. Raises `zorven.ZorvenError` on failure.
`Tunnel` has `url`, `urls`, `process`, and `close()`.
