import * as vscode from 'vscode';
import { RulesResp, HealthResp, IndexWarning, FileInfo } from './types';
import { formatTime, journalMonth, uriFor } from './util';

export interface TreeHost {
  projectName(): string;
  serverUrl(): string;
  state(): { rules: RulesResp | null; health: HealthResp | null; error: string };
  refresh(silent?: boolean): Promise<void>;
  showWarningDetail(warning: IndexWarning): void;
}

export interface TreeNode {
  item: vscode.TreeItem;
  children?: TreeNode[];
}

const WARNING_TITLES: Record<string, string> = {
  missing_in_index: '未登记到索引',
  missing_file: '索引指向的文件不存在',
  index_too_large: '索引过大',
  row_too_long: '索引行过长',
};

export function warningTitle(type: string): string {
  return WARNING_TITLES[type] || type;
}

/** 侧边栏树：一个根节点（项目）+ 规则 / 任务 / 主题 / journal（按月）/ 索引警告。 */
export class MemoryTreeProvider implements vscode.TreeDataProvider<TreeNode> {
  private readonly emitter = new vscode.EventEmitter<TreeNode | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  constructor(private readonly host: TreeHost) {}

  refresh(): void {
    this.emitter.fire(undefined);
  }

  getTreeItem(node: TreeNode): vscode.TreeItem {
    return node.item;
  }

  getChildren(node?: TreeNode): TreeNode[] {
    if (!node) return [this.buildRoot()];
    return node.children || [];
  }

  private buildRoot(): TreeNode {
    const project = this.host.projectName();
    if (!project) {
      const item = new vscode.TreeItem('未选择项目，点击选择', vscode.TreeItemCollapsibleState.None);
      item.iconPath = new vscode.ThemeIcon('folder');
      item.tooltip = '从服务的项目列表里选一个，或新建项目';
      item.command = { command: 'teamMemory.pickProject', title: '切换项目' };
      return { item };
    }

    const { rules, health, error } = this.host.state();
    if (!rules) {
      return { item: this.offlineItem(health, error) };
    }

    const files = rules.files || [];
    const topics = files.filter((f) => f.path.startsWith('topics/'));
    const journals = files.filter((f) => f.path.startsWith('journal/'));

    const root = new vscode.TreeItem(project, vscode.TreeItemCollapsibleState.Expanded);
    root.iconPath = new vscode.ThemeIcon('database');
    root.description = `${files.length} 个文件`;
    root.tooltip = this.rootTooltip(rules, health);
    root.contextValue = 'teamMemory.root';

    const children: TreeNode[] = [];
    if (health) children.push(this.healthNode(health));
    children.push(this.fileNode(this.infoOf(rules, 'RULE.md'), 'RULE.md', '规则与索引'));
    children.push(this.fileNode(this.infoOf(rules, 'TASKS.md'), 'TASKS.md', '任务'));
    children.push(this.serviceRuleNode());

    if (topics.length > 0) children.push(this.topicGroup(topics));
    if (journals.length > 0) children.push(this.journalGroup(journals));

    const warnings = rules.warnings || [];
    if (warnings.length > 0) {
      children.push(this.warningGroup(warnings));
    }

    return { item: root, children };
  }

  private offlineItem(health: HealthResp | null, error: string): vscode.TreeItem {
    const item = new vscode.TreeItem(
      error ? '连接失败，点击重试' : '未连接，点击连接',
      vscode.TreeItemCollapsibleState.None
    );
    item.iconPath = new vscode.ThemeIcon(error ? 'error' : 'plug');
    item.command = { command: 'teamMemory.connect', title: '连接服务' };
    item.tooltip = error || '检查地址是否正确：命令面板 → 团队记忆：配置服务地址';
    if (health) {
      item.description = `服务在运行（v${health.version}）`;
    }
    return item;
  }

  private rootTooltip(rules: RulesResp, health: HealthResp | null): vscode.MarkdownString {
    const md = new vscode.MarkdownString();
    md.appendMarkdown(`**项目** \`${rules.project}\`\n\n`);
    md.appendMarkdown(`- 服务：\`${this.host.serverUrl()}\`${health ? `（v${health.version}）` : ''}\n`);
    md.appendMarkdown(`- 上次写入：${formatTime(rules.last_write) || '暂无记录'}\n`);
    md.appendMarkdown(`- 文件：${rules.files.length} 个，索引警告：${rules.warnings.length} 条\n`);
    if (typeof rules.days_since_write === 'number' && rules.days_since_write >= 2) {
      md.appendMarkdown(`- 已有 ${rules.days_since_write} 天没记录结论\n`);
    }
    if (rules.record_reminder) {
      md.appendMarkdown(`\n> ${rules.record_reminder}`);
    }
    return md;
  }

