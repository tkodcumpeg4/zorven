import type { ChildProcess } from 'child_process'

export interface ConnectOptions {
  /** Client token (zrv_live_...). Required. */
  token: string
  /** Local port (e.g. 8080). Either `port` or `target` is required. */
  port?: number | string
  /** Full target (e.g. "http://localhost:8080"). */
  target?: string
  /** Server address (defaults to the CLI's configured server). */
  server?: string
  /** Path to the zorven CLI (default "zorven"). */
  bin?: string
  /** Connect timeout in ms (default 30000). */
  timeoutMs?: number
}

export interface Tunnel {
  /** First public HTTPS URL. */
  url: string
  /** All public URLs. */
  urls: string[]
  /** The underlying CLI process. */
  process: ChildProcess
  /** Stop the tunnel (kills the CLI process). */
  close(): void
}

export function connect(opts: ConnectOptions): Promise<Tunnel>
