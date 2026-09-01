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
  const q = props.searchQuery?.trim().toLowerCase() ?? "";
  return !q || "javsp javsp-web 鍏冩暟鎹埉鍓?nfo 鎸夐渶瀹瑰櫒".includes(q);
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
    toast.error(getApiErrorMessage(error, "鍔犺浇 JavSP 璁剧疆澶辫触"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  saving.value = true;
  try {
    assignConfig(await javspApi.setConfig({ ...config }));
    toast.success(config.enabled ? "JavSP 鎸夐渶鍒墛宸插惎鐢? : "JavSP 鎸夐渶鍒墛宸插仠鐢?);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "淇濆瓨 JavSP 璁剧疆澶辫触"));
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
    toast.success("JavSP 浠诲姟宸插姞鍏ラ槦鍒楋紱瀹瑰櫒浼氬湪瀹屾垚鍚庤嚜鍔ㄥ垹闄?);
    await refreshTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "鍒涘缓 JavSP 浠诲姟澶辫触"));
  } finally {
    starting.value = false;
  }
}

async function refreshTasks() {
  try {
    tasks.value = await javspApi.listTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "鍒锋柊浠诲姟鐘舵€佸け璐?));
  }
}

async function cancel(id: string) {
  canceling.value = id;
  try {
    await javspApi.cancelTask(id);
    await refreshTasks();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "鍋滄浠诲姟澶辫触"));
  } finally {
    canceling.value = null;
  }
}

function statusLabel(status: JavSPTask["status"]) {
  return ({ queued: "鎺掗槦涓?, running: "杩愯涓?, success: "宸插畬鎴?, failed: "澶辫触", canceled: "宸插仠姝? })[status];
}

onMounted(load);
</script>

<template>
  <CloudToolCard
    v-show="visible"
    :enabled="config.enabled"
    name="JavSP 鎸夐渶鍏冩暟鎹埉鍓?
    driver="鐭敓鍛藉懆鏈?Docker 瀹瑰櫒"
    logo-alt="JavSP"
    :tags="[{ label: activeTask ? '浠诲姟杩愯涓? : '鎸夐渶鍚姩', variant: activeTask ? 'warn' : 'default' }]"
    :stat-value="activeTask ? statusLabel(activeTask.status) : '绌洪棽'"
    stat-label="JavSP 瀹瑰櫒鐘舵€?
  >
    <template #logo>
      <span class="javsp-logo">JS</span>
    </template>

    姣忔鍒墛鎵嶅垱寤?JavSP 瀹瑰櫒锛屽畬鎴愭垨鍙栨秷鍚庤嚜鍔ㄥ垹闄わ紱涓嶄細甯搁┗鍗犵敤鍐呭瓨銆?
    <template #details>
      <div class="javsp-form">
      <label>
        Docker 涓绘満濯掍綋鐩綍
        <input v-model.trim="config.host_media_dir" placeholder="渚嬪 /mnt/media" :disabled="loading" />
      </label>
      <label>
        瀹瑰櫒鍐呮寕杞界洰褰?        <input v-model.trim="config.container_media_dir" placeholder="/video" :disabled="loading" />
      </label>
      <label>
        JavSP 闀滃儚
        <input v-model.trim="config.image" placeholder="apecme/javsp-web:bata" :disabled="loading" />
      </label>
      <label>
        鍐呭瓨涓婇檺锛圡B锛? 涓轰笉闄愬埗锛?        <input v-model.number="config.memory_limit_mb" type="number" min="0" max="4096" :disabled="loading" />
      </label>
      <label class="javsp-enabled">
        <input v-model="config.enabled" type="checkbox" :disabled="loading" />
        鍚敤鎸夐渶鍒墛
      </label>
      <AppButton size="sm" :disabled="loading || saving" @click="save">
        {{ saving ? "淇濆瓨涓€? : "淇濆瓨璁剧疆" }}
      </AppButton>
      </div>

      <div class="javsp-start">
      <input
        v-model.trim="relativePath"
        placeholder="濯掍綋鐩綍涓嬬殑鐩稿璺緞锛涚暀绌哄垯鍒墛鏁翠釜鐩綍"
        :disabled="loading || starting || !config.enabled"
        @keyup.enter="start"
      />
      <AppButton size="sm" :disabled="loading || starting || !config.enabled" @click="start">
        {{ starting ? "鍒涘缓涓€? : "寮€濮嬪埉鍓? }}
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
          {{ canceling === task.id ? "鍋滄涓€? : "鍋滄" }}
        </AppButton>
        <details v-if="task.log" class="javsp-log">
          <summary>鏃ュ織</summary>
          <pre>{{ task.log }}</pre>
        </details>
      </div>
      <AppButton size="sm" variant="secondary" @click="refreshTasks">鍒锋柊浠诲姟</AppButton>
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

