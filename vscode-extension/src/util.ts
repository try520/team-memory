import * as vscode from 'vscode';

export const SCHEME = 'team-memory';

/** 服务端项目 slug 只允许小写字母、数字与中划线。 */
export function slugify(name: string): string {
  return String(name || '')
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 60);
}

/** 文件名里去掉系统非法字符，保留中文，空格换中划线。 */
export function safeName(name: string, fallback = 'note'): string {
  const out = String(name || '')
    .replace(/[\\/:*?"<>|\r\n\t]/g, '')
    .trim()
    .replace(/\s+/g, '-')
    .slice(0, 80);
  return out || fallback;
}

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

export function nowParts(): { ym: string; hhmm: string; date: string } {
  const d = new Date();
  return {
    ym: `${d.getFullYear()}-${pad2(d.getMonth() + 1)}`,
    hhmm: `${pad2(d.getHours())}${pad2(d.getMinutes())}`,
    date: `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`,
  };
}

export function relOf(uri: vscode.Uri): string {
  return uri.path.replace(/^\/+/, '');
}

export function uriFor(project: string, rel: string): vscode.Uri {
  return vscode.Uri.from({ scheme: SCHEME, authority: project, path: '/' + rel });
}

export function formatTime(value?: string): string {
  if (!value) return '';
  const d = new Date(value);
  if (isNaN(d.getTime())) return '';
  const diff = Date.now() - d.getTime();
  const day = 24 * 3600 * 1000;
  if (diff < 3600 * 1000) return `${Math.max(1, Math.round(diff / 60000))} 分钟前`;
  if (diff < day) return `${Math.round(diff / 3600000)} 小时前`;
  if (diff < 45 * day) return `${Math.round(diff / day)} 天前`;
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}

/** journal/2026-11/xx.md → 2026-11；journal/2026-11.INDEX.md → 2026-11。 */
export function journalMonth(rel: string): string {
  const rest = rel.slice('journal/'.length);
  if (rest.endsWith('.INDEX.md')) return rest.slice(0, -'.INDEX.md'.length);
  const slash = rest.indexOf('/');
  return slash > 0 ? rest.slice(0, slash) : rest.replace(/\.md$/, '');
}

export function topicSkeleton(title: string): string {
  return `# ${title}

> 一句话定位：这个主题覆盖什么（≤40 字）

## 结论（现在就该这么做）
- 

## 为什么
- 

## 坑
1. 

## 代码位置
- 

## 相关
- 
`;
}
