import type { SupportTicket, SupportTicketDetail, TicketCategory, TicketStatus } from './types.js';
import { dateTime, dayTime, escapeHtml as e, icon } from './ui.js';

export interface TicketState {
  items: SupportTicket[];
  detail?: SupportTicketDetail | null;
  loading: boolean;
  error?: string | null;
  filter?: 'all' | TicketStatus;
  nextCursor?: string;
  submitting?: boolean;
}

export const ticketCategoryLabels: Record<TicketCategory, string> = {
  task: '课程与任务', account: '账号使用', data: '数据与隐私', suggestion: '意见建议', other: '其他问题',
};
export const ticketStatusLabels: Record<TicketStatus, string> = {
  open: '待回复', processing: '处理中', resolved: '已解决', closed: '已关闭',
};
export const ticketPrivacyHint = '别写密码、Cookie 或密钥。工单只用于沟通，不进题库。';
const statusLabel = (status: string): string => ticketStatusLabels[status as TicketStatus] ?? '状态待确认';
const categoryLabel = (category: string): string => ticketCategoryLabels[category as TicketCategory] ?? '其他问题';
const plainLines = (value: string): string => e(value).replace(/\r\n?|\n/g, '<br>');
/** ticketResult states a ticket's standing with a mark, never colour alone. */
function ticketResult(ticket: SupportTicket): string {
  const known = Object.prototype.hasOwnProperty.call(ticketStatusLabels, ticket.status);
  const label = statusLabel(ticket.status);
  const replied = ticket.last_author_role === 'admin';
  if (!known) return `<span class="state muted res">${e(label)}</span>`;
  if (ticket.status === 'resolved') return `<span class="state done res"><span class="tick">${icon('tick')}</span>已解决</span>`;
  if (ticket.status === 'closed') return `<span class="state muted res">已关闭</span>`;
  if (ticket.status === 'processing') return `<span class="state run res"><span class="pulse"></span>处理中${replied ? ' · 已回复' : ''}</span>`;
  return `<span class="state res">${replied ? `${icon('chat')}已回复你` : '等客服回复'}</span>`;
}

function createForm(disabled: boolean, error: string): string {
  return `<section class="card" aria-labelledby="ticket-create-heading">
    <div class="panel-head"><h2 id="ticket-create-heading">写反馈</h2><span class="muted">课程、作业名称写清楚些</span></div>
    <form id="ticket-create-form" class="ticket-form">
      ${error ? `<div class="form-feedback" role="alert">${e(error)}</div>` : ''}
      <div class="ticket-form-row">
        <div class="field"><label for="ticket-subject">一句话说问题</label><input id="ticket-subject" name="subject" required maxlength="100" placeholder="例如：计算机网络第 3 关一直没完成" ${disabled ? 'disabled' : ''}></div>
        <div class="field"><label for="ticket-category">分类</label><select id="ticket-category" name="category" ${disabled ? 'disabled' : ''}>${Object.entries(ticketCategoryLabels).map(([value, label]) => `<option value="${value}">${label}</option>`).join('')}</select></div>
      </div>
      <div class="field"><label for="ticket-body">说清楚些</label><textarea id="ticket-body" name="body" required maxlength="5000" rows="5" placeholder="哪门课、哪份作业、看到了什么、希望我们做什么。" ${disabled ? 'disabled' : ''}></textarea></div>
      <div class="ticket-form-foot"><p class="muted">${e(ticketPrivacyHint)}</p><button class="btn red" type="submit" ${disabled ? 'disabled' : ''}>${disabled ? '正在发送…' : '发送'}</button></div>
    </form></section>`;
}

