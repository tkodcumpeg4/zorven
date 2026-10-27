// Calistirma: node --test --experimental-strip-types web/tests/screenVideo.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { isVideoFrame, parseVideoFrame } from '../app/lib/screenVideo.ts'
import { parseAgentStats } from '../app/lib/screenPerf.ts'

// shared/protocol/screen2_video.go ile ayni tel bicimi.
function header(kind: number, key: boolean, seq = 1): number[] {
  const h = new Array(14).fill(0)
  h[0] = 9
  h[8] = 7 // streamID
  h[12] = seq
  h[13] = key ? 0x02 : 0
  return [...h, kind]
}

const buf = (a: number[]) => new Uint8Array(a).buffer as ArrayBuffer

test('config cercevesi', () => {
  const codec = 'avc1.42c01f'
  const desc = [1, 0x42, 0xc0, 0x1f, 0xff]
  const b = buf([...header(1, true), 0x05, 0x00, 0x03, 0x00, codec.length, ...[...codec].map(c => c.charCodeAt(0)), ...desc])
  assert.ok(isVideoFrame(b))
  const m = parseVideoFrame(b)!
  assert.equal(m.kind, 1)
  assert.equal(m.w, 1280)
  assert.equal(m.h, 768)
  assert.equal(m.codec, codec)
  assert.deepEqual([...m.desc], desc)
  assert.equal(m.key, true)
})

test('chunk cercevesi: 64 bit zaman damgasi ve AVCC verisi', () => {
  const ts = [0, 0, 0, 1, 0, 0, 0, 5] // 2^32 + 5
  const data = [0, 0, 0, 2, 0x65, 0x88]
  const m = parseVideoFrame(buf([...header(2, false, 9), ...ts, ...data]))!
  assert.equal(m.kind, 2)
  assert.equal(m.ts, 4294967296 + 5)
  assert.equal(m.seq, 9)
  assert.equal(m.key, false)
  assert.deepEqual([...m.data], data)
})

test('stop ve bozuk girdiler', () => {
  assert.equal(parseVideoFrame(buf(header(3, false)))!.kind, 3)
  assert.equal(parseVideoFrame(buf([9, 0, 0])), null)
  assert.equal(parseVideoFrame(buf(header(77, false))), null)
  assert.equal(parseVideoFrame(buf([...header(1, true), 0, 0])), null) // kisa config
  assert.equal(parseVideoFrame(buf([...header(2, false), 1, 2, 3])), null) // kisa chunk
  assert.equal(isVideoFrame(buf([6, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1])), false) // karo (FrameScreen)
})

test('ajan istatistiginde video alanlari', () => {
  const a = parseAgentStats({ mode: 'video', video_kbps: 2500, video_bitrate: 4000000, backend: 'mf-hw' })!
  assert.equal(a.mode, 'video')
  assert.equal(a.video_kbps, 2500)
  assert.equal(a.video_bitrate, 4000000)
  assert.equal(a.backend, 'mf-hw')
  const old = parseAgentStats({})!
  assert.equal(old.mode, 'tiles')
  assert.equal(old.backend, '')
})
