<script setup lang="ts">
import { X, Plus, Trash2, RefreshCw, CheckCircle2, Loader2 } from 'lucide-vue-next'
import { Message, Button, Input, Switch, Chip, Select, Tabs } from 'fuxsto-design'
import type { DNSConfig, ProviderConfig, Settings } from '~/composables/useLicode'

const dnsModeOptions = [
  { label: '系统默认', value: 'system' },
  { label: '普通 DNS', value: 'plain' },
  { label: 'DoT (TLS)', value: 'dot' },
  { label: 'DoH (HTTPS)', value: 'doh' },
]

function setDnsMode(m: string) {
  if (!local.value.dns) local.value.dns = {}
  local.value.dns.mode = m
  if (m === 'system') local.value.dns.server = ''
}

function setDnsServer(v: string) {
  if (!local.value.dns) local.value.dns = {}
  local.value.dns.server = v
}

type ProviderRow = ProviderConfig & { _newModel?: string }

const licode = useLicode()
const { state } = licode

const tab = ref<'basic' | 'providers' | 'advanced'>('basic')
const local = ref<Settings>({})
const toolRows = ref<{ tool: string; rule: string }[]>([])
const mcpJson = ref('')
const fetching = ref('')
const saving = ref(false)

const NUM_KEYS = [
  'temperature',
  'max_tokens',
  'max_iterations',
  'retry_max',
  'sub_timeout',
  'max_ctx_tokens',
  'cache_ttl',
  'tool_retry_max',
  'shutdown_timeout',
  'rag_top_files',
] as const

const COMMA_KEYS = ['ask_tools', 'deny_tools', 'audit_scan_dirs', 'audit_exclude'] as const

const typeOptions = [
  { label: 'OpenAI 兼容', value: 'openai' },
  { label: 'Claude', value: 'claude' },
  { label: 'Ollama', value: 'ollama' },
  { label: 'Gemini', value: 'gemini' },
]

const ruleOptions = [
  { label: 'allow', value: 'allow' },
  { label: 'ask', value: 'ask' },
  { label: 'deny', value: 'deny' },
]

const newProvider = ref<ProviderRow>({ provider: '', name: '', type: 'openai', base_url: '', api_key: '', model: '', models: [] })

watch(
  () => state.settingsOpen,
  (v) => {
    if (v && state.settings) {
      local.value = JSON.parse(JSON.stringify(state.settings))
      if (local.value.streaming === null || local.value.streaming === undefined) local.value.streaming = true
      toolRows.value = Object.entries(local.value.tool_rules || {}).map(([tool, rule]) => ({
        tool,
        rule: String(rule),
      }))
      mcpJson.value = JSON.stringify(local.value.mcp_servers || [], null, 2)
      tab.value = 'basic'
    }
  },
)

const providers = computed<ProviderRow[]>(() => (local.value.providers as ProviderRow[]) || [])

// 当前模型的候选列表：已配置列表 + 当前使用模型（去重），保证旧配置/自定义模型都能显示。
function modelOpts(p: ProviderRow): { label: string; value: string }[] {
  const set = new Set<string>()
  for (const m of p.models || []) set.add(m)
  if (p.model) set.add(p.model)
  return [...set].map((m) => ({ label: m, value: m }))
}

function addModel(p: ProviderRow) {
  const m = (p._newModel || '').trim()
  if (!m) return
  if (!p.models) p.models = []
  if (!p.models.includes(m)) p.models.push(m)
  p.model = m
  p._newModel = ''
}

function removeModel(p: ProviderRow, m: string) {
  if (p.models) p.models = p.models.filter((x) => x !== m)
  if (p.model === m) p.model = ''
}

function activate(p: ProviderRow) {
  local.value.provider = p.provider
  local.value.base_url = p.base_url || ''
  local.value.api_key = p.api_key || ''
  if (p.model) local.value.model = p.model
  Message.info(`已切换激活厂商为「${p.name || p.provider}」，点击保存生效`)
}

