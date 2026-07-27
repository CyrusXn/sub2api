<template>
  <AppLayout>
    <!-- 桌面端直接锚定在顶部栏下方，避免负边距与视口高度换算产生外层滚动条。 -->
    <div
      class="-m-4 min-h-[calc(100vh-4rem)] overflow-hidden bg-white dark:bg-dark-900 md:-m-6 lg:-m-8 xl:absolute xl:inset-x-0 xl:bottom-0 xl:top-16 xl:m-0 xl:h-auto xl:min-h-0"
    >
      <div
        class="grid min-h-0 xl:h-full"
        :class="historyCollapsed
          ? 'xl:grid-cols-[minmax(390px,440px)_minmax(280px,1fr)_56px]'
          : 'xl:grid-cols-[minmax(390px,440px)_minmax(280px,1fr)_minmax(230px,280px)]'"
      >
        <aside class="flex min-h-0 flex-col border-b border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900 xl:border-b-0 xl:border-r">
          <div class="flex-shrink-0 space-y-3 border-b border-gray-200 p-4 dark:border-dark-700">
            <div class="flex items-center justify-between gap-3">
              <div class="flex min-w-0 items-center gap-2">
                <Icon name="sparkles" size="sm" class="flex-shrink-0 text-primary-500" />
                <span class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ AI_IMAGE_MODEL }}</span>
              </div>
              <label class="flex flex-shrink-0 items-center gap-2 text-xs font-medium text-gray-600 dark:text-gray-300">
                <span>{{ t('aiImage.connection.rememberKey') }}</span>
                <Toggle v-model="rememberKey" />
              </label>
            </div>

            <div class="relative">
              <Icon name="key" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input
                id="ai-image-api-key"
                v-model="apiKey"
                :type="showAPIKey ? 'text' : 'password'"
                class="input h-10 px-9 font-mono text-sm"
                autocomplete="off"
                :disabled="generationBusy"
                :placeholder="t('aiImage.connection.apiKeyPlaceholder')"
              />
              <button
                type="button"
                class="absolute right-1 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-md text-gray-400 hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                :title="t(showAPIKey ? 'aiImage.connection.hideKey' : 'aiImage.connection.showKey')"
                :aria-label="t(showAPIKey ? 'aiImage.connection.hideKey' : 'aiImage.connection.showKey')"
                @click="showAPIKey = !showAPIKey"
              >
                <Icon :name="showAPIKey ? 'eyeOff' : 'eye'" size="sm" :stroke-width="2" />
              </button>
            </div>

            <div>
              <div class="mb-1 flex items-center justify-between gap-2">
                <label for="ai-image-base-url" class="text-xs font-medium text-gray-600 dark:text-gray-300">
                  {{ t('aiImage.connection.baseURL') }}
                </label>
                <button
                  type="button"
                  class="inline-flex items-center gap-1 text-[11px] font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400"
                  :disabled="generationBusy"
                  @click="resetBaseURL"
                >
                  <Icon name="refresh" size="xs" :stroke-width="2" />
                  {{ t('aiImage.connection.resetDefault') }}
                </button>
              </div>
              <input
                id="ai-image-base-url"
                v-model="baseURL"
                type="url"
                class="input h-9 font-mono text-xs"
                autocomplete="url"
                spellcheck="false"
                :disabled="generationBusy"
              />
              <p v-if="endpointErrorMessage" class="mt-1 text-[11px] leading-4 text-red-600 dark:text-red-400">
                {{ endpointErrorMessage }}
              </p>
              <p v-else-if="endpoint" class="mt-1 flex min-w-0 items-center gap-1 text-[11px] text-amber-700 dark:text-amber-300">
                <Icon name="shield" size="xs" class="flex-shrink-0" />
                <span class="truncate">{{ t('aiImage.connection.target') }}: {{ endpoint.host }}</span>
              </p>
            </div>
          </div>

          <div class="flex-shrink-0 space-y-3 p-4 pb-3">
            <div>
              <label for="ai-image-free-prompt" class="mb-1.5 block text-sm font-semibold text-gray-900 dark:text-white">
                Prompt
              </label>
              <textarea
                id="ai-image-free-prompt"
                v-model="freePrompt"
                maxlength="4000"
                class="input h-24 resize-none px-3 py-2 text-sm leading-5"
                :disabled="generationBusy"
                :placeholder="t('aiImage.composer.promptPlaceholder')"
              ></textarea>
            </div>

            <div class="grid grid-cols-3 gap-2">
              <div class="min-w-0">
                <label for="ai-image-size-tier" class="mb-1.5 block text-xs font-semibold text-gray-900 dark:text-white">
                  {{ t('aiImage.composer.sizeTier') }}
                </label>
                <select id="ai-image-size-tier" v-model="sizeTier" class="input h-10 px-2 text-xs" :disabled="generationBusy">
                  <option v-for="option in sizeTierOptions" :key="option" :value="option">{{ option }}</option>
                </select>
              </div>
              <div class="min-w-0">
                <label for="ai-image-ratio" class="mb-1.5 block text-xs font-semibold text-gray-900 dark:text-white">
                  {{ t('aiImage.composer.ratio') }}
                </label>
                <select id="ai-image-ratio" v-model="aspectRatio" class="input h-10 px-2 text-xs" :disabled="generationBusy">
                  <option v-for="option in ratioOptions" :key="option.value" :value="option.value">
                    {{ option.shortLabel }}
                  </option>
                </select>
              </div>
              <div class="min-w-0">
                <label for="ai-image-count" class="mb-1.5 block text-xs font-semibold text-gray-900 dark:text-white">
                  {{ t('aiImage.composer.count') }}
                </label>
                <select id="ai-image-count" v-model.number="imagesPerPrompt" class="input h-10 px-2 text-xs" :disabled="generationBusy">
                  <option v-for="option in imageCountOptions" :key="option" :value="option">
                    {{ option }} {{ t('aiImage.composer.imageUnit') }}
                  </option>
                </select>
              </div>
            </div>

            <div>
              <div class="mb-1.5 flex items-center justify-between gap-3">
                <p class="text-[11px] text-gray-500 dark:text-gray-400">
                  {{ referenceImages.length }}/3 · PNG / JPEG / WebP
                </p>
                <button
                  v-if="referenceImages.length"
                  type="button"
                  class="text-[11px] font-medium text-gray-500 hover:text-red-600 dark:text-gray-400"
                  :disabled="generationBusy"
                  @click="clearReferenceImages"
                >
                  {{ t('aiImage.references.clear') }}
                </button>
              </div>
              <div
                class="flex h-24 w-full items-center justify-center overflow-hidden rounded-md border border-dashed border-gray-300 bg-white text-left transition-colors hover:border-primary-400 hover:bg-primary-50/30 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-primary-700 dark:hover:bg-primary-900/10"
                @dragover.prevent
                @drop.prevent="handleReferenceDrop"
                @paste="handleReferencePaste"
              >
                <span v-if="referenceImages.length" class="flex w-full gap-2 p-2">
                  <span v-for="image in referenceImages" :key="image.id" class="group relative h-20 w-20 flex-shrink-0 overflow-hidden rounded-md bg-gray-100 dark:bg-dark-950">
                    <img :src="image.url" :alt="image.file.name" class="h-full w-full object-cover" />
                    <button
                      type="button"
                      class="absolute right-1 top-1 flex h-6 w-6 items-center justify-center rounded-md bg-black/65 text-white opacity-0 transition-opacity group-hover:opacity-100"
                      :aria-label="t('aiImage.references.remove')"
                      :disabled="generationBusy"
                      @click="removeReferenceImage(image.id)"
                    >
                      <Icon name="x" size="xs" :stroke-width="2" />
                    </button>
                  </span>
                  <button
                    v-if="referenceImages.length < MAX_REFERENCE_IMAGES"
                    type="button"
                    class="flex h-20 w-20 flex-shrink-0 items-center justify-center rounded-md border border-dashed border-gray-300 text-gray-400 hover:border-primary-400 hover:text-primary-500 dark:border-dark-600"
                    :title="t('aiImage.references.upload')"
                    :aria-label="t('aiImage.references.upload')"
                    :disabled="generationBusy"
                    @click="openReferencePicker"
                  >
                    <Icon name="plus" size="md" />
                  </button>
                </span>
                <button v-else type="button" class="flex h-full w-full flex-col items-center justify-center px-4 text-center focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/30" :disabled="generationBusy" @click="openReferencePicker">
                  <Icon name="upload" size="md" class="text-gray-400" />
                  <span class="mt-1 text-xs font-medium text-gray-700 dark:text-gray-200">
                    {{ t('aiImage.references.title') }} · {{ t('aiImage.references.upload') }}
                  </span>
                  <span class="mt-0.5 text-[11px] text-gray-400">{{ t('aiImage.references.hint') }}</span>
                </button>
              </div>
              <input
                ref="referenceInputRef"
                type="file"
                class="hidden"
                accept="image/png,image/jpeg,image/webp"
                multiple
                @change="handleReferenceSelect"
              />
              <p v-if="referenceError" class="mt-1 text-[11px] leading-4 text-red-600 dark:text-red-400">{{ referenceError }}</p>
            </div>
          </div>

          <section class="mx-4 mb-3 flex h-[300px] flex-none flex-col overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800 xl:h-auto xl:min-h-[220px] xl:flex-1">
            <div class="flex flex-shrink-0 items-center justify-between gap-3 border-b border-gray-100 px-3 py-2.5 dark:border-dark-700">
              <div>
                <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('aiImage.prompts.title') }}</h2>
                <p class="mt-0.5 text-[11px] text-gray-500 dark:text-gray-400">
                  {{ t('aiImage.prompts.selectedCount', { count: selectedPromptIds.length }) }}
                </p>
              </div>
              <button
                type="button"
                class="btn btn-secondary btn-sm h-8 px-2"
                :disabled="generationBusy || customPrompts.length >= MAX_CUSTOM_PROMPTS"
                @click="openPromptDialog()"
              >
                <Icon name="plus" size="sm" />
                {{ t('aiImage.prompts.addCustom') }}
              </button>
            </div>

            <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
              <div class="px-3 pb-1 pt-2 text-[10px] font-semibold uppercase text-gray-400">{{ t('aiImage.prompts.builtIn') }}</div>
              <PromptTemplateRow
                v-for="prompt in builtInPrompts"
                :key="prompt.id"
                :prompt="prompt"
                :selected="isPromptSelected(prompt.id)"
                :disabled="generationBusy"
                @toggle="togglePrompt(prompt.id)"
              />

              <div class="flex items-center justify-between px-3 pb-1 pt-3 text-[10px] font-semibold uppercase text-gray-400">
                <span>{{ t('aiImage.prompts.custom') }}</span>
                <span>{{ customPrompts.length }}/{{ MAX_CUSTOM_PROMPTS }}</span>
              </div>
              <div v-if="customPrompts.length">
                <PromptTemplateRow
                  v-for="prompt in customPrompts"
                  :key="prompt.id"
                  :prompt="prompt"
                  :selected="isPromptSelected(prompt.id)"
                  :disabled="generationBusy"
                  editable
                  @toggle="togglePrompt(prompt.id)"
                  @edit="openPromptDialog(prompt)"
                  @delete="requestDeletePrompt(prompt)"
                />
              </div>
              <p v-else class="px-4 py-4 text-center text-xs text-gray-400">{{ t('aiImage.prompts.noCustom') }}</p>
            </div>
          </section>

          <div class="flex-shrink-0 border-t border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
            <div class="mb-3 flex items-center justify-between gap-2 text-xs">
              <span class="font-semibold text-gray-800 dark:text-gray-100">{{ t('aiImage.composer.standardGeneration') }}</span>
              <span class="flex items-center gap-1.5 text-gray-500 dark:text-gray-400">
                <span class="rounded-full border border-gray-200 px-2 py-0.5 dark:border-dark-600">{{ generationCount }} {{ t('aiImage.composer.imageUnit') }}</span>
                <span class="rounded-full border border-gray-200 px-2 py-0.5 dark:border-dark-600">{{ sizeTier }}</span>
                <span class="rounded-full border border-gray-200 px-2 py-0.5 dark:border-dark-600">{{ aspectRatio }}</span>
                <span class="rounded-full border border-gray-200 px-2 py-0.5 dark:border-dark-600">png</span>
              </span>
            </div>
            <p v-if="selectionError" class="mb-2 text-center text-[11px] text-red-600 dark:text-red-400">{{ selectionError }}</p>
            <button type="button" class="btn btn-primary h-11 w-full" :disabled="!canGenerate" @click="requestGeneration">
              <Icon :name="generationBusy ? 'refresh' : 'sparkles'" size="sm" :class="generationBusy ? 'animate-spin' : ''" :stroke-width="2" />
              {{ t(generationBusy ? 'aiImage.generation.generating' : 'aiImage.generation.start', { count: generationCount }) }}
            </button>
          </div>
        </aside>

        <main class="flex min-h-[580px] min-w-0 flex-col border-b border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900 xl:min-h-0 xl:border-b-0 xl:border-r">
          <div class="flex h-14 flex-shrink-0 items-center justify-between gap-3 border-b border-gray-200 px-4 dark:border-dark-700">
            <div class="flex min-w-0 items-center gap-2">
              <Icon name="grid" size="sm" class="text-gray-600 dark:text-gray-300" />
              <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ t('aiImage.workspace.title') }}</h2>
            </div>
            <div class="flex items-center gap-2">
              <span v-if="currentTask" class="hidden text-xs text-gray-500 dark:text-gray-400 sm:inline">
                {{ currentTaskIndex + 1 }}/{{ tasks.length }} · {{ t(`aiImage.status.${currentTask.status}`) }}
              </span>
              <button
                v-if="tasks.length"
                type="button"
                class="btn-ghost btn-icon"
                :disabled="generationBusy"
                :title="t('aiImage.results.clear')"
                @click="clearResults"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </div>

          <div class="min-h-0 flex-1 p-4">
            <div class="relative flex h-full min-h-[390px] items-center justify-center overflow-hidden rounded-md border border-dashed border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-950/60">
              <template v-if="currentTask">
                <button
                  v-if="currentTask.status === 'succeeded' && currentTask.result"
                  type="button"
                  class="group flex h-full w-full items-center justify-center focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500"
                  :aria-label="t('aiImage.results.preview')"
                  @click="openTaskPreview(currentTask)"
                >
                  <img :src="currentTask.result.url" :alt="currentTask.prompt.name" class="max-h-full max-w-full object-contain" />
                  <span class="absolute bottom-4 right-4 flex h-10 w-10 items-center justify-center rounded-md bg-black/60 text-white opacity-0 transition-opacity group-hover:opacity-100">
                    <Icon name="eye" size="md" />
                  </span>
                </button>
                <div v-else-if="currentTask.status === 'failed'" class="flex max-w-sm flex-col items-center px-6 text-center">
                  <span class="flex h-12 w-12 items-center justify-center rounded-md bg-red-50 text-red-500 dark:bg-red-900/20 dark:text-red-400">
                    <Icon name="exclamationCircle" size="lg" />
                  </span>
                  <p class="mt-3 text-sm font-medium text-red-700 dark:text-red-300">{{ taskErrorMessage(currentTask) }}</p>
                  <button type="button" class="btn btn-secondary btn-sm mt-4" :disabled="generationBusy || !apiKey.trim()" @click="retryTask(currentTask)">
                    <Icon name="refresh" size="sm" />
                    {{ t('aiImage.results.retry') }}
                  </button>
                </div>
                <div v-else class="flex flex-col items-center text-center text-gray-500 dark:text-gray-400">
                  <Icon :name="currentTask.status === 'running' ? 'refresh' : 'clock'" size="xl" :class="currentTask.status === 'running' ? 'animate-spin text-primary-500' : ''" />
                  <p class="mt-3 text-sm font-medium">{{ t(`aiImage.status.${currentTask.status}`) }}</p>
                  <p class="mt-1 max-w-xs truncate text-xs">{{ currentTask.prompt.name }}</p>
                </div>
              </template>
              <div v-else class="flex flex-col items-center px-6 text-center">
                <span class="flex h-14 w-14 items-center justify-center rounded-md bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-gray-500">
                  <Icon name="grid" size="xl" />
                </span>
                <h3 class="mt-4 text-lg font-semibold text-gray-900 dark:text-white">{{ t('aiImage.workspace.emptyTitle') }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('aiImage.workspace.emptyDescription') }}</p>
              </div>
            </div>
          </div>

          <div v-if="tasks.length" class="flex-shrink-0 border-t border-gray-200 px-4 py-3 dark:border-dark-700">
            <div class="flex gap-2 overflow-x-auto pb-1">
              <button
                v-for="(task, index) in tasks"
                :key="task.id"
                type="button"
                class="relative h-20 w-20 flex-shrink-0 overflow-hidden rounded-md border-2 bg-gray-100 focus:outline-none dark:bg-dark-800"
                :class="activeTaskID === task.id ? 'border-primary-500' : 'border-transparent hover:border-gray-300 dark:hover:border-dark-500'"
                :title="task.prompt.name"
                @click="activeTaskID = task.id"
              >
                <img v-if="task.status === 'succeeded' && task.result" :src="task.result.url" :alt="task.prompt.name" class="h-full w-full object-cover" />
                <span v-else class="flex h-full w-full items-center justify-center" :class="taskStatusClass(task.status)">
                  <Icon :name="task.status === 'failed' ? 'exclamationCircle' : task.status === 'running' ? 'refresh' : 'clock'" size="md" :class="task.status === 'running' ? 'animate-spin' : ''" />
                </span>
                <span class="absolute left-1 top-1 rounded bg-black/60 px-1.5 py-0.5 text-[10px] font-semibold text-white">{{ index + 1 }}</span>
              </button>
            </div>
          </div>
        </main>

        <aside class="flex min-h-[280px] min-w-0 flex-col bg-gray-50/60 dark:bg-dark-900 xl:min-h-0">
          <button
            type="button"
            class="flex h-14 flex-shrink-0 items-center border-b border-gray-200 px-4 text-left hover:bg-gray-100/70 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500/30 dark:border-dark-700 dark:hover:bg-dark-800"
            :class="historyCollapsed ? 'justify-center px-0' : 'justify-between gap-3'"
            :title="t(historyCollapsed ? 'aiImage.history.expand' : 'aiImage.history.collapse')"
            @click="historyCollapsed = !historyCollapsed"
          >
            <span v-if="!historyCollapsed" class="flex min-w-0 items-center gap-2">
              <Icon name="grid" size="sm" class="flex-shrink-0 text-gray-600 dark:text-gray-300" />
              <span class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ t('aiImage.history.title') }}</span>
            </span>
            <Icon :name="historyCollapsed ? 'chevronLeft' : 'chevronRight'" size="sm" class="flex-shrink-0 text-gray-500" />
          </button>

          <template v-if="!historyCollapsed">
            <div class="flex h-12 flex-shrink-0 items-center justify-between gap-3 border-b border-gray-200 px-4 dark:border-dark-700">
              <span class="rounded-full border border-gray-200 bg-white px-2.5 py-0.5 text-xs font-semibold text-gray-700 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200">{{ historyItems.length }}</span>
              <button
                v-if="historyItems.length"
                type="button"
                class="inline-flex items-center gap-1 text-xs font-medium text-gray-500 hover:text-red-600 dark:text-gray-400"
                @click.stop="clearHistory"
              >
                <Icon name="trash" size="xs" />
                {{ t('aiImage.history.clear') }}
              </button>
            </div>

            <div v-if="historyItems.length" class="min-h-0 flex-1 overflow-y-auto overscroll-contain p-3">
              <div class="grid grid-cols-2 gap-2 xl:grid-cols-1 2xl:grid-cols-2">
                <article v-for="(item, index) in historyItems" :key="item.id" class="group relative overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
                  <button type="button" class="block aspect-square w-full overflow-hidden bg-gray-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:bg-dark-950" @click="openHistoryPreview(item)">
                    <img :src="item.url" :alt="item.name" class="h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.02]" />
                  </button>
                  <div class="absolute right-2 top-2 flex gap-1 opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                    <button type="button" class="flex h-7 w-7 items-center justify-center rounded-md bg-black/65 text-white" :title="t('aiImage.results.download')" :aria-label="t('aiImage.results.download')" @click="downloadHistoryItem(item, index)">
                      <Icon name="download" size="xs" />
                    </button>
                    <button type="button" class="flex h-7 w-7 items-center justify-center rounded-md bg-black/65 text-white" :title="t('aiImage.history.delete')" :aria-label="t('aiImage.history.delete')" @click="deleteHistoryItem(item)">
                      <Icon name="trash" size="xs" />
                    </button>
                  </div>
                  <div class="px-2 py-2">
                    <p class="truncate text-xs font-medium text-gray-800 dark:text-gray-100">{{ item.name }}</p>
                    <p class="mt-0.5 text-[10px] text-gray-400">{{ formatHistoryTime(item.createdAt) }}</p>
                  </div>
                </article>
              </div>
            </div>
            <div v-else class="flex min-h-0 flex-1 flex-col items-center justify-center px-5 py-12 text-center">
              <span class="flex h-12 w-12 items-center justify-center rounded-md bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-gray-500">
                <Icon name="grid" size="lg" />
              </span>
              <h3 class="mt-4 text-base font-semibold text-gray-900 dark:text-white">{{ t('aiImage.history.emptyTitle') }}</h3>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('aiImage.history.emptyDescription') }}</p>
            </div>
          </template>
        </aside>
      </div>
    </div>

    <ConfirmDialog
      :show="showGenerationConfirm"
      :title="t('aiImage.generation.confirmTitle')"
      :message="generationConfirmMessage"
      :confirm-text="t('aiImage.generation.confirmButton')"
      @confirm="confirmGeneration"
      @cancel="showGenerationConfirm = false"
    />

    <BaseDialog
      :show="showPromptDialog"
      :title="t(editingPromptID ? 'aiImage.customDialog.editTitle' : 'aiImage.customDialog.addTitle')"
      width="normal"
      @close="closePromptDialog"
    >
      <div class="space-y-4">
        <div>
          <label for="ai-image-prompt-name" class="input-label">{{ t('aiImage.customDialog.name') }}</label>
          <input id="ai-image-prompt-name" v-model="promptDraftName" type="text" maxlength="50" class="input" :placeholder="t('aiImage.customDialog.namePlaceholder')" />
        </div>
        <div>
          <label for="ai-image-prompt-content" class="input-label">{{ t('aiImage.customDialog.prompt') }}</label>
          <textarea id="ai-image-prompt-content" v-model="promptDraftContent" rows="6" maxlength="4000" class="input min-h-[150px] resize-y" :placeholder="t('aiImage.customDialog.promptPlaceholder')"></textarea>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closePromptDialog">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="!canSavePrompt" @click="savePrompt">{{ t('common.save') }}</button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="!!promptPendingDeletion"
      :title="t('aiImage.deleteDialog.title')"
      :message="t('aiImage.deleteDialog.message', { name: promptPendingDeletion?.name || '' })"
      :confirm-text="t('aiImage.prompts.delete')"
      danger
      @confirm="deletePrompt"
      @cancel="promptPendingDeletion = null"
    />

    <Teleport to="body">
      <Transition name="fade">
        <div v-if="previewItem" class="fixed inset-0 z-[100] flex items-center justify-center bg-black/85 p-4" role="dialog" aria-modal="true" :aria-label="t('aiImage.results.preview')" @click.self="previewItem = null">
          <div class="flex max-h-[94vh] max-w-[94vw] flex-col overflow-hidden rounded-md bg-gray-950 shadow-2xl">
            <div class="flex items-center justify-between gap-4 border-b border-white/10 px-4 py-3 text-white">
              <span class="min-w-0 truncate text-sm font-medium">{{ previewItem.name }}</span>
              <div class="flex items-center gap-1">
                <button type="button" class="flex h-9 w-9 items-center justify-center rounded-md text-gray-300 hover:bg-white/10 hover:text-white" :title="t('aiImage.results.download')" @click="downloadPreviewItem">
                  <Icon name="download" size="sm" />
                </button>
                <button type="button" class="flex h-9 w-9 items-center justify-center rounded-md text-gray-300 hover:bg-white/10 hover:text-white" :aria-label="t('common.close')" @click="previewItem = null">
                  <Icon name="x" size="md" />
                </button>
              </div>
            </div>
            <img :src="previewItem.result.url" :alt="previewItem.name" class="min-h-0 max-h-[calc(94vh-4rem)] max-w-[94vw] object-contain" />
          </div>
        </div>
      </Transition>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, ref, watch, type PropType } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Toggle from '@/components/common/Toggle.vue'
