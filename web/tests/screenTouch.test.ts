import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TouchGestures, ZOOM_MAX, ZOOM_IDENTITY, applyPinch, clampPan, codeForChar, consumeSticky, zoomTransform } from '../app/lib/screenTouch.ts'

test('tek dokunus = sol tik; dokunus aninda imlec parmagin altina gider', () => {
  const g = new TouchGestures()
  assert.deepEqual(g.pointerDown(1, 100, 200, 0), [{ type: 'move', x: 100, y: 200 }])
  assert.deepEqual(g.pointerUp(1, 120), [{ type: 'click', button: 'left', x: 100, y: 200, count: 1 }])
  assert.equal(g.active, false)
})

test('iki hizli dokunus = cift tik (count 2); yavas ya da uzak ikinci dokunus tek tik', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 50, 50, 0); g.pointerUp(1, 80)
  g.pointerDown(1, 55, 52, 200)
  assert.deepEqual(g.pointerUp(1, 260), [{ type: 'click', button: 'left', x: 55, y: 52, count: 2 }])
  // ucuncu dokunus yeni bir dizi baslatir
  g.pointerDown(1, 55, 52, 300)
  assert.equal((g.pointerUp(1, 340)[0] as any).count, 1)
  // yavas
  const h = new TouchGestures()
  h.pointerDown(1, 10, 10, 0); h.pointerUp(1, 50)
  h.pointerDown(1, 10, 10, 1000)
  assert.equal((h.pointerUp(1, 1040)[0] as any).count, 1)
  // uzak
  const k = new TouchGestures()
  k.pointerDown(1, 10, 10, 0); k.pointerUp(1, 50)
  k.pointerDown(1, 200, 200, 100)
  assert.equal((k.pointerUp(1, 140)[0] as any).count, 1)
})

test('uzun basma (500 ms, hareketsiz) = sag tik; birakinca ek tik yok', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 30, 40, 1000)
  assert.equal(g.nextDeadline, 1500)
  assert.deepEqual(g.tick(1400), [])
  assert.deepEqual(g.tick(1500), [{ type: 'click', button: 'right', x: 30, y: 40, count: 1 }])
  assert.equal(g.nextDeadline, null)
  assert.deepEqual(g.pointerUp(1, 1600), [])
})

test('zamanlayici gec kalsa da uzun basili birakma sag tik sayilir', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 30, 40, 0)
  assert.deepEqual(g.pointerUp(1, 700), [{ type: 'click', button: 'right', x: 30, y: 40, count: 1 }])
})

test('kucuk titreme tik/uzun basmayi bozmaz; buyuk hareket surukleme olur ve uzun basma iptal', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 10, 10, 0)
  assert.deepEqual(g.pointerMove(1, 14, 12, 20), [])
  assert.deepEqual(g.tick(500).length, 1)
  const h = new TouchGestures()
  h.pointerDown(1, 10, 10, 0)
  assert.deepEqual(h.pointerMove(1, 60, 10, 30), [{ type: 'move', x: 60, y: 10 }])
  assert.deepEqual(h.tick(900), [])
  assert.deepEqual(h.pointerMove(1, 80, 20, 60), [{ type: 'move', x: 80, y: 20 }])
  assert.deepEqual(h.pointerUp(1, 100), [], 'surukleme birakilinca tik yok')
})

test('surukleme kilidi: sol dugme baslangicta basilir, birakinca kalkar', () => {
  let lock = true
  const g = new TouchGestures({ dragLock: () => lock })
  g.pointerDown(1, 10, 10, 0)
  assert.deepEqual(g.pointerMove(1, 50, 10, 20), [
    { type: 'down', button: 'left', x: 10, y: 10 },
    { type: 'move', x: 50, y: 10 },
  ])
  assert.deepEqual(g.pointerMove(1, 70, 30, 40), [{ type: 'move', x: 70, y: 30 }])
  assert.deepEqual(g.pointerUp(1, 60), [{ type: 'up', button: 'left', x: 70, y: 30 }])
  lock = false
  g.pointerDown(1, 10, 10, 100)
  assert.deepEqual(g.pointerMove(1, 50, 10, 120), [{ type: 'move', x: 50, y: 10 }])
})

test('surukleme kilidi: ikinci parmak gelirse basili dugme birakilir', () => {
  const g = new TouchGestures({ dragLock: () => true })
  g.pointerDown(1, 10, 10, 0)
  g.pointerMove(1, 50, 10, 20)
  assert.deepEqual(g.pointerDown(2, 200, 200, 40), [{ type: 'up', button: 'left', x: 50, y: 10 }])
})

