package main

import "strings"

// doorAddrHosts, web door diger-IP-ailesi yan kanal hostlarini belirler.
// Bos env: door4./door6. + platform domain (platform domain yoksa kapali). "off" ya da "-"
// o aileyi kapatir. Env bir host verirse kucuk harfe cevrilip aynen kullanilir.
func doorAddrHosts(platformDomain, v4Env, v6Env string) (v4, v6 string) {
	pick := func(env, prefix string) string {
		env = strings.ToLower(strings.TrimSpace(env))
		switch env {
		case "off", "-":
			return ""
		case "":
			if platformDomain == "" {
				return ""
			}
			return prefix + "." + platformDomain
		}
		return env
	}
	return pick(v4Env, "door4"), pick(v6Env, "door6")
}
