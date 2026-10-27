//go:build !linux

package main

import "github.com/kardianos/service"

// Linux disi platformlarda ek servis secenegi yok: Windows servisi SYSTEM, macOS
// LaunchDaemon root olarak calisir (TUN yetkisi bununla gelir).
func platformServiceOptions() service.KeyValue { return nil }
