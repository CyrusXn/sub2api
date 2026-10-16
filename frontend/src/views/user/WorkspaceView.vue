<template>
  <AppLayout>
    <div class="workspace-page">
      <header class="workspace-header">
        <div>
          <p class="eyebrow">{{ t('workspace.eyebrow') }}</p>
          <h1>{{ t('workspace.title') }}</h1>
          <p class="subtitle">{{ t('workspace.subtitle') }}</p>
        </div>
      </header>

      <nav class="workspace-tabs" :aria-label="t('workspace.tabsLabel')">
        <button v-for="item in tabs" :key="item.id" type="button" :data-tab="item.id" :class="{ active: tab === item.id }" :aria-pressed="tab === item.id" :disabled="imageBusy" @click="switchTab(item.id)">
          <Icon :name="item.icon" size="sm" /> {{ item.label }}
        </button>
      </nav>

      <div v-if="imageVisited" v-show="tab === 'image'">
      <WorkspaceImageStudio :selection="tab === 'image' ? selection : null" :loading="loading" @busy-change="imageBusy = $event">
        <template #connection>
          <WorkspaceConnection v-if="tab === 'image'" v-model:key-id="selectedKeyId" v-model:model="selectedModel" :show-all="showAllKeys" :keys="keys" :tab="tab" :loading="loading" :busy="imageBusy" :error="error" @all-keys-change="toggleAllKeys" @key-change="refreshModelForKey" @model-change="newConversation" @refresh="reload" @create-key="goToKeys" />
        </template>
      </WorkspaceImageStudio>
      </div>
      <div v-if="tab !== 'image'" class="workspace-grid">
        <aside class="control-panel panel">
          <fieldset :disabled="loading || busy">
            <WorkspaceConnection v-model:key-id="selectedKeyId" v-model:model="selectedModel" :show-all="showAllKeys" :keys="keys" :tab="tab" :loading="loading" :busy="busy" :error="error" @all-keys-change="toggleAllKeys" @key-change="refreshModelForKey" @model-change="newConversation" @refresh="reload" @create-key="goToKeys" />

            <template v-if="tab !== 'chat'">
              <hr />
              <label for="workspace-prompt">{{ t('workspace.prompt') }}</label>
              <textarea id="workspace-prompt" v-model="state.prompt" rows="8" :placeholder="t('workspace.videoPlaceholder')" />
            </template>

            <template v-if="tab === 'video'">
              <label for="workspace-video-reference">{{ t('workspace.videoReference') }}</label>
              <div class="video-reference-box">
                <Icon name="plus" size="lg" />
                <input id="workspace-video-reference" v-model="referenceVideoURL" type="url" inputmode="url" :placeholder="t('workspace.videoReferencePlaceholder')" />
                <button v-if="referenceVideoURL" type="button" class="text-button" @click="referenceVideoURL = ''">{{ t('common.delete') }}</button>
              </div>
              <p class="field-hint">{{ t(isSeedance ? 'workspace.seedanceReferenceHint' : 'workspace.grokReferenceHint') }}</p>
              <p v-if="videoReferenceError" class="error-message" role="alert">{{ videoReferenceError }}</p>
              <fieldset :disabled="Boolean(referenceVideoURL.trim()) && !isSeedance">
              <div class="field-row">
                <div><label for="workspace-duration">{{ t('workspace.duration') }}</label><input id="workspace-duration" v-model.number="duration" type="number" :min="isSeedance ? 2 : 1" :max="isSeedance ? 12 : 15" step="1" /></div>
                <div><label for="workspace-resolution">{{ t('workspace.resolution') }}</label><Select id="workspace-resolution" v-model="resolution" :options="videoResolutions.map(value => ({ value, label: value }))" :searchable="true" :disabled="loading || busy || Boolean(referenceVideoURL.trim()) && !isSeedance" :aria-label="t('workspace.resolution')" /></div>
              </div>
              <label for="workspace-video-ratio">{{ t('workspace.aspectRatio') }}</label>
              <Select id="workspace-video-ratio" v-model="videoRatio" :options="ratioOptions" :searchable="true" :disabled="loading || busy || Boolean(referenceVideoURL.trim()) && !isSeedance" :aria-label="t('workspace.aspectRatio')" />
              </fieldset>
            </template>
          </fieldset>
          <div v-if="tab !== 'chat'" class="control-actions">
            <button type="button" class="primary-button" data-test="submit" :disabled="!canSubmit" @click="submit"><Icon name="play" size="sm" /> {{ busy ? t('workspace.working') : t('workspace.generateVideo') }}</button>
          </div>
          <button v-if="busy" type="button" class="outline-button" @click="cancelRequest">{{ t('workspace.cancel') }}</button>
          <p v-if="busy && tab !== 'chat'" class="field-hint">{{ t('workspace.cancelHint') }}</p>
        </aside>

        <section class="canvas-column">
          <div class="preview-panel panel" aria-live="polite" :aria-busy="busy">
            <div v-if="tab === 'chat' && state.messages.length" class="conversation">
              <article v-for="(message, index) in state.messages" :key="index" class="message" :class="message.role"><small>{{ t(message.role === 'user' ? 'workspace.you' : 'workspace.assistant') }}</small><div>{{ message.content }}</div></article>
              <div v-if="busy" class="inline-progress"><span class="loader" /> {{ t('workspace.working') }}</div>
            </div>
            <div v-else-if="busy" class="empty-state"><span class="loader" /><strong>{{ statusText || t('workspace.working') }}</strong></div>
            <div v-else-if="state.output && tab !== 'chat'" class="media-result">
              <video :src="state.output" controls playsinline preload="metadata" />
              <a :href="state.output" download="workspace-video.mp4" target="_blank" rel="noopener noreferrer"><Icon name="download" size="sm" /> {{ t('workspace.download') }}</a>
            </div>
            <div v-else class="empty-state"><div class="empty-icon"><Icon :name="tab === 'chat' ? 'chat' : 'play'" size="xl" /></div><strong>{{ t('workspace.empty.' + tab + '.title') }}</strong><span>{{ t('workspace.empty.' + tab + '.description') }}</span></div>
          </div>
          <form v-if="tab === 'chat'" class="chat-composer panel" @submit.prevent="submit">
            <label class="sr-only" for="workspace-prompt">{{ t('workspace.prompt') }}</label>
            <textarea id="workspace-prompt" v-model="state.prompt" rows="3" :disabled="busy" :placeholder="t('workspace.chatPlaceholder')" @keydown="handleChatKeydown" />
            <button type="submit" class="primary-button" data-test="submit" :disabled="!canSubmit"><Icon name="arrowUp" size="sm" /> {{ t('workspace.send') }}</button>
          </form>
        </section>

        <aside class="history-panel panel">
          <button v-if="tab === 'chat'" type="button" class="text-button new-chat" :disabled="busy" @click="newConversation"><Icon name="plus" size="sm" /> {{ t('workspace.newChat') }}</button>
          <div class="history-heading"><h2>{{ t('workspace.history') }}</h2><span>{{ activeHistory.length }}</span></div>
          <p class="field-hint">{{ t('workspace.tabs.' + tab) }} · {{ t('workspace.historyHint') }}</p>
          <button v-if="activeHistory.length" type="button" class="text-button" :disabled="busy" @click="clearHistory"><Icon name="trash" size="xs" /> {{ t('workspace.clearHistory') }}</button>
          <div v-if="!activeHistory.length" class="history-empty"><Icon name="inbox" size="lg" /><span>{{ t('workspace.historyEmpty') }}</span></div>
          <button v-for="item in activeHistory" :key="item.id" type="button" class="history-item" :disabled="busy || loading" @click="restore(item)"><span>{{ item.prompt }}</span><small>{{ item.model }} · {{ item.createdAt }}</small></button>
        </aside>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import WorkspaceConnection from '@/components/workspace/WorkspaceConnection.vue'
