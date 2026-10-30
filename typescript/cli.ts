#!/usr/bin/env node
import { createInterface } from 'node:readline/promises';
import { stdin as input, stdout as output } from 'node:process';
import { loadProtocolConstants, HttpTransport } from './protocol.ts';
import { EducoderClient, HarTransport, type Course, type Homework } from './client.ts';
import { replayCodingHar } from './programming.ts';

type Args = { command: string; course?: string; homework?: number; shixun?: string; har?: string; json: boolean; trace: boolean; status?: string; pageSize?: number; passwordStdin: boolean; constants?: string };

function parse(argv: string[]): Args {
  const command = argv.find(x => !x.startsWith('-')) ?? 'flow';
  const a: Args = { command, json: argv.includes('--json'), trace: argv.includes('--trace'), passwordStdin: argv.includes('--password-stdin') };
  for (let i = 0; i < argv.length; i++) {
    const x = argv[i];
    if (x === '--course') a.course = argv[++i];
    else if (x === '--homework') a.homework = Number(argv[++i]);
    else if (x === '--shixun') a.shixun = argv[++i];
    else if (x === '--har') a.har = argv[++i];
    else if (x === '--status') a.status = argv[++i];
    else if (x === '--page-size') a.pageSize = Number(argv[++i]);
    else if (x === '--constants') a.constants = argv[++i];
  }
  if (a.homework !== undefined && !Number.isSafeInteger(a.homework)) throw new Error('--homework 必须为正整数');
  return a;
}

async function passwordFromStdin(): Promise<string> {
  if (!process.stdin.isTTY) {
    const chunks: Buffer[] = [];
    for await (const chunk of process.stdin) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
    return Buffer.concat(chunks).toString('utf8').replace(/\r?\n$/, '');
  }
  const rl = createInterface({ input, output });
  try { return await rl.question('密码（输入不回显）：'); } finally { rl.close(); }
}

function print(value: unknown, json: boolean): void {
  if (json) { console.log(JSON.stringify(value)); return; }
  if (Array.isArray(value)) printRows(value as Record<string, unknown>[]);
  else if (value && typeof value === 'object') for (const [k, v] of Object.entries(value)) console.log(`${k}\t${String(v ?? '')}`);
  else console.log(String(value ?? ''));
}

function printRows(rows: Record<string, unknown>[]): void {
  if (!rows.length) { console.log('（无记录）'); return; }
  const keys = Object.keys(rows[0]);
  const widths = keys.map(k => Math.max(k.length, ...rows.map(r => String(r[k] ?? '').replace(/\n/g, ' ').length)));
  const line = (r: Record<string, unknown>) => keys.map((k, i) => String(r[k] ?? '').replace(/\n/g, ' ').padEnd(widths[i])).join('  ');
  console.log(line(Object.fromEntries(keys.map(k => [k, k]))));
  console.log(widths.map(n => '-'.repeat(n)).join('  '));
  for (const row of rows) console.log(line(row));
}

async function main(): Promise<void> {
  const args = parse(process.argv.slice(2));
  const constants = await loadProtocolConstants(args.constants);
  if (args.command === 'coding-replay' || args.command === 'coding-analyze') {
    if (!args.har) throw new Error(`${args.command} 需要 --har <本地录制路径>`);
    print(await replayCodingHar(args.har, constants), true); return;
  }
  const transport = args.har ? await HarTransport.load(args.har) : new HttpTransport(constants, {
    trace: args.trace ? event => console.error(`[trace] ${event.method} ${event.path} -> ${event.status}`) : undefined,
  });
  const client = new EducoderClient(transport, constants, { pageSize: args.pageSize });
  const offline = Boolean(args.har);
  if (offline && transport instanceof HarTransport) client.user = transport.restoreRecordedUser();
  else {
    const login = process.env.EDUCODER_LOGIN;
    const password = args.passwordStdin ? await passwordFromStdin() : process.env.EDUCODER_PASSWORD;
    if (!login || !password) throw new Error('请设置 EDUCODER_LOGIN、EDUCODER_PASSWORD，或使用 --password-stdin');
    await client.login(login, password);
  }
  if (args.command === 'login') { print(client.user, args.json); return; }
  if (args.command === 'courses') { print(await client.courses({ status: args.status }), args.json); return; }
  if (args.command === 'tasks') {
    if (!args.course) throw new Error('tasks 需要 --course <课堂标识>');
    print(await client.homeworks(args.course), args.json); return;
  }
  if (args.command === 'enter') {
    if (!args.course || args.homework === undefined) throw new Error('enter 需要 --course <课堂标识> --homework <作业 ID>');
    print(await client.enter(args.course, args.homework, args.shixun), args.json); return;
  }
  if (args.command !== 'flow') throw new Error(`未知命令：${args.command}（可用 login、courses、tasks、enter、flow）`);
  if (args.course && args.homework !== undefined) {
    print(await client.enter(args.course, args.homework, args.shixun), args.json); return;
  }
  const courses = await client.courses({ status: args.status });
  if (args.json) {
    const outputRows: Array<{ course: Course; tasks: Homework[] }> = [];
    for (const course of courses) outputRows.push({ course, tasks: await client.homeworks(course.identifier) });
    print({ user: client.user, courses: outputRows }, true); return;
  }
  print(courses.map(x => ({ identifier: x.identifier, name: x.name, tasks: x.homeworkCount ?? '' })), false);
  for (const course of courses) {
    console.log(`\n[${course.identifier}] ${course.name}`);
    print((await client.homeworks(course.identifier)).map(x => ({ id: x.id, name: x.name, shixun: x.shixunIdentifier ?? '', status: x.status })), false);
  }
}

main().catch(error => { console.error(`错误：${error instanceof Error ? error.message : String(error)}`); process.exitCode = 1; });
