package main

import "strings"

// legacyUnit, eski surumlerin kurdugu systemd birimini tarif eder. Eski birim
// `ExecStart=/usr/local/bin/zorven --enable-terminal` biciminde olup "service
// run" argumani tasimaz; systemd altinda ajani on plan kipinde calistirir.
type legacyUnit struct {
	terminal, screen bool // eski ExecStart'ta istenen kalici yetenekler
}

// parseLegacyUnit, birim dosyasi metnini inceler. Birim zorven'e ait bir
// ExecStart tasiyor ve "service run" icermiyorsa eski sayilir.
func parseLegacyUnit(unitText string) (legacyUnit, bool) {
	var exec string
	found := false
	for _, line := range strings.Split(unitText, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "ExecStart="); ok {
			exec, found = v, true
			break
		}
	}
	if !found {
		return legacyUnit{}, false
	}
	toks := splitExecStart(exec)
	if len(toks) == 0 {
		return legacyUnit{}, false
	}
	// Yalniz zorven ikilisine ait birimler (baska bir program degil).
	base := toks[0]
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	if !strings.HasPrefix(strings.ToLower(base), "zorven") {
		return legacyUnit{}, false
	}
	for i := 1; i+1 < len(toks); i++ {
		if toks[i] == "service" && toks[i+1] == "run" {
			return legacyUnit{}, false
		}
	}
	var lu legacyUnit
	for _, t := range toks[1:] {
		name, val, hasVal := strings.Cut(strings.TrimLeft(t, "-"), "=")
		on := !hasVal || val == "true" || val == "1"
		switch name {
		case "enable-terminal":
			lu.terminal = on
		case "enable-screen":
			lu.screen = on
		}
	}
	return lu, true
}

// splitExecStart, ExecStart degerini bosluklara gore boler; tirnaklari ve
// systemd oneklerini (@ - : + !) dikkate alir.
func splitExecStart(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "@-:+!")
	var (
		toks  []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, has = r, true
		case r == ' ' || r == '\t':
			if has || cur.Len() > 0 {
				toks = append(toks, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
		}
	}
	if has || cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}