import WorkspaceImageStudio from '@/components/workspace/WorkspaceImageStudio.vue'
import { generateWorkspaceVideo, getWorkspaceSelection, loadWorkspaceKeys, loadWorkspaceModels, streamWorkspaceChat, validateWorkspaceReferenceVideoURL, workspaceModelsForKey, workspaceSelectableModels, type WorkspaceKey, type WorkspaceSelection, type WorkspaceTab } from '@/api/workspace'
import { KeyTestError, type TestMessage } from '@/api/keyTest'
import { useAppStore } from '@/stores/app'
import { getWorkspaceTab } from '@/utils/workspaceNavigation'

interface TabState { prompt: string; output: string; resultPrompt: string; messages: TestMessage[] }
interface HistoryItem { id: number; prompt: string; model: string; keyId: number; output: string; messages: TestMessage[]; createdAt: string }
const { t } = useI18n()
const router = useRouter()
const route = useRoute()
const appStore = useAppStore()
const tab = ref<WorkspaceTab>(getWorkspaceTab(route.query.tab))
const keys = ref<WorkspaceKey[]>([])
const selectedKeyId = ref('')
const selectedModel = ref('')
const showAllKeys = ref(false)
const selectedKey = computed(() => keys.value.find(key => String(key.id) === selectedKeyId.value))
const models = computed(() => selectedKey.value ? workspaceSelectableModels(selectedKey.value, tab.value, showAllKeys.value) : [])
const selection = computed<WorkspaceSelection | null>(() => selectedKey.value && selectedModel.value.trim() && (showAllKeys.value || models.value.includes(selectedModel.value)) ? { key: selectedKey.value, model: selectedModel.value.trim() } : null)
const imageVisited = ref(tab.value === 'image')
const imageBusy = ref(false)
const isSeedance = computed(() => /seedance|doubao.*video/i.test(selectedModel.value))
const states = reactive<Record<WorkspaceTab, TabState>>({
  chat: { prompt: '你是谁', output: '', resultPrompt: '', messages: [] }, image: { prompt: '', output: '', resultPrompt: '', messages: [] }, video: { prompt: '', output: '', resultPrompt: '', messages: [] }
})
const state = computed(() => states[tab.value])
const history = reactive<Record<WorkspaceTab, HistoryItem[]>>({ chat: [], image: [], video: [] })
const activeHistory = computed(() => history[tab.value])
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const statusText = ref('')
const videoRatio = ref('16:9')
const duration = ref(6)
const resolution = ref('480p')
const videoResolutions = computed(() => isSeedance.value ? ['480p', '720p', '1080p'] : ['480p', '720p'])
const referenceVideoURL = ref('')
const videoReferenceError = computed(() => {
  if (!referenceVideoURL.value.trim()) return ''
  try {
    validateWorkspaceReferenceVideoURL(referenceVideoURL.value)
    if (isSeedance.value && /seedance[-_.]?1(?:[-_.]|$)/i.test(selectedModel.value)) return t('workspace.videoReferenceUnsupported')
    return ''
  } catch { return t('workspace.videoReferenceError') }
})
const canSubmit = computed(() => !loading.value && !busy.value && Boolean(selection.value) && Boolean(state.value.prompt.trim()) &&
  (tab.value !== 'video' || (!videoReferenceError.value && (Boolean(referenceVideoURL.value.trim()) && !isSeedance.value || (Number.isInteger(duration.value) && duration.value >= (isSeedance.value ? 2 : 1) && duration.value <= (isSeedance.value ? 12 : 15))))))
