<template>
  <BaseDialog :show="true" :title="t('keys.testModal.title')" width="wide" @close="close">
    <div class="space-y-4">
      <div class="rounded-xl bg-gray-50 p-3 text-sm dark:bg-dark-900">
        <div class="font-medium text-gray-900 dark:text-gray-100">{{ apiKey.name }} · {{ apiKey.group?.name || t('keys.testModal.noGroup') }}</div>
        <p class="mt-1 break-all text-xs text-gray-500">{{ baseUrl || origin }}</p>
        <p class="mt-2 text-xs text-gray-500">{{ t('keys.testModal.hint') }}</p>
      </div>

      <div>
        <label for="key-test-model" class="input-label">{{ t('keys.testModal.model') }}</label>
        <div class="flex gap-2">
          <Select
            id="key-test-model"
            v-model="model"
            class="min-w-0 flex-1"
            :options="modelOptions"
            :disabled="running || loadingModels"
            :loading="loadingModels"
            :searchable="true"
            :aria-label="t('keys.testModal.model')"
            :placeholder="loadingModels ? t('common.loading') : t('keys.testModal.modelPlaceholder')"
            :search-placeholder="t('keys.testModal.searchModels')"
          />
          <button v-if="running" type="button" class="btn btn-secondary shrink-0" @click="stop">{{ t('keys.testModal.stop') }}</button>
          <button v-else type="button" class="btn btn-primary shrink-0" :disabled="loadingModels || !models.includes(model) || !apiKey.group_id || apiKey.status !== 'active'" @click="send">{{ t('keys.testModal.send') }}</button>
        </div>
        <p v-if="modelError" role="alert" class="mt-2 text-xs text-amber-600 dark:text-amber-400">{{ modelError }}</p>
        <p v-if="!apiKey.group_id || apiKey.status !== 'active'" class="mt-2 text-xs text-amber-600">{{ t('keys.testModal.unavailableKey') }}</p>
      </div>

      <div ref="conversation" role="log" aria-live="polite" :aria-label="t('keys.testModal.conversation')" class="h-72 space-y-4 overflow-y-auto rounded-xl border border-gray-200 p-4 dark:border-dark-700 sm:h-80">
        <p v-if="turns.length === 0" class="py-16 text-center text-sm text-gray-400">{{ t('keys.testModal.emptyConversation') }}</p>
        <div v-for="(turn, index) in turns" :key="index" class="space-y-3">
          <div class="ml-8 rounded-xl bg-primary-50 p-3 dark:bg-primary-900/20">
            <p class="mb-1 text-xs text-primary-600 dark:text-primary-400">{{ t('keys.testModal.you') }}</p>
            <p class="whitespace-pre-wrap break-words text-sm text-gray-900 dark:text-gray-100">{{ turn.prompt }}</p>
          </div>
          <div class="mr-8 rounded-xl bg-gray-50 p-3 dark:bg-dark-800">
            <div class="mb-2 flex flex-wrap items-center gap-2 text-xs text-gray-500">
              <span>{{ turn.model }}</span>
              <span v-if="turn.firstTextMs !== null" class="rounded bg-primary-100 px-2 py-0.5 font-mono text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ turn.firstTextMs < 1000 ? t('keys.testModal.firstTextMs', { milliseconds: Math.round(turn.firstTextMs) }) : t('keys.testModal.firstText', { seconds: (turn.firstTextMs / 1000).toFixed(2) }) }}</span>
              <span v-else>{{ t('keys.testModal.noFirstText') }}</span>
            </div>
            <p class="whitespace-pre-wrap break-words text-sm text-gray-900 dark:text-gray-100">{{ turn.reply || (turn.status === 'running' ? t('keys.testModal.waiting') : '—') }}</p>
            <p class="mt-2 text-xs" :class="turn.status === 'success' ? 'text-green-600 dark:text-green-400' : turn.status === 'error' ? 'text-red-600 dark:text-red-400' : 'text-gray-500'">{{ turn.error || t(`keys.testModal.${turn.status}`) }}</p>
          </div>
        </div>
      </div>

    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { fetchKeyTestModels, KeyTestError, streamKeyTest } from '@/api/keyTest'
