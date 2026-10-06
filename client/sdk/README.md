# Zorven Go SDK

Open a public [Zorven](https://zorven.app) tunnel to a local service from Go.
Native — embeds the agent, no external binary needed.

```go
import "github.com/tkodcumpeg4/zorven/client/sdk"

sess, err := sdk.Connect(ctx, sdk.Config{
    Token:  os.Getenv("ZORVEN_TOKEN"), // client token from the panel
    Target: "8080",                    // or "http://localhost:8080"
})
if err != nil { log.Fatal(err) }
defer sess.Close()

log.Println("Public URL:", sess.URL()) // https://...zorven.app
sess.Wait()                            // block until the tunnel closes
```

## Config

| Field | Notes |
|---|---|
| `Token` | **required** — client token (`zrv_live_...`) |
| `Target` | **required** — `"8080"`, `"localhost:8080"` or `"http://localhost:8080"` |
| `ServerAddr` | default `"zorven.app:443"` |
| `Insecure` | `ws://` for local dev |
| `NoTerminal` / `NoScreen` | disable remote shell / screen (recommended for embedded use) |
| `ConnectTimeout` | default 30s |

`Session`: `URL()`, `URLs()`, `ClientID()`, `Wait() error`, `Close() error`.
