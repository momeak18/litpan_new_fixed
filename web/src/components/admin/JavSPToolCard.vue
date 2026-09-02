<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { javspApi, type JavSPConfig, type JavSPTask } from "@/api/javsp";
import { toast } from "@/composables/useToast";
import AppButton from "@/components/base/AppButton.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";

const props = defineProps<{ searchQuery?: string }>();

const config = reactive<JavSPConfig>({
  enabled: false,
  host_media_dir: "",
  container_media_dir: "/video",
  image: "apecme/javsp-web:bata",
  memory_limit_mb: 1024,
});
const tasks = ref<JavSPTask[]>([]);
const relativePath = ref("");
const loading = ref(true);
const saving = ref(false);
const starting = ref(false);
const canceling = ref<string | null>(null);

const visible = computed(() => {
  const query = props.searchQuery?.trim().toLowerCase() ?? "";
  return !query || "javsp javsp-web metadata scraper docker nfo".includes(query);
});
const activeTask = computed(() => tasks.value.find((task) => task.status === "queued" || task.status === "running"));

function assignConfig(next: JavSPConfig) {
  Object.assign(config, next);
}

async function load() {
  loading.value = true;
  try {
    const [savedConfig, savedTasks] = await Promise.all([javspApi.getConfig(), javspApi.listTasks()]);
    assignConfig(savedConfig);
    tasks.value = savedTasks;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Failed to load JavSP settings"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    assignConfig(await javspApi.setConfig({ ...config }));
    toast.success(config.enabled ? "JavSP on-demand scraping enabled" : "JavSP on-demand scraping disabled");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Failed to save JavSP settings"));
  } finally {
    saving.value = false;
  }
}

async function start() {
  starting.value = true;
  try {
    const task = await javspApi.createTask(relativePath.value);
    tasks.value = [task, ...tasks.value.filter((item) => item.id !== task.id)];
    relativePath.value = "";
    toast.success("JavSP task added to the queue");
    await refreshTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Failed to create JavSP task"));
  } finally {
    starting.value = false;
  }
}

async function refreshTasks() {
  try {
    tasks.value = await javspApi.listTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Failed to refresh JavSP tasks"));
  }
}

async function cancel(id: string) {
  canceling.value = id;
  try {
    await javspApi.cancelTask(id);
    await refreshTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "Failed to cancel JavSP task"));
  } finally {
    canceling.value = null;
  }
}

function statusLabel(status: JavSPTask["status"]) {
  return ({
    queued: "Queued",
    running: "Running",
    success: "Completed",
    failed: "Failed",
    canceled: "Canceled",
  })[status];
}

onMounted(load);
</script>

<template>
  <CloudToolCard
    v-show="visible"
    :enabled="config.enabled"
    name="JavSP on-demand metadata scraping"
    driver="Runs a short-lived JavSP Docker container"
    logo-alt="JavSP"
    :tags="[{ label: activeTask ? 'Task running' : 'Ready', variant: activeTask ? 'warn' : 'default' }]"
    :stat-value="activeTask ? statusLabel(activeTask.status) : 'Idle'"
    stat-label="JavSP container status"
  >
    <template #logo>
      <span class="javsp-logo">JS</span>
    </template>

    A JavSP container is created only for each scraping task and is removed when the task finishes or is canceled.

    <template #details>
      <div class="javsp-form">
        <label>
          Host media directory
          <input v-model.trim="config.host_media_dir" placeholder="/mnt/media" :disabled="loading" />
        </label>
        <label>
          Media directory inside the container
          <input v-model.trim="config.container_media_dir" placeholder="/video" :disabled="loading" />
        </label>
        <label>
          JavSP image
          <input v-model.trim="config.image" placeholder="apecme/javsp-web:beta" :disabled="loading" />
        </label>
        <label>
          Memory limit (MB, 0 for unlimited)
          <input v-model.number="config.memory_limit_mb" type="number" min="0" max="4096" :disabled="loading" />
        </label>
        <label class="javsp-enabled">
          <input v-model="config.enabled" type="checkbox" :disabled="loading" />
          Enable on-demand scraping
        </label>
        <AppButton size="sm" :disabled="loading || saving" @click="save">
          {{ saving ? "Saving..." : "Save settings" }}
        </AppButton>
      </div>

      <div class="javsp-start">
        <input
          v-model.trim="relativePath"
          placeholder="Relative path of the media directory to scrape"
          :disabled="loading || starting || !config.enabled"
          @keyup.enter="start"
        />
        <AppButton size="sm" :disabled="loading || starting || !config.enabled" @click="start">
          {{ starting ? "Starting..." : "Start task" }}
        </AppButton>
      </div>

      <div v-if="tasks.length" class="javsp-tasks">
        <div v-for="task in tasks.slice(0, 5)" :key="task.id" class="javsp-task">
          <div>
            <strong>{{ statusLabel(task.status) }}</strong>
            <span>{{ task.relative_path }}</span>
            <small>{{ task.error || task.message }}</small>
          </div>
          <AppButton
            v-if="task.status === 'queued' || task.status === 'running'"
            size="sm"
            variant="danger"
            :disabled="canceling === task.id"
            @click="cancel(task.id)"
          >
            {{ canceling === task.id ? "Canceling..." : "Cancel" }}
          </AppButton>
          <details v-if="task.log" class="javsp-log">
            <summary>Task log</summary>
            <pre>{{ task.log }}</pre>
          </details>
        </div>
        <AppButton size="sm" variant="secondary" @click="refreshTasks">Refresh tasks</AppButton>
      </div>
    </template>
  </CloudToolCard>
</template>

<style scoped>
.javsp-logo { display: grid; width: 36px; height: 36px; place-items: center; border-radius: 10px; color: #fff; background: linear-gradient(135deg, #6d28d9, #db2777); font-size: 12px; font-weight: 800; }
.javsp-form { display: grid; gap: 8px; margin-top: 14px; }
.javsp-form label { display: grid; gap: 4px; color: var(--text-muted); font-size: 12px; }
.javsp-form input, .javsp-start input { width: 100%; min-width: 0; box-sizing: border-box; padding: 7px 9px; border: 1px solid var(--border); border-radius: var(--radius-sm); color: var(--text); background: var(--surface); }
.javsp-enabled { display: flex !important; align-items: center; gap: 7px; }
.javsp-enabled input { width: auto; }
.javsp-start { display: flex; gap: 8px; margin-top: 14px; }
.javsp-start input { flex: 1; }
.javsp-tasks { display: grid; gap: 8px; margin-top: 14px; }
.javsp-task { display: flex; flex-wrap: wrap; align-items: start; justify-content: space-between; gap: 8px; padding: 8px; border-radius: var(--radius-sm); background: var(--surface-alt); }
.javsp-task div { display: grid; min-width: 0; gap: 2px; }
.javsp-task span, .javsp-task small { overflow: hidden; color: var(--text-muted); text-overflow: ellipsis; white-space: nowrap; }
.javsp-task small { max-width: 250px; }
.javsp-log { width: 100%; font-size: 12px; }
.javsp-log pre { max-height: 180px; overflow: auto; margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; }
@media (max-width: 480px) { .javsp-start { align-items: stretch; flex-direction: column; } }
</style>