const tabs = computed(() => [
  { id: 'chat' as const, label: t('workspace.tabs.chat'), icon: 'chat' as const },
  { id: 'image' as const, label: t('workspace.tabs.image'), icon: 'sparkles' as const },
  { id: 'video' as const, label: t('workspace.tabs.video'), icon: 'play' as const }
])
const ratios = ['1:1', '16:9', '9:16', '4:3', '3:4']
const ratioOptions = ratios.map(value => ({ value, label: value }))
const blobURLs = new Set<string>()
let loadController: AbortController | undefined
let requestController: AbortController | undefined
let disposed = false
let nextHistoryId = 0

function applySelection(result: WorkspaceSelection | null): void {
  selectedKeyId.value = result ? String(result.key.id) : ''
  selectedModel.value = result?.model || ''
  if (!videoResolutions.value.includes(resolution.value)) resolution.value = videoResolutions.value[0]
}

async function loadSelection(preserve = false): Promise<void> {
  loadController?.abort()
  const abort = new AbortController()
  loadController = abort
  loading.value = true
  error.value = ''
  try {
    const settings = await appStore.fetchPublicSettings()
    abort.signal.throwIfAborted()
    const loaded = await loadWorkspaceKeys(settings?.api_base_url || window.location.origin, abort.signal)
    abort.signal.throwIfAborted()
    keys.value = loaded
    await loadWorkspaceModels(keys.value, abort.signal)
    abort.signal.throwIfAborted()
    const result = getWorkspaceSelection(keys.value, tab.value)
    if (!preserve || !selection.value) applySelection(result)
  } catch {
    if (!abort.signal.aborted) { selectedModel.value = ''; error.value = t('workspace.loadError') }
  } finally {
    if (loadController === abort) { loading.value = false; loadController = undefined }
  }
}

