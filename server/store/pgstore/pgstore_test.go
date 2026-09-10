package pgstore

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// testDSN, Postgres testleri icin DSN'i doner; yoksa testi atlar.
//
// Neden atlama: her gelistiricide Postgres olmayabilir. CI'da
// ZORVEN_TEST_PG_DSN ayarli oldugu icin testler orada gercekten kosar.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("ZORVEN_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("ZORVEN_TEST_PG_DSN ayarli degil; Postgres testleri atlaniyor")
	}
	assertTestDatabase(t, dsn)
	return dsn
}

// assertTestDatabase, DSN'in bir TEST veritabanina isaret ettigini dogrular.
//
// NEDEN VAR: freshStore tablolari TRUNCATE ediyor. DSN yanlislikla gercek
// veritabanini gosterirse "go test" tum istemcileri, tunelleri ve ayarlari
// yok eder. Bu bir kez yasandi (SQLite gocu sirasinda test kalintilariyla
// cakisma cikti), o yuzden artik kod duzeyinde engelliyoruz.
func assertTestDatabase(t *testing.T, dsn string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("ZORVEN_TEST_PG_DSN cozulemedi: %v", err)
	}
	db := strings.TrimPrefix(u.Path, "/")
	if !strings.Contains(db, "test") {
		t.Fatalf("ZORVEN_TEST_PG_DSN bir TEST veritabanina isaret etmeli "+
			"(adinda \"test\" gecmeli); verilen: %q.\n"+
			"Testler tablolari TRUNCATE eder; gercek veritabaniyla calistirmak "+
			"tum veriyi yok eder.", db)
	}
}

// freshStore, temiz tablolarla bir store doner.
//
// Neden TRUNCATE: testler ayni veritabanini paylasir; her test kendi baslangic
// durumunu garanti etmeli. tenants da temizlenir, ama VARSAYILAN KIRACI geri
// konur — goc yalnizca bir kez calistigi icin TRUNCATE onu da siler.
func freshStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`TRUNCATE clients, tunnels, hostnames, settings, tenant_members, users, tenants,
		 "user", "session", "account", "verification", "organization", "member", "invitation"
		 RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE: %v", err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO tenants (id, slug, created_at) VALUES ($1,$2,now())`,
		store.DefaultTenantID, store.DefaultTenantSlug); err != nil {
		t.Fatalf("varsayilan kiraci eklenemedi: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testDSN(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	// clients tablosu olusmus olmali: bos liste hatasiz donmeli.
	if _, err := s.ListClients(ctx, store.DefaultTenantID); err != nil {
		t.Fatalf("ListClients migration sonrasi hata verdi: %v", err)
	}
	// 0002 gocu de uygulanmis olmali: varsayilan kiraci var.
	if _, err := s.GetTenant(ctx, store.DefaultTenantID); err != nil {
		t.Fatalf("varsayilan kiraci bulunamadi: %v", err)
	}
}

func TestClientCRUD(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tn := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tn, "ev-pc", "tid1", "hash1")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if c.ID == "" || c.Name != "ev-pc" || c.TenantID != tn {
		t.Fatalf("beklenmeyen client: %+v", c)
	}

	got, err := s.GetClient(ctx, tn, c.ID)
	if err != nil || got.ID != c.ID {
		t.Fatalf("GetClient: %v %+v", err, got)
	}

	byTok, err := s.GetClientByTokenID(ctx, "tid1")
	if err != nil || byTok.ID != c.ID {
		t.Fatalf("GetClientByTokenID: %v", err)
	}

	if err := s.RotateClientToken(ctx, tn, c.ID, "tid2", "hash2"); err != nil {
		t.Fatalf("RotateClientToken: %v", err)
	}
	if _, err := s.GetClientByTokenID(ctx, "tid2"); err != nil {
		t.Fatalf("rotate sonrasi yeni tokenID bulunamadi: %v", err)
	}

	list, err := s.ListClients(ctx, tn)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListClients: %v len=%d", err, len(list))
	}

	if err := s.DeleteClient(ctx, tn, c.ID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}
	if _, err := s.GetClient(ctx, tn, c.ID); err != store.ErrNotFound {
		t.Fatalf("silinen client icin ErrNotFound bekleniyordu, geldi: %v", err)
	}
}

