<template>
  <AppLayout>
    <main class="quality-page">
      <header class="quality-hero">
        <div><span class="quality-eyebrow">{{ t('modelQuality.eyebrow') }}</span><h1>{{ t('modelQuality.title') }}<span class="hero-mark">✦</span></h1><p>{{ t('modelQuality.intro') }}</p></div>
        <button type="button" class="btn btn-primary" data-test="start" :disabled="!canStart" @click="start">{{ submitting ? t('modelQuality.starting') : t('modelQuality.start') }} <span aria-hidden="true">↗</span></button>
      </header>

      <section class="quality-settings" :aria-label="t('modelQuality.title')">
        <div class="connection-grid">
          <div><label for="quality-key">{{ t('modelQuality.key') }}</label><select id="quality-key" v-model="keyID" class="input" :disabled="keysLoading || submitting" @change="selectModel"><option :value="0">{{ t('modelQuality.selectKey') }}</option><option v-for="key in keys" :key="key.id" :value="key.id">{{ key.name }} · {{ key.groupName }}</option></select></div>
          <div><label for="quality-model">{{ t('modelQuality.model') }}</label><select id="quality-model" v-model="model" class="input" :disabled="keysLoading || submitting"><option value="">{{ t('modelQuality.selectModel') }}</option><option v-for="item in models" :key="item" :value="item">{{ item }}</option></select></div>
        </div>
        <p v-if="keyError" class="quality-error">{{ keyError }}</p>
        <p class="quality-note">{{ t('modelQuality.billing') }}</p>
        <div class="theme-bar" role="tablist" :aria-label="t('modelQuality.title')"><button v-for="item in themes" :key="item" type="button" role="tab" :aria-selected="theme === item" :data-theme="item" :class="{ active: theme === item }" :disabled="submitting" @click="theme = item">{{ t('modelQuality.' + item) }}</button></div>
        <div class="challenge"><p>{{ t('modelQuality.' + theme + 'Hint') }}</p><small v-if="theme === 'candy'">{{ t('modelQuality.reference') }}</small></div>
        <div v-if="theme === 'custom'" class="custom-prompt"><label for="quality-prompt">{{ t('modelQuality.promptLabel') }} <small>{{ prompt.length }}/2000</small></label><textarea id="quality-prompt" v-model="prompt" class="input" rows="4" maxlength="2000" :disabled="submitting" :placeholder="t('modelQuality.placeholder')" /></div>
        <p class="quality-note">{{ t('modelQuality.background') }}</p>
      </section>

      <div v-if="error" class="quality-error error-banner" role="alert">{{ error }}</div>
      <section ref="resultsSection" class="results-section" :aria-label="t('modelQuality.shared')" :aria-busy="loading">
        <div class="results-heading"><div><h2>{{ t('modelQuality.shared') }} <span v-if="total">{{ total }}</span></h2><p>{{ t('modelQuality.sharedHint') }}</p></div><div class="result-filters"><select v-model="filterTheme" class="input" :aria-label="t('modelQuality.allThemes')" @change="resetResults"><option value="">{{ t('modelQuality.allThemes') }}</option><option v-for="item in themes" :key="item" :value="item">{{ t('modelQuality.' + item) }}</option></select><select v-model="filterStatus" class="input" :aria-label="t('modelQuality.allStatuses')" @change="resetResults"><option value="">{{ t('modelQuality.allStatuses') }}</option><option v-for="status in statuses" :key="status" :value="status">{{ t('modelQuality.' + status) }}</option></select><button type="button" class="btn btn-secondary" :disabled="loading" @click="loadResults">{{ t('modelQuality.refresh') }}</button></div></div>
        <div v-if="loading && !runs.length" class="quality-empty">{{ t('modelQuality.loading') }}</div>
        <div v-else-if="!runs.length && !error" class="quality-empty"><span aria-hidden="true">◇</span><h3>{{ t('modelQuality.empty') }}</h3><p>{{ t('modelQuality.emptyHint') }}</p></div>
        <div class="results-grid">
          <article v-for="run in runs" :key="run.id" class="result-card">
            <div class="result-top"><span class="group-name">{{ run.group_name }}</span><span class="quality-status" :class="run.status">{{ t('modelQuality.' + run.status) }}</span></div>
            <h3>{{ run.model }}</h3>
            <p class="result-author">{{ run.author }} <span>·</span> <time :datetime="run.created_at">{{ formatDate(run.created_at) }}</time></p>
            <div v-if="auth.isAdmin" class="account-line"><span>{{ t('modelQuality.account') }}</span><strong>{{ run.account_name || (run.account_id ? t('modelQuality.unavailableAccount') : t('modelQuality.noAccount')) }}</strong><small>{{ t('modelQuality.adminOnly') }}</small></div>
            <template v-if="run.theme === 'candy' && run.status !== 'failed'">
              <div class="answer-panel"><div class="answer-header">{{ t('modelQuality.answer') }} <small>{{ t('modelQuality.reference') }}</small></div><section v-if="run.content.trim()" class="answer-scroll"><div class="markdown-body answer-markdown" v-html="renderMarkdown(run.content)"></div></section><p v-else-if="run.status === 'running'" class="answer-pending" role="status"><span class="loading-spinner" aria-hidden="true" />{{ t('modelQuality.runningHint') }}</p></div>
            </template>
            <div v-else-if="run.status === 'completed' || (run.status === 'running' && /<svg[\s>]/i.test(run.content))" class="preview-panel"><iframe :title="t('modelQuality.previewTitle') + ' · ' + run.model" sandbox="" referrerpolicy="no-referrer" loading="lazy" :srcdoc="safeModelQualityHTML(run.content)" /><button v-if="run.status === 'completed'" type="button" class="preview-enlarge" @click="expanded = run">{{ t('modelQuality.enlarge') }} ↗</button></div>
            <div v-else-if="run.status === 'running'" class="result-placeholder" role="status"><span class="loading-spinner" aria-hidden="true" />{{ t('modelQuality.runningHint') }}</div>
            <div v-else class="result-placeholder failure">{{ run.error || t('modelQuality.failed') }}</div>
            <details class="prompt-details"><summary>{{ t('modelQuality.viewPrompt') }}</summary><p>{{ run.prompt }}</p></details>
          </article>
        </div>
        <nav v-if="total > 12" class="pagination" aria-label="Pagination"><button type="button" class="btn btn-secondary" :disabled="page === 1 || loading" @click="changePage(-1)">{{ t('modelQuality.previous') }}</button><span>{{ page }} / {{ Math.ceil(total / 12) }}</span><button type="button" class="btn btn-secondary" :disabled="page * 12 >= total || loading" @click="changePage(1)">{{ t('modelQuality.next') }}</button></nav>
      </section>
      <dialog ref="previewDialog" class="quality-dialog" @close="expanded = null" @click="closeOutside"><div v-if="expanded" class="dialog-content"><header><h2>{{ expanded.model }} · {{ expanded.group_name }}</h2><button type="button" class="btn btn-secondary" @click="closePreview">{{ t('modelQuality.close') }}</button></header><iframe :title="t('modelQuality.previewTitle')" sandbox="" referrerpolicy="no-referrer" :srcdoc="safeModelQualityHTML(expanded.content)" /></div></dialog>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAuthStore } from '@/stores/auth'
