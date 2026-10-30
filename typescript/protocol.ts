import { createCipheriv, createHash, randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

export interface ProtocolConstants {
  baseUrl: string;
  origin: string;
  referer: string;
  ak: string;
  sk: string;
  aesKey: string;
  aesIv: string;
  passwordOuterBase64?: boolean;
  signatureHeader?: string;
  timestampHeader?: string;
  clientTypeHeader?: string;
  clientType?: string;
  sessionCookie?: string;
  authorizationHeader?: string;
}

export async function loadProtocolConstants(path?: string): Promise<ProtocolConstants> {
  const filename = path ?? fileURLToPath(new URL('../protocol-constants.json', import.meta.url));
  const raw = JSON.parse(await readFile(filename, 'utf8'));
  // The file is generated from public frontend modules, and contains no account secrets.
  const source = raw as any;
  // analyzer output keeps derivation metadata nested; normalize it once here so
  // the rest of the client can use a small stable shape.
  const c: ProtocolConstants = {
    baseUrl: source.baseUrl ?? 'https://data.educoder.net',
    origin: source.origin ?? 'https://www.educoder.net',
    referer: source.referer ?? 'https://www.educoder.net/',
    ak: source.ak ?? source.signature?.access_key,
    sk: source.sk ?? source.signature?.secret_key,
    aesKey: source.aesKey ?? source.password?.key_utf8 ?? source.attachment?.aes_key_iv_utf8,
    aesIv: source.aesIv ?? source.password?.iv_utf8 ?? source.attachment?.aes_key_iv_utf8,
    // The captured login body is one AES-CBC ciphertext encoded once: for the
    // supplied 18-character fixture it is exactly 44 Base64 characters.
    passwordOuterBase64: source.passwordOuterBase64 ?? false,
    signatureHeader: source.signatureHeader ?? 'X-EDU-Signature',
    timestampHeader: source.timestampHeader ?? 'X-EDU-Timestamp',
    clientTypeHeader: source.clientTypeHeader ?? 'X-EDU-Type',
    clientType: source.clientType ?? source.signature?.edu_type ?? 'pc',
    sessionCookie: source.sessionCookie ?? '_educoder_session',
    authorizationHeader: source.authorizationHeader ?? 'Pc-Authorization',
  };
  if (!c.baseUrl || !c.ak || !c.sk || !c.aesKey || !c.aesIv) {
    throw new Error('protocol-constants.json 缺少 baseUrl、ak、sk、aesKey 或 aesIv');
  }
  return c;
}

export function signRequest(method: string, timestamp: string | number, c: Pick<ProtocolConstants, 'ak' | 'sk'>): string {
  const source = `method=${method.toUpperCase()}&ak=${c.ak}&sk=${c.sk}&time=${timestamp}`;
  const encoded = Buffer.from(source, 'utf8').toString('base64');
  return createHash('md5').update(encoded, 'utf8').digest('hex');
}

export function encryptPassword(password: string, c: Pick<ProtocolConstants, 'aesKey' | 'aesIv' | 'passwordOuterBase64'>): string {
  const key = Buffer.from(c.aesKey, 'utf8');
  const iv = Buffer.from(c.aesIv, 'utf8');
  if (key.length !== 16 || iv.length !== 16) throw new Error('AES-128-CBC 的 key 和 iv 必须为 16 字节');
  const cipher = createCipheriv('aes-128-cbc', key, iv);
  const ciphertext = Buffer.concat([cipher.update(password.trim(), 'utf8'), cipher.final()]).toString('base64');
  return c.passwordOuterBase64 ? Buffer.from(ciphertext, 'utf8').toString('base64') : ciphertext;
}

interface Cookie {
  name: string;
  value: string;
  domain: string;
  path: string;
  hostOnly: boolean;
  secure: boolean;
  expiresAt?: number;
}

/** Per-client, memory-only cookie jar. Multiple Set-Cookie headers remain separate. */
export class CookieJar {
  private cookies: Cookie[] = [];

  setFromHeaders(url: URL, headers: string[]): void {
    for (const line of headers) {
      const parts = line.split(';');
      const equal = parts[0].indexOf('=');
      if (equal <= 0) continue;
      const cookie: Cookie = {
        name: parts[0].slice(0, equal).trim(),
        value: parts[0].slice(equal + 1).trim(),
        domain: url.hostname.toLowerCase(),
        path: url.pathname.includes('/') ? url.pathname.slice(0, url.pathname.lastIndexOf('/') + 1) : '/',
        hostOnly: true,
        secure: false,
      };
      let maxAge: number | undefined;
      for (const item of parts.slice(1)) {
        const sep = item.indexOf('=');
        const name = (sep < 0 ? item : item.slice(0, sep)).trim().toLowerCase();
        const value = (sep < 0 ? '' : item.slice(sep + 1)).trim();
        if (name === 'domain') { cookie.domain = value.replace(/^\./, '').toLowerCase(); cookie.hostOnly = false; }
        if (name === 'path' && value.startsWith('/')) cookie.path = value;
        if (name === 'secure') cookie.secure = true;
        if (name === 'expires') { const ms = Date.parse(value); if (Number.isFinite(ms)) cookie.expiresAt = ms; }
        if (name === 'max-age') { const n = Number(value); if (Number.isFinite(n)) maxAge = n; }
      }
      if (!matchesDomain(url.hostname, cookie.domain, false)) continue;
      if (maxAge !== undefined) cookie.expiresAt = Date.now() + maxAge * 1000;
      this.cookies = this.cookies.filter(x => !(x.name === cookie.name && x.domain === cookie.domain && x.path === cookie.path));
      if (cookie.expiresAt === undefined || cookie.expiresAt > Date.now()) this.cookies.push(cookie);
    }
  }

  header(url: URL): string { return this.matching(url).map(c => `${c.name}=${c.value}`).join('; '); }

  get(url: URL, name: string): string | undefined { return this.matching(url).find(c => c.name === name)?.value; }

  private matching(url: URL): Cookie[] {
    const now = Date.now();
    this.cookies = this.cookies.filter(c => c.expiresAt === undefined || c.expiresAt > now);
    return this.cookies.filter(c => matchesDomain(url.hostname, c.domain, c.hostOnly)
      && (url.pathname === c.path || url.pathname.startsWith(c.path.endsWith('/') ? c.path : `${c.path}/`))
      && (!c.secure || url.protocol === 'https:')).sort((a, b) => b.path.length - a.path.length);
  }
}

function matchesDomain(host: string, domain: string, hostOnly: boolean): boolean {
  return host.toLowerCase() === domain || (!hostOnly && host.toLowerCase().endsWith(`.${domain}`));
}

export interface Transport {
  request(method: string, path: string, query?: Record<string, unknown>, body?: unknown): Promise<unknown>;
}

/** A real 304 carries no representation. Callers must keep polling, not invent a response. */
export const NOT_MODIFIED = Object.freeze({ notModified: true as const });
export function isNotModified(value: unknown): value is typeof NOT_MODIFIED {
  return Boolean(value && typeof value === 'object' && (value as any).notModified === true);
}

/** These two routes use status as a build/evaluation state, unlike ordinary API envelopes. */
export function validateApiResponse(method: string, path: string, value: unknown): unknown {
  if (!value || typeof value !== 'object') return value;
  const data = value as Record<string, unknown>;
  if (typeof data.status !== 'number' || data.status === 0) return value;
  if (method.toUpperCase() === 'POST' && /^\/api\/tasks\/[^/]+\/game_build\.json$/.test(path) && data.status === 1) return value;
  if (method.toUpperCase() === 'GET' && /^\/api\/tasks\/[^/]+\/game_status\.json$/.test(path) && data.status === 2) return value;
  // Upstream text may contain account or session data. Keep errors to route and status.
  throw new Error(`${path}: status=${data.status}`);
}

export class HttpTransport implements Transport {
  readonly constants: ProtocolConstants;
  readonly jar = new CookieJar();
  private readonly fetcher: typeof fetch;
  private readonly clock: () => number;
  private readonly trace?: (event: { method: string; path: string; status: number }) => void;

  constructor(constants: ProtocolConstants, options: { fetcher?: typeof fetch; clock?: () => number; trace?: (event: { method: string; path: string; status: number }) => void } = {}) {
    this.constants = constants;
    this.fetcher = options.fetcher ?? fetch;
    this.clock = options.clock ?? Date.now;
    this.trace = options.trace;
  }

  async request(method: string, path: string, query: Record<string, unknown> = {}, body?: unknown): Promise<unknown> {
    const c = this.constants;
    const url = new URL(path, c.baseUrl);
    for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== null && v !== '') url.searchParams.set(k, String(v));
    const timestamp = String(this.clock());
    const headers: Record<string, string> = {
      Accept: 'application/json',
      Origin: c.origin,
      Referer: c.referer,
      [c.signatureHeader ?? 'X-EDU-Signature']: signRequest(method, timestamp, c),
      [c.timestampHeader ?? 'X-EDU-Timestamp']: timestamp,
      [c.clientTypeHeader ?? 'X-EDU-Type']: c.clientType ?? 'pc',
      'X-Original-Protocol': url.protocol,
      'X-Original-Host': new URL(c.origin).host,
      'X-Original-Origin': c.origin,
      'X-Request-Id': randomUUID(),
      'Content-Type': 'application/json; charset=utf-8',
    };
    const cookies = this.jar.header(url);
    if (cookies) headers.Cookie = cookies;
    const session = this.jar.get(url, c.sessionCookie ?? '_educoder_session');
    headers[c.authorizationHeader ?? 'Pc-Authorization'] = session ?? 'null';
    const response = await this.fetcher(url, {
      method: method.toUpperCase(), headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      redirect: 'manual', signal: AbortSignal.timeout(30_000),
    });
    this.trace?.({ method: method.toUpperCase(), path: url.pathname, status: response.status });
    this.jar.setFromHeaders(url, response.headers.getSetCookie());
    if (response.status === 304) return NOT_MODIFIED;
    const text = await response.text();
    if (!response.ok) throw new Error(`HTTP ${response.status}：${url.pathname}`);
    let data: unknown;
    try { data = JSON.parse(text); } catch { throw new Error(`接口未返回 JSON：${url.pathname}`); }
    return validateApiResponse(method, url.pathname, data);
  }
}
