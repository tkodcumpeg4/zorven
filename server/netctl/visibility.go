package netctl

import (
	"context"
	"strings"
)

// OpenVisibility, acik cekirdek gorunurluk kurallari: organizasyon uyeleri +
// acik paylasim izinleri. Kullanici/cihaz kimligi, "everyone" ve "role:<rol>"
// gruplari. Etiket/kosul tabanli gruplar ticari katmanin islevidir.
type OpenVisibility struct {
	Dir Directory
}

func (v OpenVisibility) Matches(g Grantee, sub Subject) bool {
	switch g.Type {
	case GranteeUser:
		return sub.UserID != "" && sub.UserID == g.ID
	case GranteeDevice:
		return sub.ClientID != "" && sub.ClientID == g.ID
	case GranteeGroup:
		if g.ID == "everyone" {
			return true
		}
		if r, ok := strings.CutPrefix(g.ID, "role:"); ok {
			return sub.Role != "" && sub.Role == r
		}
	}
	return false
}

func (v OpenVisibility) ValidateGrantee(ctx context.Context, tenantID string, g Grantee) error {
	if g.ID == "" || len(g.ID) > 128 {
		return invalid("invalid_grantee", "alici kimligi gecersiz")
	}
	switch g.Type {
	case GranteeUser:
		if v.Dir == nil {
			return nil
		}
		role, err := v.Dir.UserRole(ctx, tenantID, g.ID)
		if err != nil {
			return err
		}
		if role == "" {
			return invalid("grantee_not_member", "kullanici bu organizasyonun uyesi degil")
		}
	case GranteeDevice:
		if v.Dir == nil {
			return nil
		}
		_, ok, err := v.Dir.DeviceName(ctx, tenantID, g.ID)
		if err != nil {
			return err
		}
		if !ok {
			return invalid("grantee_not_found", "cihaz bulunamadi")
		}
	case GranteeGroup:
		if g.ID == "everyone" {
			return nil
		}
		if r, ok := strings.CutPrefix(g.ID, "role:"); ok && (r == "owner" || r == "admin" || r == "member") {
			return nil
		}
		return invalid("invalid_group", "grup everyone veya role:<owner|admin|member> olmali")
	default:
		return invalid("invalid_grantee", "alici turu user, group veya device olmali")
	}
	return nil
}
