<template>
  <span v-if="!eligible" class="text-gray-400">-</span>
  <span
    v-else-if="displayAmount"
    data-test="upstream-account-balance"
    class="font-mono text-sm font-semibold text-gray-900 dark:text-white"
  >
    {{ displayAmount }}
  </span>
  <span
    v-else
    data-test="upstream-account-balance-state"
    class="text-xs font-medium"
    :class="stateClass"
    :title="snapshot?.balance?.last_error || snapshot?.last_error || ''"
  >
    {{ stateLabel }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'

const props = defineProps<{ account: Account }>()
const { t } = useI18n()

const eligible = computed(() => props.account.platform === 'openai' && props.account.type === 'apikey')
const snapshot = computed(() => props.account.extra?.upstream_billing_probe)

const displayAmount = computed(() => {
  const balance = snapshot.value?.balance
  const amount = Number(balance?.amount)
  if (balance?.status !== 'ok' || !Number.isFinite(amount)) return ''
  return `${amount.toFixed(2)} ${balance.unit || 'USD'}`
})

const stateLabel = computed(() => {
  if (!snapshot.value) return t('admin.accounts.upstreamBalance.notQueried')
  if (snapshot.value.last_error === 'missing_web_login_credentials') {
    return t('admin.accounts.upstreamBalance.notConfigured')
  }
  if (snapshot.value.balance?.status === 'failed') return t('admin.accounts.upstreamBalance.failed')
  if (snapshot.value.status === 'unsupported') return t('admin.accounts.upstreamBalance.unsupported')
  if (!snapshot.value.balance) return t('admin.accounts.upstreamBalance.notConfigured')
  return t('admin.accounts.upstreamBalance.notQueried')
})

const stateClass = computed(() => {
  if (snapshot.value?.last_error === 'missing_web_login_credentials' || !snapshot.value?.balance) {
    return 'text-amber-600 dark:text-amber-400'
  }
  if (snapshot.value.balance.status === 'failed') return 'text-red-600 dark:text-red-400'
  return 'text-gray-500 dark:text-gray-400'
})
</script>