import { Icon } from '@/components/icons'
import {
  AI_IMAGE_MODEL,
  AIImageAPIError,
  downloadAIImageResult,
  generateAIImage,
  getAIImageResultBlob,
  getDefaultAIImageBaseURL,
  resolveAIImageEndpoint,
  revokeAIImageResult,
  type AIImageEndpoint,
  type AIImageErrorCode,
  type AIImagePrompt,
  type AIImageResult,
  type AIImageSizeTier,
  type AIImageTask,
  type AIImageTaskStatus
} from '@/api/aiImage'

const { t, locale } = useI18n()

const API_KEY_STORAGE_KEY = 'sub2api:ai-image:api-key:v1'
const CUSTOM_PROMPTS_STORAGE_KEY = 'sub2api:ai-image:custom-prompts:v1'
const SETTINGS_STORAGE_KEY = 'sub2api:ai-image:settings:v2'
const HISTORY_DB_NAME = 'sub2api-ai-image-cache-v1'
const HISTORY_STORE_NAME = 'images'
const HISTORY_MAX_ITEMS = 30
const HISTORY_MAX_BYTES = 120 * 1024 * 1024
const HISTORY_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000
const MAX_CUSTOM_PROMPTS = 50
const MAX_SELECTED_PROMPTS = 10
const MAX_REFERENCE_IMAGES = 3
const MAX_REFERENCE_BYTES = 20 * 1024 * 1024
const GENERATION_CONCURRENCY = 2
const ACCEPTED_REFERENCE_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp'])

