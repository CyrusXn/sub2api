<template>
  <AppLayout>
    <main class="mx-auto w-full max-w-[1680px] space-y-5 px-4 py-5 sm:px-6">
      <section class="border-b border-gray-200 pb-4 dark:border-dark-700">
        <div class="flex flex-col gap-4 xl:flex-row xl:items-end xl:justify-between">
          <div>
            <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.balanceCenter.title') }}</h1>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.balanceCenter.description') }}</p>
          </div>
          <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
            <label v-for="item in settingToggles" :key="item.key" class="flex items-center justify-between gap-3 text-sm text-gray-700 dark:text-gray-200">
              <span>{{ item.label }}</span><Toggle v-model="settings[item.key]" />
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-200">
              <span class="whitespace-nowrap">{{ t('admin.balanceCenter.threshold') }}</span>
              <input v-model.number="settings.low_balance_threshold" type="number" min="0" step="0.01" class="input w-24" />
            </label>
            <button class="btn btn-primary" :disabled="saving" data-test="save-settings" @click="saveSettings">
              <Icon name="check" size="sm" />{{ t('admin.balanceCenter.saveSettings') }}
            </button>
          </div>
        </div>
      </section>

      <nav class="flex gap-1 overflow-x-auto border-b border-gray-200 dark:border-dark-700" aria-label="Balance center views">
        <button v-for="tab in tabs" :key="tab" class="whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium"
          :class="activeTab === tab ? 'border-primary-500 text-primary-600 dark:text-primary-400' : 'border-transparent text-gray-500 hover:text-gray-800 dark:text-gray-400'"
          @click="selectTab(tab)">{{ t(`admin.balanceCenter.tabs.${tab}`) }}</button>
      </nav>

      <section v-if="activeTab === 'overview'" class="space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="text-sm text-gray-500">{{ overview.length }} {{ t('admin.balanceCenter.account') }}</div>
          <div class="flex items-center gap-2">
            <input v-model="probeAccountIds" class="input w-64" :placeholder="`${t('admin.balanceCenter.accountId')} (1,2,3)`" />
            <button class="btn btn-secondary" :disabled="busy" data-test="probe-accounts" @click="probeAccounts"><Icon name="refresh" size="sm" />{{ t('admin.balanceCenter.probeSelected') }}</button>
          </div>
        </div>
        <DataTable :columns="overviewColumns" :data="overview" :loading="loading" row-key="account_id">
          <template #cell-status="{ value }"><StatusBadge :value="String(value)" /></template>
          <template #cell-balance="{ value }">{{ money(value) }}</template>
          <template #cell-converted_balance="{ value }"><strong>{{ money(value) }}</strong></template>
          <template #cell-probed_at="{ value }">{{ date(value) }}</template>
          <template #cell-last_used_at="{ value }">{{ date(value) }}</template>
        </DataTable>
      </section>

      <section v-else-if="activeTab === 'snapshots'" class="space-y-3">
        <FilterBar :sites="sites" :model="filters" :show-dates="true" @apply="loadSnapshots" />
        <DataTable :columns="snapshotColumns" :data="snapshotPage.items" :loading="loading" row-key="id">
          <template #cell-status="{ value }"><StatusBadge :value="String(value)" /></template>
          <template #cell-balance="{ value }">{{ money(value) }}</template><template #cell-converted_balance="{ value }">{{ money(value) }}</template>
          <template #cell-probed_at="{ value }">{{ date(value) }}</template>
        </DataTable>
        <Pagination v-if="snapshotPage.total" :page="snapshotPage.page" :page-size="snapshotPage.page_size" :total="snapshotPage.total" @update:page="p => changePage(snapshotPage, p, loadSnapshots)" @update:page-size="s => changeSize(snapshotPage, s, loadSnapshots)" />
      </section>

      <section v-else-if="activeTab === 'recharge'" class="space-y-3">
        <form class="grid gap-3 border-b border-gray-200 pb-4 dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-6" @submit.prevent="addRecharge">
          <input v-model="rechargeForm.source_key" class="input" required placeholder="source_key" />
          <input v-model.number="rechargeForm.amount" type="number" min="0.01" step="0.01" class="input" required :placeholder="t('admin.balanceCenter.amount')" />
          <input v-model="rechargeForm.currency" class="input" placeholder="CNY" />
          <input v-model="rechargeForm.occurred_at" type="datetime-local" class="input" required />
          <input v-model="rechargeForm.note" class="input" :placeholder="t('admin.balanceCenter.note')" />
          <button class="btn btn-primary" :disabled="busy"><Icon name="plus" size="sm" />{{ t('admin.balanceCenter.addRecharge') }}</button>
        </form>
        <DataTable :columns="rechargeColumns" :data="rechargePage.items" :loading="loading" row-key="id">
          <template #cell-amount="{ row }">{{ money(row.amount) }} {{ row.currency }}</template>
          <template #cell-occurred_at="{ value }">{{ date(value) }}</template>
          <template #cell-actions="{ row }"><button class="btn btn-ghost text-red-600" :title="t('admin.balanceCenter.delete')" @click="removeRecharge(row.id)"><Icon name="trash" size="sm" /></button></template>
        </DataTable>
        <Pagination v-if="rechargePage.total" :page="rechargePage.page" :page-size="rechargePage.page_size" :total="rechargePage.total" @update:page="p => changePage(rechargePage, p, loadRecharge)" @update:page-size="s => changeSize(rechargePage, s, loadRecharge)" />
      </section>

      <section v-else-if="activeTab === 'manual'" class="space-y-3">
        <div class="flex justify-end gap-2"><button class="btn btn-secondary" @click="addManualRow"><Icon name="plus" size="sm" />{{ t('admin.balanceCenter.addRow') }}</button><button class="btn btn-primary" :disabled="saving" data-test="save-manual" @click="saveManualRows"><Icon name="check" size="sm" />{{ t('admin.balanceCenter.saveManual') }}</button></div>
        <div class="overflow-x-auto border-y border-gray-200 dark:border-dark-700"><table class="w-full min-w-[760px] text-sm"><thead class="bg-gray-50 text-left text-gray-500 dark:bg-dark-800"><tr><th class="p-3">{{ t('admin.balanceCenter.siteName') }}</th><th class="p-3">{{ t('admin.balanceCenter.expression') }}</th><th class="p-3">{{ t('admin.balanceCenter.amount') }}</th><th class="w-16 p-3"></th></tr></thead><tbody><tr v-for="(row,index) in manualRows" :key="`${row.source_key}-${index}`" class="border-t border-gray-100 dark:border-dark-700"><td class="p-2"><input v-model="row.label" class="input" /></td><td class="p-2"><input v-model="row.expression" class="input font-mono" /></td><td class="p-2"><input v-model.number="row.amount" type="number" min="0" step="0.01" class="input" /></td><td class="p-2"><button class="btn btn-ghost text-red-600" @click="manualRows.splice(index,1)"><Icon name="trash" size="sm" /></button></td></tr></tbody></table></div>
      </section>

      <section v-else-if="activeTab === 'reconciliation'" class="space-y-4">
        <div class="flex flex-wrap gap-2"><button class="btn btn-secondary" :disabled="busy" @click="syncAutomatic"><Icon name="refresh" size="sm" />{{ t('admin.balanceCenter.automaticSync') }}</button><input v-model="liandongCurl" class="input min-w-[320px] flex-1" :placeholder="t('admin.balanceCenter.liandongCurl')" /><button class="btn btn-secondary" :disabled="busy || !liandongCurl" @click="saveLiandongSession">{{ t('admin.balanceCenter.saveSession') }}</button><button class="btn btn-secondary" :disabled="busy" @click="syncLiandong">{{ t('admin.balanceCenter.liandongSync') }}</button></div>
        <form class="grid gap-3 border-b border-gray-200 pb-4 dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-6" @submit.prevent="addReconciliation"><input v-model="reconciliationForm.source_key" class="input" required placeholder="source_key" /><input v-model.number="reconciliationForm.expected_amount" type="number" step="0.01" class="input" :placeholder="t('admin.balanceCenter.expected')" /><input v-model.number="reconciliationForm.actual_amount" type="number" step="0.01" class="input" :placeholder="t('admin.balanceCenter.actual')" /><input v-model="reconciliationForm.status" class="input" placeholder="matched" /><span></span><button class="btn btn-primary"><Icon name="plus" size="sm" />{{ t('admin.balanceCenter.addReconciliation') }}</button></form>
        <DataTable :columns="reconciliationColumns" :data="reconciliationPage.items" :loading="loading" row-key="id"><template #cell-created_at="{ value }">{{ date(value) }}</template></DataTable>
        <Pagination v-if="reconciliationPage.total" :page="reconciliationPage.page" :page-size="reconciliationPage.page_size" :total="reconciliationPage.total" @update:page="p => changePage(reconciliationPage, p, loadReconciliations)" @update:page-size="s => changeSize(reconciliationPage, s, loadReconciliations)" />
      </section>

      <section v-else class="space-y-3">
        <FilterBar :sites="sites" :model="filters" @apply="loadAlerts" />
        <DataTable :columns="alertColumns" :data="alertPage.items" :loading="loading" row-key="id"><template #cell-status="{ value }"><StatusBadge :value="String(value)" /></template><template #cell-attempted_at="{ value }">{{ date(value) }}</template></DataTable>
        <Pagination v-if="alertPage.total" :page="alertPage.page" :page-size="alertPage.page_size" :total="alertPage.total" @update:page="p => changePage(alertPage, p, loadAlerts)" @update:page-size="s => changeSize(alertPage, s, loadAlerts)" />
      </section>
    </main>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { BalanceCenterAlert, BalanceCenterListParams, BalanceCenterManualRow, BalanceCenterOverviewItem, BalanceCenterPage, BalanceCenterRechargeEvent, BalanceCenterReconciliation, BalanceCenterSettings, BalanceCenterSite, BalanceCenterSnapshot } from '@/api/admin/balanceCenter'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n(); const app = useAppStore()
