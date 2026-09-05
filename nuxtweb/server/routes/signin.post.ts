import http from 'node:http'
import https from 'node:https'

/**
 * 登录中转：后端 POST /login 成功时通过 302 设置 licode_auth cookie。
 * fetch 跟随重定向会丢失中间响应的 Set-Cookie，因此用 Node http.request
 * （不自动跟随重定向）读取 302 的 Set-Cookie 并原样转发给浏览器。
 */
export default defineEventHandler(async (event) => {
  const body = await readBody(event).catch(() => ({} as any))
  const username = String(body?.username ?? '')
  const password = String(body?.password ?? '')
  const backend = process.env.LICODE_BACKEND || 'http://127.0.0.1:8080'
  const u = new URL(backend)
  const lib = u.protocol === 'https:' ? https : http
  const postData = new URLSearchParams({ username, password }).toString()

  const result = await new Promise<{ ok: boolean; error?: string }>((resolve) => {
    const req = lib.request(
      {
        hostname: u.hostname,
        port: u.port || (u.protocol === 'https:' ? 443 : 80),
        method: 'POST',
        path: '/login',
        headers: {
          'content-type': 'application/x-www-form-urlencoded',
          'content-length': Buffer.byteLength(postData),
        },
      },
      (res) => {
        const cookies = (res.headers['set-cookie'] || []).map((c) => String(c))
        const auth = cookies.find((c) => c.startsWith('licode_auth='))
        if (auth) {
          for (const c of cookies) appendHeader(event, 'set-cookie', c)
          resolve({ ok: true })
        } else {
          resolve({ ok: false, error: '用户名或密码错误' })
        }
        res.resume()
      },
    )
    req.on('error', (e: any) => resolve({ ok: false, error: e?.message || '无法连接 licode 后端' }))
    req.write(postData)
    req.end()
  })
  return result
})