import type { ApiKey } from '@/types'

const props = defineProps<{ apiKey: ApiKey; baseUrl: string }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const origin = window.location.origin
const model = ref('')
const models = ref<string[]>([])
const modelOptions = computed(() => models.value.map(id => ({ value: id, label: id })))
const modelError = ref('')
const loadingModels = ref(false)
const testPrompt = '你好，请简短回复，确认可以正常对话。'
const conversation = ref<HTMLElement | null>(null)
interface Turn {
  prompt: string
  model: string
  reply: string
  firstTextMs: number | null
  status: 'running' | 'success' | 'error' | 'stopped'
  error?: string
}
const turns = ref<Turn[]>([])
const running = computed(() => turns.value.some(turn => turn.status === 'running'))
let modelController: AbortController | undefined
let chatController: AbortController | undefined

function errorText(error: unknown): string {
  if (error instanceof KeyTestError) {
    if (error.code === 'http') {
      const status = error.status || 0
      const reason = [401, 402, 403, 429].includes(status) ? t(`keys.testModal.http${status}`) : t('keys.testModal.httpOther')
      return t('keys.testModal.httpError', { status, reason })
    }
    return t(`keys.testModal.${error.code}`)
  }
  return t('keys.testModal.networkError')
}

async function loadModels() {
  if (loadingModels.value) return
  const controller = new AbortController()
  modelController = controller
  loadingModels.value = true
  modelError.value = ''
  const timeout = setTimeout(() => controller.abort(), 15000)
  try {
    models.value = await fetchKeyTestModels(props.baseUrl, props.apiKey.key, controller.signal)
    // 只选择分组实际返回的模型，GPT 优先 sol，其余优先文字模型。
    model.value = models.value.find(id => id === 'gpt-5.6-sol')
      || models.value.find(id => /^gpt-.*-sol(?:$|-)/i.test(id))
      || models.value.find(id => !/image|imagine|video|audio|tts|whisper|embedding|rerank/i.test(id))
      || models.value[0] || ''
    if (!models.value.length) modelError.value = t('keys.testModal.noModels')
  } catch (error) {
    modelError.value = t('keys.testModal.modelsFailed', { reason: controller.signal.aborted ? t('keys.testModal.timeout') : errorText(error) })
  } finally {
    clearTimeout(timeout)
    loadingModels.value = false
  }
}

async function send() {
  if (running.value || loadingModels.value || !models.value.includes(model.value) || !props.apiKey.group_id || props.apiKey.status !== 'active') return
  const selectedModel = model.value
  // 每次仅发送固定短消息，避免历史对话影响连通性测试和首字耗时。
  const messages = [{ role: 'user' as const, content: testPrompt }]
  turns.value.push({ prompt: testPrompt, model: selectedModel, reply: '', firstTextMs: null, status: 'running' })
  const turn = turns.value[turns.value.length - 1]
  const controller = new AbortController()
  chatController = controller
  const startedAt = performance.now()
  let timedOut = false
  const timeout = setTimeout(() => { timedOut = true; controller.abort() }, 120000)
  try {
    await streamKeyTest({
      baseUrl: props.baseUrl, apiKey: props.apiKey.key, model: selectedModel, messages, signal: controller.signal,
      onText(text) {
        if (controller.signal.aborted) return
        if (turn.firstTextMs === null) turn.firstTextMs = performance.now() - startedAt
        turn.reply += text
      }
    })
    turn.status = 'success'
  } catch (error) {
    turn.status = controller.signal.aborted && !timedOut ? 'stopped' : 'error'
    if (turn.status === 'error') turn.error = timedOut ? t('keys.testModal.timeout') : errorText(error)
  } finally {
    clearTimeout(timeout)
    chatController = undefined
  }
}

function stop() { chatController?.abort() }
function close() {
  stop()
  modelController?.abort()
  emit('close')
}
watch(() => turns.value.map(turn => `${turn.reply}${turn.status}`).join(''), async () => {
  await nextTick()
  if (conversation.value) conversation.value.scrollTop = conversation.value.scrollHeight
})
onMounted(loadModels)
onUnmounted(() => { stop(); modelController?.abort() })
</script>
