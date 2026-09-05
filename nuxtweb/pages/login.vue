<script setup lang="ts">
import { Button, Input } from 'fuxsto-design'
import { Bot, LogIn } from 'lucide-vue-next'

const { initTheme } = useTheme()
const username = ref('licode')
const password = ref('')
const error = ref('')
const route = useRoute()

onMounted(async () => {
  initTheme()
  if (route.query.error === '1') error.value = '用户名或密码错误'
  try {
    const auth = await useApi<{ enabled: boolean }>('/api/auth')
    if (!auth.enabled) {
      navigateTo('/')
      return
    }
  } catch {
    // 后端不可达：保持登录页并提示
  }
})
</script>

<template>
  <div class="flex h-full items-center justify-center bg-zinc-50 p-4 dark:bg-zinc-950">
    <div
      class="w-full max-w-sm rounded-2xl border border-zinc-200 bg-white p-6 shadow-lg dark:border-zinc-800 dark:bg-zinc-900"
    >
      <div class="mb-5 flex flex-col items-center gap-2">
        <span class="flex h-12 w-12 items-center justify-center rounded-xl bg-zinc-900 text-white dark:bg-zinc-100 dark:text-zinc-900">
          <Bot :size="22" />
        </span>
        <h1 class="text-lg font-semibold tracking-tight">licode</h1>
        <p class="text-xs text-zinc-500 dark:text-zinc-400">登录以继续使用本地 AI 编程助手</p>
      </div>

      <form class="space-y-3" action="/login" method="post">
        <!-- 原生表单提交：Go 后端 POST /login 返回 302 + Set-Cookie，浏览器自动保存登录 cookie -->
        <input type="hidden" name="username" :value="username" />
        <input type="hidden" name="password" :value="password" />
        <label class="block space-y-1">
          <span class="text-xs text-zinc-500">用户名</span>
          <Input v-model="username" placeholder="licode" autocomplete="username" />
        </label>
        <label class="block space-y-1">
          <span class="text-xs text-zinc-500">密码</span>
          <Input v-model="password" type="password" placeholder="••••••••" autocomplete="current-password" />
        </label>

        <div
          v-if="error"
          class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-600 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-400"
        >
          {{ error }}
        </div>

        <Button variant="primary" class="w-full" type="submit" :icon="LogIn">
          登录
        </Button>
      </form>
    </div>
  </div>
</template>
