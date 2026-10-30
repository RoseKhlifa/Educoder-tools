import { readFile } from 'node:fs/promises';
import { encryptPassword } from './protocol.ts';
import type { ProtocolConstants, Transport } from './protocol.ts';

export type JsonObject = Record<string, any>;
export interface User { login: string; name: string; id?: number }
export interface Course {
  identifier: string; name: string; homeworkCount?: number; membersCount?: number; ended?: boolean;
}
export interface Homework {
  id: number; name: string; shixunIdentifier?: string; status: string;
  challengeCount?: number; finishedChallengeCount?: number; deadline?: string; canEnter?: boolean;
}
export interface EnteredTask {
  courseIdentifier: string; homeworkId: number; gameIdentifier: string;
  challenge: { id?: number; subject?: string; position?: number; path?: string };
  game: { id?: number; status?: number };
  shixun: { identifier?: string; name?: string; challengesCount?: number };
  myshixunIdentifier?: string; nextGame?: string | null; requiredStatus?: string;
}

function object(data: unknown, path: string): JsonObject {
  if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error(`${path} 响应结构异常`);
  return data as JsonObject;
}

export class EducoderClient {
  readonly transport: Transport;
  readonly constants: ProtocolConstants;
  user?: User;
  readonly pageSize: number;

  constructor(transport: Transport, constants: ProtocolConstants, options: { pageSize?: number } = {}) {
    this.transport = transport;
    this.constants = constants;
    this.pageSize = options.pageSize ?? 20;
    if (!Number.isInteger(this.pageSize) || this.pageSize < 1 || this.pageSize > 100) throw new Error('page-size 必须为 1 到 100 的整数');
  }

  async login(login: string, password: string): Promise<User> {
    if (!login || !password) throw new Error('请通过 EDUCODER_LOGIN 和 EDUCODER_PASSWORD 或 --password-stdin 提供账号密码');
    const data = object(await this.transport.request('POST', '/api/accounts/login.json', {}, {
      login: login.trim(), password: encryptPassword(password.trim(), this.constants), autologin: true,
    }), 'login');
    if (typeof data.login !== 'string' || !data.login) throw new Error('登录未返回用户标识，请检查账号密码和服务端提示');
    return await this.currentUser(login.trim());
  }

  async currentUser(loginHint?: string): Promise<User> {
    const data = object(await this.transport.request('GET', '/api/users/get_user_info.json', {
      school: 1, zzud: loginHint ?? this.user?.login,
    }), 'get_user_info');
    if (typeof data.login !== 'string' || !data.login) throw new Error('当前会话未登录');
    this.user = { login: data.login, name: data.real_name || data.username || data.name || data.login, id: data.user_id };
    return this.user;
  }

  private loginId(): string {
    if (!this.user) throw new Error('请先登录');
    return this.user.login;
  }

  async courses(options: { status?: string } = {}): Promise<Course[]> {
    const login = this.loginId();
    const path = `/api/users/${encodeURIComponent(login)}/courses.json`;
    const rows = await this.paginate(path, 'courses', 'count', page => ({
      category: '', status: options.status === 'all' ? '' : (options.status ?? 'processing'),
      page, per_page: this.pageSize, sort_by: 'updated_at', sort_direction: 'desc', username: login, zzud: login,
    }), x => String(x.id));
    return rows.map(x => ({
      identifier: typeof x.first_category_url === 'string' && /^\/classrooms\/[^/]+/.test(x.first_category_url)
        ? x.first_category_url.split('/')[2] : String(x.identifier ?? x.id), name: String(x.name ?? ''), homeworkCount: x.homework_commons_count,
      membersCount: x.members_count, ended: x.is_end,
    }));
  }

  async homeworks(courseIdentifier: string, options: { category?: string } = {}): Promise<Homework[]> {
    const login = this.loginId();
    const path = `/api/courses/${encodeURIComponent(courseIdentifier)}/homework_commons.json`;
    const rows = await this.paginate(path, 'homeworks', 'query_total_count', page => ({
      limit: this.pageSize, status: 0, id: courseIdentifier, type: 4, order: 0,
      category: options.category, page, zzud: login,
    }), x => String(x.homework_id));
    return rows.map(x => ({
      id: x.homework_id, name: String(x.name ?? ''), shixunIdentifier: x.shixun_identifier,
      status: Array.isArray(x.status) ? x.status.join('、') : String(x.status ?? ''),
      challengeCount: x.challenge_count, finishedChallengeCount: x.finished_challenge_count,
      deadline: x.end_time, canEnter: x.is_enter_shixun,
    }));
  }

