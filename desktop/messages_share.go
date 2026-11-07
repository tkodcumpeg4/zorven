package main

// messages.go'ya ek anahtarlar: Zorven Net paylasim/erisim sayfasinin eylem
// hatalari (netshare.go). Sema ve denetim messages.go ile aynidir: {tr, en, ja, zh}.
// Sayfanin kendi metinleri arayuz sozlugundedir (frontend/dist/i18n.js).

func init() {
	for k, v := range shareMessages {
		dmessages[k] = v
	}
}

var shareMessages = map[string][4]string{
	"ns.err.proto": {
		"Geçersiz protokol %q (tcp ya da udp olmalı).",
		"Invalid protocol %q (must be tcp or udp).",
		"",
		"",
	},
	"ns.err.to": {
		"Alıcı gerekli: paylaşımın kime açılacağını belirtin (örn. ali@firma.com, group:everyone, device:nas).",
		"Recipients are required: say who the share is opened to (e.g. ali@company.com, group:everyone, device:nas).",
		"",
		"",
	},
	"ns.err.expires": {
		"Geçersiz süre %q (en az 1m, örnek 24h).",
		"Invalid duration %q (minimum 1m, e.g. 24h).",
		"",
		"",
	},
	"ns.err.local": {
		"Geçersiz yerel port (1-65535 arasında olmalı).",
		"Invalid local port (must be between 1 and 65535).",
		"",
		"",
	},
	"ns.err.target": {
		"Erişilecek paylaşımı belirtin (ad ya da ad:port).",
		"Specify the share to connect to (name or name:port).",
		"",
		"",
	},
	"ns.err.share": {
		"Paylaşım kimliği ya da adı gerekli.",
		"A share ID or name is required.",
		"",
		"",
	},
	"ns.err.noinfo": {
		"Çalışan istemci paylaşım bilgisi döndürmedi.",
		"The running client returned no share information.",
		"",
		"",
	},
	"ns.err.old": {
		"Çalışan istemci bu işlemi desteklemiyor (eski sürüm); 'zorven update' ile güncelleyip yeniden başlatın.",
		"The running client does not support this operation (old version); update it with 'zorven update' and restart it.",
		"",
		"",
	},
	"ns.err.nonet": {
		"Bu oturumda Zorven Net etkin değil (sunucu ya da planınız sunmuyor olabilir).",
		"Zorven Net is not enabled in this session (the server or your plan may not offer it).",
		"",
		"",
	},
	"ns.err.denied": {
		"Çalışan istemci bu kullanıcıya yerel erişim vermedi (%s); istemciyle aynı kullanıcıyla ya da yönetici olarak deneyin.",
		"The running client did not grant local access to this user (%s); try as the same user as the client or as administrator.",
		"",
		"",
	},
	"ns.err.connect": {
		"Önce Zorven'e bağlanın: paylaşım ve erişim bağlı cihazdan yapılır.",
		"Connect to Zorven first: sharing and access are done through the connected device.",
		"",
		"",
	},
	"ns.err.unreach": {
		"Çalışan Zorven istemcisine erişilemedi (%s).",
		"Could not reach the running Zorven client (%s).",
		"",
		"",
	},
}
