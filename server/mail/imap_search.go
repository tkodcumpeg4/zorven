package mail

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	gomessage "github.com/emersion/go-message"
	gomail "github.com/emersion/go-message/mail"
)

// Search, IMAP SEARCH'un temel kriterlerini (sira no/UID, bayrak, tarih, boyut,
// baslik, metin/govde, NOT/OR) destekler. Baslik/metin kriterleri icin ham
// mesaj yalnizca gerektiginde yuklenir.
func (s *imapSession) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	sel := s.sel
	if sel == nil {
		return nil, imapNo("", "No mailbox selected")
	}
	ctx, cancel := s.ctx()
	defer cancel()

	var maxUID uint32
	if n := len(sel.msgs); n > 0 {
		maxUID = sel.msgs[n-1].UID
	}
	maxSeq := uint32(len(sel.msgs))

	var data imap.SearchData
	var seqSet imap.SeqSet
	var uidSet imap.UIDSet
	for i, m := range sel.msgs {
		seq := uint32(i + 1)
		var raw []byte
		loaded := false
		getRaw := func() []byte {
			if !loaded {
				loaded = true
				if b, err := s.rawFor(ctx, m); err == nil {
					raw = b
				}
			}
			return raw
		}
		if !s.matchCriteria(m, seq, maxSeq, maxUID, criteria, sel.f.Folder, getRaw) {
			continue
		}
		uidSet.AddNum(imap.UID(m.UID))
		var num uint32
		switch kind {
		case imapserver.NumKindSeq:
			seqSet.AddNum(seq)
			num = seq
		case imapserver.NumKindUID:
			num = m.UID
		}
		if data.Min == 0 || num < data.Min {
			data.Min = num
		}
		if num > data.Max {
			data.Max = num
		}
		data.Count++
	}
	if kind == imapserver.NumKindSeq {
		data.All = seqSet
	} else {
		data.All = uidSet
	}
	return &data, nil
}

func hasFlag(m *imapMsg, folder string, f imap.Flag) bool {
	for _, x := range m.flags(folder) {
		if strings.EqualFold(string(x), string(f)) {
			return true
		}
	}
	return false
}

func (s *imapSession) matchCriteria(m *imapMsg, seq, maxSeq, maxUID uint32, c *imap.SearchCriteria, folder string, getRaw func() []byte) bool {
	for _, ss := range c.SeqNum {
		hit := false
		for _, r := range ss {
			if rangeHas(r.Start, r.Stop, maxSeq, seq) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	for _, us := range c.UID {
		hit := false
		for _, r := range us {
			if rangeHas(uint32(r.Start), uint32(r.Stop), maxUID, m.UID) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if !matchDate(m.ReceivedAt, c.Since, c.Before) {
		return false
	}
	for _, f := range c.Flag {
		if !hasFlag(m, folder, f) {
			return false
		}
	}
	for _, f := range c.NotFlag {
		if hasFlag(m, folder, f) {
			return false
		}
	}
	if c.Larger != 0 && m.Size <= c.Larger {
		return false
	}
	if c.Smaller != 0 && m.Size >= c.Smaller {
		return false
	}

	needsRaw := len(c.Header) > 0 || len(c.Text) > 0 || len(c.Body) > 0 || !c.SentSince.IsZero() || !c.SentBefore.IsZero()
	if needsRaw {
		raw := getRaw()
		entity := func() *gomessage.Entity {
			e, _ := gomessage.Read(bytes.NewReader(raw))
			if e == nil {
				e, _ = gomessage.New(gomessage.Header{}, bytes.NewReader(nil))
			}
			return e
		}
		for _, hf := range c.Header {
			if !matchHeaderFields(entity().Header.FieldsByKey(hf.Key), hf.Value) {
				return false
			}
		}
		if !c.SentSince.IsZero() || !c.SentBefore.IsZero() {
			mh := gomail.Header{Header: entity().Header}
			t, err := mh.Date()
			if err != nil || !matchDate(t, c.SentSince, c.SentBefore) {
				return false
			}
		}
		for _, t := range c.Text {
			if !matchEntity(entity(), t, true) {
				return false
			}
		}
		for _, b := range c.Body {
			if !matchEntity(entity(), b, false) {
				return false
			}
		}
	}

	for i := range c.Not {
		if s.matchCriteria(m, seq, maxSeq, maxUID, &c.Not[i], folder, getRaw) {
			return false
		}
	}
	for i := range c.Or {
		if !s.matchCriteria(m, seq, maxSeq, maxUID, &c.Or[i][0], folder, getRaw) &&
			!s.matchCriteria(m, seq, maxSeq, maxUID, &c.Or[i][1], folder, getRaw) {
			return false
		}
	}
	return true
}

// matchDate, RFC 3501 geregi saat dilimi/saat bilgisini yok sayip yalniz tarihi karsilastirir.
func matchDate(t, since, before time.Time) bool {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !before.IsZero() && !t.Before(before) {
		return false
	}
	return true
}

func matchHeaderFields(fields gomessage.HeaderFields, pattern string) bool {
	if pattern == "" {
		return fields.Len() > 0
	}
	pattern = strings.ToLower(pattern)
	for fields.Next() {
		v, _ := fields.Text()
		if strings.Contains(strings.ToLower(v), pattern) {
			return true
		}
	}
	return false
}

func matchEntity(e *gomessage.Entity, pattern string, includeHeader bool) bool {
	if pattern == "" {
		return true
	}
	if includeHeader && matchHeaderFields(e.Header.Fields(), pattern) {
		return true
	}
	if mr := e.MultipartReader(); mr != nil {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				return false
			}
			if matchEntity(part, pattern, includeHeader) {
				return true
			}
		}
		return false
	}
	t, _, err := e.Header.ContentType()
	if err != nil {
		return false
	}
	if !strings.HasPrefix(t, "text/") && !strings.HasPrefix(t, "message/") {
		return false
	}
	buf, err := io.ReadAll(e.Body)
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(buf), bytes.ToLower([]byte(pattern)))
}