  async enter(courseIdentifier: string, homeworkId: number, shixunIdentifier?: string): Promise<EnteredTask> {
    const login = this.loginId();
    if (!Number.isSafeInteger(homeworkId) || homeworkId <= 0) throw new Error('homework 必须为正整数');
    if (!shixunIdentifier) {
      const homework = (await this.homeworks(courseIdentifier)).find(x => x.id === homeworkId);
      if (!homework) throw new Error(`课堂 ${courseIdentifier} 未找到作业 ${homeworkId}`);
      shixunIdentifier = homework.shixunIdentifier;
    }
    if (!shixunIdentifier) throw new Error('该作业缺少实训标识，当前链路只支持实训作业');
    const exec = object(await this.transport.request('GET', `/api/shixuns/${encodeURIComponent(shixunIdentifier)}/shixun_exec.json`, {
      id: shixunIdentifier, homework_common_id: homeworkId, zzud: login,
    }), 'shixun_exec');
    if (typeof exec.game_identifier !== 'string' || !exec.game_identifier) throw new Error('进入实训未返回 game_identifier');
    const task = object(await this.transport.request('GET', `/api/tasks/${encodeURIComponent(exec.game_identifier)}.json`, {
      homework_common_id: homeworkId, zzud: login,
    }), 'tasks');
    return {
      courseIdentifier, homeworkId, gameIdentifier: exec.game_identifier,
      challenge: { id: task.challenge?.id, subject: task.challenge?.subject, position: task.challenge?.position, path: task.challenge?.path },
      game: { id: task.game?.id, status: task.game?.status },
      shixun: { identifier: task.shixun?.identifier, name: task.shixun?.name, challengesCount: task.shixun?.challenges_count },
      myshixunIdentifier: task.myshixun?.identifier, nextGame: task.next_game,
      requiredStatus: typeof task.status === 'string' ? task.status : undefined,
    };
  }

  private async paginate(path: string, field: string, totalField: string,
    query: (page: number) => Record<string, unknown>, key: (x: JsonObject) => string): Promise<JsonObject[]> {
    const results: JsonObject[] = [];
    const seen = new Set<string>();
    for (let page = 1; page <= 10_000; page++) {
      const data = object(await this.transport.request('GET', path, query(page)), path);
      if (!Array.isArray(data[field])) throw new Error(`${path} 缺少 ${field} 数组`);
      const rows = data[field] as JsonObject[];
      let added = 0;
      for (const row of rows) {
        const id = key(row);
        if (!seen.has(id)) { seen.add(id); results.push(row); added++; }
      }
      const total = data[totalField];
      if (!rows.length || (typeof total === 'number' && total >= 0 && results.length >= total)) return results;
      if (added === 0) throw new Error(`${path} 分页未前进（page=${page}），请检查服务端分页响应`);
      if (typeof total !== 'number' && rows.length < this.pageSize) return results;
    }
    throw new Error(`${path} 分页超过 10000 页`);
  }
}

interface HarEntry {
  request: { method: string; url: string };
  response: { status: number; content: { text?: string; encoding?: string } };
}

/** Replay only captured API response bodies; this implementation never sends network requests. */
export class HarTransport implements Transport {
  private readonly entries: HarEntry[];

  constructor(entries: HarEntry[]) { this.entries = entries; }

  static async load(path: string): Promise<HarTransport> {
    const har = JSON.parse(await readFile(path, 'utf8'));
    if (!Array.isArray(har.log?.entries)) throw new Error('HAR 文件缺少 log.entries');
    return new HarTransport(har.log.entries);
  }

  async request(method: string, path: string, query: Record<string, unknown> = {}): Promise<unknown> {
    const candidates = this.entries.filter(e => e.request.method.toUpperCase() === method.toUpperCase()
      && new URL(e.request.url).pathname === path && e.response.status >= 200 && e.response.status < 300);
    const entry = candidates.find(e => {
      const q = new URL(e.request.url).searchParams;
      return ['page', 'category', 'homework_common_id'].every(key => {
        const expected = query[key];
        const actual = q.get(key);
        return expected === undefined || expected === '' ? !actual : String(expected) === (actual ?? (key === 'page' ? '1' : ''));
      });
    });
    if (!entry) throw new Error(`HAR 未录制 ${method} ${path} 的这组分页/筛选参数`);
    const content = entry.response.content;
    if (!content.text) throw new Error(`HAR 缺少响应体：${path}`);
    const text = content.encoding === 'base64' ? Buffer.from(content.text, 'base64').toString('utf8') : content.text;
    return JSON.parse(text);
  }
}
