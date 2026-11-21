import * as vscode from 'vscode';
import { MemoryClient, ToolError } from './client';
import { MemoryFileSystem, MemoryFsHost } from './memoryFs';
import { MemoryTreeProvider, TreeNode, TreeHost, warningTitle } from './tree';
import { HealthResp, IndexWarning, RulesResp } from './types';
import { SCHEME, nowParts, safeName, topicSkeleton, uriFor } from './util';

interface UiState {
  rules: RulesResp | null;
  health: HealthResp | null;
  error: string;
}

interface Config {
  serverUrl: string;
  autoConnect: boolean;
  timeout: number;
}

/** 当前项目只记在 workspaceState（每个工作区各一份），不写用户设置。 */
const PROJECT_STATE_KEY = 'teamMemory.currentProject';

/** 扩展主体：把 MemoryClient（MCP）、虚拟文件系统、树视图和命令串起来。 */
class TeamMemoryExtension implements vscode.Disposable, MemoryFsHost, TreeHost {
  private readonly disposables: vscode.Disposable[] = [];
  private readonly fs: MemoryFileSystem;
  private readonly tree: MemoryTreeProvider;
  private readonly view: vscode.TreeView<TreeNode>;
  private readonly status: vscode.StatusBarItem;

  private clientInstance: MemoryClient | null = null;
  private current: UiState = { rules: null, health: null, error: '' };
  private refreshing: Promise<void> | null = null;
  private refreshTimer: ReturnType<typeof setTimeout> | undefined;
  /** 当前项目 slug；空串表示还没选过。 */
  private project = '';
  /** 项目切换时递增，用来丢弃切换前发出的刷新结果。 */
  private projectEpoch = 0;

  constructor(private readonly context: vscode.ExtensionContext) {
    this.project = context.workspaceState.get<string>(PROJECT_STATE_KEY) || '';
    this.fs = new MemoryFileSystem(this);
    this.tree = new MemoryTreeProvider(this);
    this.view = vscode.window.createTreeView('teamMemory.view', {
      treeDataProvider: this.tree,
    });
    this.view.description = this.project || '未选择项目';
    this.status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 80);
    this.status.command = 'teamMemory.refresh';
    this.status.show();