function switchTab(nextTab: WorkspaceTab): void {
  if (tab.value === nextTab || imageBusy.value) return
  cancelRequest()
  tab.value = nextTab
  showAllKeys.value = false
  if (nextTab === 'image') imageVisited.value = true
  error.value = ''
  selectedModel.value = ''
  void router.replace({ path: '/workspace', query: { tab: nextTab } })
  if (!loading.value) applySelection(getWorkspaceSelection(keys.value, nextTab))
}
function refreshModelForKey(): void {
  selectedModel.value = models.value[0] || ''
  if (!videoResolutions.value.includes(resolution.value)) resolution.value = videoResolutions.value[0]
  newConversation()
}
function toggleAllKeys(value: boolean): void {
  showAllKeys.value = value
  if (!value && !selection.value) {
    applySelection(getWorkspaceSelection(keys.value, tab.value))
    newConversation()
  }
}
function reload(): void { if (!busy.value && !imageBusy.value && !loading.value) void loadSelection(true) }
function goToKeys(): void { void router.push({ path: '/keys', query: { returnTo: '/workspace', tab: tab.value } }) }
function newConversation(): void { if (tab.value === 'chat' && !busy.value) states.chat.messages = [] }
function handleChatKeydown(event: KeyboardEvent): void {
  if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); void submit() }
}
function cancelRequest(): void {
  requestController?.abort()
  requestController = undefined
  busy.value = false
  statusText.value = ''
}
function retainURL(url: string): void { if (url.startsWith('blob:')) blobURLs.add(url) }
function releaseUnusedURLs(): void {
  const used = new Set([...Object.values(states).map(item => item.output), ...Object.values(history).flatMap(items => items.map(item => item.output))])
  for (const url of blobURLs) if (!used.has(url)) { URL.revokeObjectURL(url); blobURLs.delete(url) }
}

function chatErrorText(cause: unknown): string {
  if (cause instanceof KeyTestError) {
    if (cause.code === 'http') {
      const status = cause.status || 0
      const reason = t([401, 402, 403, 429].includes(status) ? `keys.testModal.http${status}` : 'keys.testModal.httpOther')
      return t('keys.testModal.httpError', { status, reason })
    }
    return t(`keys.testModal.${cause.code}`)
  }
  return t('keys.testModal.networkError')
}

