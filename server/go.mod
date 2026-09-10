module github.com/tkodcumpeg4/zorven/server

go 1.25.0

require (
	github.com/emersion/go-message v0.18.2
	github.com/emersion/go-smtp v0.25.0
	github.com/jackc/pgx/v5 v5.10.0
	golang.org/x/crypto v0.55.0
	golang.org/x/time v0.15.0
	modernc.org/sqlite v1.58.0
)

require (
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

require (
	github.com/coder/websocket v1.8.15
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/tkodcumpeg4/zorven/shared v0.0.0
	golang.org/x/sys v0.47.0 // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/tkodcumpeg4/zorven/shared => ../shared