async function fetchModels(p: ProviderRow) {
  if (!p.base_url && p.type !== 'claude') {
    Message.warning('请先填写该厂商的 API 地址')
    return
  }
  saving.value = true
  const base = state.settings
  const prev = base ? JSON.parse(JSON.stringify(base)) : null
  const switched = !!(base && base.provider !== p.provider)
  try {
    // /api/models 使用「激活厂商」的 key。目标厂商不是当前激活时，临时切到该厂商获取列表，
    // 完成后立即还原原激活厂商（不改变已保存的设置）。
    if (switched && prev) {
      licode.saveSettings({
        ...prev,
        provider: p.provider,
        base_url: p.base_url || '',
        api_key: p.api_key || '',
        model: p.model || prev.model || '',
      })
    }
    fetching.value = p.provider
    const q = new URLSearchParams()
    if (p.type) q.set('type', p.type)
    if (p.base_url) q.set('base', p.base_url)
    const res = await useApi<{ models: string[] }>(`/api/models?${q.toString()}`)
    const fetched = res.models || []
    if (!p.models) p.models = []
    for (const m of fetched) if (!p.models.includes(m)) p.models.push(m)
    if (!fetched.length) Message.info('该厂商无公开模型列表（如 Claude），请手动填写模型名')
    else Message.success(`获取到 ${fetched.length} 个模型`)
  } catch (e: any) {
    Message.error(e?.message || '获取模型失败')
  } finally {
    if (switched && prev) licode.saveSettings(prev)
    fetching.value = ''
    saving.value = false
  }
}

function addProvider() {
  const np = newProvider.value
  const id = (np.provider || np.name || np.type || 'custom')
    .trim()
    .toLowerCase()
    .replace(/\s+/g, '-')
  if (!id) {
    Message.warning('请填写厂商标识或名称')
    return
  }
  if (providers.value.some((p) => p.provider === id)) {
    Message.error('厂商标识已存在')
    return
  }
  local.value.providers = [
    ...providers.value,
    {
      provider: id,
      name: np.name || '',
      type: np.type || 'openai',
      base_url: np.base_url || '',
      api_key: np.api_key || '',
      model: np.model || '',
    },
  ]
  newProvider.value = { provider: '', name: '', type: 'openai', base_url: '', api_key: '', model: '' }
  Message.success('厂商已添加，点击保存生效')
}

function removeProvider(p: ProviderRow) {
  local.value.providers = providers.value.filter((x) => x.provider !== p.provider)
  if (local.value.provider === p.provider) {
    const next = providers.value[0]
    local.value.provider = next?.provider || ''
  }
  Message.success('厂商已移除，点击保存生效')
}

function splitList(v: unknown): string[] {
  return String(v ?? '')
    .split(/[,，]/)
    .map((x) => x.trim())
    .filter(Boolean)
}

function buildSettings(): Settings {
  const s = JSON.parse(JSON.stringify(local.value)) as Settings
  for (const k of NUM_KEYS) s[k] = Number(s[k]) || 0
  s.streaming = !!s.streaming
  const rules: Record<string, string> = {}
  for (const r of toolRows.value) {
    if (r.tool.trim()) rules[r.tool.trim()] = r.rule || 'ask'
  }
  s.tool_rules = rules
  for (const k of COMMA_KEYS) s[k] = splitList(s[k])
  try {
    s.mcp_servers = JSON.parse(mcpJson.value || '[]')
  } catch {
    throw new Error('MCP 服务器 JSON 格式无效')
  }
  const dns = s.dns
if (dns && !dns.mode && !dns.server) {
  delete s.dns
} else if (dns) {
  s.dns = { mode: dns.mode || 'system', server: dns.server || '' }
}
// _newModel 与 __models 都是临时字段，不随设置持久化
  s.providers = (s.providers || []).map((p) => {
    const { _newModel: _a, ...rest } = p as ProviderRow
    return rest
  })
  const act = (s.providers || []).find((p) => p.provider === s.provider)
  if (act) {
    act.base_url = s.base_url || ''
    act.api_key = s.api_key || ''
    act.model = s.model || act.model || ''
  }
  return s
}

