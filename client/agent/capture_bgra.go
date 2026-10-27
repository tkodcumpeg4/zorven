package agent

import (
	"encoding/binary"
	"runtime"
	"sync"
)

// bgraToRGBA, GDI'nin BGRA (alfa guvenilmez) satirlarini RGBA'ya cevirir ve
// alfayi 255 yapar. Platformdan bagimsiz oldugu icin her yerde test edilir.
// Piksel basina bayt bayt atama yerine iki pikseli tek 64 bit sozcukte cevirir;
// buyuk goruntulerde satir bantlari paralel islenir.
func bgraToRGBA(dst, src []byte, w, h, srcStride, dstStride int) {
	workers := min(runtime.GOMAXPROCS(0), 4)
	if workers <= 1 || w*h < 256*1024 || h < 16 {
		bgraToRGBARows(dst, src, w, 0, h, srcStride, dstStride)
		return
	}
	var wg sync.WaitGroup
	per := (h + workers - 1) / workers
	for y := 0; y < h; y += per {
		y1 := min(y+per, h)
		wg.Add(1)
		go func() {
			defer wg.Done()
			bgraToRGBARows(dst, src, w, y, y1, srcStride, dstStride)
		}()
	}
	wg.Wait()
}

func bgraToRGBARows(dst, src []byte, w, y0, y1, srcStride, dstStride int) {
	le := binary.LittleEndian
	const lane = 0x000000ff000000ff
	for y := y0; y < y1; y++ {
		s := src[y*srcStride : y*srcStride+w*4]
		d := dst[y*dstStride : y*dstStride+w*4]
		x := 0
		for ; x+8 <= len(s); x += 8 {
			v := le.Uint64(s[x:])
			le.PutUint64(d[x:], (v&lane)<<16|(v>>16)&lane|v&0x0000ff000000ff00|0xff000000ff000000)
		}
		for ; x+4 <= len(s); x += 4 {
			d[x], d[x+1], d[x+2], d[x+3] = s[x+2], s[x+1], s[x], 255
		}
	}
}
