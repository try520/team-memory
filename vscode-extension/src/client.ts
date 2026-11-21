import * as http from 'http';
import * as https from 'https';
import { URL } from 'url';
import {
  HealthResp,
  ImportResp,
  MetaResp,
  ReadResp,
  RulesResp,
  ToolPayload,
  WriteResp,
} from './types';

export const PROTOCOL = '2025-06-18';
const CLIENT_NAME = 'team-memory-vscode';
const CLIENT_VERSION = '0.1.8';

/** 工具级错误（HTTP/JSON-RPC 都成功，result.isError=true）。 */
export class ToolError extends Error {
  readonly code: string;
  readonly hint: string;
  readonly payload: ToolPayload;
  readonly status: number;

  constructor(payload: ToolPayload, status = 0) {
    const code = payload.error || 'ERROR';
    super(`${code}: ${payload.message || code}`);
    this.name = 'ToolError';
    this.code = code;
    this.hint = payload.hint || '';
    this.payload = payload;
    this.status = status;
  }
}

/** 传输层 / 协议层错误。 */
export class HttpError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(status: number, body: unknown) {
    super(`HTTP ${status}${describe(body)}`);
    this.name = 'HttpError';
    this.status = status;
    this.body = body;
  }
}

function describe(body: unknown): string {
  if (body && typeof body === 'object') {
    const b = body as Record<string, unknown>;
    const hint = typeof b.hint === 'string' ? b.hint : typeof b.message === 'string' ? b.message : '';
    return hint ? `：${hint}` : '';
  }
  return '';
}

interface HttpResponse {
  status: number;
  headers: http.IncomingHttpHeaders;
  body: string;
}

interface RequestOptions {
  method?: string;
  headers?: Record<string, string | number>;
  body?: string | Buffer | null;
}

interface RpcResponse {
  jsonrpc?: string;
  id?: unknown;
  result?: Record<string, unknown>;
  error?: { code: number; message: string; data?: unknown };
}

export interface WriteArgs {
  path: string;
  content?: string;
  mode: 'create' | 'append' | 'replace' | 'reindex';
  expectedHash?: string;
  index?: { scene: string; examples: string[] };
}

function parseJSON(text: string): any {
  try {
    return JSON.parse(text);
  } catch {
    return { raw: text };
  }
}

/**
 * 只实现本服务用到的 MCP Streamable HTTP 子集：
 * initialize（取 Mcp-Session-Id）→ notifications/initialized → tools/call。
 */
export class MemoryClient {
  readonly serverUrl: string;
  readonly project: string;
  readonly timeout: number;
  private mcpSession = '';
  private ruleSession = '';
  private seq = 0;

  lastRules: RulesResp | null = null;
  serverVersion = '';

  constructor(opts: { serverUrl: string; project: string; timeout?: number }) {
    this.serverUrl = opts.serverUrl.replace(/\/+$/, '');
    this.project = opts.project;
    this.timeout = opts.timeout || 15000;
  }

  get mcpUrl(): string {
    return `${this.serverUrl}/mcp/${encodeURIComponent(this.project)}`;
  }

  get importUrl(): string {
    return `${this.serverUrl}/import/${encodeURIComponent(this.project)}`;
  }

  private headers(): Record<string, string> {
    const h: Record<string, string> = {
      'Content-Type': 'application/json',
      Accept: 'application/json, text/event-stream',
    };
    if (this.mcpSession) {
      h['Mcp-Session-Id'] = this.mcpSession;
      h['MCP-Protocol-Version'] = PROTOCOL;
    }
    return h;
  }

  private request(url: string, opts: RequestOptions = {}): Promise<HttpResponse> {
    return new Promise((resolve, reject) => {
      let u: URL;
      try {
        u = new URL(url);
      } catch {
        reject(new Error(`服务地址不合法：${url}`));
        return;
      }
      const isHttps = u.protocol === 'https:';
      const mod = isHttps ? https : http;
      const data = opts.body == null ? null : Buffer.isBuffer(opts.body) ? opts.body : Buffer.from(opts.body, 'utf8');
      const headers: Record<string, string | number> = { ...(opts.headers || {}) };
      if (data) {
        headers['Content-Length'] = data.length;
      }
      const req = mod.request(
        {
          protocol: u.protocol,
          hostname: u.hostname,
          port: u.port || (isHttps ? 443 : 80),
          path: u.pathname + u.search,
          method: opts.method || 'GET',
          headers,
        },
        (res) => {
          const chunks: Buffer[] = [];
          res.on('data', (c: Buffer) => chunks.push(c));
          res.on('end', () =>
            resolve({
              status: res.statusCode || 0,
              headers: res.headers,
              body: Buffer.concat(chunks).toString('utf8'),
            })
          );
        }
      );
      req.setTimeout(this.timeout, () => req.destroy(new Error(`请求超时（${this.timeout}ms）：${url}`)));
      req.on('error', reject);
      if (data) {
        req.write(data);
      }
      req.end();
    });
  }

  async health(): Promise<HealthResp> {
    const res = await this.request(`${this.serverUrl}/healthz`);
    const body = parseJSON(res.body);
    if (res.status !== 200) throw new HttpError(res.status, body);
    this.serverVersion = body.version || '';
    return body as HealthResp;
  }

