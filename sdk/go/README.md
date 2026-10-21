# Zorven Go SDK

Go client for the Zorven REST API (`/api/v1`), plus a `Connect` helper that opens a
public tunnel through the `zorven` CLI (same scope as the Node.js and Python SDKs).
Standard library only; requires Go 1.23+.

```sh
go get github.com/tkodcumpeg4/zorven/sdk/go
```

```go
import zorven "github.com/tkodcumpeg4/zorven/sdk/go"
```

## REST client

Create an API token in the panel under *API Keys* (Pro plan or above).

```go
c := zorven.NewClient(zorven.Options{
    Token:   os.Getenv("ZORVEN_TOKEN"),
    Project: "my-project", // optional, sent as X-Zorven-Project
})

created, err := c.Clients.Create(ctx, "home-pc") // created.Token is shown only once
tun, err := c.Tunnels.Create(ctx, zorven.CreateTunnelRequest{
    Name: "api", ClientID: created.Client.ID, Target: "http://localhost:8000",
})
hn, err := c.Hostnames.Create(ctx, zorven.CreateHostnameRequest{Name: "api", TunnelID: tun.ID})
fmt.Println("https://" + hn.FQDN)
```

### Options

| Field | Default |
|---|---|
| `BaseURL` | `https://zorven.app/api/v1` |
| `Token` | none (`Authorization: Bearer ...`) |
| `Project` | none (`X-Zorven-Project`) |
| `HTTPClient` | `http.Client` with 30 s timeout |
| `UserAgent` | appended to `zorven-go/<version>` |
| `MaxRetries` | 3 (negative disables) |
| `RetryBaseDelay` | 500 ms, exponential, capped at 30 s |

Every call takes a `context.Context`. `429` is always retried, honoring `Retry-After`;
`5xx` and network errors are retried for `GET`/`PUT`/`DELETE` only (POST/PATCH are not,
to avoid duplicate writes).

### Resources

| Service | Methods |
|---|---|
| `Meta` | `Health`, `Me`, `Plans` |
| `Clients` | `List`, `Iter`, `ListAll`, `Get`, `Create`, `Delete`, `RotateToken` |
| `Tunnels` | `List`, `Iter`, `ListAll`, `Get`, `Create`, `Update`, `Delete`, `GetDoor`, `SetDoor`, `ListDoorGrants`, `RevokeDoorGrant` |
| `Hostnames` | `List`, `Iter`, `ListAll`, `Create`, `CreateCustom`, `Attach`, `Detach`, `Verify`, `Delete` |
| `IPAllowlist` | `List`, `Create`, `Update`, `Delete` |
| `Analytics` | `Requests`, `AccessEvents`, `IterAccessEvents`, `AccessEventSummary`, `Subscription` |

The live SSE stream (`/events`) is not wrapped.

### Pagination

`List` returns one `Page` (`Items`, `NextCursor`, `HasMore`, from the `X-Next-Cursor` /
`X-Has-More` headers). `Iter` walks all pages as an `iter.Seq2`; `ListAll` collects them.

```go
for t, err := range c.Tunnels.Iter(ctx, nil) {
    if err != nil { log.Fatal(err) }
    fmt.Println(t.ID, t.Target)
}
```

### Errors

Non-2xx responses return `*zorven.APIError` (`Status`, `Code`, `Message`, `RequestID`,
`RetryAfter`):

```go
if ae, ok := zorven.AsAPIError(err); ok && ae.Code == "insufficient_scope" { ... }
```

## Tunnel from code (CLI wrapper)

Requires the `zorven` CLI (`curl -fsSL https://zorven.app/install.sh | sh`) and a
*client* token.

```go
s, err := zorven.Connect(ctx, zorven.ConnectOptions{Token: os.Getenv("ZORVEN_CLIENT_TOKEN"), Port: 8080})
if err != nil { log.Fatal(err) }
defer s.Close()
fmt.Println(s.URL)
s.Wait()
```

To embed the agent without an external binary, see `client/sdk` in the main repository.

## Example

```sh
ZORVEN_TOKEN=zrv_api_... go run ./examples/tunnels
```

## Tests

```sh
go vet ./... && go test ./...
```
