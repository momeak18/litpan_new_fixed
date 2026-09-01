import { http } from "./client";

export type LocalExtractTask = {
  task_id: string;
  account_id: number;
  source_file_id: string;
  source_file_name: string;
  target_parent_id: string;
  target_display_path: string;
  status: "queued" | "downloading" | "extracting" | "uploading" | "success" | "failed" | "canceled";
  progress: number;
  message: string;
  error?: string;
  uploaded_files: number;
  total_files: number;
  created_at: number;
  updated_at: number;
};

export const localExtractApi = {
  create(payload: {
    account_id: number;
    source_file_ids: string[];
    source_file_names: string[];
    target_parent_id: string;
    target_display_path: string;
  }) {
    return http.post<LocalExtractTask[]>("/files/local-extract/tasks", payload);
  },
  list(accountId?: number) {
    return http.get<LocalExtractTask[]>("/files/local-extract/tasks", { account_id: accountId });
  },
  remove(taskId: string) {
    return http.del<void>(`/files/local-extract/tasks/${encodeURIComponent(taskId)}`);
  },
};
