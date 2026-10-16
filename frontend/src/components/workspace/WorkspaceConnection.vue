<template>
  <div class="connection-fields">
    <div class="key-label-row">
      <label for="workspace-key">{{ t('workspace.apiKey') }}</label>
      <span class="key-status" :class="{ matching: loading, ready: matched && !loading }" role="status" aria-live="polite">
        <span v-if="loading" class="matching-spinner" aria-hidden="true" /><Icon v-else-if="matched" name="check" size="xs" />
        {{ t(loading ? 'workspace.matching' : matched ? 'workspace.matched' : keys.length ? 'workspace.noMatch' : 'workspace.noKey') }}
      </span>
    </div>
    <Select id="workspace-key" :model-value="keyId" :options="keyOptions" :searchable="true" :disabled="loading || busy" :aria-label="t('workspace.apiKey')" :placeholder="t('workspace.selectKey')" @update:model-value="emit('update:keyId', $event)" @change="emit('key-change')">
      <template #footer>
        <button type="button" class="all-keys-button" data-test="show-all-keys" :aria-pressed="showAll" @click.stop="emit('all-keys-change', !showAll)" @keydown.enter.stop @keydown.space.stop>
          <Icon :name="showAll ? 'chevronUp' : 'chevronDown'" size="xs" />{{ t(showAll ? 'workspace.showMatchingKeys' : 'workspace.showAllKeys') }}
        </button>
      </template>
    </Select>
    <p class="field-hint">{{ t('workspace.autoSelectHint') }}</p>
    <div class="label-row">
      <label for="workspace-model">{{ t('workspace.model') }}</label>
      <button type="button" class="text-button" :disabled="loading || busy" :title="t('workspace.refresh')" @click="emit('refresh')"><Icon name="refresh" size="xs" />{{ t('common.refresh') }}</button>
    </div>
    <Select id="workspace-model" :model-value="model" :options="modelOptions" :searchable="true" :creatable="showAll" :disabled="loading || busy || !models.length && !showAll" :aria-label="t('workspace.model')" :placeholder="t('workspace.noModel')" @update:model-value="emit('update:model', $event)" @change="emit('model-change')" />
    <p v-if="matched && tab === 'chat'" class="protocol-hint">{{ t('workspace.protocol') }}<code>{{ /^gemini-/i.test(model) ? 'Gemini · generateContent' : 'OpenAI · chat/completions' }}</code></p>
    <div v-if="!loading && !matched" class="key-guide"><p>{{ t(keys.length ? 'workspace.noModelHint' : 'workspace.noKey') }}</p><button type="button" class="outline-button" data-test="create-key" @click="emit('create-key')"><Icon name="key" size="sm" />{{ t('workspace.createKey') }}</button></div>
    <p v-if="error || videoModelError" class="error-message" role="alert">{{ error || videoModelError }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { modelMatchesTab, workspaceModelsForKey, workspaceSelectableModels, type WorkspaceKey, type WorkspaceTab } from '@/api/workspace'
const props = defineProps<{ keys: WorkspaceKey[]; tab: WorkspaceTab; keyId: string; model: string; loading: boolean; busy: boolean; error: string; showAll: boolean }>()
const emit = defineEmits<{
  (event: 'update:keyId' | 'update:model', value: string): void
  (event: 'key-change' | 'model-change' | 'refresh' | 'create-key'): void
  (event: 'all-keys-change', value: boolean): void
}>()
const { t } = useI18n()
const keyOptions = computed(() => props.keys.filter(key => props.showAll || workspaceModelsForKey(key, props.tab).length > 0).map(key => ({ value: String(key.id), label: `${key.name} · ${key.groupName}` })))
const models = computed(() => {
  const key = props.keys.find(key => String(key.id) === props.keyId)
  return key ? workspaceSelectableModels(key, props.tab, props.showAll) : []
})
const modelOptions = computed(() => models.value.map(model => ({ value: model, label: model })))
const videoModelError = computed(() => props.tab === 'video' && props.model.trim() && !modelMatchesTab(props.model, 'video') ? t('workspace.videoModelUnsupported') : '')
const matched = computed(() => Boolean(props.keyId && props.model.trim()) && !videoModelError.value && (props.showAll || models.value.includes(props.model)))
</script>

<style scoped>
.connection-fields { display: flex; flex-direction: column; gap: .6rem; min-width: 0; color: #243841; }
.key-label-row,.label-row { display: flex; align-items: center; justify-content: space-between; gap: .5rem; }
label { font-size: .73rem; font-weight: 650; }
.key-status { display: inline-flex; align-items: center; gap: .3rem; border-radius: .4rem; padding: .3rem .45rem; color: #70838e; font-size: .65rem; font-weight: 650; }
.key-status.matching { color: #a16207; background: #fef3c7; border: 1px solid #fcd34d; }.key-status.ready { color: #138f87; background: #e7f7f4; }
.matching-spinner { width: .75rem; height: .75rem; border: 2px solid #fcd34d; border-top-color: currentColor; border-radius: 50%; animation: spin .8s linear infinite; }
.field-hint,.key-guide p { margin: 0; color: #70838e; font-size: .68rem; line-height: 1.65; }
.text-button { display: inline-flex; align-items: center; gap: .3rem; color: #138f87; font-size: .7rem; }
.protocol-hint { display: grid; gap: .4rem; color: #70838e; font-size: .7rem; }.protocol-hint code { padding: .65rem; border-radius: .45rem; background: #e7f7f4; color: #138f87; font-size: .65rem; overflow-wrap: anywhere; }
.all-keys-button { display: flex; align-items: center; justify-content: center; gap: .35rem; width: 100%; padding: .7rem; border-top: 1px solid #dce7eb; color: #138f87; font-size: .75rem; background: #f5fbfa; }.all-keys-button:hover { background: #e7f7f4; }
.outline-button { display: inline-flex; align-items: center; justify-content: center; gap: .4rem; width: 100%; border: 1px solid #a6d9d3; border-radius: .5rem; padding: .65rem; color: #138f87; background: #e7f7f4; font-size: .78rem; }
.key-guide { display: grid; gap: .6rem; }.error-message { color: #dc2626; font-size: .75rem; line-height: 1.65; }
.dark .connection-fields { color: #e2e8f0; }
.dark .all-keys-button { background: #163733; color: #75d9cf; border-color: #30434f; }
.dark .field-hint, .dark .key-guide p { color: #97aeb9; }
.dark .key-status.ready, .dark .protocol-hint code, .dark .outline-button { background: #163733; color: #75d9cf; }
.dark .key-status.matching { color: #fcd34d; background: #42331b; border-color: #806324; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
