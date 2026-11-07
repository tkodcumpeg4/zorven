package mailauth

// decide, dogrulama sonucundan teslim kararini verir. Politika muhafazakardir
// (posta kaybetmemek once gelir):
//
//   - DMARC basarisiz (hizali SPF ve hizali DKIM yok) ve alan adinin politikasi
//     p=reject: ret (SMTP DATA sonunda 550 5.7.1).
//   - DMARC basarisiz ve p=quarantine: Onemsiz klasoru.
//   - DMARC kaydi HIC yokken SPF "fail" (-all) ve gecerli DKIM yok: Onemsiz klasoru.
//   - Gecici DNS hatasi, zaman asimi, gecersiz/belirsiz kayit, SPF atlandi
//     (genel olmayan istemci adresi), p=none ve geri kalan her sey: Gelen Kutusu.
func decide(r *Result) (Action, string) {
	d := r.DMARC
	if d.Result == Fail {
		switch d.Disposition {
		case "reject":
			return ActionReject, "dmarc fail, p=reject"
		case "quarantine":
			return ActionJunk, "dmarc fail, p=quarantine"
		}
		return ActionInbox, "dmarc fail, p=none"
	}
	if d.NoRecord && r.SPF.Result == Fail && !r.AnyDKIMPass() {
		return ActionJunk, "spf fail, no dmarc record, no valid dkim"
	}
	return ActionInbox, ""
}
