<template>
  <section class="space-y-3 rounded-xl border border-amber-200 bg-white p-4 dark:border-amber-900 dark:bg-dark-800" aria-labelledby="gemini-review-title" :aria-busy="loading || saving">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 id="gemini-review-title" class="text-sm font-semibold text-gray-900 dark:text-white">Gemini Interactions 账务待核对</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">仅列出未结算的待核对请求。冻结金额为本站美元余额；结算按请求原始定价计算，不支持手工填费用。</p>
      </div>
      <button type="button" data-test="review-refresh" class="btn btn-secondary" :disabled="loading || saving || !!selected" @click="load()">刷新</button>
    </div>
    <p v-if="notice" role="status" class="text-sm text-amber-700 dark:text-amber-300">{{ notice }}</p>
    <p v-if="loadError" role="alert" class="text-sm text-red-600">{{ loadError }}</p>
    <p v-if="loading" class="text-sm text-gray-500">正在读取待核对记录…</p>
    <div class="overflow-x-auto">
      <table class="w-full min-w-[860px] text-sm">
        <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-900 dark:text-gray-400">
          <tr><th class="p-2">请求 / 创建时间</th><th class="p-2">关联信息</th><th class="p-2">模型 / 状态</th><th class="p-2">待核对原因</th><th class="p-2 text-right">冻结金额（USD）</th><th class="p-2">操作</th></tr>
        </thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="item in result.items" :key="item.id" class="align-top text-gray-700 dark:text-gray-200">
            <td class="max-w-52 break-all p-2"><span class="font-mono text-xs">{{ item.public_id }}</span><small class="mt-1 block text-gray-500">{{ formatDateTime(item.created_at) }}</small></td>
            <td class="whitespace-nowrap p-2"><div>用户 #{{ item.user_id }} · 密钥 #{{ item.api_key_id }}</div><small class="block text-gray-500">分组 {{ identifier(item.group_id) }} · 账号 {{ identifier(item.account_id) }}</small></td>
            <td class="max-w-48 break-words p-2">{{ item.model }}<small class="block text-amber-700 dark:text-amber-300">{{ awaitingSettlement(item) ? '等待自动结算' : '待人工核对' }}</small></td>
            <td class="max-w-60 p-2">{{ reviewReason(item.review_reason) }}</td>
            <td class="whitespace-nowrap p-2 text-right font-mono">${{ item.hold_amount.toFixed(6) }}</td>
            <td class="whitespace-nowrap p-2">
              <button type="button" :data-test="`review-settle-${item.id}`" class="btn btn-secondary" :disabled="loading || saving || !!selected || awaitingSettlement(item)" @click="open(item, 'settle')">核实用量结算</button>
              <button type="button" :data-test="`review-release-${item.id}`" class="btn btn-ghost ml-1" :disabled="loading || saving || !!selected || awaitingSettlement(item)" @click="open(item, 'release')">核实后解冻</button>
            </td>
          </tr>
          <tr v-if="!loading && !loadError && result.items.length === 0"><td colspan="6" class="p-6 text-center text-gray-500">暂无待核对记录</td></tr>
        </tbody>
      </table>
    </div>
    <nav class="flex flex-wrap items-center justify-end gap-3 text-sm" aria-label="待核对记录分页">
      <span>共 {{ result.total }} 条 · 第 {{ currentPage }} / {{ totalPages }} 页</span>
      <button type="button" data-test="review-previous" class="btn btn-secondary" :disabled="loading || saving || !!selected || currentPage <= 1" @click="load(currentPage - 1)">上一页</button>
      <button type="button" data-test="review-next" class="btn btn-secondary" :disabled="loading || saving || !!selected || currentPage >= totalPages" @click="load(currentPage + 1)">下一页</button>
    </nav>

    <BaseDialog :show="!!selected" :title="action === 'settle' ? '确认人工核实用量并结算' : '确认无需收费并释放冻结金额'" :show-close-button="!saving" :close-on-escape="!saving" @close="close">
      <form v-if="selected" data-test="review-form" class="space-y-4" @submit.prevent="submit">
        <p class="break-all text-sm text-gray-600 dark:text-gray-300">本站请求 {{ selected.public_id }} · 冻结 ${{ selected.hold_amount.toFixed(6) }}</p>
        <p v-if="action === 'settle'" class="text-sm text-amber-700 dark:text-amber-300">请先核实上游最终状态及实际用量。确认后按请求原始定价结算，不以冻结金额代替实际费用。</p>
        <p v-else class="text-sm text-amber-700 dark:text-amber-300">仅在已确认无需收费时解冻。已有权威用量或已扣费的请求不能通过解冻覆盖。</p>
        <p v-if="action === 'settle'" class="text-xs text-gray-500 dark:text-gray-400">Google 原始 total_input_tokens 需减去 total_cached_tokens 后填写非缓存输入；total_output_tokens 需加上 total_thought_tokens 后填写总输出。图片输出已包含在总输出内，不要再次相加，避免重复计费。</p>
        <fieldset class="space-y-4" :disabled="saving">
          <div v-if="action === 'settle'" class="grid gap-3 sm:grid-cols-2">
            <label class="text-sm">非缓存输入 Token<input v-model="usage.input_tokens" data-test="review-input-tokens" class="input mt-1 w-full" type="number" min="0" step="1" required /></label>
            <label class="text-sm">总输出 Token（含思考）<input v-model="usage.output_tokens" data-test="review-output-tokens" class="input mt-1 w-full" type="number" min="0" step="1" required /></label>
            <label class="text-sm">缓存读取 Token<input v-model="usage.cache_read_input_tokens" data-test="review-cache-tokens" class="input mt-1 w-full" type="number" min="0" step="1" required /></label>
            <label class="text-sm">图片输出 Token（总输出子集）<input v-model="usage.image_output_tokens" data-test="review-image-tokens" class="input mt-1 w-full" type="number" min="0" :max="usage.output_tokens" step="1" required /></label>
          </div>
          <label class="block text-sm">核对理由（5–500 字，勿填凭据或上游标识）<textarea v-model="reason" data-test="review-reason" class="input mt-1 w-full" rows="3" required /></label>
        </fieldset>
        <p v-if="operationError" role="alert" class="text-sm text-red-600">{{ operationError }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" data-test="review-cancel" class="btn btn-secondary" :disabled="saving" @click="close">取消</button>
          <button type="submit" data-test="review-confirm" class="btn btn-primary" :disabled="saving">{{ saving ? '正在保存…' : action === 'settle' ? '确认按原价结算' : '已确认无需收费，解冻' }}</button>
        </div>
      </form>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { geminiInteractionsAPI, type GeminiInteractionResolution, type GeminiInteractionReviewItem, type GeminiInteractionReviewPage } from '@/api/admin/geminiInteractions'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { formatDateTime } from '@/utils/format'

const pageSize = 20
const currentPage = ref(1)
const result = ref<GeminiInteractionReviewPage>({ items: [], total: 0, page: 1, page_size: pageSize })
const totalPages = computed(() => Math.max(1, Math.ceil(result.value.total / pageSize)))
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const operationError = ref('')
const notice = ref('')
const selected = ref<GeminiInteractionReviewItem | null>(null)
const action = ref<'settle' | 'release'>('settle')
const reason = ref('')
const usage = reactive({ input_tokens: '', output_tokens: '', cache_read_input_tokens: '', image_output_tokens: '' })

function identifier(value: number | null) { return value == null ? '—' : `#${value}` }
function awaitingSettlement(item: GeminiInteractionReviewItem) { return item.review_reason === 'manual_usage_pending' && item.usage != null }
function reviewReason(value: string) {
  switch (value) {
    case 'create_result_unknown': return '创建结果不确定，需核实是否实际执行'
    case 'terminal_usage_missing': return '请求已结束但缺少用量，需核实实际计费'
    case 'manual_usage_pending': return '已人工核实用量，等待结算重试'
    default: return '请人工核实上游最终状态及实际用量'
  }
}

async function load(page = currentPage.value) {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    let response = await geminiInteractionsAPI.list({ page, page_size: pageSize })
    const lastPage = Math.max(1, Math.ceil(response.total / pageSize))
    // 最后一条处理完成后回到仍有记录的页，避免留在空的越界页。
    if (page > lastPage) response = await geminiInteractionsAPI.list({ page: lastPage, page_size: pageSize })
    result.value = response
    currentPage.value = response.page
  } catch {
    loadError.value = '读取待核对记录失败，请刷新重试'
  } finally {
    loading.value = false
  }
}

