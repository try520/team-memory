// 与服务端 JSON 字段一一对应；接口变了要同步改这里。

export interface FileInfo {
  path: string;
  lines: number;
  updated: string;
}

export interface IndexWarning {
  type: 'missing_in_index' | 'missing_file' | 'index_too_large' | 'row_too_long' | string;
  path?: string;
  rows?: number;
  hint?: string;
}

export interface JournalWindow {
  from: string;
  to: string;
  files: number;
}

export interface RulesResp {
  project: string;
  rule_hash: string;
  session: string;
  user_name?: string;
  user_name_source?: 'param' | 'header' | 'session';
  service_rule: string;
  index: string;
  files: FileInfo[];
  journal_index: string[];
  warnings: IndexWarning[];
  journal_window: JournalWindow;
  last_write?: string;
  days_since_write: number;
  record_reminder?: string;
}

export interface ReadResp {
  path: string;
  rev?: string;
  hash: string;
  lines: number;
  bytes: number;
  truncated: boolean;
  next_offset?: number;
  content: string;
}

export interface WriteResp {
  ok: boolean;
  path: string;
  hash: string;
  lines: number;
  index_updated: boolean;
  warnings?: IndexWarning[];
  hint?: string;
  next?: string;
}

export interface ImportResp {
  ok: boolean;
  project: string;
  dry_run: boolean;
  created: number;
  replaced: number;
  skipped: number;
  rule_written: boolean;
  index_added: number;
  session?: string;
  files?: Array<{ path: string; action: string; bytes?: number }>;
  warnings?: IndexWarning[];
  hint?: string;
}

export interface HealthResp {
  ok: boolean;
  projects: number;
  pid: number;
  version: string;
  uptime_s: number;
  sessions: number;
}

export interface MetaResp {
  service: string;
  version: string;
  protocol: string;
  projects: string[];
  tools: string[];
  endpoint: string;
  import: string;
  hint: string;
}

export interface ToolPayload {
  error?: string;
  message?: string;
  hint?: string;
  [key: string]: unknown;
}