  private healthNode(health: HealthResp): TreeNode {
    const item = new vscode.TreeItem('服务健康', vscode.TreeItemCollapsibleState.None);
    const hours = health.uptime_s >= 3600 ? `${(health.uptime_s / 3600).toFixed(1)} 小时` : `${Math.round(health.uptime_s / 60)} 分钟`;
    item.description = `v${health.version} · 运行 ${hours}`;
    item.iconPath = new vscode.ThemeIcon('pulse');
    item.tooltip = `pid ${health.pid} · ${health.projects} 个项目 · ${health.sessions} 个会话`;
    return { item };
  }

  private serviceRuleNode(): TreeNode {
    const item = new vscode.TreeItem('服务级规则（只读）', vscode.TreeItemCollapsibleState.None);
    item.iconPath = new vscode.ThemeIcon('law');
    item.command = { command: 'teamMemory.showServiceRules', title: '查看服务级规则' };
    item.tooltip = '所有项目共用的写入规范，来自 memory_get_rules';
    return { item };
  }

  private fileNode(info: FileInfo | undefined, rel: string, label?: string): TreeNode {
    const uri = uriFor(this.host.projectName(), rel);
    const item = new vscode.TreeItem(label || basename(rel), vscode.TreeItemCollapsibleState.None);
    item.resourceUri = uri;
    item.description = info ? `${info.lines} 行 · ${formatTime(info.updated)}` : '';
    item.tooltip = rel;
    item.iconPath = new vscode.ThemeIcon('markdown');
    item.command = { command: 'vscode.open', title: '打开', arguments: [uri] };
    item.contextValue = 'teamMemory.file';
    return { item };
  }

  private topicGroup(topics: FileInfo[]): TreeNode {
    const item = new vscode.TreeItem('主题', vscode.TreeItemCollapsibleState.Expanded);
    item.iconPath = new vscode.ThemeIcon('book');
    item.description = `${topics.length}`;
    const children = topics
      .sort((a, b) => a.path.localeCompare(b.path))
      .map((f) => this.fileNode(f, f.path, f.path.slice('topics/'.length).replace(/\.md$/, '')));
    return { item, children };
  }

  /** journal 按月分组，索引文件排在每月第一条。 */
  private journalGroup(journals: FileInfo[]): TreeNode {
    const months = new Map<string, FileInfo[]>();
    for (const f of journals) {
      const m = journalMonth(f.path);
      if (!months.has(m)) months.set(m, []);
      months.get(m)!.push(f);
    }
    const item = new vscode.TreeItem('Journal', vscode.TreeItemCollapsibleState.Collapsed);
    item.iconPath = new vscode.ThemeIcon('history');
    item.description = `${months.size} 个月/${journals.length} 篇`;

    const children: TreeNode[] = Array.from(months.keys())
      .sort()
      .reverse()
      .map((m) => {
        const list = months.get(m)!.sort((a, b) => a.path.localeCompare(b.path));
        const group = new vscode.TreeItem(m, vscode.TreeItemCollapsibleState.Collapsed);
        group.iconPath = new vscode.ThemeIcon('calendar');
        group.description = `${list.length} 篇`;
        return {
          item: group,
          children: list.map((f) => {
            const isIndex = f.path.endsWith('.INDEX.md');
            return this.fileNode(f, f.path, isIndex ? '索引' : basename(f.path).replace(/\.md$/, ''));
          }),
        };
      });
    return { item, children };
  }

  private warningGroup(warnings: IndexWarning[]): TreeNode {
    const item = new vscode.TreeItem('索引警告', vscode.TreeItemCollapsibleState.Expanded);
    item.iconPath = new vscode.ThemeIcon('warning', new vscode.ThemeColor('problemsWarningIcon.foreground'));
    item.description = `${warnings.length}`;
    const children: TreeNode[] = warnings.map((w) => {
      const node = new vscode.TreeItem(w.path || w.type, vscode.TreeItemCollapsibleState.None);
      node.iconPath = new vscode.ThemeIcon('warning');
      node.description = WARNING_TITLES[w.type] || w.type;
      node.tooltip = w.hint || '';
      node.command = { command: 'teamMemory.showWarningDetail', title: '查看警告', arguments: [w] };
      return { item: node };
    });
    return { item, children };
  }

  private infoOf(rules: RulesResp, rel: string): FileInfo | undefined {
    return (rules.files || []).find((f) => f.path === rel);
  }
}

function basename(rel: string): string {
  const slash = rel.lastIndexOf('/');
  return slash >= 0 ? rel.slice(slash + 1) : rel;
}
