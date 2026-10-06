'use strict'
// Zorven Node SDK — Zorven CLI'sini ("zorven") saran ince bir kütüphane.
// Yerel bir portu programatik olarak tünelller ve herkese açık URL'i döner.
//
// Gereksinim: `zorven` CLI kurulu olmalı (https://zorven.app/install.sh) veya
// `bin` seçeneğiyle tam yol verilmeli.
//
// const { connect } = require('@zorven/sdk')
// const t = await connect({ token: process.env.ZORVEN_TOKEN, port: 8080 })
// console.log(t.url)          // https://...zorven.app
// // ... iş bitince:
// t.close()

const { spawn } = require('child_process')

/**
 * @param {Object} opts
 * @param {string} opts.token   İstemci token'ı (zrv_live_...). Zorunlu.
 * @param {number|string} [opts.port]   Yerel port (ör. 8080).
 * @param {string} [opts.target]        Tam hedef (ör. "http://localhost:8080"). port veya target zorunlu.
 * @param {string} [opts.server]        Sunucu adresi (varsayılan CLI ayarı).
 * @param {string} [opts.bin]           CLI yolu (varsayılan "zorven").
 * @param {number} [opts.timeoutMs]     Bağlantı zaman aşımı (varsayılan 30000).
 * @returns {Promise<{url:string, urls:string[], process:import('child_process').ChildProcess, close:()=>void}>}
 */
function connect(opts = {}) {
  const { token, port, target, server, bin = 'zorven', timeoutMs = 30000 } = opts
  if (!token) return Promise.reject(new Error('zorven: token zorunlu'))
  const tgt = target != null ? String(target) : (port != null ? String(port) : null)
  if (!tgt) return Promise.reject(new Error('zorven: port veya target zorunlu'))

  const args = [tgt, '--token', token, '--print-url', '--no-auto-update']
  if (server) args.push('--server', server)

  return new Promise((resolve, reject) => {
    let proc
    try {
      proc = spawn(bin, args, { stdio: ['ignore', 'pipe', 'pipe'] })
    } catch (e) {
      return reject(e)
    }

    const urls = []
    let settled = false
    let buf = ''

    const timer = setTimeout(() => {
      if (!settled) {
        settled = true
        try { proc.kill() } catch (_) {}
        reject(new Error('zorven: bağlantı zaman aşımına uğradı'))
      }
    }, timeoutMs)

    function onLine(line) {
      const m = line.match(/^zorven-url:\s*(\S+)/)
      if (m) {
        urls.push(m[1])
        if (!settled) {
          settled = true
          clearTimeout(timer)
          resolve({
            url: urls[0],
            urls,
            process: proc,
            close: () => { try { proc.kill() } catch (_) {} },
          })
        }
      }
    }

    function onData(chunk) {
      buf += chunk.toString()
      let i
      while ((i = buf.indexOf('\n')) >= 0) {
        onLine(buf.slice(0, i))
        buf = buf.slice(i + 1)
      }
    }

    proc.stdout.on('data', onData)
    proc.stderr.on('data', onData)
    proc.on('error', (e) => { if (!settled) { settled = true; clearTimeout(timer); reject(e) } })
    proc.on('exit', (code) => {
      if (!settled) { settled = true; clearTimeout(timer); reject(new Error('zorven CLI çıktı: kod ' + code)) }
    })
  })
}

module.exports = { connect }
