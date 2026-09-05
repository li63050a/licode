<script setup lang="ts">
const { connect } = useLicode()
const { initTheme } = useTheme()
const authEnabled = ref(false)

onMounted(async () => {
  initTheme()
  try {
    const auth = await useApi<{ enabled: boolean }>('/api/auth')
    authEnabled.value = !!auth.enabled
  } catch {
    // 后端暂不可达：继续渲染界面，靠连接状态横幅提示并自动重连
  }
  if (!authEnabled.value) {
    connect()
    return
  }
  try {
    await useApi('/api/version')
    connect()
  } catch {
    navigateTo('/login')
  }
})
</script>

<template>
  <div class="flex h-full overflow-hidden">
    <SidebarNav />
    <main class="flex min-w-0 flex-1 flex-col">
      <ConnectionBanner />
      <TopBar />
      <ChatArea />
    </main>
    <RightPanel />
    <SettingsDialog />
  </div>
</template>
