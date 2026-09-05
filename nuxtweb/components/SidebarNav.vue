<script setup lang="ts">
import { Plus, Trash2, Pencil, Check, X } from 'lucide-vue-next'
import { Message, Dialog, Button, Input, Empty } from 'fuxsto-design'

const licode = useLicode()
const { state } = licode

const editingId = ref('')
const editingTitle = ref('')

function startRename(id: string, title: string) {
  editingId.value = id
  editingTitle.value = title
}

function commitRename() {
  if (editingId.value && editingTitle.value.trim()) {
    licode.renameSession(editingId.value, editingTitle.value.trim())
  }
  editingId.value = ''
}

function removeSession(id: string, title: string) {
  Dialog.confirm({
    title: '删除会话',
    content: `确定删除「${title}」？该操作不可恢复。`,
    danger: true,
    confirmText: '删除',
    onConfirm: () => {
      licode.deleteSession(id)
      Message.success('会话已删除')
    },
  })
}

const statusMap = {
  connected: { text: '已连接', cls: 'bg-emerald-500' },
  connecting: { text: '连接中', cls: 'bg-amber-500' },
  disconnected: { text: '已断开', cls: 'bg-zinc-400' },
} as const
</script>

<template>
  <aside
    class="flex h-full shrink-0 flex-col border-r border-zinc-200 bg-white transition-all duration-200 dark:border-zinc-800 dark:bg-zinc-900"
    :class="state.sidebarCollapsed ? 'w-0 overflow-hidden border-r-0' : 'w-64'"
  >
    <div class="p-3">
      <Button variant="primary" class="w-full" :icon="Plus" @click="licode.newSession()">新对话</Button>
    </div>
    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
      <Empty
        v-if="!state.sessions.length"
        title="暂无会话"
        description="点击「新对话」开始"
        size="sm"
        variant="dashed"
        class="mt-8"
      />
      <div
        v-for="s in state.sessions"
        :key="s.id"
        class="group mb-0.5 flex cursor-pointer items-center gap-1 rounded-lg px-2.5 py-2 text-sm transition-colors"
        :class="
          s.id === state.sessionId
            ? 'bg-zinc-100 text-zinc-900 dark:bg-zinc-800 dark:text-zinc-50'
            : 'text-zinc-600 hover:bg-zinc-50 dark:text-zinc-400 dark:hover:bg-zinc-800/60'
        "
        @click="licode.switchSession(s.id)"
      >
        <template v-if="editingId === s.id">
          <div class="flex flex-1 items-center gap-1" @click.stop>
            <Input v-model="editingTitle" size="sm" class="flex-1" @keydown.enter="commitRename" @keydown.esc="editingId = ''" />
            <button class="p-1 text-emerald-600" @click="commitRename"><Check :size="14" /></button>
            <button class="p-1 text-zinc-400" @click="editingId = ''"><X :size="14" /></button>
          </div>
        </template>
        <template v-else>
          <span class="min-w-0 flex-1 truncate" :title="s.title">{{ s.title }}</span>
          <span class="shrink-0 text-[10px] tabular-nums text-zinc-400">{{ s.count }}</span>
          <span class="hidden shrink-0 items-center group-hover:flex">
            <button class="p-1 text-zinc-400 hover:text-zinc-700 dark:hover:text-zinc-200" title="重命名" @click.stop="startRename(s.id, s.title)">
              <Pencil :size="13" />
            </button>
            <button class="p-1 text-zinc-400 hover:text-red-500" title="删除" @click.stop="removeSession(s.id, s.title)">
              <Trash2 :size="13" />
            </button>
          </span>
        </template>
      </div>
    </div>
    <div class="flex items-center gap-2 border-t border-zinc-200 px-3 py-2.5 text-xs text-zinc-500 dark:border-zinc-800">
      <span class="h-2 w-2 rounded-full" :class="statusMap[state.wsStatus].cls" />
      {{ statusMap[state.wsStatus].text }}
    </div>
  </aside>
</template>