    this.disposables.push(
      this.view,
      this.status,
      vscode.workspace.registerFileSystemProvider(SCHEME, this.fs, { isCaseSensitive: false })
    );
  }

  start(): void {
    const bind = (id: string, fn: (...args: any[]) => Promise<void> | void) => {
      this.disposables.push(
        vscode.commands.registerCommand(id, async (...args: any[]) => {
          try {
            await fn(...args);
          } catch (e) {
            this.reportError(e);
          }
        })
      );
    };

    bind('teamMemory.refresh', () => this.refresh(false));
    bind('teamMemory.connect', () => this.refresh(false));
    bind('teamMemory.pickProject', () => this.pickProject());
    bind('teamMemory.newTopic', () => this.newTopic());
    bind('teamMemory.appendJournal', () => this.appendJournal());
    bind('teamMemory.appendTask', () => this.appendTask());
    bind('teamMemory.reindex', () => this.reindex());
    bind('teamMemory.showWarnings', () => this.showWarnings());
    bind('teamMemory.showServiceRules', () => this.showServiceRules());
    bind('teamMemory.showSkill', (path: string) => this.showSkill(path));
    bind('teamMemory.showWarningDetail', (w: IndexWarning) => this.showWarningDetail(w));
    bind('teamMemory.configure', () => this.configure());

    this.disposables.push(
      vscode.workspace.onDidChangeConfiguration((e) => {
        if (!e.affectsConfiguration('teamMemory')) return;
        this.clientInstance = null;
        void this.refresh(true);
      })
    );

    if (!this.project) {
      // 这个工作区还没选过项目：先弹一次选择。
      this.showNoProjectStatus();
      void this.pickProject();
    } else if (this.config().autoConnect) {
      void this.refresh(true);
    } else {
      this.setIdleStatus();
    }
  }

  dispose(): void {
    if (this.refreshTimer) clearTimeout(this.refreshTimer);
    for (const d of this.disposables) d.dispose();
  }

  // ---- 配置 ----

  private config(): Config {
    const c = vscode.workspace.getConfiguration('teamMemory');
    return {
      serverUrl: (c.get<string>('serverUrl') || 'http://127.0.0.1:8080').trim().replace(/\/+$/, ''),
      autoConnect: c.get<boolean>('autoConnect') ?? true,
      timeout: c.get<number>('requestTimeoutMs') || 15000,
    };
  }

  projectName(): string {
    return this.project;
  }

  client(): MemoryClient {
    const c = this.config();
    const project = this.projectName();
    if (
      !this.clientInstance ||
      this.clientInstance.serverUrl !== c.serverUrl ||
      this.clientInstance.project !== project
    ) {
      this.clientInstance = new MemoryClient({
        serverUrl: c.serverUrl, project, timeout: c.timeout,
      });
    }
    return this.clientInstance;
  }

  serverUrl(): string {
    return this.config().serverUrl;
  }

  state(): { rules: RulesResp | null; health: HealthResp | null; error: string } {
    return this.current;
  }

  knownPaths(): string[] {
    const files = this.current.rules?.files?.map((f) => f.path) || [];
    return Array.from(new Set([...files, 'RULE.md', 'TASKS.md']));
  }

  onDidWrite(): void {
    // 保存后服务端可能插入索引行，稍等再刷新，避免和保存请求抢时序。
    if (this.refreshTimer) clearTimeout(this.refreshTimer);
    this.refreshTimer = setTimeout(() => {
      this.refreshTimer = undefined;
      void this.refresh(true);
    }, 400);
  }

  // ---- 连接与刷新 ----

  refresh(silent = false): Promise<void> {
    if (this.refreshing) return this.refreshing;
    const epoch = this.projectEpoch;
    this.refreshing = this.doRefresh(silent, epoch).finally(() => {
      // 切换项目时 setProject 已经把 refreshing 清空，这里不要覆盖新刷新。
      if (epoch === this.projectEpoch) this.refreshing = null;
    });
    return this.refreshing;
  }

  private async doRefresh(silent: boolean, epoch: number): Promise<void> {
    if (!this.project) {
      this.current = { rules: null, health: null, error: '' };
      this.showNoProjectStatus();
      this.tree.refresh();
      return;
    }
    const client = this.client();
    this.status.command = 'teamMemory.refresh';
    this.status.text = '$(sync~spin) 团队记忆';
    this.status.backgroundColor = undefined;
    this.status.tooltip = `正在连接 ${client.serverUrl}`;
    try {
      const health = await client.health().catch(() => null);
      const rules = await client.getRules();
      if (epoch !== this.projectEpoch) return; // 结果属于切换前的项目，丢弃
      this.current = { rules, health, error: '' };
      this.status.text = `$(database) ${rules.project}`;
      this.status.backgroundColor = undefined;
      this.status.tooltip = `${client.serverUrl} · ${rules.files.length} 个文件 · ${rules.warnings.length} 条索引警告`;
    } catch (e) {
      if (epoch !== this.projectEpoch) return;
      this.current = { rules: null, health: null, error: messageOf(e) };
      this.setOfflineStatus(messageOf(e));
      if (!silent) this.reportError(e);
    }
    this.tree.refresh();
  }

  private setOfflineStatus(reason: string): void {
    this.status.command = 'teamMemory.refresh';
    this.status.text = this.project ? `$(database) ${this.project}：未连接` : '$(database) 团队记忆：未连接';
    this.status.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
    this.status.tooltip = reason || '点击连接';
  }

  private setIdleStatus(): void {
    this.status.command = 'teamMemory.refresh';
    this.status.text = `$(database) ${this.project}`;
    this.status.backgroundColor = undefined;
    this.status.tooltip = `${this.serverUrl()} · 点击连接`;
  }

  private showNoProjectStatus(): void {
    this.status.command = 'teamMemory.pickProject';
    this.status.text = '$(database) 团队记忆：未选择项目';
    this.status.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
    this.status.tooltip = '点击选择项目';
  }

  reportError(e: unknown): void {
    const message = messageOf(e);
    const hint = e instanceof ToolError && e.hint ? `（${e.hint}）` : '';
    void vscode.window
      .showErrorMessage(`团队记忆：${message}${hint}`, '重试', '配置')
      .then((choice) => {
        if (choice === '重试') void this.refresh(false);
        if (choice === '配置') void this.configure();
      });
  }

  private async configure(): Promise<void> {
    await vscode.commands.executeCommand(
      'workbench.action.openSettings',
      `@ext:${this.context.extension.id}`,
    );
  }

  /** 切换当前项目：只写入 workspaceState（每个工作区各自记忆），不碰用户设置。 */
  private async setProject(slug: string): Promise<void> {
    if (slug === this.project) return;
    const previous = this.project;
    this.project = slug;
    this.projectEpoch++;
    this.refreshing = null;
    this.clientInstance = null;
    this.current = { rules: null, health: null, error: '' };
    this.fs.reset();
    await this.context.workspaceState.update(PROJECT_STATE_KEY, slug);
    this.view.description = slug;

    if (previous) {
      const stale = vscode.workspace.textDocuments.filter(
        (d) => d.uri.scheme === SCHEME && d.uri.authority === previous
      );
      if (stale.length > 0) {
        void vscode.window.showWarningMessage(
          `已切到项目 ${slug}：还有 ${stale.length} 个属于 ${previous} 的文档处于打开状态，保存会被拒绝，请从左侧树重新打开。`
        );
      }
    }
  }

  // ---- 命令 ----

  private async pickProject(): Promise<void> {
    const client = this.client();
    const meta = await client.meta();
    const current = this.projectName();
    const items: Array<vscode.QuickPickItem & { slug?: string }> = (meta.projects || []).map((p) => ({
      label: p,
      description: p === current ? '当前' : '',
      slug: p,
    }));
    items.push({ label: '$(add) 新建项目…', slug: '__new__' });

    const pick = await vscode.window.showQuickPick(items, {
      placeHolder: `服务 ${client.serverUrl} 上的项目（${(meta.projects || []).length} 个）`,
    });
    if (!pick) return;

    let slug = pick.slug || pick.label;
    if (slug === '__new__') {
      const value = await vscode.window.showInputBox({
        prompt: '新项目 slug（小写字母、数字、中划线）',
        placeHolder: '例如 payment-service',
        ignoreFocusOut: true,
        validateInput: (v) =>
          /^[a-z0-9][a-z0-9-]{0,63}$/.test(v.trim()) ? undefined : '只允许小写字母、数字与中划线',
      });
      if (!value) return;
      slug = value.trim();
    }

    await this.setProject(slug);
    await this.refresh(true);
  }

  private async newTopic(): Promise<void> {
    const title = await ask('主题标题', '', '例如：支付回调幂等', (v) =>
      v.trim() ? undefined : '不能为空'
    );
    if (!title) return;

    const scene = await ask(
      '适用场景：什么情况下必须先读这篇？',
      '',
      '涉及渠道回调、重复扣款、幂等键的改动前必读',
      (v) => (v.trim().length >= 6 ? undefined : '写具体一点，至少 6 个字')
    );
    if (!scene) return;

    const exampleText = await ask(
      '举例：同事会怎么问？（逗号分隔，1~3 条）',
      '',
      '回调为什么会重复扣款？, 幂等键怎么定？',
      (v) => (v.trim() ? undefined : '至少给一条')
    );
    if (!exampleText) return;

    const defaultPath = `topics/${safeName(title)}.md`;
    const rel = await ask('文件路径', defaultPath, '', (v) =>
      /^topics\/[^/]+\.md$/.test(v.trim()) ? undefined : '形如 topics/xxx.md'
    );
    if (!rel) return;

    const examples = exampleText
      .split(/[,，;；]/)
      .map((s) => s.trim())
      .filter(Boolean)
      .slice(0, 5);

    await this.client().write({
      path: rel.trim(),
      content: topicSkeleton(title.trim()),
      mode: 'create',
      index: { scene: scene.trim(), examples },
    });

    void vscode.window.showInformationMessage(`已创建 ${rel.trim()}，并写入索引行。`);
    await this.refresh(true);
    const doc = await vscode.workspace.openTextDocument(uriFor(this.projectName(), rel.trim()));
    await vscode.window.showTextDocument(doc, { preview: false });
  }

  private async appendJournal(): Promise<void> {
    const label = await ask('这次做了什么（一句话，用作文件名）', '', '修复支付回调重复扣款', (v) =>
      v.trim() ? undefined : '不能为空'
    );
    if (!label) return;

    const note =
      (await vscode.window.showInputBox({
        prompt: '补充说明（可空）',
        placeHolder: '结论、影响范围、后续动作',
        ignoreFocusOut: true,
      })) || '';

    const { ym } = nowParts();
    const base = `journal/${ym}/${safeName(label)}`;
    let written = '';

    for (let i = 0; i < 5 && !written; i++) {
      const rel = `${base}${i === 0 ? '' : `-${i + 1}`}.md`;
      try {
        const resp = await this.client().write({
          path: rel,
          content: `# ${label.trim()}\n\n${note.trim()}\n`,
          mode: 'create',
        });
        // 服务端会把 journal 文件名改为 <姓名或会话标签>-<标题>-<hhmmss>.md，以返回的 path 为准。
        written = resp.path || rel;
      } catch (e) {
        if (e instanceof ToolError && e.code === 'ALREADY_EXISTS') continue;
        throw e;
      }
    }
    if (!written) throw new Error('同一秒已有同名 journal，换个标题再试。');

    void vscode.window.showInformationMessage(`已写入 ${written}`);
    await this.refresh(true);
  }

  private async appendTask(): Promise<void> {
    const text = await ask('任务内容', '', '补回调重试测试', (v) => (v.trim() ? undefined : '不能为空'));
    if (!text) return;
    await this.client().write({ path: 'TASKS.md', content: `- [ ] ${text.trim()}\n`, mode: 'append' });
    void vscode.window.showInformationMessage('已追加到 TASKS.md');
    await this.refresh(true);
  }

  private async reindex(): Promise<void> {
    const resp = await this.client().write({ path: 'RULE.md', mode: 'reindex' });
    if (resp.index_updated) {
      void vscode.window.showInformationMessage(`已补索引行。${resp.hint || ''}`);
    } else {
      void vscode.window.showInformationMessage('索引与文件一致，不需要补行。');
    }
    await this.refresh(true);
  }

  private async showWarnings(): Promise<void> {
    if (!this.current.rules) await this.refresh(true);
    const rules = this.current.rules;
    if (!rules) {
      void vscode.window.showWarningMessage('尚未连接服务。');
      return;
    }
    if (!rules.warnings.length) {
      void vscode.window.showInformationMessage(`索引检查通过：${rules.files.length} 个文件，0 条警告。`);
      return;
    }
    const items = rules.warnings.map((w) => ({
      label: w.path || w.type,
      description: warningTitle(w.type),
      detail: w.hint,
      warning: w,
    }));
    const pick = await vscode.window.showQuickPick(items, {
      placeHolder: `${rules.warnings.length} 条索引警告`,
    });
    if (pick) this.showWarningDetail(pick.warning);
  }

  showWarningDetail(w: IndexWarning): void {
    const lines = [`类型：${warningTitle(w.type)}（${w.type}）`];
    if (w.path) lines.push(`文件：${w.path}`);
    if (typeof w.rows === 'number') lines.push(`行数：${w.rows}`);
    if (w.hint) lines.push('', w.hint);
    void vscode.window.showInformationMessage(lines.join('\n'), { modal: true });
  }

  private async showServiceRules(): Promise<void> {
    if (!this.current.rules) await this.refresh(true);
    const text = this.current.rules?.service_rule || '（服务未返回 service_rule）';
    const doc = await vscode.workspace.openTextDocument({ content: text, language: 'markdown' });
    await vscode.window.showTextDocument(doc, { preview: true });
  }

  /** 全局技能只读：用 memory_read 取正文，开一个 untitled 预览。 */
  private async showSkill(path: string): Promise<void> {
    const resp = await this.client().readAll(path);
    const doc = await vscode.workspace.openTextDocument({ content: resp.content, language: 'markdown' });
    await vscode.window.showTextDocument(doc, { preview: true });
  }
}

function ask(
  prompt: string,
  value: string,
  placeHolder: string,
  validate?: (v: string) => string | undefined
): Thenable<string | undefined> {
  return vscode.window.showInputBox({
    prompt,
    value,
    placeHolder,
    ignoreFocusOut: true,
    validateInput: (v) => (validate ? validate(v) : undefined),
  });
}

function messageOf(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

export function activate(context: vscode.ExtensionContext): void {
  const ext = new TeamMemoryExtension(context);
  context.subscriptions.push(ext);
  ext.start();
}

export function deactivate(): void {
  // 资源在 dispose() 里释放
}
