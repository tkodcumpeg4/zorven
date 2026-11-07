package mail

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/server/store"
)

// rawSMTP, ham TCP uzerinden satir satir SMTP konusmasi yapar.
type rawSMTP struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func dialRaw(t *testing.T, addr string) *rawSMTP {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	x := &rawSMTP{t: t, c: c, r: bufio.NewReader(c)}
	x.reply() // 220 karsilama
	return x
}

// reply, coklu satirli yanitin son satirini doner.
func (x *rawSMTP) reply() string {
	x.t.Helper()
	for {
		l, err := x.r.ReadString('\n')
		if err != nil {
			x.t.Fatalf("yanit okunamadi: %v", err)
		}
		if len(l) >= 4 && l[3] == ' ' {
			return strings.TrimRight(l, "\r\n")
		}
	}
}

func (x *rawSMTP) cmd(line string) string {
	x.t.Helper()
	if _, err := x.c.Write([]byte(line + "\r\n")); err != nil {
		x.t.Fatal(err)
	}
	return x.reply()
}

// Gercek go-smtp sunucusu (yamali yerel kopya) uzerinden alan adsiz postmaster
// alicisi kabul edilir ve ileti postmaster@<platform> sistem kutusuna teslim edilir.
func TestWireBarePostmasterRecipientDelivered(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	e.dns.txt["example.com"] = []string{"v=spf1 -all"}
	e.dns.txt["_dmarc.example.com"] = []string{"v=DMARC1; p=reject"} // postmaster muaf
	addr := startRcvServer(t, e)

	x := dialRaw(t, addr)
	if r := x.cmd("EHLO mx.sender.example"); !strings.HasPrefix(r, "250") {
		t.Fatal(r)
	}
	if r := x.cmd("MAIL FROM:<bounce@example.com>"); !strings.HasPrefix(r, "250") {
		t.Fatal(r)
	}
	for _, rc := range []string{"RCPT TO:<postmaster>", "RCPT TO:<PostMaster>"} {
		if r := x.cmd(rc); !strings.HasPrefix(r, "250") {
			t.Fatalf("%s: %s", rc, r)
		}
	}
	if r := x.cmd("DATA"); !strings.HasPrefix(r, "354") {
		t.Fatal(r)
	}
	if _, err := x.c.Write([]byte(rcvMsg("") + ".\r\n")); err != nil {
		t.Fatal(err)
	}
	if r := x.reply(); !strings.HasPrefix(r, "250") {
		t.Fatalf("DATA: %s", r)
	}
	x.cmd("QUIT")

	got := e.st.all(store.DefaultTenantID, "postmaster@zorven.app", store.MailFolderInbox)
	if len(got) == 0 {
		t.Fatal("ileti postmaster@zorven.app sistem kutusuna teslim edilmedi")
	}
}

// Alan adsiz diger adresler hala reddedilir; MAIL FROM alan adsiz postmaster kabul etmez.
func TestWireBareNonPostmasterRejected(t *testing.T) {
	e := newRcvEnv(t, AuthEnforce)
	addr := startRcvServer(t, e)

	x := dialRaw(t, addr)
	x.cmd("EHLO mx.sender.example")
	if r := x.cmd("MAIL FROM:<postmaster>"); !strings.HasPrefix(r, "501") {
		t.Fatalf("MAIL FROM:<postmaster> reddedilmeli: %s", r)
	}
	if r := x.cmd("MAIL FROM:<bounce@example.com>"); !strings.HasPrefix(r, "250") {
		t.Fatal(r)
	}
	for _, rc := range []string{"RCPT TO:<baskasi>", "RCPT TO:<abuse>", "RCPT TO:<postmasterx>", "RCPT TO:<>"} {
		if r := x.cmd(rc); strings.HasPrefix(r, "2") {
			t.Fatalf("%s kabul edilmemeli: %s", rc, r)
		}
	}
}
