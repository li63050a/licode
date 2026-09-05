import http from 'node:http'
import tailwindcss from '@tailwindcss/vite'

const backend = process.env.LICODE_BACKEND || 'http://127.0.0.1:8080'

/**
 * dev 模式下 Nuxt CLI 的外层服务器不会把 WebSocket upgrade 转发给 worker，
 * 因此在 listen 钩子里对 /ws 做原始 TCP 隧道，转发到 Go 后端。
 * 生产构建（node .output/server/index.mjs）由 server/routes/ws.get.ts 的
 * crossws 中继处理，这里挂的监听不会存在。
 */
function attachWsTunnel(server: import('node:http').Server) {
  server.on('upgrade', (req, socket, head) => {
    if (!req.url || !req.url.startsWith('/ws')) return
    const u = new URL(backend)
    const headers: http.OutgoingHttpHeaders = { ...req.headers, host: u.host }
    const upstream = http.request({
      protocol: u.protocol === 'https:' ? 'https:' : 'http:',
      hostname: u.hostname,
      port: u.port || (u.protocol === 'https:' ? 443 : 80),
      method: 'GET',
      path: req.url,
      headers,
    })
    upstream.on('upgrade', (ures, usocket, uhead) => {
      const lines = [`HTTP/1.1 ${ures.statusCode} ${ures.statusMessage || 'Switching Protocols'}`]
      for (const [k, v] of Object.entries(ures.headers)) {
        if (Array.isArray(v)) v.forEach((x) => lines.push(`${k}: ${x}`))
        else if (v !== undefined) lines.push(`${k}: ${x}`)
      }
      socket.write(lines.join('\r\n') + '\r\n\r\n')
      if (uhead && uhead.length) socket.write(uhead)
      usocket.pipe(socket)
      socket.pipe(usocket)
      const kill = () => {
        socket.destroy()
        usocket.destroy()
      }
      socket.on('error', kill)
      usocket.on('error', kill)
      socket.on('close', kill)
      usocket.on('close', kill)
    })
    upstream.on('response', (res) => {
      // 后端拒绝升级（如未登录 401）：原样回给浏览器后关闭
      let headText = `HTTP/1.1 ${res.statusCode} ${res.statusMessage || ''}\r\n`
      for (const [k, v] of Object.entries(res.headers)) {
        if (v !== undefined) headText += `${k}: ${Array.isArray(v) ? v.join(', ') : v}\r\n`
      }
      try {
        socket.end(headText + '\r\n')
      } catch {}
      res.resume()
    })
    upstream.on('error', () => {
      try {
        socket.destroy()
      } catch {}
    })
    upstream.end()
  })
}

export default defineNuxtConfig({
  compatibilityDate: '2025-07-15',
  devtools: { enabled: false },
  ssr: false,
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    public: { licodeBackend: backend },
  },
  hooks: {
    listen(server) {
      // WS upgrade 由 CLI(nuxt.server.upgrade) 转发到 worker 的 crossws 中继处理，
      // 这里仅在转发不可用时（保险起见检查一下）才挂原始隧道。
      const hasForwarding = server.listeners('upgrade').length > 0
      if (!hasForwarding) attachWsTunnel(server as import('node:http').Server)
    },
  },
  vite: {
    plugins: [tailwindcss()],
  },
  nitro: {
    // 静态生成产物输出到 dist/（默认 .output/public），便于拷贝/内嵌
    output: { publicDir: 'dist' },
    experimental: { websocket: true },
    // devProxy 是挂载点语义（会剥掉前缀），因此 target 需带同一路径前缀。
    // dev 下 devProxy（中间件）先于 routeRules 生效；prod 下由 routeRules 代理 REST。
    devProxy: {
      '/api': { target: `${backend}/api`, changeOrigin: true },
      '/health': { target: `${backend}/health`, changeOrigin: true },
      '/ready': { target: `${backend}/ready`, changeOrigin: true },
    },
    routeRules: {
      '/api/**': { proxy: `${backend}/api/**` },
      '/health': { proxy: `${backend}/health` },
      '/ready': { proxy: `${backend}/ready` },
    },
  },
  app: {
    head: {
      title: 'licode',
      htmlAttrs: { lang: 'zh-CN' },
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
      ],
      link: [
        { rel: 'icon', type: 'image/svg+xml', href: 'data:image/svg+xml,%3Csvg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 100 100%22%3E%3Ctext y=%22.9em%22 font-size=%2290%22%3E%F0%9F%A6%8A%3C/text%3E%3C/svg%3E' },
      ],
    },
  },
})
