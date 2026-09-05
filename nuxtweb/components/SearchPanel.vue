<script setup lang="ts">
import { Search, BookMarked, ExternalLink, Eye, Trash2, X, Globe, Loader2 } from 'lucide-vue-next'
import { Message, Button, Input, Chip, Empty, Dialog } from 'fuxsto-design'

interface SearchResult {
  engine: string
  title: string
  url: string
  snippet: string
  local?: boolean
}

interface CatalogDoc {
  url: string
  title: string
  fetched_at: number
  len: number
}

const engines = ref<string[]>(['bing', 'baidu', 'duckduckgo'])
const sel = ref<Set<string>>(new Set(['bing', 'baidu', 'duckduckgo']))
const useLocal = ref(true)
const query = ref('')
const mode = ref<'search' | 'catalog'>('search')
const results = ref<SearchResult[]>([])
const docs = ref<CatalogDoc[]>([])
const loading = ref(false)
const catalogLoading = ref(false)
const preview = ref<null | { url: string; title: string; text: string; loading: boolean }>(null)

const engineLabels: Record<string, string> = {
  bing: '必应',
  baidu: '百度',
  duckduckgo: 'DuckDuckGo',
}

onMounted(async () => {
  try {
    const s = await useApi<{ engines?: string[] }>('/api/search/stats')
    if (s.engines?.length) {
      engines.value = s.engines
      sel.value = new Set(s.engines)
    }
  } catch {}
  loadCatalog()
})

function toggleEngine(e: string) {
  const s = new Set(sel.value)
  if (s.has(e)) s.delete(e)
  else s.add(e)
  sel.value = s
}

async function loadCatalog() {
  catalogLoading.value = true
  try {
    const d = await useApi<{ docs: CatalogDoc[] }>('/api/search/catalog')
    docs.value = d.docs || []
  } catch (e: any) {
    Message.error(e?.message || '加载本地库失败')
  } finally {
    catalogLoading.value = false
  }
}

function showCatalog() {
  mode.value = 'catalog'
  if (!docs.value.length) loadCatalog()
}

async function runSearch() {
  const q = query.value.trim()
  if (!q) {
    Message.warning('请输入搜索关键词')
    return
  }
  loading.value = true
  mode.value = 'search'
  try {
    const qs = new URLSearchParams({ q, max: '10' })
    if (sel.value.size) qs.set('engines', [...sel.value].join(','))
    qs.set('local', useLocal.value ? '1' : '0')
    const d = await useApi<{ results: SearchResult[] }>(`/api/search?${qs.toString()}`)
    results.value = d.results || []
  } catch (e: any) {
    Message.error(e?.message || '搜索失败')
  } finally {
    loading.value = false
  }
}

async function viewUrl(url: string) {
  preview.value = { url, title: '', text: '抓取中…', loading: true }
  try {
    const d = await useApi<{ title: string; text: string }>('/api/search/fetch', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ url }),
    })
    preview.value = { url, title: d.title || '', text: d.text || '', loading: false }
  } catch (e: any) {
    preview.value = { url, title: '', text: '抓取失败：' + (e?.message || ''), loading: false }
  }
}

async function saveUrl(url: string) {
  try {
    const d = await useApi<{ title: string }>('/api/search/save', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ url }),
    })
    Message.success('已收录：' + (d.title || url))
    loadCatalog()
  } catch (e: any) {
    Message.error(e?.message || '收录失败')
  }
}

function delDoc(url: string) {
  Dialog.confirm({
    title: '从本地库删除',
    content: '确定从已收录库中删除该页面？',
    danger: true,
    confirmText: '删除',
    onConfirm: async () => {
      try {
        await useApi('/api/search/delete', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({ url }),
        })
        Message.success('已删除')
        loadCatalog()
      } catch (e: any) {
        Message.error(e?.message || '删除失败')
      }
    },
  })
}

