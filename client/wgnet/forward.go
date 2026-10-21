package wgnet

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

type closeWriter interface{ CloseWrite() error }

// pipe, iki baglanti arasinda iki yonlu kopyalar; biri bitince digerini kapatir.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

// DialFunc, her yeni yerel baglanti icin ic aga baglanti acar.
type DialFunc func(ctx context.Context) (net.Conn, error)

// ServeTCP, yerel dinleyiciye gelen her baglantiyi dial ile ic aga koprular.
func ServeTCP(ctx context.Context, ln net.Listener, dial DialFunc, onErr func(error)) {
	go func() { <-ctx.Done(); ln.Close() }()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			r, err := dial(dctx)
			cancel()
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
				c.Close()
				return
			}
			pipe(c, r)
		}()
	}
}

// ServeUDP, yerel UDP soketine gelen datagramlari kaynak adresi basina bir ic
// ag akisina esler (60 sn bosta kalinca akis kapanir).
func ServeUDP(ctx context.Context, pc net.PacketConn, dial DialFunc, onErr func(error)) {
	go func() { <-ctx.Done(); pc.Close() }()
	type flow struct {
		c    net.Conn
		last time.Time
	}
	var mu sync.Mutex
	flows := map[string]*flow{}
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		key := addr.String()
		mu.Lock()
		f := flows[key]
		if f == nil {
			dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			c, derr := dial(dctx)
			cancel()
			if derr != nil {
				mu.Unlock()
				if onErr != nil {
					onErr(derr)
				}
				continue
			}
			f = &flow{c: c, last: time.Now()}
			flows[key] = f
			go func(f *flow, key string, dst net.Addr) {
				rb := make([]byte, 64*1024)
				for {
					_ = f.c.SetReadDeadline(time.Now().Add(udpFlowIdle))
					m, err := f.c.Read(rb)
					if err != nil {
						mu.Lock()
						idle := time.Since(f.last) >= udpFlowIdle
						mu.Unlock()
						if ne, ok := err.(net.Error); ok && ne.Timeout() && !idle {
							continue
						}
						break
					}
					if _, err := pc.WriteTo(rb[:m], dst); err != nil {
						break
					}
				}
				f.c.Close()
				mu.Lock()
				delete(flows, key)
				mu.Unlock()
			}(f, key, addr)
		}
		f.last = time.Now()
		mu.Unlock()
		_, _ = f.c.Write(buf[:n])
	}
}
