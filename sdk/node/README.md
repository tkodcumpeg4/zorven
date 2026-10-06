# @zorven/sdk (Node.js)

Open a public [Zorven](https://zorven.app) tunnel to a local port from Node.js.
Wraps the `zorven` CLI (install: `curl -fsSL https://zorven.app/install.sh | sh`).

```js
const { connect } = require('@zorven/sdk')

const t = await connect({ token: process.env.ZORVEN_TOKEN, port: 8080 })
console.log('Public URL:', t.url)   // https://...zorven.app
// t.urls  -> all public URLs
// t.close()  -> stop the tunnel
```

## API

`connect(options) => Promise<Tunnel>`

| Option | Type | Notes |
|---|---|---|
| `token` | string | **required** — client token (`zrv_live_...`) |
| `port` | number\|string | local port; or use `target` |
| `target` | string | full target, e.g. `http://localhost:8080` |
| `server` | string | server address (default: CLI config) |
| `bin` | string | path to the `zorven` CLI (default `zorven`) |
| `timeoutMs` | number | connect timeout (default 30000) |

`Tunnel`: `{ url, urls, process, close() }`.
