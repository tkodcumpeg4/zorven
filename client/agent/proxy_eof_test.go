package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tkodcumpeg4/zorven/shared/protocol"
)

// eofReader, son veriyle birlikte io.EOF doner (net/http Content-Length govdesi gibi).
type eofReader struct{ b []byte }

func (r *eofReader) Read(p []byte) (int, error) {
	n := copy(p, r.b)
	r.b = r.b[n:]
	if len(r.b) == 0 {
		return n, io.EOF
	}
	return n, nil
}

// O13: kucuk yanitta EOF bayragi son veri cercevesinde gider (ayri bos EOF
// cercevesi yok); trailer varsa eski sira (veri, trailer, EOF) korunur.
func TestStreamResponseMergesEOF(t *testing.T) {
	h := newBWHarness(t, false, false)
	defer h.close()
	cs := &clientSession{conn: h.cli, cork: h.cork}
	if err := cs.streamResponseTT(context.Background(), 7, "", &eofReader{b: []byte("merhaba")}, nil); err != nil {
		t.Fatal(err)
	}
	b := <-h.recv
	f, err := protocol.DecodeBodyFrame(b)
	if err != nil || !f.EOF || string(f.Payload) != "merhaba" || f.ReqID != 7 {
		t.Fatalf("tek veri+EOF cercevesi bekleniyordu: %+v %v", f, err)
	}
	select {
	case extra := <-h.recv:
		t.Fatalf("fazladan mesaj: %q", extra)
	case <-time.After(200 * time.Millisecond):
	}

	tr := func() http.Header { return http.Header{"Grpc-Status": {"0"}} }
	if err := cs.streamResponseTT(context.Background(), 8, "", &eofReader{b: []byte("x")}, tr); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for i := 0; i < 3; i++ {
		m := <-h.recv
		if f, err := protocol.DecodeBodyFrame(m); err == nil && m[0] == protocol.FrameResponseBody {
			kinds = append(kinds, fmt.Sprintf("veri(eof=%v,%d)", f.EOF, len(f.Payload)))
		} else {
			kinds = append(kinds, "kontrol")
		}
	}
	if strings.Join(kinds, " ") != "veri(eof=false,1) kontrol veri(eof=true,0)" {
		t.Fatalf("trailer sirasi: %v", kinds)
	}
}
