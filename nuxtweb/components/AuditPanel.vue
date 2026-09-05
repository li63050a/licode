<script setup lang="ts">
import { Play, RefreshCw, Wrench, X } from 'lucide-vue-next'
import { Message, Button, Input, Select, Checkbox, Badge, Chip, Empty } from 'fuxsto-design'

interface AuditIssue {
  id: string
  file: string
  line: number
  severity: string
  category: string
  description: string
  suggestion: string
}

interface AuditReport {
  task_id: string
  root: string
  status: string
  progress: number
  scanned_files: number
  issues: AuditIssue[]
  static_files?: number
  llm_files?: number
}

interface AuditStatus {
  enabled: boolean
  running: boolean
  latest: string
  summary: { critical: number; high: number; medium: number; low: number } | null
  scan_dirs: string[]
}

const licode = useLicode()
const { state } = licode

const status = ref<AuditStatus | null>(null)
const report = ref<AuditReport | null>(null)
const scanDirs = ref('.')
const sev = ref('all')
const selected = ref<Set<string>>(new Set())
const loading = ref(false)
const starting = ref(false)
const previewOpen = ref(false)
const preview = ref<{ path: string; diff: string }[]>([])
const fixing = ref(false)
const confirming = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

const sevMeta: Record<string, { label: string; badge: 'destructive' | 'default' | 'secondary' | 'outline' }> = {
  critical: { label: '严重', badge: 'destructive' },
  high: { label: '高', badge: 'destructive' },
  medium: { label: '中', badge: 'default' },
  low: { label: '低', badge: 'secondary' },
}

const filtered = computed(() =>
  (report.value?.issues || []).filter((i) => sev.value === 'all' || i.severity === sev.value),
)

const allSelected = computed(
  () => filtered.value.length > 0 && filtered.value.every((i) => selected.value.has(i.id)),
)

async function refresh(poll = false) {
  loading.value = true
  try {
    status.value = await useApi<AuditStatus>('/api/audit/status')
    if (status.value.running) {
      startPoll()
    } else {
      stopPoll()
      if (status.value.latest && !poll) await loadResult(status.value.latest)
    }
  } catch (e: any) {
    if (!poll) Message.error(e?.message || '审计状态获取失败')
  } finally {
    loading.value = false
  }
}

function startPoll() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    try {
      const s = await useApi<AuditStatus>('/api/audit/status')
      status.value = s
      if (!s.running) {
        stopPoll()
        if (s.latest) await loadResult(s.latest)
      }
    } catch {}
  }, 1500)
}

function stopPoll() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function loadResult(taskId: string) {
  try {
    report.value = await useApi<AuditReport>(`/api/audit/result?task_id=${encodeURIComponent(taskId)}`)
    selected.value = new Set()
  } catch (e: any) {
    Message.error(e?.message || '审计结果获取失败')
  }
}

async function startAudit() {
  starting.value = true
  try {
    const dirs = scanDirs.value
      .split(/[,，]/)
      .map((s) => s.trim())
      .filter(Boolean)
    await useApi('/api/audit/start', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ scan_dirs: dirs }),
    })
    Message.success('审计已启动')
    await refresh(true)
  } catch (e: any) {
    Message.error(e?.message || '启动失败')
  } finally {
    starting.value = false
  }
}

function toggleSelect(id: string) {
  const s = new Set(selected.value)
  if (s.has(id)) s.delete(id)
  else s.add(id)
  selected.value = s
}

function toggleAll() {
  if (allSelected.value) selected.value = new Set()
  else selected.value = new Set(filtered.value.map((i) => i.id))
}

async function genPreview() {
  if (!status.value?.latest || !selected.value.size) return
  fixing.value = true
  try {
    const res = await useApi<{ preview: Record<string, string>; files: string[] }>('/api/audit/fix', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ task_id: status.value.latest, issue_ids: [...selected.value] }),
    })
    preview.value = Object.entries(res.preview || {}).map(([path, diff]) => ({ path, diff }))
    if (!preview.value.length) {
      Message.info('所选问题没有可自动修复的补丁')
      return
    }
    previewOpen.value = true
  } catch (e: any) {
    Message.error(e?.message || '生成预览失败')
  } finally {
    fixing.value = false
  }
}

async function confirmFix() {
  if (!status.value?.latest) return
  confirming.value = true
  try {
    const res = await useApi<{ applied: boolean; files: string[]; backup_path?: string }>(
      '/api/audit/fix?confirm=true',
      {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ task_id: status.value.latest, issue_ids: [...selected.value] }),
      },
    )
    Message.success(`已修复 ${res.files?.length ?? 0} 个文件${res.backup_path ? `（备份：${res.backup_path}）` : ''}`)
    previewOpen.value = false
    await loadResult(status.value.latest)
  } catch (e: any) {
    Message.error(e?.message || '修复失败')
  } finally {
    confirming.value = false
  }
}

