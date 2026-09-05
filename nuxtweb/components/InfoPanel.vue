<script setup lang="ts">
import { Download, Upload } from 'lucide-vue-next'
import { Message, Button, Chip, Divider, Progress } from 'fuxsto-design'

const licode = useLicode()
const { state } = licode
const version = ref('…')
const impRef = ref<HTMLInputElement | null>(null)

onMounted(async () => {
  try {
    const v = await useApi<{ version: string }>('/api/version')
    version.value = v.version
  } catch {
    version.value = '-'
  }
})

async function doExport() {
  try {
    const res = await fetch('/api/export')
    if (!res.ok) throw new Error(`导出失败 (${res.status})`)
    const blob = await res.blob()
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = 'licode-backup.zip'
    a.click()
    URL.revokeObjectURL(a.href)
    Message.success('备份已导出')
  } catch (e: any) {
    Message.error(e?.message || '导出失败')
  }
}

async function onImport(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!file) return
  try {
    const res = await fetch('/api/import', {
      method: 'POST',
      body: file,
      headers: { Accept: 'application/json' },
    })
    const data = await res.json().catch(() => ({}))
    if (data.ok) Message.success('备份导入成功')
    else Message.error(data.error || '导入失败')
  } catch (e: any) {
    Message.error(e?.message || '导入失败')
  }
}

const links = [
  { label: 'GitHub', text: 'li63050a/licode', href: 'https://github.com/li63050a/licode' },
  { label: 'Gitee', text: 'li63050a/licode', href: 'https://gitee.com/li63050a/licode' },
  { label: 'B 站', text: '小帅5656', href: 'https://b23.tv/nDqj0DT' },
]
</script>

<template>
  <div class="space-y-4 p-3 text-sm">
    <section>
      <h4 class="mb-2 text-xs font-semibold uppercase tracking-wider text-zinc-400">会话</h4>
      <div class="space-y-1.5 text-zinc-600 dark:text-zinc-300">
        <div class="flex justify-between">
          <span>消息数</span><span class="tabular-nums">{{ state.stats.messages }}</span>
        </div>
        <div class="flex justify-between">
          <span>上下文</span>
          <span class="tabular-nums">
            {{ state.stats.context_tokens }}{{ state.stats.context_max ? ` / ${state.stats.context_max}` : '' }}
          </span>
        </div>
        <Progress
          v-if="state.stats.context_max"
          :percentage="Math.min(100, Math.round(state.stats.context_pct || 0))"
          size="sm"
          :status="state.stats.context_pct > 85 ? 'warning' : 'normal'"
        />
        <div class="flex justify-between">
          <span>输入 / 输出 tokens</span>
          <span class="tabular-nums">{{ state.stats.conversation_in }} / {{ state.stats.conversation_out }}</span>
        </div>
        <div class="flex justify-between">
          <span>缓存命中</span>
          <span class="tabular-nums">
            {{ state.stats.cache_hit_rate || 0 }}%（{{ state.stats.usage_cached }} 次）
          </span>
        </div>
        <div class="flex justify-between">
          <span>厂商 / 模型</span>
          <span class="max-w-40 truncate" :title="`${state.stats.provider || state.settings?.provider || '-'} / ${state.stats.model || state.settings?.model || '-'}`">
            {{ state.stats.provider || state.settings?.provider || '-' }} / {{ state.stats.model || state.settings?.model || '-' }}
          </span>
        </div>
        <div class="flex items-start justify-between gap-2">
          <span class="shrink-0">始终允许</span>
          <div class="flex flex-wrap justify-end gap-1">
            <template v-if="state.stats.always_allow?.length">
              <Chip v-for="t in state.stats.always_allow" :key="t" size="sm" variant="secondary">
                {{ t }}
              </Chip>
            </template>
            <span v-else class="text-zinc-400">-</span>
          </div>
        </div>
      </div>
    </section>

    <Divider />

    <section>
      <h4 class="mb-2 text-xs font-semibold uppercase tracking-wider text-zinc-400">版本</h4>
      <div class="flex justify-between text-zinc-600 dark:text-zinc-300">
        <span>licode</span><span class="font-mono text-xs">{{ version }}</span>
      </div>
    </section>

    <Divider />

    <section>
      <h4 class="mb-2 text-xs font-semibold uppercase tracking-wider text-zinc-400">备份</h4>
      <div class="flex gap-2">
        <Button size="sm" variant="outline" class="flex-1" :icon="Download" @click="doExport">导出</Button>
        <Button size="sm" variant="outline" class="flex-1" :icon="Upload" @click="impRef?.click()">导入</Button>
        <input ref="impRef" type="file" accept=".zip" class="hidden" @change="onImport" />
      </div>
    </section>

    <Divider />

    <section>
      <h4 class="mb-2 text-xs font-semibold uppercase tracking-wider text-zinc-400">联系与交流</h4>
      <div class="space-y-1.5 text-zinc-600 dark:text-zinc-300">
        <div v-for="l in links" :key="l.label" class="flex justify-between">
          <span>{{ l.label }}</span>
          <a :href="l.href" target="_blank" rel="noopener" class="underline underline-offset-2 hover:text-zinc-900 dark:hover:text-zinc-50">
            {{ l.text }}
          </a>
        </div>
      </div>
    </section>
  </div>
</template>