func TestTunnelCRUD(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tid := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tid, "c", "t", "h")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	tn, err := s.CreateTunnel(ctx, tid, c.ID, "http://localhost:8000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	enabled := false
	upd, err := s.UpdateTunnel(ctx, tid, tn.ID, store.TunnelPatch{Enabled: &enabled})
	if err != nil || upd.Enabled {
		t.Fatalf("UpdateTunnel: %v %+v", err, upd)
	}
	// Target verilmedi -> degismemeli.
	if upd.Target != "http://localhost:8000" {
		t.Fatalf("nil patch alani target'i degistirdi: %q", upd.Target)
	}

	byClient, err := s.ListTunnelsByClient(ctx, c.ID)
	if err != nil || len(byClient) != 1 {
		t.Fatalf("ListTunnelsByClient: %v len=%d", err, len(byClient))
	}

	all, err := s.ListTunnels(ctx, tid)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListTunnels: %v len=%d", err, len(all))
	}

	if err := s.DeleteTunnel(ctx, tid, tn.ID); err != nil {
		t.Fatalf("DeleteTunnel: %v", err)
	}
	if _, err := s.GetTunnel(ctx, tid, tn.ID); err != store.ErrNotFound {
		t.Fatalf("silinen tunel icin ErrNotFound bekleniyordu: %v", err)
	}
}

