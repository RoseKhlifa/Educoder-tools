import type { SupportTicket, SupportTicketDetail, TicketCategory, TicketStatus } from './types.js';
import { dateTime, escapeHtml as e, icon } from './ui.js';

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
export const ticketPrivacyHint = '请勿填写密码、Cookie或密钥；工单用于反馈跟进，不进入题库。';
const statusLabel = (status: string): string => ticketStatusLabels[status as TicketStatus] ?? '状态待确认';
const categoryLabel = (category: string): string => ticketCategoryLabels[category as TicketCategory] ?? '其他问题';
const plainLines = (value: string): string => e(value).replace(/\r\n?|\n/g, '<br>');
function statusTag(status: string): string {
  const known = Object.prototype.hasOwnProperty.call(ticketStatusLabels, status);
  return `<span class="ticket-status ticket-status-${known ? e(status) : 'unknown'}">${e(statusLabel(status))}</span>`;
}

function createForm(disabled: boolean): string {
  return `<section class="panel-box ticket-compose" aria-labelledby="ticket-create-heading">
    <div class="panel-toolbar"><h2 id="ticket-create-heading">提交问题</h2><span class="muted">服务人员会在这里回复</span></div>
    <form id="ticket-create-form" class="panel-body ticket-form">
      <div class="ticket-form-row"><div class="field"><label for="ticket-subject">问题标题</label><input id="ticket-subject" name="subject" required maxlength="100" placeholder="用一句话描述你遇到的问题" ${disabled ? 'disabled' : ''}></div>
      <div class="field"><label for="ticket-category">问题分类</label><select id="ticket-category" name="category" ${disabled ? 'disabled' : ''}>${Object.entries(ticketCategoryLabels).map(([value, label]) => `<option value="${value}">${label}</option>`).join('')}</select></div></div>
      <div class="field"><label for="ticket-body">详细说明</label><textarea id="ticket-body" name="body" required maxlength="5000" rows="5" placeholder="请写明课程、作业名称、遇到的情况及希望我们协助的事项。" ${disabled ? 'disabled' : ''}></textarea></div>
      <div class="ticket-form-footer"><p class="muted ticket-privacy-hint">${e(ticketPrivacyHint)}</p><button class="button primary" type="submit" ${disabled ? 'disabled' : ''}>${disabled ? '正在提交…' : '提交反馈'}</button></div>
    </form></section>`;
}

/** A pure renderer: authentication, requests, drafts and mutations are owned by the app shell. */
export function renderTickets(state: TicketState, admin: boolean): string {
  const filter = state.filter ?? 'all';
  const filters: Array<'all' | TicketStatus> = ['all', 'open', 'processing', 'resolved', 'closed'];
  const rows = state.items.map(ticket => `<tr>
    ${admin ? `<td class="ticket-user-cell"><span class="cell-strong">${e(ticket.user?.name || '用户姓名未提供')}</span></td>` : ''}
    <td class="ticket-subject-cell"><button class="text-button ticket-subject" type="button" data-ticket-open="${e(ticket.id)}">${e(ticket.subject)}</button><div class="cell-meta">${e(categoryLabel(ticket.category))} · ${e(ticket.message_count)} 条记录${ticket.last_author_role === 'admin' ? ' · 服务人员已回复' : ''}</div></td>
    <td>${statusTag(ticket.status)}</td><td class="ticket-time-cell"><time datetime="${e(ticket.updated_at)}">${e(dateTime(ticket.updated_at))}</time></td>
    <td><button class="text-button" type="button" data-ticket-open="${e(ticket.id)}" aria-label="查看反馈：${e(ticket.subject)}">查看${icon('arrow')}</button></td>
  </tr>`).join('');
  const empty = state.loading ? '正在读取反馈记录…' : filter === 'all' ? '还没有反馈记录。遇到问题时，可以在这里联系我们。' : '这个状态下暂时没有反馈记录。';
  return `<div class="ticket-workspace ${admin ? 'admin-ticket-workspace' : 'consumer-ticket-workspace'}">
    <div class="page-head"><div><p class="kicker">${admin ? 'SUPPORT DESK' : 'WE ARE HERE'}</p><h1>${admin ? '反馈工单' : '问题反馈'}</h1><p class="page-subtitle">${admin ? '查看用户反馈，回复处理进展，及时跟进每一个问题。' : '遇到问题，留个消息。你可以在这里查看回复并继续补充情况。'}</p></div><div class="head-actions"><button type="button" class="button" data-ticket-refresh ${state.loading ? 'disabled' : ''}>${icon('refresh')}刷新记录</button></div></div>
    ${state.error ? `<div class="notice error" role="alert">${e(state.error)}</div>` : ''}
    ${admin ? '' : createForm(Boolean(state.submitting))}
    <section class="panel-box ticket-list" aria-label="${admin ? '用户反馈记录' : '我的反馈记录'}">
      <div class="panel-toolbar"><h2>${admin ? '所有反馈' : '我的反馈'}</h2><div class="filter-seg ticket-filters" role="group" aria-label="按工单状态筛选">${filters.map(value => `<button type="button" data-ticket-filter="${value}" class="${filter === value ? 'active' : ''}" aria-pressed="${filter === value}">${value === 'all' ? '全部' : ticketStatusLabels[value]}</button>`).join('')}</div></div>
      <div class="table-scroll"><table class="data-table ticket-table"><thead><tr>${admin ? '<th>用户</th>' : ''}<th>反馈内容</th><th>状态</th><th>更新时间</th><th><span class="sr-only">操作</span></th></tr></thead><tbody>${rows || `<tr><td colspan="${admin ? 5 : 4}" class="empty-state">${e(empty)}</td></tr>`}</tbody></table></div>
      ${state.nextCursor ? `<div class="ticket-list-more"><button type="button" class="button" data-ticket-more="${e(state.nextCursor)}" ${state.loading ? 'disabled' : ''}>${state.loading ? '正在读取…' : '加载更多'}</button></div>` : ''}
    </section></div>`;
}