import { loadWorkspaceKeys, loadWorkspaceModels, workspaceModelsForKey, type WorkspaceKey } from '@/api/workspace'
import { modelQualityAPI, modelQualityPrompt, safeModelQualityHTML, type QualityRun, type QualityTheme } from '@/api/modelQuality'
import { streamKeyTest } from '@/api/keyTest'
import { renderMarkdown } from '@/utils/markdown'
import '@/styles/announcement-markdown.css'

const { t } = useI18n()
const auth = useAuthStore()
const themes: QualityTheme[] = ['pelican', 'candy', 'custom']
const statuses = ['running', 'completed', 'failed']
const theme = ref<QualityTheme>('pelican'), prompt = ref(''), keys = ref<WorkspaceKey[]>([]), keyID = ref(0), model = ref('')
const runs = ref<QualityRun[]>([]), total = ref(0), page = ref(1), filterTheme = ref(''), filterStatus = ref('')
const keysLoading = ref(true), loading = ref(false), submitting = ref(false), ready = ref(false), error = ref(''), keyError = ref('')
const expanded = ref<QualityRun | null>(null), previewDialog = ref<HTMLDialogElement | null>(null)
const resultsSection = ref<HTMLElement | null>(null)
// 列表确认读到已保存记录后，才移除当前页面的流式结果。
const pendingRuns = new Map<number, QualityRun>()
const lifecycle = new AbortController()
let request: AbortController | undefined, timer: ReturnType<typeof setTimeout> | undefined, disposed = false
const models = computed(() => { const key = keys.value.find(item => item.id === keyID.value); return key ? workspaceModelsForKey(key, 'chat') : [] })
const canStart = computed(() => !keysLoading.value && !submitting.value && keyID.value > 0 && models.value.includes(model.value) && (theme.value !== 'custom' || prompt.value.trim().length > 0))
function selectModel() { model.value = models.value[0] || '' }
function formatDate(value: string) { return new Date(value).toLocaleString('zh-CN', { hour12: false }) }
async function loadKeys() {
  try {
    keys.value = await loadWorkspaceKeys(window.location.origin, lifecycle.signal)
    await loadWorkspaceModels(keys.value, lifecycle.signal)
    keyID.value = keys.value.find(key => workspaceModelsForKey(key, 'chat').length)?.id || 0
    selectModel()
    if (!keys.value.length) keyError.value = t('modelQuality.noKeys')
    else if (!models.value.length) keyError.value = t('modelQuality.keyError')
  } catch { if (!disposed) keyError.value = t('modelQuality.keyError') }
  finally { keysLoading.value = false }
}
async function loadResults() {
  request?.abort(); clearTimeout(timer)
  const current = new AbortController(); request = current
  loading.value = true
  try {
    const result = await modelQualityAPI.list({ page: page.value, theme: filterTheme.value, status: filterStatus.value }, current.signal)
    if (disposed || current.signal.aborted) return
    const receivedIDs = new Set(result.items.map(run => run.id))
    for (const [id, run] of pendingRuns) if (receivedIDs.has(run.id)) pendingRuns.delete(id)
    const pending = page.value === 1 ? [...pendingRuns.values()].filter(run => (!filterTheme.value || run.theme === filterTheme.value) && (!filterStatus.value || run.status === filterStatus.value)) : []
    runs.value = [...pending, ...result.items]; total.value = result.total + pending.length; ready.value = true; error.value = ''
  } catch (cause) {
    if (disposed || current.signal.aborted) return
    ready.value = false
    error.value = t((cause as {status?: number}).status === 404 ? 'modelQuality.notDeployed' : 'modelQuality.loadError')
  } finally {
    if (!disposed && !current.signal.aborted) { loading.value = false; if (ready.value) timer = setTimeout(loadResults, runs.value.some(run => run.status === 'running') ? 5000 : 30000) }
  }
}
async function start() {
  if (!canStart.value) return
  submitting.value = true; error.value = ''
  const selected = keys.value.find(item => item.id === keyID.value)
  if (!selected) { submitting.value = false; return }
  const selectedTheme = theme.value
  const fullPrompt = modelQualityPrompt(selectedTheme, prompt.value)
  filterTheme.value = ''; filterStatus.value = ''; page.value = 1
  const localRun = reactive<QualityRun>({ id: -Date.now(), author: t('modelQuality.currentUser'), group_name: selected.groupName, model: model.value, theme: selectedTheme, prompt: fullPrompt, status: 'running', content: '', created_at: new Date().toISOString() })
  pendingRuns.set(localRun.id, localRun)
  runs.value = [localRun, ...runs.value]
  total.value += 1
  await nextTick()
  resultsSection.value?.scrollIntoView?.({ block: 'end', behavior: 'smooth' })
  const requestController = new AbortController()
  const timeout = window.setTimeout(() => requestController.abort(), 8 * 60 * 1000)
  let requestID = ''
  let content = ''
  try {
    await streamKeyTest({
      baseUrl: selected.baseUrl,
      apiKey: selected.key,
      model: model.value,
      protocol: /^gemini-/i.test(model.value) ? 'gemini' : 'openai',
      messages: [{ role: 'user', content: fullPrompt }],
      signal: requestController.signal,
      maxTokens: 8192,
      toolChoice: 'none',
      requireStop: true,
      onRequestID: id => { requestID = id },
      onText: text => { content += text; localRun.content = content }
    })
    content = content.trim()
    if (!content || (selectedTheme !== 'candy' && !/<svg|<html/i.test(content))) throw new Error('invalid-model-output')
    localRun.content = content
    localRun.status = 'completed'
    try {
      const saved = await modelQualityAPI.save({ api_key_id: selected.id, model: model.value, theme: selectedTheme, prompt: selectedTheme === 'custom' ? prompt.value.trim() : '', content, status: 'completed', request_id: requestID || undefined })
      if (saved?.id) Object.assign(localRun, saved)
      await loadResults()
    } catch { error.value = t('modelQuality.historyUnavailable') }
  } catch {
    if (!disposed) error.value = t('modelQuality.createError')
    localRun.status = 'failed'
    localRun.error = t('modelQuality.failed')
    try {
      const saved = await modelQualityAPI.save({ api_key_id: selected.id, model: model.value, theme: selectedTheme, prompt: selectedTheme === 'custom' ? prompt.value.trim() : '', content: '', status: 'failed', request_id: requestID || undefined })
      if (saved?.id) Object.assign(localRun, saved)
      await loadResults()
    } catch { /* 历史 API 不可用时保留本地失败记录。 */ }
  } finally { clearTimeout(timeout); submitting.value = false }
}
function resetResults() { page.value = 1; void loadResults() }
function changePage(delta: number) { page.value += delta; void loadResults() }
watch(expanded, async (value) => { await nextTick(); if (value && !previewDialog.value?.open) previewDialog.value?.showModal(); else if (!value && previewDialog.value?.open) previewDialog.value.close() })
function closePreview() { previewDialog.value?.close(); expanded.value = null }
function closeOutside(event: MouseEvent) { if (event.target === previewDialog.value) closePreview() }
onMounted(() => { void loadKeys(); void loadResults() })
onBeforeUnmount(() => { disposed = true; lifecycle.abort(); request?.abort(); clearTimeout(timer) })
</script>