func TestSettingsUpsertAndDelete(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	if _, err := s.GetSetting(ctx, "yok"); err != store.ErrNotFound {
		t.Fatalf("olmayan ayar icin ErrNotFound bekleniyordu: %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// Ikinci yazim upsert olmali (cakisma hatasi degil).
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatalf("SetSetting upsert: %v", err)
	}
	v, err := s.GetSetting(ctx, "k")
	if err != nil || v != "v2" {
		t.Fatalf("GetSetting: %v %q", err, v)
	}
	if err := s.DeleteSetting(ctx, "k"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if _, err := s.GetSetting(ctx, "k"); err != store.ErrNotFound {
		t.Fatalf("silinen ayar icin ErrNotFound bekleniyordu: %v", err)
	}
}

// --- Kiraci izolasyonu: GUVENLIGIN KALBI -----------------------------------

// Kiracilar birbirinin kaydini GORMEMELI, degistirememeli, silememeli.
func TestTenantIsolation(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, err := s.CreateTenant(ctx, "kiraci-a")
	if err != nil {
		t.Fatalf("CreateTenant a: %v", err)
	}
	b, err := s.CreateTenant(ctx, "kiraci-b")
	if err != nil {
		t.Fatalf("CreateTenant b: %v", err)
	}

	ca, err := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	cb, err := s.CreateClient(ctx, b.ID, "b-pc", "tid-b", "hash-b")
	if err != nil {
		t.Fatal(err)
	}

	// Listeler sizdirmamali.
	listA, err := s.ListClients(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listA) != 1 || listA[0].ID != ca.ID {
		t.Fatalf("SIZINTI: ListClients(a) = %+v", listA)
	}

	// Capraz erisim "bulunamadi" donmeli (varligini bile sizdirmamali).
	if _, err := s.GetClient(ctx, a.ID, cb.ID); err != store.ErrNotFound {
		t.Fatalf("SIZINTI: capraz GetClient: %v", err)
	}
	if err := s.DeleteClient(ctx, a.ID, cb.ID); err != store.ErrNotFound {
		t.Fatalf("SIZINTI: capraz DeleteClient: %v", err)
	}
	if err := s.RotateClientToken(ctx, a.ID, cb.ID, "x", "y"); err != store.ErrNotFound {
		t.Fatalf("SIZINTI: capraz RotateClientToken: %v", err)
	}
	// B'nin istemcisi bozulmamis olmali.
	if _, err := s.GetClient(ctx, b.ID, cb.ID); err != nil {
		t.Fatalf("capraz islem B'nin istemcisini bozdu: %v", err)
	}
	// B'nin token'i hala gecerli olmali (rotate etkisiz kalmali).
	if _, err := s.GetClientByTokenID(ctx, "tid-b"); err != nil {
		t.Fatalf("capraz rotate B'nin token'ini bozdu: %v", err)
	}

	// Tuneller de izole.
	ta, err := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTunnel(ctx, b.ID, cb.ID, "http://y"); err != nil {
		t.Fatal(err)
	}
	tunA, err := s.ListTunnels(ctx, a.ID)
	if err != nil || len(tunA) != 1 || tunA[0].ID != ta.ID {
		t.Fatalf("SIZINTI/eksik: ListTunnels(a) = %+v err=%v", tunA, err)
	}
	if _, err := s.GetTunnel(ctx, a.ID, "tun_yok"); err != store.ErrNotFound {
		t.Fatalf("GetTunnel(yok): %v", err)
	}
	// Capraz tunel guncelleme/silme de etkisiz.
	target := "http://ele-gecirildi"
	if _, err := s.UpdateTunnel(ctx, a.ID, "tun_b", store.TunnelPatch{Target: &target}); err != store.ErrNotFound {
		t.Fatalf("SIZINTI: capraz UpdateTunnel: %v", err)
	}
}

// Veri duzlemi yollari KASITLI olarak kiracidan bagimsizdir; aksi halde tunel
// calismaz (istek yalnizca hostname tasir, istemci kiracisini bilmez).
func TestDataPlanePathsAreTenantAgnostic(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, _ := s.CreateTenant(ctx, "kiraci-a")
	b, _ := s.CreateTenant(ctx, "kiraci-b")
	ca, _ := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	cb, _ := s.CreateClient(ctx, b.ID, "b-pc", "tid-b", "hash-b")
	if _, err := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTunnel(ctx, b.ID, cb.ID, "http://y"); err != nil {
		t.Fatal(err)
	}

	// Istemci token dogrulamasi kiracidan bagimsiz, ama DOGRU kiraciyi bildirmeli.
	got, err := s.GetClientByTokenID(ctx, "tid-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.TenantID != b.ID {
		t.Fatalf("token -> kiraci esleme yanlis: %q != %q", got.TenantID, b.ID)
	}
}

// Ice aktarma id, token hash ve zaman damgasini KORUMALI — kurulu istemcilerin
// yeniden yapilandirma gerektirmemesi buna bagli.
func TestImportPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	created := time.Date(2026, 9, 2, 17, 31, 31, 0, time.UTC)
	c := store.Client{
		ID: "cli_eski", Name: "ev-pc", TokenID: "tid-eski",
		TokenHash: "argon2hash", CreatedAt: created,
	}
	if err := s.ImportClient(ctx, c); err != nil {
		t.Fatalf("ImportClient: %v", err)
	}
	// Idempotent olmali: goc iki kez calistirilabilir.
	if err := s.ImportClient(ctx, c); err != nil {
		t.Fatalf("ImportClient tekrar: %v", err)
	}

	got, err := s.GetClientByTokenID(ctx, "tid-eski")
	if err != nil {
		t.Fatalf("GetClientByTokenID: %v", err)
	}
	if got.ID != "cli_eski" || got.TokenHash != "argon2hash" {
		t.Fatalf("kimlik korunmadi: %+v", got)
	}
	if got.TenantID != store.DefaultTenantID {
		t.Fatalf("ice aktarilan kayit varsayilan kiraciya gitmeliydi: %q", got.TenantID)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("created_at korunmadi: %v != %v", got.CreatedAt, created)
	}

	tn := store.Tunnel{
		ID: "tun_eski", ClientID: "cli_eski",
		Target: "http://localhost:8003", Enabled: true, CreatedAt: created,
	}
	if err := s.ImportTunnel(ctx, tn); err != nil {
		t.Fatalf("ImportTunnel: %v", err)
	}
	gotT, err := s.GetTunnel(ctx, store.DefaultTenantID, "tun_eski")
	if err != nil {
		t.Fatalf("GetTunnel: %v", err)
	}
	if !gotT.Enabled || !gotT.CreatedAt.Equal(created) {
		t.Fatalf("tunel kimligi korunmadi: %+v", gotT)
	}

	// Eski hostname ayri bir kayit olarak 'legacy' tipiyle tasinir (importCmd
	// bunu yapar); platform ad kurallari bunlara UYGULANMAZ.
	if _, err := s.AddHostname(ctx, store.DefaultTenantID, "tun_eski",
		"api.localhost", store.HostTypeLegacy); err != nil {
		t.Fatalf("legacy hostname aktarilamadi: %v", err)
	}
}

// --- Kullanicilar ve uyelik ------------------------------------------------

// Ayni GitHub hesabi her giriste AYNI kullaniciya dusmeli; kullanici adi
// degisse bile (github_id sabittir).
func TestUserUpsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	u1, err := s.UpsertUserByGitHubID(ctx, 4242, "octocat")
	if err != nil {
		t.Fatalf("UpsertUserByGitHubID: %v", err)
	}
	u2, err := s.UpsertUserByGitHubID(ctx, 4242, "octocat")
	if err != nil {
		t.Fatalf("ikinci upsert: %v", err)
	}
	if u1.ID != u2.ID {
		t.Fatalf("ayni github_id iki kullanici uretti: %q != %q", u1.ID, u2.ID)
	}

	u3, err := s.UpsertUserByGitHubID(ctx, 4242, "octocat-yeni")
	if err != nil {
		t.Fatalf("login degisikligi: %v", err)
	}
	if u3.ID != u1.ID {
		t.Fatalf("login degisince yeni kullanici olustu: %q", u3.ID)
	}
	if u3.GitHubLogin != "octocat-yeni" {
		t.Fatalf("login guncellenmedi: %q", u3.GitHubLogin)
	}
}