interface StoredSettings {
  baseURL: string
  rememberKey: boolean
  aspectRatio: string
  sizeTier: AIImageSizeTier
  imagesPerPrompt: number
  historyCollapsed: boolean
}

interface ReferenceImage {
  id: string
  file: File
  url: string
}

interface WorkbenchTask extends AIImageTask {
  aspectRatio: string
  sizeTier: AIImageSizeTier
}

interface HistoryRecord {
  id: string
  name: string
  prompt: string
  aspectRatio: string
  createdAt: number
  blob: Blob
  mimeType: string
  size: number
}

interface HistoryItem extends HistoryRecord {
  url: string
}

interface PreviewItem {
  name: string
  result: AIImageResult
}

const PromptTemplateRow = defineComponent({
  name: 'PromptTemplateRow',
  props: {
    prompt: { type: Object as PropType<AIImagePrompt>, required: true },
    selected: { type: Boolean, required: true },
    disabled: { type: Boolean, default: false },
    editable: { type: Boolean, default: false }
  },
  emits: ['toggle', 'edit', 'delete'],
  setup(props, { emit }) {
    return () => h('div', {
      class: [
        'group flex items-center gap-1 border-b border-gray-100 px-2 py-1.5 dark:border-dark-700',
        props.selected ? 'bg-primary-50/80 dark:bg-primary-900/15' : 'hover:bg-gray-50 dark:hover:bg-dark-700/50'
      ]
    }, [
      h('button', {
        type: 'button',
        class: 'flex min-w-0 flex-1 items-start gap-2 rounded px-1 py-1.5 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/30',
        disabled: props.disabled,
        'aria-pressed': props.selected,
        onClick: () => emit('toggle')
      }, [
        h('span', {
          class: [
            'mt-0.5 flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border',
            props.selected
              ? 'border-primary-600 bg-primary-600 text-white'
              : 'border-gray-300 bg-white text-transparent dark:border-dark-500 dark:bg-dark-800'
          ]
        }, [h(Icon, { name: 'check', size: 'xs', strokeWidth: 2.5 })]),
        h(HelpTooltip, {
          content: props.prompt.prompt,
          widthClass: 'w-80 max-w-[calc(100vw-2rem)]',
          class: '!ml-0 min-w-0 flex-1'
        }, {
          trigger: () => h('span', { class: 'block truncate text-xs font-semibold text-gray-900 dark:text-gray-100' }, props.prompt.name)
        })
      ]),
      props.editable
        ? h('div', { class: 'flex flex-shrink-0 items-center opacity-0 transition-opacity group-hover:opacity-100' }, [
            h('button', {
              type: 'button',
              class: 'flex h-7 w-7 items-center justify-center rounded text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600 dark:hover:text-gray-200',
              title: t('aiImage.prompts.edit'),
              'aria-label': t('aiImage.prompts.edit'),
              disabled: props.disabled,
              onClick: () => emit('edit')
            }, [h(Icon, { name: 'edit', size: 'xs' })]),
            h('button', {
              type: 'button',
              class: 'flex h-7 w-7 items-center justify-center rounded text-gray-400 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400',
              title: t('aiImage.prompts.delete'),
              'aria-label': t('aiImage.prompts.delete'),
              disabled: props.disabled,
              onClick: () => emit('delete')
            }, [h(Icon, { name: 'trash', size: 'xs' })])
          ])
        : null
    ])
  }
})

