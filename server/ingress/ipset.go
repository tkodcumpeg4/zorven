package ingress

// IP kumesi — CIDR/IP listeleri icin bellek ici arama yapisi (FAZ 1 Part 2 / F03b-d).
//
// Tor cikis dugumleri, hosting/datacenter araliklari ve itibar (reputation)
// listeleri hep ayni bicimde dagitilir: satir satir IP veya CIDR. Bu yuzden
// hepsi ayni yapiyi kullanir.
//
// Arama yontemi: prefix uzunluguna gore kovalama. Her uzunluk icin maskelenmis
// adresten kumeye bir harita tutulur; arama, listede GERCEKTEN bulunan
// uzunluklar icin adresi maskeleyip harita aramasi yapar. Tipik bir listede
// 10-20 farkli uzunluk olur, yani arama sabit sayida harita aramasidir.
// Trie'ye gore cok daha az kod, benzer hiz.

import (
	"bufio"
	"io"
	"net/netip"
	"sort"
	"strings"
)

// ipSet, degismez bir IP/CIDR kumesi. Olusturulduktan sonra yalnizca okunur;
// guncelleme yeni bir ipSet uretip atomik olarak degistirmekle yapilir.
type ipSet struct {
	v4    map[int]map[[4]byte]struct{}
	v6    map[int]map[[16]byte]struct{}
	lens4 []int // buyukten kucuge (once en spesifik)
	lens6 []int
	count int
}

func newIPSet() *ipSet {
	return &ipSet{
		v4: make(map[int]map[[4]byte]struct{}),
		v6: make(map[int]map[[16]byte]struct{}),
	}
}

// add, tek bir prefix ekler.
func (s *ipSet) add(p netip.Prefix) {
	p = p.Masked()
	bits := p.Bits()
	a := p.Addr()
	if a.Is4() {
		m, ok := s.v4[bits]
		if !ok {
			m = make(map[[4]byte]struct{})
			s.v4[bits] = m
		}
		m[a.As4()] = struct{}{}
	} else {
		m, ok := s.v6[bits]
		if !ok {
			m = make(map[[16]byte]struct{})
			s.v6[bits] = m
		}
		m[a.As16()] = struct{}{}
	}
	s.count++
}

// finalize, uzunluk listelerini siralar. add'lerden sonra BIR KEZ cagrilir.
func (s *ipSet) finalize() {
	s.lens4 = s.lens4[:0]
	for b := range s.v4 {
		s.lens4 = append(s.lens4, b)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(s.lens4)))
	s.lens6 = s.lens6[:0]
	for b := range s.v6 {
		s.lens6 = append(s.lens6, b)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(s.lens6)))
}

// contains, adresin kumedeki herhangi bir prefix'e girip girmedigini soyler.
func (s *ipSet) contains(a netip.Addr) bool {
	if s == nil {
		return false
	}
	a = a.Unmap()
	if a.Is4() {
		for _, bits := range s.lens4 {
			p, err := a.Prefix(bits)
			if err != nil {
				continue
			}
			if _, ok := s.v4[bits][p.Addr().As4()]; ok {
				return true
			}
		}
		return false
	}
	for _, bits := range s.lens6 {
		p, err := a.Prefix(bits)
		if err != nil {
			continue
		}
		if _, ok := s.v6[bits][p.Addr().As16()]; ok {
			return true
		}
	}
	return false
}

// size, kumedeki prefix sayisi (tani/log icin).
func (s *ipSet) size() int {
	if s == nil {
		return 0
	}
	return s.count
}

// parseIPSet, satir satir IP/CIDR listesini okur.
// Yorum satirlari (#, ;) ve bos satirlar atlanir. Bir satirda bosluk varsa
// yalnizca ilk alan kullanilir (bazi listeler "IP  # aciklama" bicimindedir).
// Cozulemeyen satirlar SESSIZCE atlanir — tek bozuk satir listeyi dusurmemeli.
func parseIPSet(r io.Reader) *ipSet {
	set := newIPSet()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if i := strings.IndexAny(line, " \t"); i > 0 {
			line = line[:i]
		}
		if p, err := netip.ParsePrefix(line); err == nil {
			set.add(p)
			continue
		}
		if a, err := netip.ParseAddr(line); err == nil {
			set.add(netip.PrefixFrom(a, a.BitLen()))
		}
	}
	set.finalize()
	return set
}
