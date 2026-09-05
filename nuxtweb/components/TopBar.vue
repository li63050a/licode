<script setup lang="ts">
import { Message, Button, Select, Badge, Divider } from 'fuxsto-design'
import {
  Sun,
  Moon,
  Settings,
  PanelLeft,
  PanelRight,
  Info,
  FolderOpen,
  ShieldCheck,
  Bot,
} from 'lucide-vue-next'

const licode = useLicode()
const { state } = licode
const { mode, toggleTheme } = useTheme()

const providerOptions = computed(() => {
  const ps = state.settings?.providers || []
  if (!ps.length) return []
  return ps.map((p) => ({
    label: p.name || p.provider,
    value: p.provider,
  }))
})

function switchProvider(v: string | number) {
  const s = state.settings
  if (!s) return
  const p = (s.providers || []).find((x) => x.provider === String(v))
  if (!p) return
  licode.saveSettings({
    ...s,
    provider: p.provider,
    base_url: p.base_url ?? '',
    api_key: p.api_key ?? '',
    model: p.model || s.model || '',
  })
  Message.success(`已切换厂商：${p.name || p.provider}`)
}

function toggleRight(tab: 'info' | 'files' | 'audit') {
  state.rightTab = state.rightTab === tab ? '' : tab
}
</script>

<template>
  <header
    class="flex h-12 shrink-0 items-center gap-2 border-b border-zinc-200 bg-white px-3 dark:border-zinc-800 dark:bg-zinc-900"
  >
    <Button variant="ghost" size="sm" :icon="PanelLeft" title="折叠侧边栏" @click="state.sidebarCollapsed = !state.sidebarCollapsed" />
    <div class="flex items-center gap-2">
      <span class="flex h-6 w-6 items-center justify-center rounded-md bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900">
        <Bot :size="14" />
      </span>
      <span class="text-sm font-semibold tracking-tight">licode</span>
    </div>
    <Badge v-if="state.settings?.model" variant="secondary" size="sm" class="max-w-48 truncate">
      {{ state.settings.model }}
    </Badge>
    <div class="min-w-0 flex-1" />
    <Select
      v-if="providerOptions.length"
      :model-value="state.settings?.provider || ''"
      :options="providerOptions"
      size="sm"
      class="w-36"
      @update:model-value="switchProvider"
    />
    <Button
      v-for="t in ([
        ['info', Info, '信息'],
        ['files', FolderOpen, '文件'],
        ['audit', ShieldCheck, '审计'],
      ] as const)"
      :key="t[0]"
      size="sm"
      :variant="state.rightTab === t[0] ? 'secondary' : 'ghost'"
      :icon="t[1]"
      :title="t[2]"
      @click="toggleRight(t[0])"
    >
      {{ t[2] }}
    </Button>
    <Divider direction="vertical" class="h-5" />
    <Button variant="ghost" size="sm" :icon="mode === 'dark' ? Sun : Moon" title="切换主题" @click="toggleTheme" />
    <Button variant="ghost" size="sm" :icon="Settings" title="设置" @click="state.settingsOpen = true" />
    <Button
      variant="ghost"
      size="sm"
      :icon="PanelRight"
      title="信息面板"
      :class="{ 'opacity-40': !state.rightTab }"
      @click="state.rightTab = state.rightTab ? '' : 'info'"
    />
  </header>
</template>