function fmtLen(n: number) {
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(1)} KB`
}
</script>

<template>
  <div class="flex flex-col text-sm">
    <div class="space-y-2 p-3">
      <div class="flex gap-1.5">
        <Input v-model="query" size="sm" placeholder="搜索关键词（联网 + 本地库）" class="flex-1" @keydown.enter="runSearch" />
        <Button size="sm" variant="primary" :icon="Search" :loading="loading" @click="runSearch">搜索</Button>
        <Button size="sm" variant="outline" :icon="BookMarked" @click="showCatalog">已收录</Button>
      </div>
      <div class="flex flex-wrap items-center gap-1.5">
        <Chip
          v-for="e in engines"
          :key="e"
          size="sm"
          variant="outline"
          selectable
          :selected="sel.has(e)"
          @click="toggleEngine(e)"
        >
          {{ engineLabels[e] || e }}
        </Chip>
        <Chip size="sm" variant="outline" selectable :selected="useLocal" @click="useLocal = !useLocal">
          本地库
        </Chip>
      </div>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto border-t border-zinc-200 dark:border-zinc-800">
      <!-- 搜索中 -->
      <div v-if="loading" class="flex items-center gap-2 p-4 text-xs text-zinc-500">
        <Loader2 :size="13" class="animate-spin" /> 搜索中…
      </div>

      <!-- 搜索结果 -->
      <template v-else-if="mode === 'search'">
        <Empty v-if="!results.length" title="暂无结果" description="输入关键词开始联网搜索" size="sm" class="mt-8" />
        <div v-for="(r, i) in results" :key="i" class="border-b border-zinc-100 px-3 py-2.5 dark:border-zinc-800/60">
          <div class="flex items-center gap-1.5">
            <Chip v-if="r.local" size="sm" variant="secondary">本地</Chip>
            <Chip v-else size="sm" variant="ghost">{{ engineLabels[r.engine] || r.engine }}</Chip>
            <button class="min-w-0 flex-1 truncate text-left font-medium text-zinc-900 hover:underline dark:text-zinc-50" :title="r.url" @click="viewUrl(r.url)">
              {{ r.title || r.url }}
            </button>
          </div>
          <div v-if="r.snippet" class="mt-1 line-clamp-3 text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">
            {{ r.snippet }}
          </div>
          <div class="mt-1.5 flex items-center gap-3 text-xs text-zinc-400">
            <a :href="r.url" target="_blank" rel="noopener" class="flex items-center gap-1 hover:text-zinc-700 dark:hover:text-zinc-200">
              <ExternalLink :size="12" /> 打开原文
            </a>
            <button class="flex items-center gap-1 hover:text-zinc-700 dark:hover:text-zinc-200" @click="viewUrl(r.url)">
              <Eye :size="12" /> 查看
            </button>
            <button class="flex items-center gap-1 hover:text-zinc-700 dark:hover:text-zinc-200" @click="saveUrl(r.url)">
              <BookMarked :size="12" /> 收藏收录
            </button>
          </div>
        </div>
      </template>

      <!-- 本地已收录库 -->
      <template v-else>
        <div class="px-3 py-2 text-[11px] text-zinc-400">
          本地已收录 {{ docs.length }} 条{{ catalogLoading ? ' · 加载中…' : '' }}
        </div>
        <Empty v-if="!docs.length && !catalogLoading" title="暂无收录" description="对搜索结果点「收藏收录」即可加入本地库" size="sm" class="mt-6" />
        <div v-for="d in docs" :key="d.url" class="border-b border-zinc-100 px-3 py-2.5 dark:border-zinc-800/60">
          <button class="block w-full truncate text-left text-[13px] font-medium text-zinc-900 hover:underline dark:text-zinc-50" :title="d.url" @click="viewUrl(d.url)">
            {{ d.title || d.url }}
          </button>
          <div class="mt-0.5 truncate text-xs text-zinc-400">
            {{ d.url }} · {{ fmtLen(d.len) }}
          </div>
          <div class="mt-1.5 flex items-center gap-3 text-xs text-zinc-400">
            <a :href="d.url" target="_blank" rel="noopener" class="flex items-center gap-1 hover:text-zinc-700 dark:hover:text-zinc-200">
              <ExternalLink :size="12" /> 打开原文
            </a>
            <button class="flex items-center gap-1 hover:text-red-500" @click="delDoc(d.url)">
              <Trash2 :size="12" /> 删除
            </button>
          </div>
        </div>
      </template>
    </div>

    <!-- 网页预览 -->
    <div v-if="preview" class="shrink-0 border-t border-zinc-200 dark:border-zinc-800">
      <div class="flex items-center gap-2 border-b border-zinc-200 px-3 py-1.5 dark:border-zinc-800">
        <Globe :size="13" class="shrink-0 text-zinc-400" />
        <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-zinc-500" :title="preview.url">
          {{ preview.url }}
        </span>
        <Button size="sm" variant="primary" :icon="BookMarked" @click="saveUrl(preview.url)">收藏收录</Button>
        <Button size="sm" variant="ghost" :icon="X" @click="preview = null" />
      </div>
      <div class="max-h-[36vh] overflow-auto whitespace-pre-wrap break-all p-3 font-mono text-[11px] leading-relaxed text-zinc-600 dark:text-zinc-300">
        <div v-if="preview.title" class="mb-1 font-semibold text-zinc-900 dark:text-zinc-50">📌 {{ preview.title }}</div>
        {{ preview.text }}
      </div>
    </div>
  </div>
</template>