func TestTenantMembershipLookup(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	u, err := s.UpsertUserByGitHubID(ctx, 7, "kullanici")
	if err != nil {
		t.Fatal(err)
	}
	// Uyelik yokken kiraci bulunmamali.
	if _, err := s.GetTenantForUser(ctx, u.ID); err != store.ErrTenantNotFound {
		t.Fatalf("uyeliksiz kullanici icin ErrTenantNotFound bekleniyordu: %v", err)
	}

	tn, err := s.CreateTenant(ctx, "kullanici")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddTenantMember(ctx, tn.ID, u.ID, store.RoleOwner); err != nil {
		t.Fatalf("AddTenantMember: %v", err)
	}

	got, err := s.GetTenantForUser(ctx, u.ID)
	if err != nil || got.ID != tn.ID {
		t.Fatalf("GetTenantForUser: %v %+v", err, got)
	}

	// Ayni uyelik tekrar eklenebilmeli (idempotent).
	if err := s.AddTenantMember(ctx, tn.ID, u.ID, store.RoleOwner); err != nil {
		t.Fatalf("AddTenantMember tekrar: %v", err)
	}
}

func TestGetTenantBySlugIsCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	tn, err := s.CreateTenant(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTenantBySlug(ctx, "acme")
	if err != nil || got.ID != tn.ID {
		t.Fatalf("GetTenantBySlug: %v %+v", err, got)
	}
	if _, err := s.GetTenantBySlug(ctx, "yok"); err != store.ErrTenantNotFound {
		t.Fatalf("olmayan slug icin ErrTenantNotFound bekleniyordu: %v", err)
	}
}

// --- Hostname isim-uzayi ---------------------------------------------------

