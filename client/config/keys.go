package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Keys, `zorven config get|set|unset` icin bilinen anahtarlar (sirali).
var Keys = []string{"server", "token", "insecure", "ca_cert", "enable_terminal", "enable_screen", "enable_deploy", "language"}

// Get, anahtarin degerini metin olarak doner.
func (c Config) Get(key string) (string, error) {
	switch key {
	case "server":
		return c.ServerAddr, nil
	case "token":
		return c.Token, nil
	case "insecure":
		return strconv.FormatBool(c.Insecure), nil
	case "ca_cert":
		return c.CACertPath, nil
	case "enable_terminal":
		return strconv.FormatBool(c.EnableTerminal), nil
	case "enable_screen":
		return strconv.FormatBool(c.EnableScreen), nil
	case "enable_deploy":
		return strconv.FormatBool(c.EnableDeploy), nil
	case "language":
		return c.Language, nil
	}
	return "", fmt.Errorf("unknown key %q", key)
}

// Set, anahtara deger atar (bool anahtarlar true/false/on/off/1/0 kabul eder).
func (c *Config) Set(key, value string) error {
	value = strings.TrimSpace(value)
	b := func() (bool, error) {
		switch strings.ToLower(value) {
		case "true", "on", "1", "yes", "evet":
			return true, nil
		case "false", "off", "0", "no", "hayir":
			return false, nil
		}
		return false, fmt.Errorf("invalid boolean %q", value)
	}
	switch key {
	case "server":
		c.ServerAddr = value
		c.ServerPinned = true // acik tercih: dugum listesi kullanilmaz
	case "token":
		c.Token = value
	case "ca_cert":
		c.CACertPath = value
	case "language":
		c.Language = value
	case "insecure", "enable_terminal", "enable_screen", "enable_deploy":
		v, err := b()
		if err != nil {
			return err
		}
		switch key {
		case "insecure":
			c.Insecure = v
		case "enable_terminal":
			c.EnableTerminal = v
		case "enable_screen":
			c.EnableScreen = v
		default:
			c.EnableDeploy = v
		}
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}

// Unset, anahtari varsayilana dondurur.
func (c *Config) Unset(key string) error {
	def := DefaultConfig()
	switch key {
	case "server":
		c.ServerAddr = def.ServerAddr
		c.ServerPinned = false
	case "token":
		c.Token = ""
	case "ca_cert":
		c.CACertPath = ""
	case "language":
		c.Language = ""
	case "insecure":
		c.Insecure = false
	case "enable_terminal":
		c.EnableTerminal = false
	case "enable_screen":
		c.EnableScreen = false
	case "enable_deploy":
		c.EnableDeploy = false
	default:
		return fmt.Errorf("unknown key %q", key)
	}
	return nil
}
