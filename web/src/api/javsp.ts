import { http } from "@/api/client";

export type JavSPConfig = {
  enabled: boolean;
  host_media_dir: string;
  container_media_dir: string;
  image: string;
  memory_limit_mb: number;
};

export type JavSPTask = {
  id: string;
  relative_path: string;
  status: "queued" | "running" | "success" | "failed" | "canceled";
  message: string;
  error?: string;
  log?: string;
  created_at: number;
  updated_at: number;
};

export const javspApi = {
  getConfig: () => http.get<JavSPConfig>("/tools/javsp/config"),
  setConfig: (body: JavSPConfig) => http.put<JavSPConfig>("/tools/javsp/config", body),
  listTasks: () => http.get<JavSPTask[]>("/tools/javsp/tasks"),
  createTask: (relativePath: string) =>
    http.post<JavSPTask>("/tools/javsp/tasks", { relative_path: relativePath }),
  cancelTask: (id: string) => http.post<{ ok: boolean }>(`/tools/javsp/tasks/${id}/cancel`),
};

