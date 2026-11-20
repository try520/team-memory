import * as vscode from 'vscode';
import { MemoryClient, ToolError } from './client';
import { relOf } from './util';

export interface MemoryFsHost {
  client(): MemoryClient;
  /** 当前项目 slug，用作虚拟文件系统的 authority。 */
  projectName(): string;
  /** 树里已知的文件路径，用于快速判断文件是否存在。 */
  knownPaths(): string[];
  /** 保存成功后通知外层刷新索引与树。 */
  onDidWrite(rel: string): void;
}

/**
 * 把云端记忆挂成可编辑的只读/可写文件系统（scheme: team-memory）：
 * 打开 = memory_read 全文；保存 = memory_write（replace 带 hash，冲突可覆盖或重新加载）。
 */
export class MemoryFileSystem implements vscode.FileSystemProvider {
  private readonly emitter = new vscode.EventEmitter<vscode.FileChangeEvent[]>();
  readonly onDidChangeFile = this.emitter.event;

  private readonly hashes = new Map<string, string>();
  private readonly seen = new Set<string>();

  constructor(private readonly host: MemoryFsHost) {}

  watch(): vscode.Disposable {
    return new vscode.Disposable(() => undefined);
  }

  /** 切换项目后清空缓存：hash 与已读路径都只对旧项目有效。 */
  reset(): void {
    this.hashes.clear();
    this.seen.clear();
  }

  private isKnown(rel: string): boolean {
    return this.seen.has(rel) || this.host.knownPaths().includes(rel);
  }

  private remember(rel: string, hash?: string): void {
    this.seen.add(rel);
    if (hash) this.hashes.set(rel, hash);
    this.emitter.fire([{ type: vscode.FileChangeType.Changed, uri: this.uriFor(rel) }]);
  }

  private uriFor(rel: string): vscode.Uri {
    return vscode.Uri.from({ scheme: 'team-memory', authority: this.host.projectName(), path: '/' + rel });
  }

  /** 切换项目后仍开着的旧文档不能写回新项目：路径相同会造成跨项目串写。 */
  private assertProject(uri: vscode.Uri): void {
    const want = this.host.projectName();
    if (uri.authority && uri.authority !== want) {
      throw vscode.FileSystemError.NoPermissions(
        `该文件属于项目 ${uri.authority}，当前项目已切到 ${want}。请从左侧树里重新打开。`
      );
    }
  }

  async stat(uri: vscode.Uri): Promise<vscode.FileStat> {
    this.assertProject(uri);
    const rel = relOf(uri);
    if (!this.isKnown(rel)) {
      await this.host.client().readAll(rel);
      this.seen.add(rel);
    }
    return { type: vscode.FileType.File, ctime: 0, mtime: Date.now(), size: 0 };
  }

  async readFile(uri: vscode.Uri): Promise<Uint8Array> {
    this.assertProject(uri);
    const rel = relOf(uri);
    try {
      const r = await this.host.client().readAll(rel);
      this.seen.add(rel);
      this.hashes.set(rel, r.hash);
      return Buffer.from(r.content, 'utf8');
    } catch (e) {
      throw toFsError(e, uri);
    }
  }

  async writeFile(uri: vscode.Uri, content: Uint8Array): Promise<void> {
    this.assertProject(uri);
    const rel = relOf(uri);
    const text = Buffer.from(content).toString('utf8');
    const client = this.host.client();

    let expected = this.hashes.get(rel);
    if (!expected) {
      try {
        const cur = await client.readAll(rel);
        expected = cur.hash;
        this.seen.add(rel);
      } catch (e) {
        if (!isNotFound(e)) throw toFsError(e, uri);
      }
    }

    try {
      const resp = await client.write({
        path: rel,
        content: text,
        mode: expected ? 'replace' : 'create',
        expectedHash: expected,
      });
      this.remember(rel, resp.hash);
      this.host.onDidWrite(rel);
    } catch (e) {
      const err = await this.handleWriteError(e, uri, rel, text);
      if (err) throw err;
    }
  }

  /**
   * 冲突时不静默覆盖：让用户选择覆盖服务器版本，或重新加载服务器版本。
   * 返回 null 表示已经处理并写入成功，返回 Error 表示本次保存失败。
   */
  private async handleWriteError(e: unknown, uri: vscode.Uri, rel: string, text: string): Promise<Error | null> {
    const code = e instanceof ToolError ? e.code : '';
    if (code === 'HASH_MISMATCH' || code === 'EXPECTED_HASH_REQUIRED') {
      const choice = await vscode.window.showWarningMessage(
        `「${rel}」已被其他人修改，本次保存未生效。`,
        { modal: true },
        '用我的覆盖',
        '重新加载服务器版本'
      );
      if (choice === '用我的覆盖') {
        try {
          const latest = await this.host.client().readAll(rel);
          const resp = await this.host.client().write({
            path: rel,
            content: text,
            mode: 'replace',
            expectedHash: latest.hash,
          });
          this.remember(rel, resp.hash);
          this.host.onDidWrite(rel);
          void vscode.window.showInformationMessage(`已覆盖服务器上的「${rel}」。`);
          return null;
        } catch (e2) {
          return toFsError(e2, uri);
        }
      }
      if (choice === '重新加载服务器版本') {
        await vscode.commands.executeCommand('workbench.action.files.revert');
        return new Error(`${rel} 已取回服务器最新内容，本次修改未保存。`);
      }
      return new Error(`${rel} 未保存：服务器内容已变更。`);
    }
    if (code === 'INDEX_ROW_REQUIRED') {
      void vscode.window.showWarningMessage(
        '新建主题必须同时提供索引行（场景 + 举例）。请用命令「团队记忆：新建主题」创建。'
      );
      return toFsError(e, uri);
    }
    return toFsError(e, uri);
  }

  async readDirectory(uri: vscode.Uri): Promise<[string, vscode.FileType][]> {
    const rel = relOf(uri);
    const prefix = rel ? rel.replace(/\/+$/, '') + '/' : '';
    const out = new Map<string, vscode.FileType>();
    const paths = new Set<string>([...this.seen, ...this.host.knownPaths()]);
    for (const p of paths) {
      if (!p.startsWith(prefix)) continue;
      const rest = p.slice(prefix.length);
      if (!rest) continue;
      const slash = rest.indexOf('/');
      const name = slash >= 0 ? rest.slice(0, slash) : rest;
      out.set(name, slash >= 0 ? vscode.FileType.Directory : vscode.FileType.File);
    }
    return Array.from(out.entries());
  }

  async createDirectory(uri: vscode.Uri): Promise<void> {
    throw vscode.FileSystemError.NoPermissions('云端记忆不支持新建目录');
  }

  async delete(uri: vscode.Uri): Promise<void> {
    throw vscode.FileSystemError.NoPermissions('云端记忆不支持删除文件；索引行需人工编辑 RULE.md');
  }

  async rename(): Promise<void> {
    throw vscode.FileSystemError.NoPermissions('云端记忆不支持重命名');
  }
}

function isNotFound(e: unknown): boolean {
  return e instanceof ToolError && e.code === 'NOT_FOUND';
}

function toFsError(e: unknown, uri: vscode.Uri): Error {
  if (e instanceof ToolError) {
    if (e.code === 'NOT_FOUND') return vscode.FileSystemError.FileNotFound(uri);
    if (e.code === 'PATH_NOT_ALLOWED') return vscode.FileSystemError.NoPermissions(e.message);
    return e;
  }
  return e instanceof Error ? e : new Error(String(e));
}
