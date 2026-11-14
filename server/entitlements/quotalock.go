package entitlements

import "context"

// Kota kaynak adlari (kilit anahtari). Ticari surumde plan limiti "say, sonra ekle"
// yarisini kesmek icin (kiraci, kaynak) basina kilit alinir; acik surumde limit
// olmadigi icin kilit gerekmez.
const (
	QuotaTunnels  = "tunnels"
	QuotaClients  = "clients"
	QuotaProjects = "projects"
	QuotaMembers  = "members"
)

// LockQuota, acik surumde no-op: plan limiti yok, siraya sokulacak say+ekle bolumu yok.
// Donen unlock her zaman gecerlidir ve birden fazla cagrilabilir.
func (s *Service) LockQuota(ctx context.Context, tenantID, resource string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return func() {}, nil
}
