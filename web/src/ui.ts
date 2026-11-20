export const escapeHtml = (value: unknown): string => String(value ?? '').replace(/[&<>\x27\"]/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[char] as string));
export function dateTime(value?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false });
}
/** dayTime writes a record's moment the way a notebook would: 11月20日 11:18. */
export function dayTime(value?: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const parts = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).formatToParts(date);
  const field = (type: string): string => parts.find(part => part.type === type)?.value ?? '';
  const month = field('month'), day = field('day'), hour = field('hour'), minute = field('minute');
  return month && day ? `${month}月${day}日 ${hour}:${minute}` : '';
}
/**
 * duration spells a span of real time in Chinese. Both ends must be genuine
 * timestamps: an absent or impossible pair prints nothing rather than a zero.
 */
export function duration(from?: string, to?: string): string {
  if (!from || !to) return '';
  const start = Date.parse(from), end = Date.parse(to);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return '';
  const seconds = Math.round((end - start) / 1000);
  if (seconds < 1) return '';
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60), rest = seconds % 60;
  if (minutes < 60) return rest ? `${minutes} 分 ${String(rest).padStart(2, '0')} 秒` : `${minutes} 分`;
  const hours = Math.floor(minutes / 60);
  return `${hours} 小时 ${minutes % 60} 分`;
}
export const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
export const count = (value: unknown): number => typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
/** cn formats a whole number the way the pages read it: 1,191. */
export const cn = (value: number): string => value.toLocaleString('zh-CN');
/**
 * deadline reads a classroom deadline as Beijing wall time and writes it the
 * way a notebook would: 12月31日 23:59. An unparseable value is shown as the
 * upstream wrote it rather than dropped.
 */
export function deadline(value?: string): string {
  const raw = String(value ?? '').trim();
  if (!raw) return '';
  const explicit = /^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?$/.test(raw) ? `${raw.replace(' ', 'T')}+08:00` : raw;
  const at = Date.parse(explicit);
  if (!Number.isFinite(at)) return raw;
  const parts = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).formatToParts(new Date(at));
  const field = (type: string): string => parts.find(part => part.type === type)?.value ?? '';
  const month = field('month'), day = field('day');
  return month && day ? `${month}月${day}日 ${field('hour')}:${field('minute')}` : raw;
}
const paths: Record<string, string> = {
  refresh: '<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3"/><path d="M19.5 4.5v4.6h-4.6"/>',
  search: '<circle cx="11" cy="11" r="6.5"/><path d="m16 16 4 4"/>',
  right: '<path d="m9.5 6 6 6-6 6"/>',
  down: '<path d="m6 9.5 6 6 6-6"/>',
  close: '<path d="M6.5 6.5 17.5 17.5M17.5 6.5 6.5 17.5"/>',
  tick: '<path d="M4 13.4c1.7 1.2 3.3 2.9 4.7 5.2.4.6 1.1.6 1.5 0 2.6-5.6 6.2-10.1 11.2-14.2"/>',
  bang: '<circle cx="12" cy="12" r="8.2"/><path d="M12 7.8v5.2M12 16.2v.3"/>',
  ring: '<circle cx="12" cy="12" r="7.5" stroke-dasharray="3 3.2"/>',
  book: '<path d="M5.5 5a2 2 0 0 1 2-2H18v15H7.5a2 2 0 0 0-2 2z"/><path d="M5.5 20a2 2 0 0 0 2 1H18v-3"/>',
  tasks: '<path d="M10 6.5h10M10 12h10M10 17.5h10"/><path d="m4 6.5 1.3 1.3L7.6 5.4M4 12l1.3 1.3 2.3-2.4M4 17.5l1.3 1.3 2.3-2.4"/>',
  chat: '<path d="M4.5 5.5h15v10h-9l-4.5 4v-4h-1.5z"/>',
  home: '<path d="M4.5 10.5 12 4.5l7.5 6V19.5h-5v-5h-5v5h-5z"/>',
  users: '<circle cx="9" cy="8.5" r="3.2"/><path d="M3.5 19.5a5.5 5.5 0 0 1 11 0"/><path d="M15.5 5.6a3 3 0 0 1 0 5.8M17.2 14a5.5 5.5 0 0 1 3.3 5.5"/>',
  doc: '<path d="M7 3.5h7.5L19 8v12.5H7z"/><path d="M14.5 3.5V8H19M10 12.5h6M10 16.5h6"/>',
  alert: '<path d="M12 4 21 19.5H3z"/><path d="M12 10v4.5M12 17.2v.3"/>',
  back: '<path d="m14.5 6-6 6 6 6"/>',
  grid: '<rect x="4" y="4" width="6.5" height="6.5" rx="1.6"/><rect x="13.5" y="4" width="6.5" height="6.5" rx="1.6"/><rect x="4" y="13.5" width="6.5" height="6.5" rx="1.6"/><rect x="13.5" y="13.5" width="6.5" height="6.5" rx="1.6"/>',
  out: '<path d="M14 4.5h4.5v15H14"/><path d="M10 16l-4-4 4-4M6 12h10"/>',
  teacher: '<path d="m2.5 8.5 9.5-4.5 9.5 4.5-9.5 4.5z"/><path d="M6.5 10.5v5c3 2.6 8 2.6 11 0v-5M21.5 8.5v7"/>',
};
export const icon = (name: string): string =>
  `<svg class="i" viewBox="0 0 24 24" aria-hidden="true">${paths[name] ?? paths.book}</svg>`;