type Tab = 'overview'|'snapshots'|'recharge'|'manual'|'reconciliation'|'alerts'
const tabs: Tab[] = ['overview','snapshots','recharge','manual','reconciliation','alerts']; const activeTab = ref<Tab>('overview')
const loading=ref(false), saving=ref(false), busy=ref(false), probeAccountIds=ref(''), liandongCurl=ref('')
const settings=reactive<BalanceCenterSettings>({enabled:false,event_probe_enabled:false,email_enabled:false,low_balance_threshold:5})
const settingToggles=computed(() => ([{key:'enabled' as const,label:t('admin.balanceCenter.enabled')},{key:'event_probe_enabled' as const,label:t('admin.balanceCenter.eventProbe')},{key:'email_enabled' as const,label:t('admin.balanceCenter.email')}]))
const overview=ref<BalanceCenterOverviewItem[]>([]), sites=ref<BalanceCenterSite[]>([]), manualRows=ref<BalanceCenterManualRow[]>([])
const emptyPage=<T>():BalanceCenterPage<T>=>({items:[],total:0,page:1,page_size:20}); const snapshotPage=reactive(emptyPage<BalanceCenterSnapshot>()), rechargePage=reactive(emptyPage<BalanceCenterRechargeEvent>()), reconciliationPage=reactive(emptyPage<BalanceCenterReconciliation>()), alertPage=reactive(emptyPage<BalanceCenterAlert>())
const filters=reactive<BalanceCenterListParams>({page:1,page_size:20})
const rechargeForm=reactive<BalanceCenterRechargeEvent>({source:'manual',source_key:'',amount:0,currency:'CNY',occurred_at:'',note:''})
const reconciliationForm=reactive<BalanceCenterReconciliation>({source:'manual',source_key:'',status:'matched'})
const col=(key:string,width=150):Column=>({key,label:t(`admin.balanceCenter.${key.replace(/_([a-z])/g,(_,c)=>c.toUpperCase())}`),width})
const overviewColumns=computed<Column[]>(()=>[col('siteName',180),col('domain',200),col('account',160),col('status',100),col('balance',110),col('convertedBalance',130),col('multiplier',90),col('probedAt',170),col('lastUsedAt',170),col('source',120)].map((c,i)=>({...c,key:['site_name','normalized_domain','account_name','status','balance','converted_balance','rate_multiplier','probed_at','last_used_at','source'][i]})))
const snapshotColumns=computed<Column[]>(()=>overviewColumns.value.filter(c=>c.key!=='last_used_at'))
const rechargeColumns=computed<Column[]>(()=>[col('source',120),{...col('site',90),key:'site_id'},col('amount',130),{...col('rechargeAt',180),key:'occurred_at'},col('note',260),col('actions',70)])
const reconciliationColumns=computed<Column[]>(()=>[col('source',120),{...col('expected',120),key:'expected_amount'},{...col('actual',120),key:'actual_amount'},{...col('difference',120),key:'difference_amount'},col('status',110),{...col('createdAt',180),key:'created_at'}])
const alertColumns=computed<Column[]>(()=>[{...col('site',80),key:'site_id'},{...col('accountId',90),key:'account_id'},{...col('alertType',150),key:'alert_type'},{...col('recipient',220),key:'recipient_email'},{...col('oldValue',100),key:'old_value'},{...col('newValue',100),key:'new_value'},col('status',100),{...col('attemptedAt',180),key:'attempted_at'}])
const date=(v?:string)=>v?formatDateTime(v):'-'; const money=(v?:number)=>v==null?'-':Number(v).toFixed(2)
const notifyError=(e:unknown)=>app.showError(e instanceof Error?e.message:String((e as {message?:string})?.message||e))
async function run(fn:()=>Promise<void>){loading.value=true;try{await fn()}catch(e){notifyError(e)}finally{loading.value=false}}
async function initial(){await run(async()=>{const [s,st,o]=await Promise.all([adminAPI.balanceCenter.getSettings(),adminAPI.balanceCenter.sites(),adminAPI.balanceCenter.overview()]);Object.assign(settings,s);sites.value=st;overview.value=o})}
async function saveSettings(){saving.value=true;try{await adminAPI.balanceCenter.updateSettings({...settings});app.showSuccess(t('admin.balanceCenter.saved'))}catch(e){notifyError(e)}finally{saving.value=false}}
async function probeAccounts(){const ids=probeAccountIds.value.split(',').map(Number).filter(n=>Number.isInteger(n)&&n>0);if(!ids.length)return;busy.value=true;try{await adminAPI.balanceCenter.probeAccounts(ids);app.showSuccess(t('admin.balanceCenter.probeComplete'));await loadOverview()}catch(e){notifyError(e)}finally{busy.value=false}}
async function loadOverview(){await run(async()=>{overview.value=await adminAPI.balanceCenter.overview()})}
const requestParams=(page:BalanceCenterPage<unknown>)=>({...filters,page:page.page,page_size:page.page_size,start_time:filters.start_time?new Date(filters.start_time).toISOString():undefined,end_time:filters.end_time?new Date(filters.end_time).toISOString():undefined})
async function loadSnapshots(){await run(async()=>{Object.assign(snapshotPage,await adminAPI.balanceCenter.snapshots(requestParams(snapshotPage)))})}
async function loadRecharge(){await run(async()=>{Object.assign(rechargePage,await adminAPI.balanceCenter.rechargeEvents(requestParams(rechargePage)))})}
async function loadReconciliations(){await run(async()=>{Object.assign(reconciliationPage,await adminAPI.balanceCenter.reconciliations(requestParams(reconciliationPage)))})}
async function loadAlerts(){await run(async()=>{Object.assign(alertPage,await adminAPI.balanceCenter.alerts(requestParams(alertPage)))})}
async function loadManual(){await run(async()=>{manualRows.value=await adminAPI.balanceCenter.manualRows()})}
function selectTab(tab:Tab){activeTab.value=tab;if(tab==='overview')loadOverview();else if(tab==='snapshots')loadSnapshots();else if(tab==='recharge')loadRecharge();else if(tab==='manual')loadManual();else if(tab==='reconciliation')loadReconciliations();else loadAlerts()}
function addManualRow(){manualRows.value.push({source:'manual',source_key:`manual-${Date.now()}`,label:'',expression:'',amount:0,sort_order:manualRows.value.length})}
async function saveManualRows(){saving.value=true;try{await adminAPI.balanceCenter.replaceManualRows(manualRows.value.map((r,i)=>({...r,source_key:r.source_key||r.label,sort_order:i})));app.showSuccess(t('admin.balanceCenter.saved'))}catch(e){notifyError(e)}finally{saving.value=false}}
async function addRecharge(){busy.value=true;try{await adminAPI.balanceCenter.createRechargeEvent({...rechargeForm,occurred_at:new Date(rechargeForm.occurred_at).toISOString()});Object.assign(rechargeForm,{source_key:'',amount:0,occurred_at:'',note:''});await loadRecharge()}catch(e){notifyError(e)}finally{busy.value=false}}
async function removeRecharge(id?:number){if(!id)return;await adminAPI.balanceCenter.deleteRechargeEvent(id);await loadRecharge()}
async function addReconciliation(){const actual=reconciliationForm.actual_amount, expected=reconciliationForm.expected_amount;await adminAPI.balanceCenter.createReconciliation({...reconciliationForm,difference_amount:actual!=null&&expected!=null?actual-expected:undefined});Object.assign(reconciliationForm,{source_key:'',expected_amount:undefined,actual_amount:undefined,status:'matched'});await loadReconciliations()}
async function externalAction(fn:()=>Promise<unknown>){busy.value=true;try{await fn();app.showSuccess(t('admin.balanceCenter.operationComplete'))}catch(e){notifyError(e)}finally{busy.value=false}}
const syncAutomatic=()=>externalAction(()=>adminAPI.balanceCenter.syncAutomaticRecords()), saveLiandongSession=()=>externalAction(()=>adminAPI.balanceCenter.saveLiandongSession(liandongCurl.value)), syncLiandong=()=>externalAction(()=>adminAPI.balanceCenter.syncLiandong())
function changePage(page:BalanceCenterPage<unknown>,value:number,load:()=>Promise<void>){page.page=value;load()} function changeSize(page:BalanceCenterPage<unknown>,value:number,load:()=>Promise<void>){page.page_size=value;page.page=1;load()}

