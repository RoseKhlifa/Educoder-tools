import assert from 'node:assert/strict';
import { test } from 'node:test';
import { renderTickets, renderTicketDetail, ticketCategoryLabels, ticketStatusLabels, ticketPrivacyHint } from './dist/tickets.js';

const ticket = { id: 'ticket/opaque', subject: '任务进度需要核对', category: 'task', status: 'open', created_at: '2026-11-02T01:00:00Z', updated_at: '2026-11-02T02:00:00Z', message_count: 1, last_author_role: 'user', user: { name: '示例同学' } };
const detail = { ticket, messages: [{ id: 'm1', author_role: 'user', body: '第一行\n第二行', created_at: '2026-11-02T01:00:00Z' }] };
const state = { items: [ticket], loading: false };

await test('support ticket renderers maintain user ownership and admin management boundaries', async t => {
  await t.test('consumer list provides real creation form and opaque ticket detail hooks', () => {
    const html = renderTickets(state, false);
    assert.match(html, /id="ticket-create-form"/);
    for (const name of ['subject', 'category', 'body']) assert.match(html, new RegExp(`name="${name}"`));
    for (const [value, label] of Object.entries(ticketCategoryLabels)) assert.match(html, new RegExp(`<option value="${value}">${label}`));
    assert.match(html, /maxlength="100"/); assert.match(html, /maxlength="5000"/);
    assert.match(html, /data-ticket-open="ticket\/opaque"/);
    assert.match(html, /任务进度需要核对/); assert.match(html, /待回复/);
    assert.ok(html.includes(ticketPrivacyHint));
    assert.doesNotMatch(html, /示例同学|name="password"|user\.id|owner_id/);
  });
  await t.test('admin list shows real user name and useful status filters without consumer creation', () => {
    const html = renderTickets({ ...state, filter: 'processing', nextCursor: 'opaque+/=' }, true);
    assert.match(html, /示例同学/); assert.match(html, /反馈工单/);
    assert.doesNotMatch(html, /ticket-create-form/);
    assert.match(html, /data-ticket-more="opaque\+\/="/);
    for (const [value, label] of Object.entries(ticketStatusLabels)) assert.match(html, new RegExp(`data-ticket-filter="${value}"[\\s\\S]*?${label}`));
    assert.match(html, /data-ticket-filter="processing" class="active" aria-pressed="true"/);
    assert.doesNotMatch(html, /private-owner|platform:/);
  });
  await t.test('feedback and user data remain plain escaped multiline text in attributes and message content', () => {
    const attack = '\"><img src=x onerror="attack()">';
    const dangerous = { ...detail, ticket: { ...ticket, id: attack, subject: attack, user: { name: attack } }, messages: [{ ...detail.messages[0], body: `${attack}\r\n**不解析markdown**\n<script>attack()</script>` }] };
    const html = renderTickets({ items: [dangerous.ticket], loading: false }, true) + renderTicketDetail(dangerous, true);
    assert.doesNotMatch(html, /<img|<script>|onerror="attack/);
    assert.match(html, /&lt;img src=x onerror=&quot;attack\(\)&quot;&gt;/);
    assert.match(html, /<br>\*\*不解析markdown\*\*<br>&lt;script&gt;attack\(\)&lt;\/script&gt;/);
  });
  await t.test('user detail has reply and close or reopen hooks without admin-only status controls', () => {
    const html = renderTicketDetail(detail, false);
    assert.match(html, /id="ticket-reply-form"/); assert.match(html, /data-ticket-id="ticket\/opaque"/);
    assert.match(html, /data-ticket-status="closed"/);
    assert.doesNotMatch(html, /data-ticket-status="processing"|data-ticket-status="resolved"|示例同学/);
    assert.match(html, /第一行<br>第二行/);
    assert.match(html, /data-ticket-detail-refresh="ticket\/opaque"/);
    const closed = renderTicketDetail({ ...detail, ticket: { ...ticket, status: 'closed' } }, false);
    assert.match(closed, /data-ticket-status="open"/);
    assert.doesNotMatch(closed, /ticket-reply-form/);
    assert.match(closed, /这条反馈已关闭/);
  });
  await t.test('admin can reply and explicitly transition processing resolved and closed states', () => {
    const html = renderTicketDetail({ ...detail, messages: [...detail.messages, { ...detail.messages[0], id: 'm2', author_role: 'admin', body: '已收到' }] }, true);
    assert.match(html, /示例同学/); assert.match(html, /服务人员/); assert.match(html, /回复用户/);
    for (const status of ['processing', 'resolved', 'closed']) assert.match(html, new RegExp(`data-ticket-status="${status}"`));
    assert.match(html, /发送回复/);
  });
  await t.test('detail heading labels the modal and sending disables reply status and refresh controls', () => {
    const html = renderTicketDetail(detail, true, true);
    assert.match(html, /<h3 id="modal-title">任务进度需要核对<\/h3>/);
    assert.match(html, /aria-busy="true"/);
    for (const tag of html.matchAll(/<(button|textarea)\b([^>]+)>/g)) assert.match(tag[2], /\bdisabled\b/, tag[0]);
    assert.match(html, /正在发送/);
  });
  await t.test('empty errors loading and unknown status do not become successful completion', () => {
    assert.match(renderTickets({ items: [], loading: false }, false), /还没有反馈记录/);
    assert.match(renderTickets({ items: [], loading: true }, true), /正在读取反馈记录/);
    assert.match(renderTickets({ items: [], loading: false, error: '<private>' }, true), /role="alert">&lt;private&gt;/);
    const unknown = renderTicketDetail({ ticket: { ...ticket, status: 'future' }, messages: [] }, false);
    assert.match(unknown, /状态待确认/); assert.match(unknown, /暂时没有可显示的对话记录/);
    assert.doesNotMatch(unknown, /已完成|data-ticket-status|ticket-reply-form/);
  });
});