async function submit(): Promise<void> {
  const current = selection.value
  if (!current || !canSubmit.value) return
  const currentTab = tab.value
  const target = states[currentTab]
  const prompt = target.prompt.trim()
  const abort = new AbortController()
  const previousMessages = target.messages.map(message => ({ ...message }))
  let pendingMessages: TestMessage[] | undefined
  const restorePreviousMessages = () => {
    if (currentTab === 'chat' && target.messages === pendingMessages) target.messages = previousMessages
  }
  abort.signal.addEventListener('abort', restorePreviousMessages, { once: true })
  requestController = abort
  busy.value = true
  error.value = ''
  try {
    if (currentTab === 'chat') {
      const messages: TestMessage[] = [...target.messages, { role: 'user', content: prompt }]
      target.messages = [...messages, { role: 'assistant', content: '' }]
      pendingMessages = target.messages
      const assistant = target.messages[target.messages.length - 1]
      await streamWorkspaceChat({ selection: current, messages, signal: abort.signal, onText: text => {
        if (!abort.signal.aborted) assistant.content += text
      } })
    } else {
      const result = await generateWorkspaceVideo({ selection: current, prompt, duration: duration.value, aspectRatio: videoRatio.value, resolution: resolution.value, referenceVideoURL: referenceVideoURL.value.trim() || undefined, signal: abort.signal, onStatus: status => {
        if (!abort.signal.aborted) statusText.value = t('workspace.videoStatus.' + (['queued', 'pending', 'running'].includes(status) ? status : 'processing'))
      } })
      if (abort.signal.aborted || disposed) { if (result.url.startsWith('blob:')) URL.revokeObjectURL(result.url); return }
      target.output = result.url
      retainURL(result.url)
    }
    abort.signal.throwIfAborted()
    target.resultPrompt = prompt
    history[currentTab].unshift({ id: ++nextHistoryId, prompt, model: current.model, keyId: current.key.id, output: target.output, messages: target.messages.map(message => ({ ...message })), createdAt: new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })
    history[currentTab] = history[currentTab].slice(0, 12)
    releaseUnusedURLs()
  } catch (cause) {
    // 只恢复本次请求前的上下文，迟到的取消回调不能覆盖更新的对话。
    restorePreviousMessages()
    if (!abort.signal.aborted) error.value = currentTab === 'chat' ? chatErrorText(cause) : t(cause instanceof Error && cause.message === 'video-generation-timeout' ? 'workspace.videoTimeout' : 'workspace.requestError')
  } finally {
    abort.signal.removeEventListener('abort', restorePreviousMessages)
    if (requestController === abort) { busy.value = false; statusText.value = ''; requestController = undefined }
  }
}
function restore(item: HistoryItem): void {
  state.value.prompt = item.prompt
  state.value.output = item.output
  state.value.resultPrompt = item.prompt
  state.value.messages = item.messages.map(message => ({ ...message }))
  const key = keys.value.find(key => key.id === item.keyId)
  if (key && item.model.trim() && (showAllKeys.value || workspaceModelsForKey(key, tab.value).includes(item.model))) { selectedKeyId.value = String(key.id); selectedModel.value = item.model }
}
function clearHistory(): void { history[tab.value] = []; releaseUnusedURLs() }
onMounted(() => { void loadSelection() })
onUnmounted(() => {
  disposed = true
  loadController?.abort()
  cancelRequest()
  for (const url of blobURLs) URL.revokeObjectURL(url)
})
</script>