const defaultBaseURL = getDefaultAIImageBaseURL()
const baseURL = ref(defaultBaseURL)
const apiKey = ref('')
const showAPIKey = ref(false)
const rememberKey = ref(false)
const freePrompt = ref('')
const aspectRatio = ref('1:1')
const sizeTier = ref<AIImageSizeTier>('1K')
const imagesPerPrompt = ref(1)
const referenceImages = ref<ReferenceImage[]>([])
const referenceInputRef = ref<HTMLInputElement | null>(null)
const referenceError = ref('')
const customPrompts = ref<AIImagePrompt[]>([])
const selectedPromptIds = ref<string[]>([])
const selectionError = ref('')
const tasks = ref<WorkbenchTask[]>([])
const activeTaskID = ref('')
const generationBusy = ref(false)
const showGenerationConfirm = ref(false)
const previewItem = ref<PreviewItem | null>(null)
const historyItems = ref<HistoryItem[]>([])
const historyCollapsed = ref(false)

const showPromptDialog = ref(false)
const editingPromptID = ref('')
const promptDraftName = ref('')
const promptDraftContent = ref('')
const promptPendingDeletion = ref<AIImagePrompt | null>(null)

const activeControllers = new Map<string, AbortController>()
let historyDBPromise: Promise<IDBDatabase | null> | null = null
let disposed = false

