export async function useApi<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...opts,
    headers: { Accept: 'application/json', ...(opts.headers || {}) },
  })
  if (res.status === 401) {
    navigateTo('/login')
    throw new Error('未登录，请先登录')
  }
  const ct = res.headers.get('content-type') || ''
  if (!ct.includes('application/json')) {
    if (!res.ok) throw new Error(`请求失败 (${res.status})`)
    const text = await res.text()
    try {
      return JSON.parse(text) as T
    } catch {
      return text as unknown as T
    }
  }
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error((data as any).error || `请求失败 (${res.status})`)
  return data as T
}