/** Message bodies always remain plain text; no markdown/HTML from a participant is executed. */
export function renderTicketDetail(detail: SupportTicketDetail, admin: boolean, submitting = false): string {
  const ticket = detail.ticket;
  const knownStatus = Object.prototype.hasOwnProperty.call(ticketStatusLabels, ticket.status);
  const actions = admin ? (Object.keys(ticketStatusLabels) as TicketStatus[]) : ticket.status === 'closed' || ticket.status === 'resolved' ? ['open', 'closed'] as TicketStatus[] : ['closed'] as TicketStatus[];
  const messages = detail.messages.map(message => `<article class="ticket-message ticket-message-${message.author_role === 'admin' ? 'admin' : 'user'}"><header><strong>${message.author_role === 'admin' ? '服务人员' : admin ? e(ticket.user?.name || '用户') : '我'}</strong><time datetime="${e(message.created_at)}">${e(dateTime(message.created_at))}</time></header><div class="ticket-message-body">${plainLines(message.body)}</div></article>`).join('');
  return `<section class="ticket-detail" data-ticket-detail="${e(ticket.id)}" aria-busy="${submitting}">
    <div class="ticket-detail-heading"><div><h3 id="modal-title">${e(ticket.subject)}</h3><div class="ticket-detail-meta">${statusTag(ticket.status)}<span>${e(categoryLabel(ticket.category))}</span>${admin ? `<span>${e(ticket.user?.name || '用户姓名未提供')}</span>` : ''}<span>创建于 ${e(dateTime(ticket.created_at))}</span></div></div><button type="button" class="icon-button" data-ticket-detail-refresh="${e(ticket.id)}" aria-label="刷新反馈详情" title="刷新反馈详情" ${submitting ? 'disabled' : ''}>${icon('refresh')}</button></div>
    <div class="ticket-status-actions" role="group" aria-label="工单处理">${knownStatus ? actions.filter(status => status !== ticket.status).map(status => `<button class="button" type="button" data-ticket-status="${status}" data-ticket-id="${e(ticket.id)}" ${submitting ? 'disabled' : ''}>${admin ? `标记为${ticketStatusLabels[status]}` : status === 'open' ? '重新打开' : '关闭反馈'}</button>`).join('') : '<p class="muted">当前状态暂未确认，请刷新后重试。</p>'}</div>
    <div class="ticket-conversation" aria-label="反馈对话">${messages || '<p class="empty-state">暂时没有可显示的对话记录，请刷新后查看。</p>'}</div>
    ${knownStatus && ticket.status !== 'closed' ? `<form id="ticket-reply-form" class="ticket-reply-form" data-ticket-id="${e(ticket.id)}"><div class="field"><label for="ticket-reply-body">${admin ? '回复用户' : '补充说明'}</label><textarea name="body" id="ticket-reply-body" required maxlength="5000" rows="4" ${submitting ? 'disabled' : ''} placeholder="${admin ? '写下答复及处理进展。' : '补充有助于我们了解问题的信息。'}"></textarea></div><div class="ticket-form-footer"><p class="muted ticket-privacy-hint">${e(ticketPrivacyHint)}</p><button class="button primary" type="submit" ${submitting ? 'disabled' : ''}>${submitting ? '正在发送…' : admin ? '发送回复' : '发送补充'}</button></div></form>` : ticket.status === 'closed' ? '<p class="ticket-closed-hint muted">这条反馈已关闭，需要继续沟通时可以重新打开。</p>' : ''}
  </section>`;
}