const ratioOptions = computed(() => [
  { value: '1:1', label: t('aiImage.ratios.square'), shortLabel: '1:1' },
  { value: '16:9', label: t('aiImage.ratios.landscape'), shortLabel: '16:9' },
  { value: '9:16', label: t('aiImage.ratios.portrait'), shortLabel: '9:16' },
  { value: '16:10', label: t('aiImage.ratios.wide'), shortLabel: '16:10' }
])
const sizeTierOptions: AIImageSizeTier[] = ['1K', '2K', '4K']
const imageCountOptions = [1, 2, 3, 4]

const builtInPrompts = computed<AIImagePrompt[]>(() => [
  { id: 'builtin-professional-avatar', name: t('aiImage.builtInPrompts.professionalAvatar'), prompt: '生成一张年轻亚洲创意工作者的专业半身头像，正面看向镜头，神态自信自然，穿深色简洁服装，柔和影棚主光与轻微轮廓光，浅灰纯色背景，真实摄影质感，面部细节清晰，主体居中，无文字，无水印。', builtIn: true },
  { id: 'builtin-product-photo', name: t('aiImage.builtInPrompts.productPhoto'), prompt: '生成一张透明玻璃香水瓶的高端电商商品主图，香水瓶置于浅灰色无缝背景上，柔和侧光，精致高光与真实反射，主体居中，四周保留干净留白，商业产品摄影质感，无文字，无品牌标识，无水印。', builtIn: true },
  { id: 'builtin-drink-poster', name: t('aiImage.builtInPrompts.drinkPoster'), prompt: '生成一张柠檬气泡饮品商业海报画面，一杯透明冰饮位于画面中央，周围有柠檬片、冰块和飞溅水花，青绿色与明黄色配色，清爽明快的夏日氛围，高速商业摄影质感，无文字，无水印。', builtIn: true },
  { id: 'builtin-healing-illustration', name: t('aiImage.builtInPrompts.healingIllustration'), prompt: '生成一幅雨夜街角书店的治愈系数字插画，橙色窗光从店内透出，湿润街道映出温暖倒影，一位撑伞行人经过，深蓝夜色与暖橙灯光形成对比，细节丰富，安静而有电影感，无文字，无水印。', builtIn: true },
  { id: 'builtin-night-wallpaper', name: t('aiImage.builtInPrompts.nightWallpaper'), prompt: '生成一张深蓝夜空、远山和平静湖面的极简自然风景壁纸，天空有稀疏清晰的星光，湖面保留细腻倒影，前中后景层次明确，大面积克制留白，冷静高级的电影色调，无文字，无水印。', builtIn: true },
  { id: 'builtin-astronaut-sticker', name: t('aiImage.builtInPrompts.astronautSticker'), prompt: 'Generate a cute orange cat astronaut sticker on a clean pastel background. Centered composition, crisp white sticker outline, soft studio lighting, polished 3D illustration, no text, no watermark.', builtIn: true }
])

const allPrompts = computed(() => [...builtInPrompts.value, ...customPrompts.value])
const promptCount = computed(() => selectedPromptIds.value.length + (freePrompt.value.trim() ? 1 : 0))
const generationCount = computed(() => promptCount.value * imagesPerPrompt.value)
const currentTask = computed(() => tasks.value.find((task) => task.id === activeTaskID.value) || tasks.value[0] || null)
const currentTaskIndex = computed(() => currentTask.value ? tasks.value.findIndex((task) => task.id === currentTask.value?.id) : -1)

const endpoint = computed<AIImageEndpoint | null>(() => {
  try {
    return resolveAIImageEndpoint(baseURL.value)
  } catch {
    return null
  }
})

