# Zorven SDKs

Open a public Zorven tunnel to a local service **from your code**, in Go, Node.js or Python.

| Language | Package | Kind |
|---|---|---|
| **Go** | `github.com/tkodcumpeg4/zorven/client/sdk` | Native — embeds the agent, no external binary needed |
| **Node.js** | `@zorven/sdk` ([`node/`](./node)) | Wraps the `zorven` CLI |
| **Python** | `zorven` ([`python/`](./python)) | Wraps the `zorven` CLI |

All you need is a **client token** (create one in the panel under *Clients*).

## Go

```go
sess, err := sdk.Connect(ctx, sdk.Config{Token: os.Getenv("ZORVEN_TOKEN"), Target: "8080"})
if err != nil { log.Fatal(err) }
defer sess.Close()
log.Println("public URL:", sess.URL())
sess.Wait()
```

## Node.js

```js
const { connect } = require('@zorven/sdk')
const t = await connect({ token: process.env.ZORVEN_TOKEN, port: 8080 })
console.log(t.url)
// t.close() when done
```

## Python

```python
import os, zorven
t = zorven.connect(token=os.environ["ZORVEN_TOKEN"], port=8080)
print(t.url)
# t.close() when done
```

### CLI contract (Node/Python)

The Node and Python SDKs spawn the `zorven` CLI with `--print-url`, which emits a
stable machine-readable line on connect:

```
zorven-url: https://<hostname>
```

Install the CLI: `curl -fsSL https://zorven.app/install.sh | sh` (Linux/macOS) or
`irm https://zorven.app/install.ps1 | iex` (Windows).
