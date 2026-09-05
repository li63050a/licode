import { Message } from 'fuxsto-design'

export interface SessionInfo {
  id: string
  title: string
  count: number
}

export interface ProviderConfig {
  provider: string
  name?: string
  type?: string
  base_url?: string
  api_key?: string
  model?: string
  models?: string[] // 该厂商的模型列表（可自由增删，仅作展示/选择用）
}

export interface McpServer {
  name: string
  type?: string
  command?: string
  args?: string[]
  url?: string
}

export interface Settings {
  provider?: string
  base_url?: string
  api_key?: string
  model?: string
  providers?: ProviderConfig[]
  temperature?: number
  max_tokens?: number
  max_iterations?: number
  subagents?: boolean
  ask_tools?: string[]
  deny_tools?: string[]
  compaction?: boolean
  title_gen?: boolean
  auto_allow?: boolean
  streaming?: boolean | null
  tool_rules?: Record<string, string>
  mcp_servers?: McpServer[]
  shell_path?: string
  retry_max?: number
  sub_timeout?: number
  max_ctx_tokens?: number
  redact_secrets?: boolean
  sandbox?: boolean
  sandbox_image?: string
  cache_enabled?: boolean
  cache_ttl?: number
  tool_auto_retry?: boolean
  tool_retry_max?: number
  shutdown_timeout?: number
  rag_enabled?: boolean
  rag_source?: string
  rag_top_files?: number
  audit_enabled?: boolean
  audit_auto_fix?: boolean
  audit_scan_dirs?: string[]
  audit_exclude?: string[]
  [key: string]: any
}

export interface ToolBlock {
  kind: 'tool'
  id: number
  name: string
  args: string
  out: string
  running: boolean
}

export interface TextBlock {
  kind: 'text'
  id: number
  text: string
}

export type Block = TextBlock | ToolBlock

export interface ChatMessage {
  id: number
  role: 'user' | 'assistant'
  blocks: Block[]
}

export interface AskInfo {
  askId: string
  toolName: string
  toolArgs: string
}

export interface StatsInfo {
  messages: number
  context_tokens: number
  context_max: number
  context_pct: number
  provider: string
  model: string
  conversation_in: number
  conversation_out: number
  usage_cached: number
  cache_hit_rate: number
  always_allow: string[]
}

type WsStatus = 'connected' | 'connecting' | 'disconnected'

function emptyStats(): StatsInfo {
  return {
    messages: 0,
    context_tokens: 0,
    context_max: 0,
    context_pct: 0,
    provider: '',
    model: '',
    conversation_in: 0,
    conversation_out: 0,
    usage_cached: 0,
    cache_hit_rate: 0,
    always_allow: [],
  }
}