const endpointErrorCode = computed<AIImageErrorCode | null>(() => {
  if (!baseURL.value.trim()) return 'invalid-base-url'
  try {
    resolveAIImageEndpoint(baseURL.value)
    return null
  } catch (error) {
    return error instanceof AIImageAPIError ? error.code : 'invalid-base-url'
  }
})

const endpointErrorMessage = computed(() => endpointErrorCode.value ? errorMessageForCode(endpointErrorCode.value) : '')
const canGenerate = computed(() => (
  !generationBusy.value &&
  !!endpoint.value &&
  !!apiKey.value.trim() &&
  promptCount.value > 0 &&
  promptCount.value <= MAX_SELECTED_PROMPTS
))
const canSavePrompt = computed(() => !!promptDraftName.value.trim() && !!promptDraftContent.value.trim() && (!!editingPromptID.value || customPrompts.value.length < MAX_CUSTOM_PROMPTS))
const generationConfirmMessage = computed(() => t('aiImage.generation.confirmMessage', { count: generationCount.value, host: endpoint.value?.host || '-' }))

function createID(prefix: string): string {
  const suffix = typeof crypto !== 'undefined' && 'randomUUID' in crypto
    ? crypto.randomUUID()
    : `${Date.now()}-${Math.random().toString(16).slice(2)}`
  return `${prefix}-${suffix}`
}

function persistSettings(): void {
  try {
    const value: StoredSettings = {
      baseURL: baseURL.value,
      rememberKey: rememberKey.value,
      aspectRatio: aspectRatio.value,
      sizeTier: sizeTier.value,
      imagesPerPrompt: imagesPerPrompt.value,
      historyCollapsed: historyCollapsed.value
    }
    localStorage.setItem(SETTINGS_STORAGE_KEY, JSON.stringify(value))
  } catch {
    // 浏览器拒绝本地存储时，连接设置仅保留在当前页面内存。
  }
}

function loadSettings(): void {
  try {
    const raw = localStorage.getItem(SETTINGS_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw) as Partial<StoredSettings>
      if (typeof parsed.baseURL === 'string' && parsed.baseURL.trim()) {
        resolveAIImageEndpoint(parsed.baseURL)
        baseURL.value = parsed.baseURL
      }
      if (['1:1', '16:9', '9:16', '16:10'].includes(parsed.aspectRatio || '')) aspectRatio.value = parsed.aspectRatio || '1:1'
      if (['1K', '2K', '4K'].includes(parsed.sizeTier || '')) sizeTier.value = parsed.sizeTier as AIImageSizeTier
      if ([1, 2, 3, 4].includes(parsed.imagesPerPrompt || 0)) imagesPerPrompt.value = parsed.imagesPerPrompt || 1
      rememberKey.value = parsed.rememberKey === true
      historyCollapsed.value = parsed.historyCollapsed === true
    }
    if (rememberKey.value) apiKey.value = localStorage.getItem(API_KEY_STORAGE_KEY) || ''
    else localStorage.removeItem(API_KEY_STORAGE_KEY)
  } catch {
    baseURL.value = defaultBaseURL
    rememberKey.value = false
    apiKey.value = ''
  }
}

function persistCustomPrompts(): void {
  try {
    localStorage.setItem(CUSTOM_PROMPTS_STORAGE_KEY, JSON.stringify(customPrompts.value.map(({ id, name, prompt }) => ({ id, name, prompt }))))
  } catch {
    // 自定义模板在本地存储不可用时仍可在当前会话使用。
  }
}

function loadCustomPrompts(): void {
  try {
    const raw = localStorage.getItem(CUSTOM_PROMPTS_STORAGE_KEY)
    if (!raw) return
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed) || parsed.length > MAX_CUSTOM_PROMPTS) throw new Error('invalid-data')
    const prompts = parsed.map((item): AIImagePrompt => {
      if (!item || typeof item !== 'object') throw new Error('invalid-data')
      const value = item as Record<string, unknown>
      if (typeof value.id !== 'string' || !value.id.startsWith('custom-') || typeof value.name !== 'string' || !value.name.trim() || typeof value.prompt !== 'string' || !value.prompt.trim()) throw new Error('invalid-data')
      return { id: value.id, name: value.name.trim().slice(0, 50), prompt: value.prompt.trim().slice(0, 4000), builtIn: false }
    })
    if (new Set(prompts.map((prompt) => prompt.id)).size !== prompts.length) throw new Error('invalid-data')
    customPrompts.value = prompts
  } catch {
    customPrompts.value = []
    try { localStorage.removeItem(CUSTOM_PROMPTS_STORAGE_KEY) } catch { /* 已回退为空列表。 */ }
  }
}

function resetBaseURL(): void {
  baseURL.value = defaultBaseURL
}

function isPromptSelected(id: string): boolean {
  return selectedPromptIds.value.includes(id)
}

function togglePrompt(id: string): void {
  selectionError.value = ''
  const index = selectedPromptIds.value.indexOf(id)
  if (index >= 0) {
    selectedPromptIds.value.splice(index, 1)
    return
  }
  if (promptCount.value >= MAX_SELECTED_PROMPTS) {
    selectionError.value = t('aiImage.errors.maxSelectedPrompts')
    return
  }
  selectedPromptIds.value.push(id)
}

function openPromptDialog(prompt?: AIImagePrompt): void {
  editingPromptID.value = prompt?.id || ''
  promptDraftName.value = prompt?.name || ''
  promptDraftContent.value = prompt?.prompt || ''
  showPromptDialog.value = true
}

function closePromptDialog(): void {
  showPromptDialog.value = false
  editingPromptID.value = ''
  promptDraftName.value = ''
  promptDraftContent.value = ''
}

function savePrompt(): void {
  if (!canSavePrompt.value) return
  const name = promptDraftName.value.trim()
  const prompt = promptDraftContent.value.trim()
  if (editingPromptID.value) {
    const target = customPrompts.value.find((item) => item.id === editingPromptID.value)
    if (target) Object.assign(target, { name, prompt })
  } else {
    customPrompts.value.push({ id: createID('custom'), name, prompt, builtIn: false })
  }
  persistCustomPrompts()
  closePromptDialog()
}

function requestDeletePrompt(prompt: AIImagePrompt): void {
  promptPendingDeletion.value = prompt
}

function deletePrompt(): void {
  const target = promptPendingDeletion.value
  if (!target) return
  customPrompts.value = customPrompts.value.filter((prompt) => prompt.id !== target.id)
  selectedPromptIds.value = selectedPromptIds.value.filter((id) => id !== target.id)
  promptPendingDeletion.value = null
  persistCustomPrompts()
}

function openReferencePicker(): void {
  referenceInputRef.value?.click()
}