function save() {
  if (!state.settings) {
    Message.warning('设置尚未加载，请稍后重试')
    return
  }
  if (state.wsStatus !== 'connected') {
    Message.error('无法连接 licode 后端，设置未保存')
    return
  }
  let s: Settings
  try {
    s = buildSettings()
  } catch (e: any) {
    Message.error(e?.message || '保存失败')
    return
  }
  licode.saveSettings(s)
  state.settingsOpen = false
  Message.success('设置已保存')
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="state.settingsOpen"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      @click.self="state.settingsOpen = false"
    >
      <div
        class="flex h-[82vh] w-full max-w-2xl flex-col overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-2xl dark:border-zinc-800 dark:bg-zinc-900"
      >
        <div class="flex shrink-0 items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <span class="text-sm font-semibold">设置</span>
          <Button variant="ghost" size="sm" :icon="X" @click="state.settingsOpen = false" />
        </div>

        <div class="shrink-0 border-b border-zinc-200 px-4 pt-2 dark:border-zinc-800">
          <Tabs
            :model-value="tab"
            :options="[
              { label: '基础', value: 'basic' },
              { label: '厂商', value: 'providers' },
              { label: '高级', value: 'advanced' },
            ]"
            variant="line"
            @update:model-value="tab = $event as any"
          />
        </div>

        <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
          <!-- 设置未加载（WS 未就绪） -->
          <div v-if="!state.settings" class="flex flex-col items-center gap-3 pt-16 text-sm text-zinc-500">
            <Loader2 :size="20" class="animate-spin" />
            正在加载设置…请确认已连接 licode 后端
            <Button size="sm" variant="outline" @click="licode.connect()">重新连接</Button>
          </div>

          <!-- 基础 -->
          <template v-else-if="tab === 'basic'">
            <div class="grid grid-cols-2 gap-3">
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">模型</span>
                <Input v-model="local.model" placeholder="如 gpt-4o-mini / claude-sonnet-4" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">API 密钥</span>
                <Input v-model="local.api_key" type="password" placeholder="sk-…" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">API 地址</span>
                <Input v-model="local.base_url" placeholder="https://api.openai.com/v1" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">温度 temperature</span>
                <Input v-model="local.temperature" type="number" step="0.1" min="0" max="2" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">最大输出 tokens</span>
                <Input v-model="local.max_tokens" type="number" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">最大迭代次数</span>
                <Input v-model="local.max_iterations" type="number" />
              </label>
            </div>
            <div class="grid grid-cols-2 gap-2 rounded-xl border border-zinc-200 p-3 dark:border-zinc-800">
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>流式输出</span>
                <Switch :model-value="!!local.streaming" size="sm" @update:model-value="local.streaming = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>自动允许风险工具</span>
                <Switch :model-value="!!local.auto_allow" size="sm" @update:model-value="local.auto_allow = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>子代理编排</span>
                <Switch :model-value="!!local.subagents" size="sm" @update:model-value="local.subagents = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>上下文压缩</span>
                <Switch :model-value="!!local.compaction" size="sm" @update:model-value="local.compaction = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>自动生成标题</span>
                <Switch :model-value="!!local.title_gen" size="sm" @update:model-value="local.title_gen = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>语义缓存</span>
                <Switch :model-value="!!local.cache_enabled" size="sm" @update:model-value="local.cache_enabled = !!$event" />
              </label>
            </div>
            <div class="rounded-xl border border-zinc-200 p-3 dark:border-zinc-800">
              <div class="mb-2 flex items-center justify-between">
                <span class="text-xs font-medium text-zinc-500">DNS 解析</span>
                <span class="text-[10px] text-zinc-400">解决 connection refused / DNS 污染</span>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <label class="space-y-1">
                  <span class="text-xs text-zinc-500">模式</span>
                  <Select
                    :model-value="local.dns?.mode || 'system'"
                    size="sm"
                    :options="dnsModeOptions"
                    placeholder="系统默认"
                    @update:model-value="setDnsMode(String($event))"
                  />
                </label>
                <label class="space-y-1">
                  <span class="text-xs text-zinc-500">服务器</span>
                  <Input
                    :model-value="local.dns?.server || ''"
                    size="sm"
                    placeholder="8.8.8.8:53 / https://doh.pub/dns-query"
                    @update:model-value="setDnsServer(String($event))"
                  />
                </label>
              </div>
              <p class="mt-1 text-[10px] text-zinc-400">
                system 系统默认 · plain 普通 DNS · dot DNS over TLS (853) · doh DNS over HTTPS
              </p>
            </div>
          </template>

          <!-- 厂商 -->
          <template v-else-if="tab === 'providers'">
            <div class="space-y-3">
              <div
                v-for="p in providers"
                :key="p.provider"
                class="rounded-xl border p-3"
                :class="
                  p.provider === local.provider
                    ? 'border-zinc-900 dark:border-zinc-100'
                    : 'border-zinc-200 dark:border-zinc-800'
                "
              >
                <div class="mb-2 flex items-center gap-2">
                  <CheckCircle2
                    v-if="p.provider === local.provider"
                    :size="15"
                    class="text-emerald-600 dark:text-emerald-400"
                  />
                  <span class="text-sm font-medium">{{ p.name || p.provider }}</span>
                  <Chip size="sm" variant="outline">{{ p.type || 'openai' }}</Chip>
                  <span class="flex-1" />
                  <Button
                    size="sm"
                    variant="outline"
                    :icon="RefreshCw"
                    :loading="fetching === p.provider"
                    @click="fetchModels(p)"
                  >
                    获取模型
                  </Button>
                  <Button
                    v-if="p.provider !== local.provider"
                    size="sm"
                    variant="secondary"
                    @click="activate(p)"
                  >
                    激活
                  </Button>
                  <Button size="sm" variant="ghost" danger :icon="Trash2" @click="removeProvider(p)" />
                </div>
                <div class="grid grid-cols-2 gap-2">
                  <Input v-model="p.name" size="sm" placeholder="显示名称" />
                  <Select
                    :model-value="p.type || 'openai'"
                    size="sm"
                    :options="typeOptions"
                    placeholder="协议类型"
                    @update:model-value="p.type = String($event)"
                  />
                  <Input v-model="p.base_url" size="sm" placeholder="API 地址" />
                  <Input v-model="p.api_key" size="sm" type="password" placeholder="API 密钥" />
                  <div class="col-span-2 space-y-2 pt-1">
                    <div class="text-xs text-zinc-500">模型</div>
                    <div class="flex flex-wrap gap-1">
                      <Chip
                        v-for="m in p.models || []"
                        :key="m"
                        size="sm"
                        variant="secondary"
                        class="group cursor-default"
                      >
                        {{ m }}
                        <button
                          class="ml-1 text-zinc-400 group-hover:text-red-500"
                          :title="`移除「${m}」`"
                          @click="removeModel(p, m)"
                        >
                          <X :size="11" />
                        </button>
                      </Chip>
                      <span v-if="!(p.models && p.models.length)" class="self-center text-xs text-zinc-400">暂未添加模型</span>
                    </div>
                    <div class="flex items-center gap-2">
                      <Input
                        v-model="p._newModel"
                        size="sm"
                        placeholder="模型名（回车或点「添加」，如 gpt-4o-mini）"
                        class="flex-1"
                        @keydown.enter.prevent="addModel(p)"
                      />
                      <Button size="sm" variant="outline" :icon="Plus" @click="addModel(p)">添加</Button>
                    </div>
                    <Select
                      v-if="modelOpts(p).length"
                      :model-value="p.model"
                      size="sm"
                      searchable
                      :options="modelOpts(p)"
                      placeholder="当前模型"
                      @update:model-value="p.model = String($event)"
                    />
                    <Input v-else v-model="p.model" size="sm" placeholder="当前模型（如 gpt-4o-mini）" />
                  </div>
                </div>
              </div>
            </div>

            <div class="rounded-xl border border-dashed border-zinc-300 p-3 dark:border-zinc-700">
              <div class="mb-2 text-xs font-medium text-zinc-500">新增厂商</div>
              <div class="grid grid-cols-2 gap-2">
                <Input v-model="newProvider.name" size="sm" placeholder="名称（如 我的 Ollama）" />
                <Select
                  :model-value="newProvider.type"
                  size="sm"
                  :options="typeOptions"
                  @update:model-value="newProvider.type = String($event)"
                />                <Input v-model="newProvider.base_url" size="sm" placeholder="API 地址" />
                <Input v-model="newProvider.api_key" size="sm" type="password" placeholder="API 密钥" />
                <Input v-model="newProvider.model" size="sm" placeholder="模型名（可留空）" class="col-span-2" />
              </div>
              <Button size="sm" variant="secondary" class="mt-2 w-full" :icon="Plus" @click="addProvider">
                添加厂商
              </Button>
            </div>
          </template>

          <!-- 高级 -->
          <template v-else>
            <div class="grid grid-cols-2 gap-3">
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">Shell 路径</span>
                <Input v-model="local.shell_path" placeholder="/bin/sh" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">沙箱镜像</span>
                <Input v-model="local.sandbox_image" placeholder="alpine" />
              </label>
              <label v-for="k in ['retry_max', 'sub_timeout', 'max_ctx_tokens', 'cache_ttl', 'tool_retry_max', 'shutdown_timeout']" :key="k" class="space-y-1">
                <span class="text-xs text-zinc-500">{{ k }}</span>
                <Input v-model="local[k]" type="number" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">RAG 来源（rag_source）</span>
                <Input v-model="local.rag_source" placeholder="留空关闭" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">ask 工具（逗号分隔）</span>
                <Input :model-value="(local.ask_tools || []).join(',')" @update:model-value="local.ask_tools = splitList($event)" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">deny 工具（逗号分隔）</span>
                <Input :model-value="(local.deny_tools || []).join(',')" @update:model-value="local.deny_tools = splitList($event)" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">审计扫描目录（逗号分隔）</span>
                <Input :model-value="(local.audit_scan_dirs || []).join(',')" @update:model-value="local.audit_scan_dirs = splitList($event)" />
              </label>
              <label class="space-y-1">
                <span class="text-xs text-zinc-500">审计排除正则（逗号分隔）</span>
                <Input :model-value="(local.audit_exclude || []).join(',')" @update:model-value="local.audit_exclude = splitList($event)" />
              </label>
            </div>

            <div class="grid grid-cols-2 gap-2 rounded-xl border border-zinc-200 p-3 dark:border-zinc-800">
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>redact_secrets</span>
                <Switch :model-value="!!local.redact_secrets" size="sm" @update:model-value="local.redact_secrets = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>sandbox</span>
                <Switch :model-value="!!local.sandbox" size="sm" @update:model-value="local.sandbox = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>tool_auto_retry</span>
                <Switch :model-value="!!local.tool_auto_retry" size="sm" @update:model-value="local.tool_auto_retry = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>rag_enabled</span>
                <Switch :model-value="!!local.rag_enabled" size="sm" @update:model-value="local.rag_enabled = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>audit_enabled</span>
                <Switch :model-value="!!local.audit_enabled" size="sm" @update:model-value="local.audit_enabled = !!$event" />
              </label>
              <label class="flex items-center justify-between gap-2 text-sm">
                <span>audit_auto_fix</span>
                <Switch :model-value="!!local.audit_auto_fix" size="sm" @update:model-value="local.audit_auto_fix = !!$event" />
              </label>
            </div>

            <div class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-xs font-medium text-zinc-500">工具规则（tool_rules）</span>
                <Button size="sm" variant="ghost" :icon="Plus" @click="toolRows.push({ tool: '', rule: 'ask' })">
                  添加
                </Button>
              </div>
              <div v-for="(r, i) in toolRows" :key="i" class="flex items-center gap-2">
                <Input v-model="r.tool" size="sm" placeholder="工具名" class="flex-1" />
                <Select
                  :model-value="r.rule"
                  size="sm"
                  :options="ruleOptions"
                  class="w-28"
                  @update:model-value="r.rule = String($event)"
                />
                <Button size="sm" variant="ghost" danger :icon="Trash2" @click="toolRows.splice(i, 1)" />
              </div>
            </div>

            <label class="block space-y-1">
              <span class="text-xs text-zinc-500">MCP 服务器（JSON 数组）</span>
              <textarea
                v-model="mcpJson"
                rows="5"
                class="w-full rounded-lg border border-zinc-200 bg-transparent p-2 font-mono text-xs outline-none focus:border-zinc-400 dark:border-zinc-800 dark:focus:border-zinc-600"
                spellcheck="false"
              />
            </label>
          </template>
        </div>

        <div class="flex shrink-0 items-center justify-end gap-2 border-t border-zinc-200 px-4 py-3 dark:border-zinc-800">
          <Button variant="ghost" @click="state.settingsOpen = false">取消</Button>
          <Button variant="primary" :loading="saving" :disabled="!state.settings || state.wsStatus !== 'connected'" @click="save">保存</Button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
