import { WebSocket as WsClient } from 'ws'

const sockets = new WeakMap<object, WsClient>()
const pending = new WeakMap<object, string[]>()

function backendUrl(): string {
  const backend = process.env.LICODE_BACKEND || 'http://127.0.0.1:8080'
  return backend.replace(/^http/, 'ws') + '/ws'
}

export default defineWebSocketHandler({
  open(peer) {
    const headers: Record<string, string> = {}
    try {
      const cookie = peer.request?.headers?.get?.('cookie')
      if (cookie) headers.cookie = cookie
    } catch {}
    let upstream: WsClient
    try {
      upstream = new WsClient(backendUrl(), { headers })
    } catch {
      try {
        peer.close()
      } catch {}
      return
    }
    sockets.set(peer, upstream)
    pending.set(peer, [])

    const flush = () => {
      const q = pending.get(peer) || []
      pending.set(peer, [])
      for (const text of q) {
        try {
          upstream.send(text)
        } catch {}
      }
    }
    upstream.on('open', flush)
    upstream.on('message', (data) => {
      try {
        peer.send(data.toString())
      } catch {}
    })
    upstream.on('close', () => {
      try {
        peer.close()
      } catch {}
      sockets.delete(peer)
      pending.delete(peer)
    })
    upstream.on('error', () => {
      try {
        peer.close()
      } catch {}
    })
  },
  message(peer, message) {
    const upstream = sockets.get(peer)
    if (!upstream) return
    try {
      const text = typeof message === 'string' ? message : (message.text?.() ?? String(message.rawData ?? ''))
      if (upstream.readyState === WsClient.OPEN) upstream.send(text)
      else pending.get(peer)?.push(text)
    } catch {}
  },
  close(peer) {
    const upstream = sockets.get(peer)
    try {
      upstream?.close()
    } catch {}
    sockets.delete(peer)
    pending.delete(peer)
  },
  error(peer) {
    const upstream = sockets.get(peer)
    try {
      upstream?.close()
    } catch {}
    sockets.delete(peer)
  },
})