function addReferenceFiles(files: File[]): void {
  referenceError.value = ''
  const availableSlots = MAX_REFERENCE_IMAGES - referenceImages.value.length
  if (availableSlots <= 0) {
    referenceError.value = t('aiImage.errors.referenceLimit')
    return
  }
  for (const file of files.slice(0, availableSlots)) {
    if (!ACCEPTED_REFERENCE_TYPES.has(file.type)) {
      referenceError.value = t('aiImage.errors.referenceType')
      continue
    }
    if (file.size > MAX_REFERENCE_BYTES) {
      referenceError.value = t('aiImage.errors.referenceSize')
      continue
    }
    referenceImages.value.push({ id: createID('reference'), file, url: URL.createObjectURL(file) })
  }
  if (files.length > availableSlots) referenceError.value = t('aiImage.errors.referenceLimit')
}

function handleReferenceSelect(event: Event): void {
  const input = event.target as HTMLInputElement
  addReferenceFiles(Array.from(input.files || []))
  input.value = ''
}

function handleReferenceDrop(event: DragEvent): void {
  addReferenceFiles(Array.from(event.dataTransfer?.files || []))
}

function handleReferencePaste(event: ClipboardEvent): void {
  const files = Array.from(event.clipboardData?.items || [])
    .filter((item) => item.kind === 'file')
    .map((item) => item.getAsFile())
    .filter((file): file is File => !!file)
  if (files.length) {
    event.preventDefault()
    addReferenceFiles(files)
  }
}

function removeReferenceImage(id: string): void {
  const target = referenceImages.value.find((image) => image.id === id)
  if (target) URL.revokeObjectURL(target.url)
  referenceImages.value = referenceImages.value.filter((image) => image.id !== id)
  referenceError.value = ''
}

function clearReferenceImages(): void {
  for (const image of referenceImages.value) URL.revokeObjectURL(image.url)
  referenceImages.value = []
  referenceError.value = ''
}

function ratioLabel(value = aspectRatio.value): string {
  return ratioOptions.value.find((option) => option.value === value)?.label || value
}

function appendRatioPrompt(prompt: string): string {
  return `${prompt.trim()}\n\n${t('aiImage.ratios.promptSuffix', { ratio: ratioLabel() })}`
}

function buildTaskPrompts(): AIImagePrompt[] {
  const prompts: AIImagePrompt[] = []
  if (freePrompt.value.trim()) {
    prompts.push({ id: createID('free-prompt'), name: t('aiImage.composer.customPromptName'), prompt: appendRatioPrompt(freePrompt.value), builtIn: false })
  }
  for (const id of selectedPromptIds.value) {
    const source = allPrompts.value.find((prompt) => prompt.id === id)
    if (source) prompts.push({ ...source, prompt: appendRatioPrompt(source.prompt) })
  }
  return prompts.slice(0, MAX_SELECTED_PROMPTS).flatMap((prompt) => (
    Array.from({ length: imagesPerPrompt.value }, (_, index) => ({
      ...prompt,
      id: `${prompt.id}-copy-${index + 1}`,
      name: imagesPerPrompt.value > 1
        ? t('aiImage.composer.copyName', { name: prompt.name, index: index + 1 })
        : prompt.name
    }))
  ))
}

function requestGeneration(): void {
  if (!canGenerate.value) return
  showGenerationConfirm.value = true
}

function confirmGeneration(): void {
  showGenerationConfirm.value = false
  void startGeneration()
}

function errorMessageForCode(code: AIImageErrorCode): string {
  const keyMap: Record<AIImageErrorCode, string> = {
    'invalid-base-url': 'aiImage.errors.invalidBaseURL',
    'insecure-http': 'aiImage.errors.insecureHTTP',
    'invalid-key': 'aiImage.errors.invalidKey',
    'endpoint-not-found': 'aiImage.errors.endpointNotFound',
    'rate-limited': 'aiImage.errors.rateLimited',
    'server-error': 'aiImage.errors.serverError',
    timeout: 'aiImage.errors.timeout',
    'network-error': 'aiImage.errors.networkError',
    'empty-response': 'aiImage.errors.emptyResponse',
    'invalid-response': 'aiImage.errors.invalidResponse',
    'request-failed': 'aiImage.errors.requestFailed',
    aborted: 'aiImage.errors.aborted'
  }
  return t(keyMap[code])
}

function taskErrorMessage(task: WorkbenchTask): string {
  return errorMessageForCode(task.errorCode || 'request-failed')
}

function taskStatusClass(status: AIImageTaskStatus): string {
  return {
    queued: 'text-gray-500 dark:text-gray-400',
    running: 'text-primary-600 dark:text-primary-400',
    succeeded: 'text-emerald-600 dark:text-emerald-400',
    failed: 'text-red-600 dark:text-red-400'
  }[status]
}

function revokeTaskResult(task: WorkbenchTask): void {
  revokeAIImageResult(task.result)
  task.result = undefined
}

function clearResults(): void {
  if (generationBusy.value) return
  for (const task of tasks.value) revokeTaskResult(task)
  tasks.value = []
  activeTaskID.value = ''
  previewItem.value = null
}

async function runTask(task: WorkbenchTask, endpointSnapshot: AIImageEndpoint, keySnapshot: string, references: File[]): Promise<void> {
  task.status = 'running'
  task.errorCode = undefined
  task.startedAt = Date.now()
  activeTaskID.value ||= task.id
  const controller = new AbortController()
  activeControllers.set(task.id, controller)
  try {
    const result = await generateAIImage({
      baseURL: endpointSnapshot.baseURL,
      apiKey: keySnapshot,
      prompt: task.prompt.prompt,
      sizeTier: task.sizeTier,
      referenceImages: references,
      signal: controller.signal
    })
    if (disposed) {
      revokeAIImageResult(result)
      return
    }
    revokeTaskResult(task)
    task.result = result
    task.status = 'succeeded'
    task.completedAt = Date.now()
    await cacheSuccessfulTask(task)
  } catch (error) {
    if (disposed) return
    task.status = 'failed'
    task.errorCode = error instanceof AIImageAPIError ? error.code : 'request-failed'
    task.completedAt = Date.now()
  } finally {
    activeControllers.delete(task.id)
  }
}

async function runQueue(queue: WorkbenchTask[], endpointSnapshot: AIImageEndpoint, keySnapshot: string, references: File[]): Promise<void> {
  let cursor = 0
  const worker = async () => {
    while (cursor < queue.length && !disposed) {
      const task = queue[cursor]
      cursor += 1
      if (task) await runTask(task, endpointSnapshot, keySnapshot, references)
    }
  }
  await Promise.all(Array.from({ length: Math.min(GENERATION_CONCURRENCY, queue.length) }, () => worker()))
}

async function startGeneration(): Promise<void> {
  if (!canGenerate.value || !endpoint.value) return
  const endpointSnapshot = endpoint.value
  const keySnapshot = apiKey.value.trim()
  const references = referenceImages.value.map((image) => image.file)
  for (const task of tasks.value) revokeTaskResult(task)
  previewItem.value = null
  tasks.value = buildTaskPrompts().map((prompt) => ({
    id: createID('task'),
    prompt,
    status: 'queued',
    aspectRatio: aspectRatio.value,
    sizeTier: sizeTier.value
  }))
  activeTaskID.value = tasks.value[0]?.id || ''
  generationBusy.value = true
  try {
    await runQueue(tasks.value, endpointSnapshot, keySnapshot, references)
  } finally {
    generationBusy.value = false
  }
}

