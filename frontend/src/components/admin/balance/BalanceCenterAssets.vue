<template>
  <section class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
    <button type="button" class="flex w-full items-center justify-between text-sm font-semibold" :aria-expanded="opened" @click="toggle">
      <span>现金余额与订阅资产（人民币）</span><span>{{ opened ? '收起' : '管理余额 / 订阅' }}</span>
    </button>
    <div v-if="opened" class="mt-4 space-y-4">
      <p class="text-xs text-gray-500">上游显示金额按 1:1 记人民币。订阅费用已在充值中记录，此处仅计算剩余价值，不新增充值。自动余额超过 24 小时视为未知；已停用站点请核实后手工填 0。</p>
      <p v-if="loading" class="text-sm">正在读取资产…</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead><tr class="text-left text-gray-500"><th class="p-2">站点</th><th class="p-2">现金余额</th><th class="p-2">订阅剩余价值</th><th class="p-2">合计</th><th class="p-2">操作</th></tr></thead>
          <tbody><tr v-for="asset in assets" :key="asset.site_id" class="border-t border-gray-100 dark:border-dark-700">
            <td class="p-2">{{ asset.site_name }}<small class="block text-gray-500">{{ asset.domain }}</small></td>
            <td class="p-2">{{ money(asset.cash_balance) }}<small v-if="asset.manual_balance != null" class="block text-amber-600">手工余额（不会自动更新）</small></td>
            <td class="p-2">{{ money(asset.subscription_balance) }}<small v-if="asset.subscription_expires_at" class="block text-gray-500">剩余 {{ remainingDays(asset) }} 天</small><!-- 日可用额度用于重置提醒，单独展示以免与人民币资产混淆。 -->
              <small v-if="asset.domain === 'sub.anzhiyu.com' && asset.subscription_auto_sync" class="block text-amber-600">日剩余额度：{{ asset.subscription_daily_remaining_usd == null ? '待同步' : '$' + asset.subscription_daily_remaining_usd.toFixed(4) }}；低于 $1 邮件提醒重置</small><small v-if="asset.subscription_sync_error" class="block text-red-600">{{ asset.subscription_sync_error }}</small></td>
            <td class="p-2">{{ money(asset.total_balance) }}</td>
            <td class="p-2"><button class="btn btn-secondary" @click="edit(asset)">校正 / 订阅</button><button v-if="asset.subscription_auto_sync" class="btn btn-secondary ml-2" :disabled="busy" @click="sync(asset.site_id)">同步订阅</button></td>
          </tr></tbody>
        </table>
      </div>
      <form v-if="draft" class="space-y-3 rounded-lg bg-gray-50 p-4 dark:bg-dark-900" @submit.prevent="save">
        <h3 class="font-medium">{{ draft.site_name }} · 资产配置</h3>
        <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <label class="text-sm">手工现金余额（留空使用自动探测）<input v-model="manualCash" class="input mt-1 w-full" type="number" min="0" step="0.01" /></label>
          <label class="text-sm">已入账订阅费用（元）<input v-model.number="draft.subscription_price" class="input mt-1 w-full" type="number" min="0" step="0.01" required /></label>
          <label class="text-sm">订阅购买天数<input v-model.number="draft.subscription_days" class="input mt-1 w-full" type="number" min="1" max="3660" step="1" required /></label>
          <label class="text-sm">上游订阅 ID<input v-model="subscriptionID" class="input mt-1 w-full" type="number" min="1" step="1" placeholder="订阅页面对应的 ID" /></label>
          <label class="text-sm">到期时间（可手工校正）<input v-model="expiresAt" class="input mt-1 w-full" type="datetime-local" step="1" /></label>
          <label class="flex items-center gap-2 text-sm"><input v-model="draft.subscription_auto_sync" type="checkbox" :disabled="draft.domain !== 'sub.anzhiyu.com'" />自动同步鱼鱼到期时间与日额度</label>
        </div>
        <p class="text-xs text-gray-500">每日成本＝订阅费用÷购买天数；剩余价值＝每日成本×剩余天数（不足一天按一天，最多为购买天数）。手工重置减少有效期后同步即可，无需重复记费用。填写手工到期时间时请关闭自动同步，否则下一次成功同步会覆盖。</p>
        <p v-if="draft.subscription_price > 0" class="text-sm">每日摊销：{{ money(draft.subscription_price / draft.subscription_days) }}。鱼鱼 808 元 / 30 天、剩余 20 天时为 ¥538.67。</p>
        <div class="flex gap-2"><button class="btn btn-primary" :disabled="busy">保存资产配置</button><button type="button" class="btn btn-secondary" :disabled="busy" @click="draft = null">取消</button></div>
      </form>
    </div>
  </section>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { balanceCenterAPI, type BalanceCenterAsset } from '@/api/admin/balanceCenter'
import { useAppStore } from '@/stores/app'

const app = useAppStore()
const opened = ref(false)
const loading = ref(false)
const busy = ref(false)
const error = ref('')
const assets = ref<BalanceCenterAsset[]>([])
const draft = ref<BalanceCenterAsset | null>(null)
const manualCash = ref('')
const subscriptionID = ref('')
const expiresAt = ref('')
function money(value: number | null) { return value == null ? '待核对' : `¥${value.toFixed(2)}` }
function remainingDays(a: BalanceCenterAsset) { return a.subscription_expires_at ? Math.min(a.subscription_days, Math.max(0, Math.ceil((Date.parse(a.subscription_expires_at) - Date.now()) / 86400000))) : 0 }
function message(e: unknown) { return (e as { response?: { data?: { message?: string } } })?.response?.data?.message || '操作失败，请稍后重试' }
async function load() {
  loading.value = true
  try { assets.value = await balanceCenterAPI.assets(); error.value = '' } catch (e) { error.value = message(e) } finally { loading.value = false }
}
async function toggle() { opened.value = !opened.value; if (opened.value) await load() }
function edit(a: BalanceCenterAsset) {
  draft.value = { ...a }
  manualCash.value = a.manual_balance == null ? '' : String(a.manual_balance)
  subscriptionID.value = a.subscription_id == null ? '' : String(a.subscription_id)
  // datetime-local 使用浏览器本地时间，保存时转成带时区的 ISO 时间。
  if (a.subscription_expires_at) { const d = new Date(a.subscription_expires_at); expiresAt.value = new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 19) } else { expiresAt.value = '' }
}
async function save() {
  if (!draft.value) return
  busy.value = true
  try {
    const payload = { ...draft.value, manual_balance: manualCash.value === '' ? null : Number(manualCash.value), subscription_id: subscriptionID.value === '' ? null : Number(subscriptionID.value), subscription_expires_at: expiresAt.value ? new Date(expiresAt.value).toISOString() : null }
    await balanceCenterAPI.saveAsset(payload)
    draft.value = null
    app.showSuccess('资产配置已保存，未新增充值记录')
    if (payload.subscription_auto_sync) await balanceCenterAPI.syncSubscription(payload.site_id)
    await load()
  } catch (e) { error.value = message(e); await load(); error.value = message(e) } finally { busy.value = false }
}
async function sync(id: number) {
  busy.value = true
  try { await balanceCenterAPI.syncSubscription(id); app.showSuccess('订阅到期时间与日额度已同步'); await load() } catch (e) { await load(); error.value = message(e) } finally { busy.value = false }
}
</script>
