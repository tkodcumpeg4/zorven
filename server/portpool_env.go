package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tkodcumpeg4/zorven/server/portpool"
)

// loadPortPool, ZORVEN_TCP_* ortam degiskenlerinden rezerve port havuzunu kurar
// (K3). Hicbiri verilmezse eski davranis: tek yuva, 10000-10099, 10 dk karantina.
//
//	ZORVEN_TCP_BIND_IPS         ip1,ip2 (veya docker bridge icin ip+kaydirma)
//	ZORVEN_TCP_HOSTS            tcp.ornek.app=ip1,tcp2.ornek.app=ip2
//	ZORVEN_TCP_PORT_MIN / MAX   yuva basina aralik
//	ZORVEN_TCP_PORT_QUARANTINE  Go suresi (10m, 0 = kapali)
func loadPortPool(platformDomain string) (*portpool.Config, error) {
	getInt := func(name string) (int, error) {
		v := strings.TrimSpace(os.Getenv(name))
		if v == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("%s sayi olmali: %q", name, v)
		}
		return n, nil
	}
	min, err := getInt("ZORVEN_TCP_PORT_MIN")
	if err != nil {
		return nil, err
	}
	max, err := getInt("ZORVEN_TCP_PORT_MAX")
	if err != nil {
		return nil, err
	}
	quarantine := time.Duration(-1)
	if v := strings.TrimSpace(os.Getenv("ZORVEN_TCP_PORT_QUARANTINE")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("ZORVEN_TCP_PORT_QUARANTINE gecerli bir sure olmali (or. 10m): %q", v)
		}
		quarantine = d
	}
	return portpool.Parse(os.Getenv("ZORVEN_TCP_BIND_IPS"), os.Getenv("ZORVEN_TCP_HOSTS"),
		min, max, quarantine, platformDomain)
}