  /** GET /mcp 是服务发现，返回项目列表。 */
  async meta(): Promise<MetaResp> {
    const res = await this.request(`${this.serverUrl}/mcp`);
    const body = parseJSON(res.body);
    if (res.status !== 200) throw new HttpError(res.status, body);
    this.serverVersion = body.version || this.serverVersion;
    return body as MetaResp;
  }

  async connect(): Promise<void> {
    const res = await this.request(this.mcpUrl, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify({
        jsonrpc: '2.0',
        id: ++this.seq,
        method: 'initialize',
        params: {
          protocolVersion: PROTOCOL,
          capabilities: {},
          clientInfo: { name: CLIENT_NAME, version: CLIENT_VERSION },
        },
      }),
    });
    const msg = parseJSON(res.body) as RpcResponse;
    if (res.status !== 200 || !msg.result) {
      const data = msg.error ? (msg.error.data as Record<string, unknown>) : msg;
      throw new HttpError(res.status, data);
    }
    const sid = res.headers['mcp-session-id'];
    this.mcpSession = Array.isArray(sid) ? sid[0] || '' : sid || '';
    if (!this.mcpSession) throw new Error('initialize 响应缺少 Mcp-Session-Id');
    await this.request(this.mcpUrl, {
      method: 'POST',
      headers: this.headers(),
      body: JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }),
    }).catch(() => undefined);
  }

  private async rpc(method: string, params: unknown): Promise<Record<string, unknown>> {
    if (!this.mcpSession) {
      await this.connect();
    }
    const payload = JSON.stringify({ jsonrpc: '2.0', id: ++this.seq, method, params });
    let res = await this.request(this.mcpUrl, { method: 'POST', headers: this.headers(), body: payload });
    if (res.status === 404) {
      // 会话过期或服务重启：重新 initialize 后重试一次。
      this.mcpSession = '';
      await this.connect();
      res = await this.request(this.mcpUrl, { method: 'POST', headers: this.headers(), body: payload });
    }
    const msg = parseJSON(res.body) as RpcResponse;
    if (msg.error) {
      const err = new Error(msg.error.message || 'JSON-RPC 错误') as Error & { code?: number };
      err.code = msg.error.code;
      throw err;
    }
    if (res.status !== 200) throw new HttpError(res.status, msg);
    if (!msg.result) throw new Error('响应缺少 result');
    return msg.result;
  }

  async callTool<T>(name: string, args: Record<string, unknown> = {}): Promise<T> {
    const result = await this.rpc('tools/call', { name, arguments: args });
    const payload = (result.structuredContent as ToolPayload) || contentPayload(result);
    if (result.isError) throw new ToolError(payload, 200);
    return payload as unknown as T;
  }

  async getRules(): Promise<RulesResp> {
    const rules = await this.callTool<RulesResp>('memory_get_rules');
    // 服务端把空切片序列化成 null，这里统一成空数组，省得调用方到处判空。
    rules.files = rules.files || [];
    rules.warnings = rules.warnings || [];
    rules.journal_index = rules.journal_index || [];
    this.ruleSession = rules.session;
    this.lastRules = rules;
    return rules;
  }

  private async ensureRuleSession(): Promise<string> {
    if (!this.ruleSession) await this.getRules();
    return this.ruleSession;
  }

  /** 读整篇：服务端单次最多 32KB，按 next_offset 续读。 */
  async readAll(rel: string): Promise<ReadResp> {
    let offset = 0;
    let content = '';
    let hash = '';
    let bytes = 0;
    let lines = 0;
    for (let page = 0; page < 64; page++) {
      const r = await this.callTool<ReadResp>('memory_read', offset ? { path: rel, offset } : { path: rel });
      content += r.content || '';
      hash = r.hash || hash;
      bytes = r.bytes || bytes;
      lines = r.lines || lines;
      if (!r.truncated || !r.next_offset) break;
      offset = r.next_offset;
    }
    return { path: rel, content, hash, bytes, lines, truncated: false };
  }

  async write(args: WriteArgs): Promise<WriteResp> {
    const attempt = async (): Promise<WriteResp> => {
      const session = await this.ensureRuleSession();
      const payload: Record<string, unknown> = {
        path: args.path,
        mode: args.mode,
        content: args.content ?? '',
        session,
      };
      if (args.expectedHash) payload.expectedHash = args.expectedHash;
      if (args.index) payload.index = args.index;
      return this.callTool<WriteResp>('memory_write', payload);
    };
    try {
      return await attempt();
    } catch (e) {
      const code = e instanceof ToolError ? e.code : '';
      if (code === 'READ_RULES_FIRST' || code === 'RULE_CHANGED') {
        this.ruleSession = '';
        return attempt();
      }
      throw e;
    }
  }

  /** 管理面批量导入（POST /import/<project>），不需要 session。 */
  async importFiles(payload: Record<string, unknown>): Promise<ImportResp> {
    const res = await this.request(this.importUrl, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(payload),
    });
    const body = parseJSON(res.body);
    if (res.status !== 200) throw new ToolError(body as ToolPayload, res.status);
    return body as ImportResp;
  }
}

function contentPayload(result: Record<string, unknown>): ToolPayload {
  const list = result.content as Array<{ text?: string }> | undefined;
  if (Array.isArray(list) && list[0] && typeof list[0].text === 'string') {
    const parsed = parseJSON(list[0].text);
    if (parsed && typeof parsed === 'object') return parsed as ToolPayload;
    return { text: list[0].text };
  }
  return {};
}