<style scoped>
.answer-scroll{display:block;max-height:600px;overflow:auto;margin-top:16px;min-width:0}
.answer-markdown{display:block;overflow-wrap:anywhere;white-space:normal}
.answer-markdown :deep(table){min-width:100%;width:max-content;max-width:none}
.answer-markdown :deep(th),.answer-markdown :deep(td){min-width:90px;max-width:360px;white-space:normal;word-break:normal}
.answer-pending{display:flex;align-items:center;gap:10px;min-height:100px;color:#70818c}
.loading-spinner{display:inline-block;flex-shrink:0;width:16px;height:16px;border:2px solid #b4d8d1;border-top-color:#0d9488;border-radius:50%;animation:quality-spin .8s linear infinite}
@keyframes quality-spin{to{transform:rotate(360deg)}}
@media(prefers-reduced-motion:reduce){.loading-spinner{animation:none}}
.quality-page{max-width:1400px;margin:0 auto;padding:8px 0 32px;color:var(--color-text-primary,#172b36)}
.quality-hero{display:flex;align-items:center;justify-content:space-between;gap:24px;margin-bottom:28px;padding:24px 0}.quality-eyebrow{font-size:11px;letter-spacing:.2em;font-weight:700;color:#0c8a80}.quality-hero h1{font-size:34px;font-weight:750;letter-spacing:-.04em;margin:10px 0}.hero-mark{color:#0d9488;margin-left:16px;font-weight:400}.quality-hero p,.results-heading p{font-size:14px;color:#77828e;line-height:1.7}.quality-hero .btn{gap:30px;white-space:nowrap;min-height:46px}
.quality-settings{background:var(--color-bg-primary,#fff);border:1px solid #dce8e6;border-radius:20px;padding:26px 30px;box-shadow:0 5px 20px #0d948805}.connection-grid{display:grid;grid-template-columns:1fr 1fr;gap:24px}label{display:block;font-size:13px;font-weight:600;margin-bottom:9px}.input{font-size:14px;width:100%;min-height:42px}.quality-note{font-size:12px;color:#818b96;line-height:1.8;margin-top:14px}.theme-bar{display:flex;gap:8px;border-top:1px solid #e9eeee;padding-top:22px;margin-top:22px}.theme-bar button{padding:10px 24px;border:1px solid #e1e7e7;border-radius:10px;color:#6d7b84;background:transparent;font-size:14px}.theme-bar button.active{background:#e5f4ef;color:#087c70;border-color:#a9d8ca;font-weight:650}.theme-bar button:focus-visible{outline:2px solid #0d9488;outline-offset:3px}.challenge{padding:18px 0 0;font-size:14px;line-height:1.8}.challenge small{display:block;margin-top:6px;color:#0e8678;font-weight:600}.custom-prompt{margin-top:18px}.custom-prompt label{display:flex;justify-content:space-between}.custom-prompt small{color:#89949e;font-weight:400}.custom-prompt textarea{resize:vertical;min-height:110px}.quality-error{font-size:13px;color:#be4937;line-height:1.7}.error-banner{margin:20px 0;background:#fff5ed;border:1px solid #f2d4be;border-radius:12px;padding:16px 20px}
.results-section{margin-top:34px}.results-heading{display:flex;align-items:center;justify-content:space-between;gap:20px;margin-bottom:24px}.results-heading h2{font-size:19px;font-weight:650;display:flex;align-items:center;gap:9px;margin-bottom:6px}.results-heading h2 span{font-size:12px;font-weight:500;padding:2px 7px;border-radius:6px;background:#e5efec;color:#58786e}.result-filters{display:flex;gap:8px;flex-shrink:0}.result-filters .input{width:125px}.results-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:24px}.result-card{padding:24px;border:1px solid #e0e7e7;border-radius:18px;background:var(--color-bg-primary,#fff);min-width:0}.result-top{display:flex;align-items:center;justify-content:space-between;gap:12px}.group-name{font-size:12px;font-weight:650;color:#0d867c;overflow-wrap:anywhere}.quality-status{font-size:11px;padding:5px 10px;border-radius:20px;background:#edf3f3;color:#577b79;white-space:nowrap}.quality-status.completed{background:#edf9f0;color:#378052}.quality-status.failed{background:#fff0ee;color:#b95547}.result-card h3{font-size:23px;letter-spacing:-.02em;margin-top:10px;font-weight:600;overflow-wrap:anywhere}.result-author{font-size:12px;color:#89929f;margin:12px 0 20px;display:flex;gap:8px;flex-wrap:wrap}.account-line{background:#f5f7fa;padding:9px 11px;border-radius:8px;display:flex;gap:8px;flex-wrap:wrap;font-size:11px;color:#70818c;margin-bottom:16px}.account-line strong{font-weight:600;color:#536878}.account-line small{margin-left:auto;font-size:10px}.preview-panel{position:relative;border:1px solid #e8ebec;border-radius:12px;overflow:hidden;background:#f3f7f6}.preview-panel iframe{display:block;width:100%;height:360px;border:0}.preview-enlarge{position:absolute;right:14px;bottom:14px;background:#ffffffed;color:#0c756b;padding:8px 12px;border:1px solid #cfdfda;border-radius:8px;font-size:12px}.answer-panel{background:#f4f8f6;border-radius:12px;padding:20px}.answer-header{display:flex;align-items:center;justify-content:space-between;gap:10px;font-size:12px;color:#508476}.answer-panel small{font-size:11px;color:#83968c}.prompt-details{border-top:1px solid #e8eceb;margin-top:20px;padding-top:15px}.prompt-details summary{font-size:12px;color:#0b8475;cursor:pointer}.prompt-details p{font-size:12px;color:#7d8891;white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.8;margin-top:12px}.result-placeholder{display:flex;gap:10px;align-items:center;justify-content:center;background:#f5f8f7;border-radius:12px;min-height:160px;color:#7f908d;font-size:13px;padding:20px}.result-placeholder.failure{color:#b16658;background:#fff8f5}.pulse-dot{width:7px;height:7px;background:#0d9488;border-radius:50%}.quality-empty{text-align:center;border:1px dashed #dbe6e3;border-radius:16px;padding:65px 20px;color:#86958f}.quality-empty>span{font-size:40px;color:#8ab6a6}.quality-empty h3{font-size:17px;margin:12px 0}.quality-empty p{font-size:13px}.pagination{display:flex;justify-content:center;align-items:center;gap:20px;margin-top:28px;font-size:13px;color:#7e8e89}.quality-dialog{width:min(1200px,94vw);max-width:94vw;padding:0;border:1px solid #dce8e3;border-radius:16px;background:white}.quality-dialog::backdrop{background:#112a2bb3}.dialog-content header{display:flex;justify-content:space-between;align-items:center;gap:18px;padding:16px 20px}.dialog-content header h2{font-size:15px;font-weight:600}.dialog-content iframe{width:100%;height:75vh;border:0;display:block}
:global(.dark) .quality-page{color:#dfebe6}:global(.dark) .quality-settings,:global(.dark) .result-card{background:#172524;border-color:#2b3e3a}:global(.dark) .theme-bar,:global(.dark) .prompt-details{border-color:#2b3e3a}:global(.dark) .answer-panel,:global(.dark) .account-line,:global(.dark) .result-placeholder{background:#20322c}:global(.dark) .quality-dialog{background:#172524;color:#dfebe6}:global(.dark) .error-banner{background:#30261d;border-color:#604635;color:#f5ba91}
@media(max-width:900px){.results-heading{align-items:flex-start;flex-direction:column}.results-grid{gap:18px}.result-card{padding:18px}.preview-panel iframe{height:280px}}
@media(max-width:640px){.quality-hero{align-items:flex-start;flex-direction:column;padding-top:8px;gap:16px}.quality-hero h1{font-size:29px}.quality-hero .btn{width:100%;justify-content:space-between}.quality-settings{padding:20px}.connection-grid{grid-template-columns:1fr;gap:16px}.theme-bar button{flex:1;padding:10px}.results-grid{grid-template-columns:1fr}.result-filters{width:100%;flex-wrap:wrap}.result-filters .input{flex:1;width:100px}.results-heading{gap:14px}.result-card h3{font-size:21px}.preview-panel iframe{height:340px}.dialog-content header h2{max-width:60%;overflow-wrap:anywhere}}
</style>