func TestHostnameCRUDAndGlobalUniqueness(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)
	tid := store.DefaultTenantID

	c, err := s.CreateClient(ctx, tid, "c", "t", "h")
	if err != nil {
		t.Fatal(err)
	}
	tn, err := s.CreateTunnel(ctx, tid, c.ID, "http://localhost:8000")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	h, err := s.AddHostname(ctx, tid, tn.ID, "Api.rpshell.app", store.HostTypeGlobal)
	if err != nil {
		t.Fatalf("AddHostname: %v", err)
	}

	// Ayni ad farkli case ile alinamaz (DNS duyarsiz, isim uzayi global).
	if _, err := s.AddHostname(ctx, tid, tn.ID, "api.rpshell.app", store.HostTypeGlobal); err != store.ErrHostnameTaken {
		t.Fatalf("ErrHostnameTaken bekleniyordu: %v", err)
	}

	// Bir tunel BIRDEN COK ad tasiyabilir.
	if _, err := s.AddHostname(ctx, tid, tn.ID, "api--default.rpshell.app", store.HostTypeScoped); err != nil {
		t.Fatalf("ikinci ad eklenemedi: %v", err)
	}
	byTunnel, err := s.ListHostnamesByTunnel(ctx, tid, tn.ID)
	if err != nil || len(byTunnel) != 2 {
		t.Fatalf("ListHostnamesByTunnel: %v len=%d", err, len(byTunnel))
	}

	list, err := s.ListHostnames(ctx, tid)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListHostnames: %v len=%d", err, len(list))
	}

	if err := s.DeleteHostname(ctx, tid, h.ID); err != nil {
		t.Fatalf("DeleteHostname: %v", err)
	}
	// Silindikten sonra ad yeniden alinabilmeli.
	if _, err := s.AddHostname(ctx, tid, tn.ID, "api.rpshell.app", store.HostTypeGlobal); err != nil {
		t.Fatalf("silinen ad yeniden alinamadi: %v", err)
	}

	// Domainler tunelden bagimsizdir (ON DELETE SET NULL). Tunel silinince
	// domainler silinmez, yalnizca baglantilari kopar (TunnelID bosalir).
	// ListHostRoutes JOIN tunnels kullandigi icin yonlendirmeden kendiliginden duser.
	if err := s.DeleteTunnel(ctx, tid, tn.ID); err != nil {
		t.Fatal(err)
	}
	left, err := s.ListHostnames(ctx, tid)
	if err != nil || len(left) != 2 {
		t.Fatalf("tunel silindikten sonra domainler korunmali: %v len=%d", err, len(left))
	}
	for _, h := range left {
		if h.TunnelID != "" {
			t.Errorf("domainin tunel baglantisi koparilmaliydi: %+v", h)
		}
	}
}

