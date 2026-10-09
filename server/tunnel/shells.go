package tunnel

import "github.com/tkodcumpeg4/zorven/shared/protocol"

const maxReportedShells = 16

func validShellID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// sanitizeShells, istemcinin bildirdigi kabuk listesini dogrular: gecersiz
// ID'ler ve tekrarlar atilir, uzunluklar sinirlanir, varsayilan listede
// degilse bos birakilir. Istemci guvenilmez girdi sayilir (panele yansir).
func sanitizeShells(in []protocol.ShellInfo, def string) ([]protocol.ShellInfo, string) {
	if len(in) == 0 {
		return nil, ""
	}
	seen := map[string]bool{}
	out := make([]protocol.ShellInfo, 0, len(in))
	for _, sh := range in {
		if len(out) >= maxReportedShells {
			break
		}
		if !validShellID(sh.ID) || seen[sh.ID] {
			continue
		}
		seen[sh.ID] = true
		name := truncate(sh.Name, 64)
		if name == "" {
			name = sh.ID
		}
		out = append(out, protocol.ShellInfo{ID: sh.ID, Name: name, Path: truncate(sh.Path, 512)})
	}
	if len(out) == 0 {
		return nil, ""
	}
	if !seen[def] {
		def = out[0].ID
	}
	return out, def
}

// HasShell, istemcinin bildirdigi listede ID var mi. Liste bos (eski istemci)
// ise kabuk secimi desteklenmez ve false doner.
func (s *Session) HasShell(id string) bool {
	for _, sh := range s.Shells {
		if sh.ID == id {
			return true
		}
	}
	return false
}