test('iki parmak surukleme = tekerlek (parmak yukari => deltaY pozitif); kaydirma esigi alti sessiz', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 100, 300, 0)
  g.pointerDown(2, 200, 300, 5)
  assert.deepEqual(g.pointerMove(1, 100, 296, 10), [], 'esik alti')
  g.pointerMove(2, 200, 296, 11)
  const a = g.pointerMove(1, 100, 280, 20)
  assert.equal(a.length, 1)
  assert.equal(a[0]!.type, 'wheel')
  assert.ok((a[0] as any).dy > 0, 'parmak yukari => icerik asagi: dy>0')
  // yatay
  const b = g.pointerMove(1, 130, 280, 30)
  assert.ok((b[0] as any).dx < 0)
  // birakinca tik yok
  assert.deepEqual(g.pointerUp(1, 40), [])
  assert.deepEqual(g.pointerUp(2, 50), [])
  assert.equal(g.active, false)
})

test('iki parmak kistirma = zoom (faktor mesafe orani); karar sonrasi tekerlek uretmez', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 100, 300, 0)
  g.pointerDown(2, 200, 300, 5)
  const a = g.pointerMove(2, 260, 300, 20)
  assert.equal(a.length, 1)
  assert.equal(a[0]!.type, 'zoom')
  assert.ok(Math.abs((a[0] as any).factor - 1.6) < 1e-9)
  const b = g.pointerMove(2, 360, 300, 30)
  assert.equal(b[0]!.type, 'zoom')
  assert.ok((b[0] as any).factor > 1)
})

test('iki parmak gesti bitince kalan parmak tik uretmez', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 100, 300, 0)
  g.pointerDown(2, 200, 300, 5)
  assert.deepEqual(g.pointerUp(2, 50), [])
  assert.deepEqual(g.pointerUp(1, 60), [])
  g.pointerDown(1, 5, 5, 900)
  assert.equal((g.pointerUp(1, 950)[0] as any).type, 'click')
})

test('iptal (pointercancel) tik uretmez', () => {
  const g = new TouchGestures()
  g.pointerDown(1, 10, 10, 0)
  assert.deepEqual(g.pointerUp(1, 40, true), [])
})

test('reset: basili surukleme dugmesi birakilir', () => {
  const g = new TouchGestures({ dragLock: () => true })
  g.pointerDown(1, 10, 10, 0)
  g.pointerMove(1, 40, 10, 10)
  assert.deepEqual(g.reset(), [{ type: 'up', button: 'left', x: 40, y: 10 }])
  assert.equal(g.active, false)
})

test('applyPinch: olcek sinirli; parmak altindaki nokta sabit; olcek 1de kaydirma yok', () => {
  const W = 800, H = 600
  let s = applyPinch(ZOOM_IDENTITY, { factor: 2, cx: 400, cy: 300, panX: 0, panY: 0 }, W, H)
  assert.equal(s.scale, 2)
  assert.equal(s.tx, 0)
  // sag ust kose yakini: o nokta sabit kalmali
  const cx = 700, cy = 100
  s = applyPinch(ZOOM_IDENTITY, { factor: 2, cx, cy, panX: 0, panY: 0 }, W, H)
  const px = cx - W / 2, py = cy - H / 2
  assert.ok(Math.abs((px * 2 + s.tx) - px) < 1e-9, 'x sabit')
  assert.ok(Math.abs((py * 2 + s.ty) - py) < 1e-9, 'y sabit')
  s = applyPinch(s, { factor: 100, cx, cy, panX: 0, panY: 0 }, W, H)
  assert.equal(s.scale, ZOOM_MAX)
  s = applyPinch(s, { factor: 0.0001, cx, cy, panX: 0, panY: 0 }, W, H)
  assert.deepEqual(s, ZOOM_IDENTITY)
})

test('clampPan ve zoomTransform', () => {
  const c = clampPan({ scale: 2, tx: 9999, ty: -9999 }, 800, 600)
  assert.equal(c.tx, 400)
  assert.equal(c.ty, -300)
  assert.equal(zoomTransform(ZOOM_IDENTITY), '')
  assert.match(zoomTransform({ scale: 2, tx: 10, ty: -5 }), /translate\(10\.0px, -5\.0px\) scale\(2\.000\)/)
})

test('yapiskan Ctrl/Alt bir sonraki tusa uygulanip sifirlanir; codeForChar', () => {
  const r = consumeSticky({ ctrl: true, alt: false })
  assert.deepEqual(r.mods, { ctrl: true, alt: false })
  assert.deepEqual(r.next, { ctrl: false, alt: false })
  assert.equal(codeForChar('c'), 'KeyC')
  assert.equal(codeForChar('Z'), 'KeyZ')
  assert.equal(codeForChar('7'), 'Digit7')
  assert.equal(codeForChar('ş'), '')
})
