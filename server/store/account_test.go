package store

import (
	"strings"
	"testing"
)

func TestAccountPseudonym(t *testing.T) {
	a := AccountPseudonym("usr_8f3a1c")
	if a != AccountPseudonym("usr_8f3a1c") {
		t.Fatal("takma ad kararli olmali (ayni kullanici -> ayni takma ad)")
	}
	if a == AccountPseudonym("usr_8f3a1d") {
		t.Fatal("farkli kullanici farkli takma ad almali")
	}
	if !strings.HasPrefix(a, "deleted-user-") || len(a) != len("deleted-user-")+10 {
		t.Fatalf("bicim beklenenden farkli: %q", a)
	}
	if strings.Contains(a, "8f3a1c") || strings.Contains(a, "usr_") {
		t.Fatalf("takma ad kullanici kimligini icermemeli: %q", a)
	}
}

func TestAccountDeletionPlanCanDelete(t *testing.T) {
	if !(AccountDeletionPlan{}).CanDelete() {
		t.Fatal("engelsiz plan silinebilir olmali")
	}
	p := AccountDeletionPlan{Blockers: []AccountOrg{{Slug: "ortak", Reason: AccountBlockSoleOwner}}}
	if p.CanDelete() {
		t.Fatal("engelli plan silinememeli")
	}
	if err := (&AccountBlockedError{Plan: p}); !strings.Contains(err.Error(), "ortak") {
		t.Fatalf("hata engelleyen organizasyonu soylemeli: %v", err)
	}
}