function open(item: GeminiInteractionReviewItem, nextAction: 'settle' | 'release') {
  if (saving.value || selected.value || awaitingSettlement(item)) return
  selected.value = item
  action.value = nextAction
  reason.value = ''
  usage.input_tokens = ''
  usage.output_tokens = ''
  usage.cache_read_input_tokens = ''
  usage.image_output_tokens = ''
  operationError.value = ''
  notice.value = ''
}

function close() {
  if (!saving.value) selected.value = null
}

async function submit() {
  if (!selected.value || saving.value) return
  const reviewReason = reason.value.trim()
  const reasonLength = Array.from(reviewReason).length
  if (reasonLength < 5 || reasonLength > 500) {
    operationError.value = '请填写 5–500 字的核对理由'
    return
  }
  let payload: GeminiInteractionResolution
  if (action.value === 'settle') {
    if (Object.values(usage).some(value => value === '' || !Number.isSafeInteger(Number(value)) || Number(value) < 0)) {
      operationError.value = '请填写已核实的非负整数 Token 用量，包含缓存读取和图片输出用量'
      return
    }
    if (Number(usage.image_output_tokens) > Number(usage.output_tokens)) {
      operationError.value = '图片输出 Token 不能超过总输出 Token'
      return
    }
    if (Object.values(usage).every(value => Number(value) === 0)) {
      operationError.value = '用量不能全部为零；确认无需收费时请取消并选择解冻'
      return
    }
    payload = { action: 'settle', reason: reviewReason, usage: { input_tokens: Number(usage.input_tokens), output_tokens: Number(usage.output_tokens), cache_read_input_tokens: Number(usage.cache_read_input_tokens), image_output_tokens: Number(usage.image_output_tokens) } }
  } else {
    payload = { action: 'release', reason: reviewReason }
  }
  saving.value = true
  operationError.value = ''
  try {
    const response = await geminiInteractionsAPI.resolve(selected.value.id, payload)
    notice.value = !response.settled ? '核对记录已保存，结算等待重试' : action.value === 'settle' ? '已按原始定价完成结算' : '冻结金额已释放'
    selected.value = null
    await load()
  } catch (error) {
    // 不回显服务端原始错误，避免上游标识或凭据出现在管理页面。
    operationError.value = (error as { status?: number })?.status === 409
      ? '记录已变化或已有计费用量，请重新核实；核对输入已保留'
      : '操作失败，请稍后重试；核对输入已保留'
  } finally {
    saving.value = false
  }
}

onMounted(() => load())
</script>
