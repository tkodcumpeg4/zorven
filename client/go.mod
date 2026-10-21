module github.com/tkodcumpeg4/zorven/client

go 1.25.0

toolchain go1.26.6

require (
	github.com/aymanbagabas/go-pty v0.2.3
	github.com/coder/websocket v1.8.15
	github.com/ebitengine/purego v0.11.1
	github.com/jezek/xgb v1.1.1
	github.com/kardianos/service v1.3.0
	github.com/kbinani/screenshot v0.0.0-20250624051815-089614a94018
	github.com/minio/selfupdate v0.6.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/spf13/cobra v1.10.2
	github.com/tkodcumpeg4/zorven/shared v0.0.0
	golang.org/x/crypto v0.55.0
	golang.org/x/image v0.45.0
	golang.org/x/sys v0.47.0
	golang.zx2c4.com/wireguard v0.0.0-20261006164505-2631ce99a06f
)

require (
	aead.dev/minisign v0.2.0 // indirect
	github.com/creack/pty v1.1.24 // indirect
	github.com/gen2brain/shm v0.1.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/google/btree v1.1.2 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	github.com/u-root/u-root v0.16.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	gvisor.dev/gvisor v0.0.0-20250503011706-39ed1f5ac29c // indirect
)

replace github.com/tkodcumpeg4/zorven/shared => ../shared
