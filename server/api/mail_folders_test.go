package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tkodcumpeg4/zorven/server/store"
)

type folderMailStore struct {
	store.Store
	names  []string
	listed []string
	moved  []string
}

func (f *folderMailStore) ListTenantMailFolderNames(context.Context, string) ([]string, error) {
	return f.names, nil
}

func (f *folderMailStore) ListMailMessages(_ context.Context, _ string, folder string, _ int) ([]store.MailMessage, error) {
	f.listed = append(f.listed, folder)
	return nil, nil
}

func (f *folderMailStore) MoveMailMessage(_ context.Context, _ string, id, folder string) (uint32, error) {
	if id == "yok" {
		return 0, store.ErrNotFound
	}
	f.moved = append(f.moved, id+">"+folder)
	return 1, nil
}

func folderReq(method, url, body string) *http.Request {
	req := httptest.NewRequest(method, url, bytes.NewReader([]byte(body)))
	return req.WithContext(withTenant(req.Context(), "ten_acme"))
}

func TestMailFolders_ListAndBox(t *testing.T) {
	st := &folderMailStore{names: []string{"Is", "Is/Musteri"}}
	s := &Server{Store: st, MailDomain: "mail.zorven.app"}

	rec := httptest.NewRecorder()
	s.listMailFolders(rec, folderReq("GET", "/api/v1/mail/folders", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"Is/Musteri"`) {
		t.Fatalf("folders: %d %s", rec.Code, rec.Body.String())
	}

	for _, box := range []string{"drafts", "Is/Musteri"} {
		rec = httptest.NewRecorder()
		s.listMail(rec, folderReq("GET", "/api/v1/mail/messages?box="+strings.ReplaceAll(box, "/", "%2F"), ""))
		if rec.Code != 200 {
			t.Fatalf("box=%s: %d", box, rec.Code)
		}
	}
	if strings.Join(st.listed, ",") != "drafts,u:Is/Musteri" {
		t.Fatalf("listed: %v", st.listed)
	}
	rec = httptest.NewRecorder()
	s.listMail(rec, folderReq("GET", "/api/v1/mail/messages?box=a%2F%2Fb", ""))
	if rec.Code != 400 {
		t.Fatalf("gecersiz box: %d", rec.Code)
	}
}

func TestMoveMail(t *testing.T) {
	st := &folderMailStore{names: []string{"Is"}}
	s := &Server{Store: st, MailDomain: "mail.zorven.app"}
	move := func(id, body string) *httptest.ResponseRecorder {
		req := folderReq("POST", "/api/v1/mail/messages/"+id+"/move", body)
		req.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		s.moveMail(rec, req)
		return rec
	}
	if rec := move("m1", `{"folder":"Is"}`); rec.Code != 200 {
		t.Fatalf("tasima: %d %s", rec.Code, rec.Body.String())
	}
	if rec := move("m2", `{"folder":"trash"}`); rec.Code != 200 {
		t.Fatalf("trash: %d", rec.Code)
	}
	if strings.Join(st.moved, ",") != "m1>u:Is,m2>trash" {
		t.Fatalf("moved: %v", st.moved)
	}
	if rec := move("m3", `{"folder":"Olmayan"}`); rec.Code != 404 {
		t.Fatalf("olmayan klasor: %d", rec.Code)
	}
	if rec := move("m3", `{"folder":""}`); rec.Code != 422 {
		t.Fatalf("bos klasor: %d", rec.Code)
	}
	if rec := move("m3", `{"folder":"a*"}`); rec.Code != 422 {
		t.Fatalf("gecersiz klasor: %d", rec.Code)
	}
	if rec := move("yok", `{"folder":"Is"}`); rec.Code != 404 {
		t.Fatalf("olmayan mesaj: %d", rec.Code)
	}
}