<style scoped>
.workspace-page { --accent: #138f87; --accent-soft: #e7f7f4; --surface: #fff; --canvas: #fbfdfd; --border: #dce7eb; --ink: #243841; --muted: #70838e; min-height: calc(100vh - 7rem); color: var(--ink); }
.workspace-header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; margin-bottom: 1.25rem; }
.eyebrow { margin: 0 0 .3rem; color: var(--accent); font-size: .65rem; font-weight: 700; letter-spacing: .16em; }
h1 { margin: 0; font-size: clamp(1.5rem, 2.6vw, 2rem); font-weight: 750; letter-spacing: -.03em; }
.subtitle { margin: .4rem 0 0; color: var(--muted); font-size: .85rem; }
.key-label-row { display: flex; align-items: center; justify-content: space-between; gap: .5rem; }
.key-status { display: inline-flex; align-items: center; gap: .3rem; border-radius: .4rem; padding: .3rem .45rem; color: var(--muted); font-size: .65rem; font-weight: 650; }
.key-status.matching { color: #a16207; background: #fef3c7; border: 1px solid #fcd34d; }.key-status.ready { color: var(--accent); background: var(--accent-soft); }
.matching-spinner { width: .75rem; height: .75rem; border: 2px solid #fcd34d; border-top-color: currentColor; border-radius: 50%; animation: spin .8s linear infinite; }
.video-reference-box { display: flex; flex-direction: column; align-items: center; gap: .6rem; border: 1px dashed #a6c9c7; border-radius: .65rem; padding: 1rem .65rem; background: #f5fbfa; color: var(--accent); }.video-reference-box input { font-size: .7rem; }.workspace-tabs { display: flex; width: fit-content; gap: .2rem; margin: 0 auto 1.25rem; padding: .25rem; border: 1px solid var(--border); border-radius: .7rem; background: var(--surface); }
.workspace-tabs button { display: inline-flex; align-items: center; gap: .4rem; border: 1px solid transparent; border-radius: .5rem; padding: .55rem 1.25rem; color: var(--muted); font-size: .85rem; }
.workspace-tabs button.active { border-color: #a6d9d3; background: var(--accent-soft); color: var(--accent); }
.workspace-grid { display: grid; grid-template-columns: minmax(15rem, 19rem) minmax(0, 1fr) minmax(12rem, 15rem); align-items: stretch; gap: 1rem; min-height: 36rem; }
.panel { min-width: 0; border: 1px solid var(--border); border-radius: .85rem; background: var(--surface); box-shadow: 0 8px 28px #27495705; }
.control-panel { display: flex; flex-direction: column; gap: .6rem; padding: 1.1rem; }
fieldset { display: flex; flex-direction: column; gap: .6rem; margin: 0; padding: 0; border: 0; min-width: 0; }
label { display: block; color: var(--ink); font-size: .73rem; font-weight: 650; }label span { color: var(--muted); font-weight: 400; }
select, textarea, input { width: 100%; min-width: 0; border: 1px solid var(--border); border-radius: .5rem; padding: .65rem .7rem; background: var(--canvas); color: var(--ink); font: inherit; font-size: .8rem; }
textarea { resize: vertical; line-height: 1.65; }input[type=file] { padding: .4rem; font-size: .68rem; }
.field-hint { margin: 0; color: var(--muted); font-size: .68rem; line-height: 1.65; }
.label-row { display: flex; align-items: center; justify-content: space-between; gap: .5rem; margin-top: .2rem; }
.protocol-hint { display: grid; gap: .4rem; margin-top: .5rem; color: var(--muted); font-size: .7rem; }.protocol-hint code { padding: .65rem; border-radius: .45rem; background: var(--accent-soft); color: var(--accent); font-size: .65rem; overflow-wrap: anywhere; }
.text-button { display: inline-flex; align-items: center; gap: .3rem; color: var(--accent); font-size: .7rem; }.new-chat { margin-bottom: .9rem; }
hr { margin: .35rem 0; border: 0; border-top: 1px solid var(--border); }
.primary-button, .outline-button { display: inline-flex; justify-content: center; align-items: center; gap: .4rem; min-height: 2.5rem; border-radius: .5rem; padding: .65rem .85rem; font-size: .78rem; font-weight: 650; }
.primary-button { color: white; background: var(--accent); }.outline-button { border: 1px solid #a6d9d3; color: var(--accent); background: var(--accent-soft); }
button:not(:disabled) { cursor: pointer; }button:disabled, select:disabled { opacity: .45; cursor: not-allowed; }
button:focus-visible, a:focus-visible, input:focus-visible, select:focus-visible, textarea:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
.control-actions { margin-top: auto; padding-top: .6rem; }.control-actions .primary-button, .key-guide .outline-button { width: 100%; }
.key-guide { padding: .8rem; border: 1px dashed var(--border); border-radius: .55rem; }.key-guide p { margin: 0 0 .6rem; font-size: .75rem; color: var(--muted); }
.segmented { display: flex; gap: .35rem; }.segmented button { flex: 1; border: 1px solid var(--border); border-radius: .4rem; padding: .4rem; color: var(--muted); font-size: .75rem; }.segmented button.selected { border-color: #9bd6cf; background: var(--accent-soft); color: var(--accent); }
.field-row { display: grid; grid-template-columns: 1fr 1fr; gap: .5rem; }.field-row label { min-height: 2rem; padding-bottom: .3rem; }
.reference-list { display: grid; gap: .2rem; margin: 0; padding: 0; list-style: none; }.reference-list li { display: flex; justify-content: space-between; gap: .5rem; font-size: .7rem; color: var(--muted); }.reference-list span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tools-link { display: inline-flex; align-items: center; gap: .3rem; margin-top: .4rem; font-size: .7rem; color: var(--accent); }
.error-message { margin: .3rem 0; color: #d45757; font-size: .75rem; line-height: 1.65; }
.canvas-column { display: flex; flex-direction: column; gap: .75rem; min-width: 0; }
.preview-panel { display: grid; place-items: center; flex: 1; min-height: 30rem; padding: 1.25rem; overflow: hidden; background: linear-gradient(160deg, var(--surface), var(--canvas)); }
.empty-state { display: flex; flex-direction: column; align-items: center; gap: .8rem; color: var(--muted); text-align: center; max-width: 24rem; font-size: .82rem; }.empty-state strong { color: var(--ink); font-size: 1.1rem; }.empty-icon { display: grid; place-items: center; width: 4.6rem; height: 4.6rem; margin-bottom: .35rem; border: 1px solid #a6d9d3; border-radius: 1.25rem; background: var(--accent-soft); color: var(--accent); }
.loader { display: inline-block; flex-shrink: 0; width: 1.6rem; height: 1.6rem; border: 2px solid var(--border); border-top-color: var(--accent); border-radius: 50%; animation: spin .8s linear infinite; }
.conversation { align-self: start; width: 100%; max-height: 38rem; overflow-y: auto; }.message { margin: 0 0 1.3rem; padding: .85rem; border: 1px solid var(--border); border-radius: .65rem; background: var(--surface); font-size: .85rem; line-height: 1.75; }.message.user { margin-left: 10%; background: var(--accent-soft); border-color: #a6d9d3; }.message small { display: block; margin-bottom: .35rem; font-size: .65rem; color: var(--accent); }.message div { white-space: pre-wrap; overflow-wrap: anywhere; }
.inline-progress { display: flex; align-items: center; gap: .6rem; color: var(--muted); font-size: .75rem; }
.chat-composer { display: flex; align-items: end; gap: .5rem; padding: .6rem; }.chat-composer textarea { border: 0; resize: none; background: transparent; }.chat-composer .primary-button { flex-shrink: 0; }
.media-result { display: grid; gap: 1rem; width: 100%; justify-items: center; }.media-result img, .media-result video { display: block; max-width: 100%; max-height: 38rem; border-radius: .5rem; object-fit: contain; }.media-result a { display: inline-flex; align-items: center; gap: .35rem; color: var(--accent); font-size: .78rem; }
.history-panel { padding: 1.1rem; }.history-heading { display: flex; align-items: center; justify-content: space-between; margin-bottom: .5rem; }.history-heading h2 { margin: 0; font-size: 1rem; font-weight: 700; }.history-heading span { border: 1px solid var(--border); border-radius: .4rem; padding: .15rem .5rem; color: var(--accent); font-size: .7rem; }.history-panel > .text-button { margin-top: .7rem; }
.history-empty { display: grid; place-content: center; justify-items: center; gap: .8rem; min-height: 23rem; color: var(--muted); text-align: center; font-size: .75rem; }.history-item { display: grid; width: 100%; gap: .3rem; margin-top: .65rem; border: 1px solid var(--border); border-radius: .5rem; padding: .65rem; text-align: left; background: var(--canvas); }.history-item span { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; font-size: .78rem; }.history-item small { color: var(--muted); font-size: .65rem; overflow-wrap: anywhere; }.history-item:hover { border-color: #a6d9d3; }
.dark .workspace-page { --accent: #50c7b9; --accent-soft: #1c3939; --surface: #17252d; --canvas: #142128; --border: #30434f; --ink: #dae7eb; --muted: #97aeb9; }
.dark .primary-button { color: #102a28; }
.dark .key-status.matching { color: #fcd34d; background: #42331b; border-color: #806324; }
.dark .video-reference-box { background: var(--canvas); border-color: var(--border); }
@keyframes spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .loader, .matching-spinner { animation: none; } }
@media (max-width: 1280px) { .workspace-grid { grid-template-columns: minmax(14rem, 17rem) minmax(0, 1fr); }.history-panel { grid-column: 1 / -1; }.history-empty { min-height: 7rem; }.history-item { display: inline-grid; width: calc(50% - .35rem); margin-right: .35rem; } }
@media (max-width: 760px) { .workspace-header { align-items: start; flex-direction: column; }.workspace-tabs { width: 100%; }.workspace-tabs button { flex: 1; justify-content: center; padding-inline: .4rem; }.workspace-grid { grid-template-columns: minmax(0, 1fr); }.preview-panel { min-height: 22rem; }.history-panel { grid-column: auto; }.history-item { width: 100%; }.conversation { max-height: 28rem; }.chat-composer { align-items: stretch; flex-direction: column; }.control-panel { gap: .7rem; } }
</style>