/** A pure renderer: authentication, requests, drafts and mutations are owned by the app shell. */
export function renderTickets(state: TicketState, admin: boolean): string {
  const filter = state.filter ?? 'all';
  const filters: Array<'all' | TicketStatus> = ['all', 'open', 'processing', 'resolved', 'closed'];
  const rows = state.items.map(ticket => `<li><button class="row" type="button" data-ticket-open="${e(ticket.id)}" aria-label="${e(`查看反馈：${ticket.subject}`)}">
    <span class="when num">${e(dayTime(ticket.updated_at) || dateTime(ticket.updated_at))}</span>
    <span class="what">${e(ticket.subject)}<span class="sub">${admin && ticket.user?.name ? `${e(ticket.user.name)} · ` : ''}${e(categoryLabel(ticket.category))} · ${e(String(ticket.message_count))} 条</span></span>
    ${ticketResult(ticket)}<span class="took"></span>${icon('right')}</button></li>`).join('');
  const empty = state.loading ? '正在读取反馈记录…' : filter === 'all' ? (admin ? '还没有用户反馈。' : '还没写过反馈。遇到问题就在上面说。') : '这个状态下没有记录。';
  return `<div class="head"><div><p class="date">${admin ? '反馈工单' : '问题反馈'}</p>
    <h1>${admin ? '用户反馈' : '有问题，直接告诉我们'}</h1>
    <p class="lead">${admin ? '看用户反馈，回复处理进展。' : '一般当天回复，回复后这里会提醒你。'}</p></div>
    <div class="actions"><button class="btn square" type="button" data-ticket-refresh aria-label="刷新记录" title="刷新记录" ${state.loading ? 'disabled' : ''}>${icon('refresh')}</button></div></div>
    ${state.error ? `<div class="form-feedback" role="alert">${e(state.error)}</div>` : ''}
    ${admin ? '' : createForm(Boolean(state.submitting), '')}
    <section class="card" aria-label="${admin ? '用户反馈记录' : '我的反馈'}">
      <div class="panel-head"><h2>${admin ? '全部反馈' : '我的反馈'}<span>${state.items.length} 条</span></h2>
        <div class="seg" role="group" aria-label="按状态筛选">${filters.map(value => `<button type="button" data-ticket-filter="${value}" class="${filter === value ? 'on' : ''}" aria-pressed="${filter === value}">${value === 'all' ? '全部' : ticketStatusLabels[value]}</button>`).join('')}</div></div>
      ${rows ? `<ul class="records">${rows}</ul>` : `<div class="empty"><h3>${e(empty)}</h3></div>`}
      ${state.nextCursor ? `<div class="panel-foot"><button type="button" class="btn" data-ticket-more="${e(state.nextCursor)}" ${state.loading ? 'disabled' : ''}>${state.loading ? '正在读取…' : '看更早的'}</button></div>` : ''}
    </section>`;
}

/** Message bodies always remain plain text; no markdown/HTML from a participant is executed. */
export function renderTicketDetail(detail: SupportTicketDetail, admin: boolean, submitting = false): string {
  const ticket = detail.ticket;
  const knownStatus = Object.prototype.hasOwnProperty.call(ticketStatusLabels, ticket.status);
  const actions = admin ? (Object.keys(ticketStatusLabels) as TicketStatus[]) : ticket.status === 'closed' || ticket.status === 'resolved' ? ['open', 'closed'] as TicketStatus[] : ['closed'] as TicketStatus[];
  const messages = detail.messages.map(message => `<article class="msg ${message.author_role === 'admin' ? 'admin' : ''}"><header><strong>${message.author_role === 'admin' ? '客服' : admin ? e(ticket.user?.name || '用户') : '我'}</strong><time datetime="${e(message.created_at)}">${e(dateTime(message.created_at))}</time></header><div class="msg-body">${plainLines(message.body)}</div></article>`).join('');
  return `<header class="sheet-head"><div class="eyebrow"><span class="tag">${e(categoryLabel(ticket.category))}</span>${e(statusLabel(ticket.status))}${admin && ticket.user?.name ? ` · ${e(ticket.user.name)}` : ''}</div>
    <h2 id="modal-title">${e(ticket.subject)}</h2>
    <p class="verdict">${e(dayTime(ticket.created_at) || dateTime(ticket.created_at))} 提出 · ${e(String(detail.messages.length))} 条记录
      <button type="button" class="link" data-ticket-detail-refresh="${e(ticket.id)}" ${submitting ? 'disabled' : ''}>${icon('refresh')}刷新</button></p></header>
    <div class="sheet-body ticket-body" data-ticket-detail="${e(ticket.id)}" aria-busy="${submitting}">
      <div class="thread">${messages || '<p class="muted">没有可显示的对话，刷新再看。</p>'}</div>
      ${knownStatus && ticket.status !== 'closed' ? `<form id="ticket-reply-form" data-ticket-id="${e(ticket.id)}" style="margin-top:16px">
        <div class="field"><label for="ticket-reply-body">${admin ? '回复用户' : '补充说明'}</label><textarea name="body" id="ticket-reply-body" required maxlength="5000" rows="4" ${submitting ? 'disabled' : ''} placeholder="${admin ? '写下答复和处理进展。' : '补充能帮我们定位的信息。'}"></textarea></div>
        <div class="ticket-form-foot"><p class="muted">${e(ticketPrivacyHint)}</p><button class="btn red" type="submit" ${submitting ? 'disabled' : ''}>${submitting ? '正在发送…' : admin ? '发送回复' : '发送补充'}</button></div></form>`
      : ticket.status === 'closed' ? '<p class="muted" style="margin-top:14px">这条已关闭，要继续聊可以重新打开。</p>' : ''}
    </div>
    <footer class="sheet-foot">${knownStatus ? actions.filter(status => status !== ticket.status).map(status => `<button class="btn" type="button" data-ticket-status="${status}" data-ticket-id="${e(ticket.id)}" ${submitting ? 'disabled' : ''}>${admin ? `标记为${ticketStatusLabels[status]}` : status === 'open' ? '重新打开' : '关闭'}</button>`).join('') : ''}<button class="btn" data-action="close-modal">关闭</button></footer>`;
}