function createStore() {
  const state = reactive({
    wsStatus: 'connecting' as WsStatus,
    sessions: [] as SessionInfo[],
    sessionId: '',
    messages: [] as ChatMessage[],
    busy: false,
    statusText: '',
    ask: null as AskInfo | null,
    settings: null as Settings | null,
    stats: emptyStats(),
    settingsOpen: false,
    rightTab: '' as '' | 'info' | 'files' | 'search' | 'audit',
    sidebarCollapsed: false,
    auditTick: 0,
  })

  let ws: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let blockId = 0
  let msgId = 0
  let lastToolKey = ''

  function send(type: string, payload: Record<string, any> = {}) {
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type, ...payload }))
    }
  }

  function scheduleReconnect() {
    if (reconnectTimer) clearTimeout(reconnectTimer)
    reconnectTimer = setTimeout(connect, 2000)
  }

  function connect() {
    if (!import.meta.client) return
    if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return
    state.wsStatus = 'connecting'
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    try {
      ws = new WebSocket(`${proto}://${location.host}/ws`)
    } catch {
      state.wsStatus = 'disconnected'
      scheduleReconnect()
      return
    }
    ws.onopen = () => {
      state.wsStatus = 'connected'
      send('settings_get')
      send('sessions_get')
    }
    ws.onmessage = (e) => {
      let evt: any
      try {
        evt = JSON.parse(e.data)
      } catch {
        return
      }
      handleEvent(evt)
    }
    ws.onerror = () => {
      try {
        ws?.close()
      } catch {}
    }
    ws.onclose = () => {
      state.wsStatus = 'disconnected'
      state.busy = false
      scheduleReconnect()
    }
  }

  function ensureAssistant(): ChatMessage {
    const last = state.messages[state.messages.length - 1]
    if (last && last.role === 'assistant') return last
    const msg: ChatMessage = { id: ++msgId, role: 'assistant', blocks: [] }
    state.messages.push(msg)
    return msg
  }

  function appendText(msg: ChatMessage, text: string) {
    const last = msg.blocks[msg.blocks.length - 1]
    if (last && last.kind === 'text') last.text += text
    else msg.blocks.push({ kind: 'text', id: ++blockId, text })
  }

  // historyToMessages 把后端会话历史（ai.Message: role/content/tool_calls/tool_call_id/tool_name）
  // 转成前端渲染用的 ChatMessage[]。tool 角色的消息并入其前一条 assistant 的工具结果。
  function historyToMessages(src: any[]): ChatMessage[] {
    const out: ChatMessage[] = []
    let mid = 1_000_000
    let bid = 1_000_000
    for (const m of src) {
      if (!m || typeof m !== 'object') continue
      const role = String(m.role ?? '')
      if (role === 'system') continue
      if (role === 'tool') {
        const last = out[out.length - 1]
        if (last && last.role === 'assistant') {
          for (let i = last.blocks.length - 1; i >= 0; i--) {
            const b = last.blocks[i]
            if (b.kind === 'tool' && !b.out) {
              b.out = String(m.content ?? '')
              break
            }
          }
        }
        continue
      }
      const blocks: Block[] = []
      if (m.content) blocks.push({ kind: 'text', id: bid++, text: String(m.content) })
      for (const tc of Array.isArray(m.tool_calls) ? m.tool_calls : []) {
        const fn = tc && tc.function && typeof tc.function === 'object' ? tc.function : null
        if (!fn || !fn.name) continue
        blocks.push({
          kind: 'tool',
          id: bid++,
          name: String(fn.name),
          args: String(fn.arguments ?? ''),
          out: '',
          running: false,
        })
      }
      if (blocks.length) out.push({ id: mid++, role: role === 'user' ? 'user' : 'assistant', blocks })
    }
    return out
  }

  function handleEvent(evt: any) {
    switch (evt.type) {
      case 'delta':
        appendText(ensureAssistant(), String(evt.content ?? ''))
        break
      case 'tool_start': {
        const msg = ensureAssistant()
        msg.blocks.push({
          kind: 'tool',
          id: ++blockId,
          name: String(evt.toolName ?? ''),
          args: String(evt.toolArgs ?? ''),
          out: '',
          running: true,
        })
        lastToolKey = String(evt.toolName ?? '')
        break
      }
      case 'tool_done': {
        const msg = state.messages[state.messages.length - 1]
        if (!msg) break
        for (let i = msg.blocks.length - 1; i >= 0; i--) {
          const b = msg.blocks[i]
          if (b.kind === 'tool' && b.running && b.name === String(evt.toolName ?? lastToolKey)) {
            b.out = String(evt.toolOut ?? '')
            b.running = false
            break
          }
        }
        break
      }
      case 'status':
        state.statusText = String(evt.content ?? '')
        break
      case 'done':
        state.busy = false
        state.statusText = ''
        send('sessions_get')
        break
      case 'error':
        state.busy = false
        state.statusText = ''
        Message.error(String(evt.error ?? '发生错误'))
        break
      case 'ask':
        state.ask = {
          askId: String(evt.askId ?? ''),
          toolName: String(evt.toolName ?? ''),
          toolArgs: String(evt.toolArgs ?? ''),
        }
        break
      case 'settings':
        state.settings = (evt.settings ?? null) as Settings | null
        break
      case 'sessions':
        state.sessions = (evt.sessions ?? []) as SessionInfo[]
        state.sessionId = String(evt.sessionId ?? '')
        if (state.sessionId) send('session_history', { sessionId: state.sessionId })
        break
      case 'history': {
        // 只应用当前会话的历史，避免切换期间旧响应覆盖新视图。
        if (String(evt.sessionId ?? '') !== state.sessionId) break
        state.messages = historyToMessages(Array.isArray(evt.messages) ? evt.messages : [])
        state.busy = false
        state.statusText = ''
        state.ask = null
        break
      }
      case 'stats':
        state.stats = { ...state.stats, ...(evt.stats ?? {}) }
        break
      case 'audit_log': {
        state.auditTick++
        try {
          const s = JSON.parse(String(evt.content ?? '{}'))
          Message.success(
            `审计完成：严重 ${s.critical ?? 0} · 高 ${s.high ?? 0} · 中 ${s.medium ?? 0} · 低 ${s.low ?? 0}`,
          )
        } catch {}
        break
      }
    }
  }

  function sendMessage(content: string) {
    const text = content.trim()
    if (!text || state.busy) return
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      Message.error('未连接 licode 后端，消息未发送')
      return
    }
    if (text === '/clear') {
      state.messages = []
      state.statusText = ''
      state.ask = null
      send('message', { content: text })
      return
    }
    state.messages.push({ id: ++msgId, role: 'user', blocks: [{ kind: 'text', id: ++blockId, text }] })
    ensureAssistant()
    state.busy = true
    state.statusText = '思考中…'
    send('message', { content: text })
  }

  function interrupt() {
    send('interrupt')
  }

  function replyAsk(approve: boolean, always: boolean) {
    if (!state.ask) return
    send('ask_reply', {
      askId: state.ask.askId,
      askApprove: approve,
      askAlways: always,
    })
    state.ask = null
  }

  function newSession() {
    send('session_new')
    state.messages = []
  }

  function switchSession(id: string) {
    if (id === state.sessionId) return
    send('session_switch', { sessionId: id })
    state.messages = []
  }

  function renameSession(id: string, title: string) {
    send('session_rename', { sessionId: id, content: title })
  }

  function deleteSession(id: string) {
    send('session_delete', { sessionId: id })
    if (id === state.sessionId) state.messages = []
  }

  function branchSession() {
    if (!state.sessionId) return
    send('session_branch', { sessionId: state.sessionId, index: -1 })
  }

  function saveSettings(next: Settings) {
    state.settings = next
    send('settings_set', { settings: next })
  }

  function clearMessages() {
    state.messages = []
  }

  return {
    state,
    connect,
    send,
    sendMessage,
    interrupt,
    replyAsk,
    newSession,
    switchSession,
    renameSession,
    deleteSession,
    branchSession,
    saveSettings,
    clearMessages,
  }
}

type LicodeStore = ReturnType<typeof createStore>

let store: LicodeStore | null = null

export function useLicode(): LicodeStore {
  if (!store) store = createStore()
  return store
}