watch(
  () => state.auditTick,
  () => refresh(true),
)

onMounted(() => refresh())
onUnmounted(stopPoll)
</script>

<template>
  <div class="space-y-3 p-3 text-sm">
    <div class="space-y-2">
      <div class="text-xs text-zinc-500">
        {{
          status?.running
            ? `审计运行中…（${report?.progress ?? 0}%）`
            : status?.enabled
              ? '就绪 · 静态扫描 + LLM 分析'
              : '审计未启用（设置中 audit_enabled）'
        }}
      </div>
      <div class="flex gap-2">
        <Input v-model="scanDirs" size="sm" placeholder="扫描目录（逗号分隔，默认 .）" class="flex-1" />
        <Button
          size="sm"
          variant="primary"
          :icon="Play"
          :loading="starting"
          :disabled="!status?.enabled || status?.running"
          @click="startAudit"
        >
          开始审计
        </Button>
        <Button size="sm" variant="ghost" :icon="RefreshCw" @click="refresh()" />
      </div>
    </div>

    <div v-if="status?.summary" class="flex flex-wrap gap-2">
      <Chip v-for="s in (['critical', 'high', 'medium', 'low'] as const)" :key="s" size="sm" variant="outline">
        <b class="mr-1">{{ status.summary?.[s] ?? 0 }}</b>{{ sevMeta[s].label }}
      </Chip>
    </div>

    <template v-if="report?.issues?.length">
      <div class="flex items-center gap-2 text-xs">
        <Checkbox :model-value="allSelected" @update:model-value="toggleAll" />
        <span>全选</span>
        <Select
          :model-value="sev"
          size="sm"
          class="w-28"
          :options="[
            { label: '全部级别', value: 'all' },
            { label: 'Critical', value: 'critical' },
            { label: 'High', value: 'high' },
            { label: 'Medium', value: 'medium' },
            { label: 'Low', value: 'low' },
          ]"
          @update:model-value="sev = String($event)"
        />
        <span class="flex-1" />
        <span class="text-zinc-400">已选 {{ selected.size }}</span>
      </div>

      <div class="space-y-2">
        <div
          v-for="i in filtered"
          :key="i.id"
          class="flex gap-2 rounded-lg border border-zinc-200 p-2.5 dark:border-zinc-800"
        >
          <Checkbox
            :model-value="selected.has(i.id)"
            class="mt-0.5"
            @update:model-value="toggleSelect(i.id)"
          />
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-1.5 text-xs">
              <Badge size="sm" :variant="sevMeta[i.severity]?.badge || 'secondary'">
                {{ sevMeta[i.severity]?.label || i.severity }}
              </Badge>
              <span class="min-w-0 truncate font-mono text-zinc-500" :title="i.file">{{ i.file }}</span>
              <span class="text-zinc-400">L{{ i.line || '-' }}</span>
              <Chip v-if="i.category" size="sm" variant="ghost">{{ i.category }}</Chip>
            </div>
            <div class="mt-1 text-[13px] leading-relaxed">{{ i.description }}</div>
            <div v-if="i.suggestion" class="mt-1 rounded bg-zinc-50 p-1.5 text-xs text-zinc-500 dark:bg-zinc-800/60 dark:text-zinc-400">
              💡 {{ i.suggestion }}
            </div>
          </div>
        </div>
      </div>

      <Button
        variant="primary"
        class="w-full"
        :icon="Wrench"
        :loading="fixing"
        :disabled="status?.running || !selected.size"
        @click="genPreview"
      >
        生成修复预览（{{ selected.size }}）
      </Button>
    </template>

    <Empty v-else-if="!status?.running" title="暂无审计结果" description="配置扫描目录后点击「开始审计」" size="sm" class="mt-8" />

    <!-- 修复预览 -->
    <Teleport to="body">
      <div
        v-if="previewOpen"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
        @click.self="previewOpen = false"
      >
        <div
          class="flex h-[82vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-zinc-200 bg-white shadow-2xl dark:border-zinc-800 dark:bg-zinc-900"
        >
          <div class="flex shrink-0 items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
            <span class="text-sm font-semibold">修复预览（{{ preview.length }} 个文件）</span>
            <Button variant="ghost" size="sm" :icon="X" @click="previewOpen = false" />
          </div>
          <div class="min-h-0 flex-1 space-y-4 overflow-y-auto p-4">
            <div v-for="p in preview" :key="p.path">
              <div class="mb-1 font-mono text-xs text-zinc-500">{{ p.path }}</div>
              <DiffView :diff="p.diff" />
            </div>
          </div>
          <div class="flex shrink-0 items-center justify-end gap-2 border-t border-zinc-200 px-4 py-3 dark:border-zinc-800">
            <Button variant="ghost" @click="previewOpen = false">取消</Button>
            <Button variant="primary" :loading="confirming" @click="confirmFix">确认修复</Button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
