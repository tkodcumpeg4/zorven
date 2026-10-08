package mail

import (
	"context"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// ExternalSendAllowed: ACIK SURUMDE harici alicilara gonderim serbesttir
// (sahip karari; plan/entitlement kontrolu yok). SMTP gonderim (587/465)
// ve panel ayni kurali kullanir.
func ExternalSendAllowed(_ context.Context, _ store.Store, _ string) bool { return true }