async function retryTask(task: WorkbenchTask): Promise<void> {
  if (generationBusy.value || !endpoint.value || !apiKey.value.trim()) return
  revokeTaskResult(task)
  task.status = 'queued'
  generationBusy.value = true
  try {
    await runTask(task, endpoint.value, apiKey.value.trim(), referenceImages.value.map((image) => image.file))
  } finally {
    generationBusy.value = false
  }
}

function sanitizeFilename(name: string): string {
  return name.replace(/[\\/:*?\"<>|]/g, '-').trim() || 'ai-image'
}

function openTaskPreview(task: WorkbenchTask): void {
  if (!task.result) return
  previewItem.value = { name: task.prompt.name, result: task.result }
}

function openHistoryPreview(item: HistoryItem): void {
  previewItem.value = { name: item.name, result: { url: item.url, source: 'base64', mimeType: item.mimeType, blob: item.blob } }
}

async function downloadPreviewItem(): Promise<void> {
  if (!previewItem.value) return
  await downloadAIImageResult(previewItem.value.result, `${sanitizeFilename(previewItem.value.name)}.png`)
}

function idbRequest<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error)
  })
}

function openHistoryDB(): Promise<IDBDatabase | null> {
  if (typeof window === 'undefined' || !('indexedDB' in window)) return Promise.resolve(null)
  if (historyDBPromise) return historyDBPromise
  historyDBPromise = new Promise((resolve) => {
    const request = window.indexedDB.open(HISTORY_DB_NAME, 1)
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(HISTORY_STORE_NAME)) request.result.createObjectStore(HISTORY_STORE_NAME, { keyPath: 'id' })
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => resolve(null)
    request.onblocked = () => resolve(null)
  })
  return historyDBPromise
}

function replaceHistoryItems(records: HistoryRecord[]): void {
  for (const item of historyItems.value) URL.revokeObjectURL(item.url)
  historyItems.value = records.map((record) => ({ ...record, url: URL.createObjectURL(record.blob) }))
}

async function refreshHistory(): Promise<void> {
  const db = await openHistoryDB()
  if (!db) return
  const records = await idbRequest<HistoryRecord[]>(db.transaction(HISTORY_STORE_NAME, 'readonly').objectStore(HISTORY_STORE_NAME).getAll()).catch(() => [])
  const now = Date.now()
  const sorted = records.sort((a, b) => b.createdAt - a.createdAt)
  const kept: HistoryRecord[] = []
  const removed: HistoryRecord[] = []
  let totalBytes = 0
  for (const record of sorted) {
    const nextBytes = totalBytes + (record.size || record.blob.size)
    if (now - record.createdAt > HISTORY_MAX_AGE_MS || kept.length >= HISTORY_MAX_ITEMS || nextBytes > HISTORY_MAX_BYTES) {
      removed.push(record)
      continue
    }
    kept.push(record)
    totalBytes = nextBytes
  }
  if (removed.length) {
    const store = db.transaction(HISTORY_STORE_NAME, 'readwrite').objectStore(HISTORY_STORE_NAME)
    for (const record of removed) store.delete(record.id)
  }
  replaceHistoryItems(kept)
}

async function cacheSuccessfulTask(task: WorkbenchTask): Promise<void> {
  if (!task.result) return
  try {
    const blob = await getAIImageResultBlob(task.result)
    const record: HistoryRecord = {
      id: createID('history'),
      name: task.prompt.name,
      prompt: task.prompt.prompt,
      aspectRatio: task.aspectRatio,
      createdAt: Date.now(),
      blob,
      mimeType: blob.type || task.result.mimeType,
      size: blob.size
    }
    const db = await openHistoryDB()
    if (db) {
      await idbRequest(db.transaction(HISTORY_STORE_NAME, 'readwrite').objectStore(HISTORY_STORE_NAME).put(record))
      await refreshHistory()
    } else {
      historyItems.value.unshift({ ...record, url: URL.createObjectURL(blob) })
    }
  } catch {
    // 缓存失败不改变已成功的生图任务，也不记录供应商原始响应。
  }
}

async function clearHistory(): Promise<void> {
  const db = await openHistoryDB()
  if (db) await idbRequest(db.transaction(HISTORY_STORE_NAME, 'readwrite').objectStore(HISTORY_STORE_NAME).clear()).catch(() => null)
  replaceHistoryItems([])
}

async function deleteHistoryItem(item: HistoryItem): Promise<void> {
  const db = await openHistoryDB()
  if (db) await idbRequest(db.transaction(HISTORY_STORE_NAME, 'readwrite').objectStore(HISTORY_STORE_NAME).delete(item.id)).catch(() => null)
  URL.revokeObjectURL(item.url)
  historyItems.value = historyItems.value.filter((history) => history.id !== item.id)
  if (previewItem.value?.result.url === item.url) previewItem.value = null
}

async function downloadHistoryItem(item: HistoryItem, index: number): Promise<void> {
  await downloadAIImageResult({ url: item.url, source: 'base64', mimeType: item.mimeType, blob: item.blob }, `${String(index + 1).padStart(2, '0')}-${sanitizeFilename(item.name)}.png`)
}

function formatHistoryTime(timestamp: number): string {
  return new Intl.DateTimeFormat(locale.value, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(timestamp)
}

function handleGlobalKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && previewItem.value) previewItem.value = null
}

watch([baseURL, rememberKey, aspectRatio, sizeTier, imagesPerPrompt, historyCollapsed], persistSettings)
watch(freePrompt, () => {
  selectionError.value = promptCount.value > MAX_SELECTED_PROMPTS ? t('aiImage.errors.maxSelectedPrompts') : ''
})
watch(apiKey, (value) => {
  try {
    if (rememberKey.value) localStorage.setItem(API_KEY_STORAGE_KEY, value)
  } catch {
    // 密钥仍只保留在页面内存中。
  }
})
watch(rememberKey, (value) => {
  try {
    if (value && apiKey.value) localStorage.setItem(API_KEY_STORAGE_KEY, apiKey.value)
    if (!value) localStorage.removeItem(API_KEY_STORAGE_KEY)
  } catch {
    // 切换记忆状态不影响当前页面继续使用密钥。
  }
})

onMounted(() => {
  loadSettings()
  loadCustomPrompts()
  void refreshHistory()
  document.addEventListener('keydown', handleGlobalKeydown)
})

onBeforeUnmount(() => {
  disposed = true
  document.removeEventListener('keydown', handleGlobalKeydown)
  for (const controller of activeControllers.values()) controller.abort()
  activeControllers.clear()
  for (const task of tasks.value) revokeTaskResult(task)
  for (const item of historyItems.value) URL.revokeObjectURL(item.url)
  clearReferenceImages()
})
</script>
