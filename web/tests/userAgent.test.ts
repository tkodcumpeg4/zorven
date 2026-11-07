// Calistirma: cd web && node --test tests/userAgent.test.ts
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { deviceLabel, parseUserAgent } from '../app/lib/userAgent.ts'

const UA = {
  chromeWin: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36',
  edgeWin: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0',
  firefoxLinux: 'Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0',
  safariMac: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15',
  safariIphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1',
  chromeAndroid: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36',
  chromeIos: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0.0.0 Mobile/15E148 Safari/604.1',
  operaWin: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 OPR/115.0.0.0',
  chromeOs: 'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36',
  curl: 'curl/8.5.0',
}

test('tarayici ve isletim sistemi ayristirilir', () => {
  assert.equal(deviceLabel(UA.chromeWin), 'Chrome · Windows')
  assert.equal(deviceLabel(UA.edgeWin), 'Edge · Windows') // Edge, Chrome'dan once
  assert.equal(deviceLabel(UA.firefoxLinux), 'Firefox · Linux')
  assert.equal(deviceLabel(UA.safariMac), 'Safari · macOS')
  assert.equal(deviceLabel(UA.safariIphone), 'Safari · iOS')
  assert.equal(deviceLabel(UA.chromeAndroid), 'Chrome · Android') // Android, Linux'tan once
  assert.equal(deviceLabel(UA.chromeIos), 'Chrome · iOS')
  assert.equal(deviceLabel(UA.operaWin), 'Opera · Windows')
  assert.equal(deviceLabel(UA.chromeOs), 'Chrome · ChromeOS') // CrOS, Linux/X11'den once
  assert.equal(deviceLabel(UA.curl), 'curl')
})

test('telefon bayragi: yalniz telefonlar', () => {
  assert.equal(parseUserAgent(UA.safariIphone)?.mobile, true)
  assert.equal(parseUserAgent(UA.chromeAndroid)?.mobile, true)
  assert.equal(parseUserAgent(UA.chromeIos)?.mobile, true)
  assert.equal(parseUserAgent(UA.chromeWin)?.mobile, false)
  assert.equal(parseUserAgent(UA.safariMac)?.mobile, false)
  // Android tablet ("Mobile" yok) masaustu ikonu
  assert.equal(parseUserAgent('Mozilla/5.0 (Linux; Android 13; SM-X700) AppleWebKit/537.36 Chrome/130.0.0.0 Safari/537.36')?.mobile, false)
})

test('bos/taninmayan User-Agent', () => {
  assert.equal(parseUserAgent(''), null)
  assert.equal(parseUserAgent(null), null)
  assert.equal(parseUserAgent(undefined), null)
  assert.equal(parseUserAgent('   '), null)
  assert.equal(deviceLabel(''), '')
  assert.equal(deviceLabel('x'), '')
  assert.deepEqual(parseUserAgent('Mozilla/5.0 (Windows NT 10.0)'), { browser: '', os: 'Windows', mobile: false })
})