const StatusBadge=defineComponent({props:{value:{type:String,required:true}},setup(p){return()=>h('span',{class:['inline-flex rounded px-2 py-1 text-xs font-medium',p.value==='ok'||p.value==='accepted'||p.value==='matched'?'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300':'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300']},p.value)}})
const FilterBar=defineComponent({props:{sites:{type:Array as ()=>BalanceCenterSite[],required:true},model:{type:Object as ()=>BalanceCenterListParams,required:true},showDates:Boolean},emits:['apply'],setup(p,{emit}){return()=>h('div',{class:'grid gap-3 border-b border-gray-200 pb-4 dark:border-dark-700 sm:grid-cols-2 lg:grid-cols-6'},[h('select',{class:'input',value:p.model.site_id||'',onChange:(e:Event)=>p.model.site_id=Number((e.target as HTMLSelectElement).value)||undefined},[h('option',{value:''},t('admin.balanceCenter.all')),...p.sites.map(s=>h('option',{value:s.id},s.name))]),h('input',{class:'input',type:'number',placeholder:t('admin.balanceCenter.accountId'),value:p.model.account_id||'',onInput:(e:Event)=>p.model.account_id=Number((e.target as HTMLInputElement).value)||undefined}),h('input',{class:'input',placeholder:t('admin.balanceCenter.status'),value:p.model.status||'',onInput:(e:Event)=>p.model.status=(e.target as HTMLInputElement).value||undefined}),...(p.showDates?[h('input',{class:'input',type:'datetime-local',value:p.model.start_time||'',onInput:(e:Event)=>p.model.start_time=(e.target as HTMLInputElement).value}),h('input',{class:'input',type:'datetime-local',value:p.model.end_time||'',onInput:(e:Event)=>p.model.end_time=(e.target as HTMLInputElement).value})]:[h('span')]),h('button',{class:'btn btn-secondary',onClick:()=>emit('apply')},t('admin.balanceCenter.filters'))])}})
onMounted(initial)
</script>