// Baska kiracinin hostname'i gorulememeli/silinememeli.
func TestHostnameTenantIsolation(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, _ := s.CreateTenant(ctx, "kiraci-a")
	b, _ := s.CreateTenant(ctx, "kiraci-b")
	ca, _ := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	cb, _ := s.CreateClient(ctx, b.ID, "b-pc", "tid-b", "hash-b")
	ta, _ := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x")
	tb, _ := s.CreateTunnel(ctx, b.ID, cb.ID, "http://y")

	ha, err := s.AddHostname(ctx, a.ID, ta.ID, "a.rpshell.app", store.HostTypeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := s.AddHostname(ctx, b.ID, tb.ID, "b.rpshell.app", store.HostTypeGlobal)
	if err != nil {
		t.Fatal(err)
	}

	listA, _ := s.ListHostnames(ctx, a.ID)
	if len(listA) != 1 || listA[0].ID != ha.ID {
		t.Fatalf("SIZINTI: ListHostnames(a) = %+v", listA)
	}
	if err := s.DeleteHostname(ctx, a.ID, hb.ID); err != store.ErrNotFound {
		t.Fatalf("SIZINTI: capraz DeleteHostname: %v", err)
	}
	// B'nin adi bozulmamis olmali.
	listB, _ := s.ListHostnames(ctx, b.ID)
	if len(listB) != 1 {
		t.Fatalf("capraz silme B'nin adini bozdu: %+v", listB)
	}
}

// Ingress yolu KASITLI olarak kiracidan bagimsiz: tum yonlendirmeleri gorur.
func TestListHostRoutesIsTenantAgnostic(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	a, _ := s.CreateTenant(ctx, "kiraci-a")
	b, _ := s.CreateTenant(ctx, "kiraci-b")
	ca, _ := s.CreateClient(ctx, a.ID, "a-pc", "tid-a", "hash-a")
	cb, _ := s.CreateClient(ctx, b.ID, "b-pc", "tid-b", "hash-b")
	ta, _ := s.CreateTunnel(ctx, a.ID, ca.ID, "http://x")
	tb, _ := s.CreateTunnel(ctx, b.ID, cb.ID, "http://y")
	if _, err := s.AddHostname(ctx, a.ID, ta.ID, "a.rpshell.app", store.HostTypeGlobal); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddHostname(ctx, b.ID, tb.ID, "b.rpshell.app", store.HostTypeGlobal); err != nil {
		t.Fatal(err)
	}

	routes, err := s.ListHostRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("ListHostRoutes 2 kayit donmeliydi: %d", len(routes))
	}
	// Yonlendirme icin gereken her sey dolu olmali; eksik bir alan sicak yolda
	// ikinci bir sorgu demek olurdu.
	for _, r := range routes {
		if r.FQDN == "" || r.TenantID == "" || r.TunnelID == "" || r.ClientID == "" || r.Target == "" {
			t.Fatalf("eksik yonlendirme kaydi: %+v", r)
		}
	}
}

func TestReservedNames(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	for _, n := range []string{"www", "api", "admin"} {
		ok, err := s.IsReservedName(ctx, n)
		if err != nil || !ok {
			t.Fatalf("%q rezerve olmaliydi: %v %v", n, ok, err)
		}
	}
	// Case duyarsiz olmali: "API" de rezerve.
	if ok, err := s.IsReservedName(ctx, "API"); err != nil || !ok {
		t.Fatalf("rezerve kontrolu case duyarli: %v %v", ok, err)
	}
	if ok, err := s.IsReservedName(ctx, "benim-projem"); err != nil || ok {
		t.Fatalf("serbest ad rezerve gorundu: %v %v", ok, err)
	}
}

func TestBetterAuthOrganizationSync(t *testing.T) {
	ctx := context.Background()
	s := freshStore(t)

	// 1. Better Auth organization olusturdugunda tenants tablosuna yansimali:
	orgID := "org_betterauth_test"
	orgSlug := "team-alpha"
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO "organization" ("id", "name", "slug") VALUES ($1, $2, $3)`,
		orgID, "Team Alpha", orgSlug); err != nil {
		t.Fatalf("organization eklenemedi: %v", err)
	}

	tn, err := s.GetTenantBySlug(ctx, orgSlug)
	if err != nil {
		t.Fatalf("sync_organization_to_tenants calismadi: %v", err)
	}
	if tn.ID != orgID {
		t.Fatalf("beklenen kiraci id %q, alinan %q", orgID, tn.ID)
	}

	// 2. Go store CreateTenant calistiginda organization tablosuna yansimali:
	tn2, err := s.CreateTenant(ctx, "team-beta")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	var orgSlug2 string
	if err := s.pool.QueryRow(ctx,
		`SELECT "slug" FROM "organization" WHERE "id" = $1`, tn2.ID).Scan(&orgSlug2); err != nil {
		t.Fatalf("sync_tenants_to_organization calismadi: %v", err)
	}
	if orgSlug2 != "team-beta" {
		t.Fatalf("organization slug beklenen team-beta, alinan %q", orgSlug2)
	}
}
