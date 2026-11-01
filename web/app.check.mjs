import assert from 'node:assert/strict';
import { test } from 'node:test';
import { renderAdminJobDetail } from './dist/admin.js';
import { renderServiceJob, renderServiceDetail } from './dist/jobs.js';
class Element {
  constructor(tag, attributes, document) {
    this.tag = tag; this.attributes = attributes; this.document = document; this.value = attributes.value ?? ''; this.checked = 'checked' in attributes; this.required = 'required' in attributes; this.open = 'open' in attributes; this.isConnected = true;
    this.dataset = Object.fromEntries(Object.entries(attributes).filter(([key]) => key.startsWith('data-')).map(([key,value]) => [key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase()), value]));
    this.listeners = new Map(); this.elements = { namedItem: name => document.items.find(item => item.attributes.name === name) };
  }
  addEventListener(name, handler) { this.listeners.set(name, [...this.listeners.get(name) ?? [], handler]); }
  dispatch(name) { if ('disabled' in this.attributes) return; for (const handler of this.listeners.get(name) ?? []) handler({currentTarget:this,target:this,preventDefault(){}}); }
  reset() { for (const item of this.document.items) if (item.tag === 'input') item.value = ''; }
  focus() { this.document.activeElement = this; }
  setSelectionRange() {}
}
class Input extends Element {}
class Document {
  items = []; html = ''; listeners = new Map(); activeElement = null;
  constructor() { const document = this; this.root = { get innerHTML() { return document.html; }, set innerHTML(value) { document.html = value; document.items.forEach(item => { item.isConnected = false; }); document.items = [...value.matchAll(/<(button|input|form|img|details)\b([^>]*)>/g)].map(([,tag,attrs]) => new (tag === 'input' ? Input : Element)(tag,Object.fromEntries([...attrs.matchAll(/([\w-]+)(?:="([^"]*)")?/g)].map(([,key,value])=>[key,value??''])),document)); } }; }
  querySelector(selector) { return selector === '#app' ? this.root : this.querySelectorAll(selector)[0] ?? null; }
  querySelectorAll(selector) { if(selector.startsWith('#')) return this.items.filter(item => item.attributes.id === selector.slice(1)); const match=selector.match(/^\[([^=\]]+)(?:="([^"]*)")?\]$/);return match?this.items.filter(item => match[1] in item.attributes && (match[2]===undefined||item.attributes[match[1]]===match[2])):[]; }
  addEventListener(name, handler) { this.listeners.set(name,handler); }
}
const ok = data => new Response(JSON.stringify({data,error:null}));
const failure=(status,code,message)=>new Response(JSON.stringify({data:null,error:{code,message}}),{status});
const user={login:'demo',name:'<测试同学>',avatarUrl:'/api/avatar'};
const courses=[{identifier:'COURSE_A',name:'课堂甲',homeworkCount:2},{identifier:'COURSE_B',name:'课堂乙',homeworkCount:3}];
const job=(extra={})=>({id:'job-1',status:'processing',phase:'processing',request:{scope:'course',course:'COURSE_A',mode:'submit'},created_at:'2026-10-31T00:00:00Z',updated_at:'2026-10-31T00:00:00Z',result:{service_status:'processing',total_homeworks:2,completed_homeworks:1},...extra});
const settle=async()=>{for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));};
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done;});return{promise,resolve};};
let version=0;
async function app(overrides={}) {
  const document=new Document(),requests=[],timers=new Map();let timerId=0;
  globalThis.HTMLInputElement=Input;globalThis.document=document;globalThis.window={};
  globalThis.setTimeout=(handler,delay)=>{const id=++timerId;timers.set(id,{handler,delay});return id;};globalThis.clearTimeout=id=>timers.delete(id);
  globalThis.localStorage=globalThis.sessionStorage={setItem(){throw new Error('no browser persistence');}};
  globalThis.fetch=async(url,options)=>{
    requests.push({url,options});if(overrides[url])return overrides[url](options);
    if(url==='/api/auth/me'||url==='/api/auth/login')return ok({user,platform:{role:'user',status:'active'}});
    if(url==='/api/auth/logout')return ok({loggedOut:true});
    if(url==='/api/courses')return ok(courses);
    if(url==='/api/question-bank/summary')return ok({verified_objective_questions:272,verified_programming_problems:8,total_verified_questions:280});
    if(url==='/api/jobs')return options.method==='POST'?ok(job({request:JSON.parse(options.body)})):ok({jobs:[]});
    if(url.endsWith('/homeworks'))return ok([{id:1,name:'测试作业',challengeCount:2,finishedChallengeCount:1,requiredProgressKnown:true,requiredChallengeCount:2,finishedRequiredChallengeCount:1}]);
    if(url.startsWith('/api/admin/'))return ok(url==='/api/admin/overview'?{total_users:3,running_jobs:1,capacity:{model_concurrency:8}}:{items:[]});
    throw new Error(`Unexpected URL ${url}`);
  };
  await import(`./dist/app.js?test=${++version}`);await settle();
  return {requests,timers,document,html:()=>document.html,get(selector){const element=document.querySelector(selector);assert.ok(element,`Missing ${selector}`);return element;},async tick(){const tasks=[...timers.entries()];for(const[id,timer]of tasks){timers.delete(id);timer.handler();}await settle();}};
}
await test('consumer course service and administrator boundary',async t=>{
  await t.test('the supplied first-party brand logo is shared by login, user and admin headers', async () => {
    const loginPage = await app({ '/api/auth/me': () => failure(401, 'AUTH_REQUIRED', 'x') });
    assert.match(loginPage.html(), /class="brand-logo" src="\/assets\/brand-logo\.png\?v=\d+\.\d+\.\d+" alt="头歌妈妈"/);
    assert.doesNotMatch(loginPage.html(), /class="mark-seal"/);
    const page = await app({ '/api/auth/me': () => ok({ user, platform: { role: 'admin', status: 'active' } }) });
    assert.match(page.html(), /class="brand-logo" src="\/assets\/brand-logo\.png/);
    page.get('[data-nav="admin-overview"]').dispatch('click'); await settle();
    assert.match(page.html(), /class="brand-logo" src="\/assets\/brand-logo\.png/);
    assert.doesNotMatch(page.html(), /class="mark-seal"/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });

  await t.test('the sidebar shows only verified bank totals and keeps the guidelines reachable', async () => {
    const page=await app();assert.match(page.html(), /题库概况/);assert.match(page.html(), /<strong>280<\/strong>/);
    assert.match(page.html(), /<dt>客观题<\/dt><dd>272<\/dd>/);assert.match(page.html(), /<dt>编程题<\/dt><dd>8<\/dd>/);
    assert.match(page.html(), /题库数量不代表课程覆盖率/);assert.match(page.html(), /不承诺成绩或完成时限/);
    assert.match(page.html(), /class="bank-mobile"/);
    const bankRequests=()=>page.requests.filter(item=>item.url==='/api/question-bank/summary');
    assert.equal(bankRequests().length,1);await page.tick();assert.equal(bankRequests().length,1,'normal job polling must not rescan the bank before its refresh interval');
    page.get('[data-action="bank-refresh"]').dispatch('click');await settle();assert.equal(bankRequests().length,2);
    for(const source of ['sidebar','mobile']) {
      page.get(`[data-guidelines-source="${source}"]`).dispatch('click');assert.match(page.html(), /平台会收集用户做题数据/);
      page.document.listeners.get('keydown')({key:'Escape'});assert.doesNotMatch(page.html(), /role="dialog"/);
      assert.equal(page.document.activeElement,page.get(`[data-guidelines-source="${source}"]`));
    }
    assert.equal(page.requests.some(item=>item.options.method==='POST'),false);
  });
  await t.test('unavailable or invalid bank counts never become a fabricated zero', async () => {
    for(const response of [()=>failure(503,'QUESTION_BANK_UNAVAILABLE','private-bank-details'),()=>ok({verified_objective_questions:-1,verified_programming_problems:0,total_verified_questions:0})]) {
      const page=await app({'/api/question-bank/summary':response});
      const bank=page.html().split('aria-label="题库概况"')[1].split('</section>')[0];
      assert.match(bank,/题库概况暂未同步/);assert.doesNotMatch(bank,/bank-total|private-bank-details/);
    }
    const empty=await app({'/api/question-bank/summary':()=>ok({verified_objective_questions:0,verified_programming_problems:0,total_verified_questions:0})});
    assert.match(empty.html(), /bank-total"><strong>0<\/strong>/);assert.doesNotMatch(empty.html(), /题库概况暂未同步/);
  });
  await t.test('a bank response after logout never restores private account UI', async () => {
    const late=deferred();const page=await app({'/api/question-bank/summary':()=>late.promise});
    page.get('[data-action="logout"]').dispatch('click');late.resolve(ok({verified_objective_questions:900,verified_programming_problems:1,total_verified_questions:901}));await settle();
    assert.match(page.html(), /id="login-form"/);assert.doesNotMatch(page.html(), /题库概况|901|课堂甲/);
    assert.equal(page.timers.size,0);
  });
  await t.test('loads only own courses and service progress, with no automatic task entry or admin request',async()=>{
    const page=await app();assert.match(page.html(),/课堂甲|课堂乙/);assert.match(page.html(),/&lt;测试同学&gt;/);assert.match(page.html(),/一键完成全部未完成作业/);assert.doesNotMatch(page.html(),/API额度|模型|Token|管理后台|自测|修复次数/);assert.equal(page.requests.some(item=>item.url.startsWith('/api/admin')||item.url.endsWith('/enter')),false);
    page.get('[data-avatar]').dispatch('error');assert.doesNotMatch(page.html(),/data-avatar/);
  });
  await t.test('selected course starts one background course job after explicit scope confirmation',async()=>{
    const page=await app();const input=page.get('[data-select-course="COURSE_A"]');input.checked=true;input.dispatch('change');page.get('[data-action="start-selected"]').dispatch('click');assert.match(page.html(),/把这些课程交给我们/);assert.equal(page.requests.some(item=>item.options.method==='POST'),false);page.get('[data-action="modal-confirm"]').dispatch('click');await settle();
    const posted=page.requests.filter(item=>item.url==='/api/jobs'&&item.options.method==='POST');assert.equal(posted.length,1);const payload=JSON.parse(posted[0].options.body);assert.equal(payload.scope,'course');assert.equal(payload.course,'COURSE_A');assert.equal(payload.mode,'submit');assert.ok(payload.idempotency_key);assert.equal(payload.game,undefined);assert.equal(payload.homework_id,undefined);assert.match(page.html(),/已收到你的安排/);
  });
  await t.test('all unfinished uses one all-scope job without a client-side per-homework execution loop',async()=>{
    const page=await app();page.get('[data-action="start-all"]').dispatch('click');assert.match(page.html(),/完成全部未完成作业/);assert.match(page.html(),/课堂甲/);assert.match(page.html(),/课堂乙/);page.get('[data-action="modal-confirm"]').dispatch('click');await settle();const post=page.requests.filter(item=>item.url==='/api/jobs'&&item.options.method==='POST');assert.equal(post.length,1);const body=JSON.parse(post[0].options.body);assert.equal(body.scope,'all');assert.equal(body.course,undefined);assert.equal(body.game,undefined);
  });
  await t.test('unfinished and diagnostic data remain a service issue, never consumer debugging',async()=>{
    const failed=job({status:'failed',phase:'repairing',error_code:'PRIVATE_MODEL_429',message:'secret-trace',result:{service_status:'support',total_homeworks:2,completed_homeworks:1,model_calls:9,cost_micro_usd:123456,diagnostics:['private secret'],levels:[{title:'未完成任务',status:'failed',reason:'PRIVATE_DETAIL'}]}});
    const html=renderServiceJob(failed,courses)+renderServiceDetail(failed,courses);assert.match(html,/放心，服务人员正在处理，无需重复提交/);assert.doesNotMatch(html,/PRIVATE_|secret|123456|模型|费用|重试|自测|请先核对/);assert.match(html,/待完成/);assert.doesNotMatch(html,/结果待核验/);
    const page=await app({'/api/jobs':()=>ok({jobs:[failed]})});page.get('[data-nav="progress"]').dispatch('click');await settle();assert.match(page.html(),/服务处理中/);assert.doesNotMatch(page.html(),/PRIVATE_|secret-trace|模型费用/);
  });
  await t.test('safe public service status is understood and active jobs poll',async()=>{
    const page=await app({'/api/jobs':()=>ok({jobs:[job()]})});assert.ok([...page.timers.values()].some(item=>item.delay===3000));page.get('[data-nav="progress"]').dispatch('click');await settle();assert.match(page.html(),/正在处理/);assert.match(page.html(),/1 \/ 2 份作业/);await page.tick();assert.ok(page.requests.filter(item=>item.url==='/api/jobs').length>=3);
  });
  await t.test('failed API calls use fixed service-facing copy rather than backend text',async()=>{
    const page=await app({'/api/courses':()=>failure(502,'UPSTREAM','INTERNAL_SECRET_TRACE')});assert.match(page.html(),/服务人员会协助处理/);assert.doesNotMatch(page.html(),/INTERNAL_SECRET_TRACE/);
  });
  await t.test('login uses only upstream account and password and clears private inputs', async () => {
    const page=await app({'/api/auth/me':()=>failure(401,'AUTH_REQUIRED','请先登录')});
    assert.doesNotMatch(page.html(), /卡密|access_code|card-code|login-meta|对象：头歌平台|密码不会在本站保存/);
    page.get('#login').value=' demo ';page.get('#password').value='test-only-password';
    page.get('#login-form').dispatch('submit');await settle();
    const post=page.requests.find(item=>item.url==='/api/auth/login');
    assert.deepEqual(JSON.parse(post.options.body),{login:'demo',password:'test-only-password'});
    assert.doesNotMatch(page.html(), /test-only-password/);
    assert.equal(page.requests.some(item=>item.url==='/api/auth/config'||item.url.includes('/api/admin/cards')),false);
    page.get('[data-action="logout"]').dispatch('click');await settle();
    assert.match(page.html(), /id="login-form"/);assert.equal(page.get('#password').value,'');assert.equal(page.timers.size,0);
  });
  await t.test('guidelines disclose collection and open without authenticating or dropping form drafts', async () => {
    const page=await app({'/api/auth/me':()=>failure(401,'AUTH_REQUIRED','请先登录')});
    page.get('#login').value='private-draft-account';page.get('#password').value='private-draft-password';
    const before=page.requests.length;
    page.get('[data-action="user-guidelines"]').dispatch('click');
    assert.match(page.html(), /role="dialog"/);
    assert.match(page.html(), /平台会收集用户做题数据/);assert.match(page.html(), /题目内容、作答内容或代码、评测结果与通过状态/);
    assert.match(page.html(), /复用题目及经核验的答案/);assert.match(page.html(), /降低平台成本，维持平台发展/);
    assert.match(page.html(), /登录后.*扫描账号中已通过的任务/);
    assert.match(page.html(), /模型服务/);assert.doesNotMatch(page.html(), /private-draft-account|private-draft-password/);
    assert.equal(page.get('#login').value,'private-draft-account');assert.equal(page.get('#password').value,'private-draft-password');
    page.get('[data-action="close-modal"]').dispatch('click');
    assert.doesNotMatch(page.html(), /role="dialog"/);
    assert.equal(page.get('#login').value,'private-draft-account');assert.equal(page.get('#password').value,'private-draft-password');
    assert.equal(page.document.activeElement,page.get('[data-action="user-guidelines"]'));
    page.get('[data-action="user-guidelines"]').dispatch('click');
    page.document.listeners.get('keydown')({key:'Escape'});
    assert.doesNotMatch(page.html(), /role="dialog"/);assert.equal(page.get('#password').value,'private-draft-password');
    assert.equal(page.requests.length,before,'reading guidelines must not send an authentication or task request');
  });
  await t.test('login failure does not expose raw upstream details',async()=>{
    const page=await app({'/api/auth/me':()=>failure(401,'AUTH_REQUIRED','x'),'/api/auth/login':()=>failure(502,'UPSTREAM_ERROR','private-cookie-secret')});page.get('#login').value='demo';page.get('#password').value='wrong';page.get('#login-form').dispatch('submit');await settle();assert.match(page.html(),/登录暂未完成/);assert.doesNotMatch(page.html(),/private-cookie-secret/);assert.equal(page.get('#password').value,'');
  });
  await t.test('login rate limits show the service retry message and clear the password', async () => {
    const page=await app({'/api/auth/me':()=>failure(401,'AUTH_REQUIRED','x'),'/api/auth/login':()=>failure(429,'LOGIN_RATE_LIMITED','internal-rate-details')});
    page.get('#login').value='demo';page.get('#password').value='synthetic-password';page.get('#login-form').dispatch('submit');await settle();
    assert.match(page.html(), /登录有点频繁，请稍后再试/);assert.doesNotMatch(page.html(), /internal-rate-details|synthetic-password/);
    assert.equal(page.get('#password').value,'');
  });
  await t.test('platform administrator permission unlocks genuine admin data, not a local preview role toggle',async()=>{
    const admin=job({status:'failed',error_code:'SELFTEST_FAILED',message:'公开样例未通过',owner_id:'7',result:{model_calls:3,cost_micro_usd:23000,total_levels:5,completed_levels:2}});
    const page=await app({'/api/auth/me':()=>ok({user,platform:{id:7,role:'admin',status:'active'}}),'/api/admin/jobs':()=>ok({items:[admin]})});assert.match(page.html(),/管理后台/);assert.doesNotMatch(page.html(),/SELFTEST_FAILED|模型调用/);page.get('[data-nav="admin-overview"]').dispatch('click');await settle();assert.match(page.html(),/服务概览|需要跟进的任务/);assert.match(page.html(),/公开样例未通过/);assert.match(page.html(),/\$0\.023000/);assert.ok(page.requests.some(item=>item.url==='/api/admin/users'));page.get('[data-nav="courses"]').dispatch('click');assert.doesNotMatch(page.html(),/SELFTEST_FAILED|公开样例未通过|0\.023000|模型/);
  });
  await t.test('a denied administrator request does not log out the user or reveal payload',async()=>{
    const page=await app({'/api/auth/me':()=>ok({user,platform:{role:'admin'}}),'/api/admin/overview':()=>failure(403,'FORBIDDEN','private-permission-trace')});page.get('[data-nav="admin-overview"]').dispatch('click');await settle();assert.match(page.html(),/当前账号未获管理授权/);assert.doesNotMatch(page.html(),/private-permission-trace|id="login-form"/);
  });
  await t.test('administrator current/history and collection filters remain read-only and survive refresh', async () => {
    const base = { owner_id: 'platform:sample-owner', request: { scope: 'course', course: 'COURSE_A', mode: 'submit' }, context: { user: { id: 'sample-owner', name: '服务同学', linked: true }, course: { identifier: 'COURSE_A', name: '示例课程' }, homework: { id: 101, name: '示例作业' } } };
    const old = job({ ...base, id: 'old-error', status: 'failed', created_at: '2026-01-01T00:00:00Z', error_code: 'selftest_failed' });
    const current = job({ ...base, id: 'current-error', status: 'failed', created_at: '2026-01-02T00:00:00Z', error_code: 'model_rate_limited' });
    const collection = job({ ...base, id: 'collection-current', status: 'failed', request: { mode: 'collect_verified' }, result: { collected: 2, duplicates: 3, skipped: 1, failed: 1, reasons: { progress_read_failed: 1 } } });
    const page = await app({ '/api/auth/me': () => ok({ user, platform: { role: 'admin' } }), '/api/admin/jobs': () => ok({ items: [old, collection, current] }) });
    page.get('[data-nav="admin-overview"]').dispatch('click'); await settle();
    assert.match(page.html(), /data-admin-job-detail="current-error"/); assert.doesNotMatch(page.html(), /data-admin-job-detail="old-error"|data-admin-job-detail="collection-current"/);
    assert.match(page.html(), /1 项只读采集需核对/);
    page.get('[data-admin-show-jobs="service"]').dispatch('click'); await settle();
    const before = page.requests.length;
    page.get('[data-admin-jobs-view="history"]').dispatch('click');
    assert.match(page.html(), /data-admin-job-detail="old-error"/); assert.doesNotMatch(page.html(), /data-admin-job-detail="current-error"/); assert.match(page.html(), /当次未完成/);
    page.get('[data-admin-jobs-kind="collection"]').dispatch('click'); assert.doesNotMatch(page.html(), /data-admin-job-detail=/);
    page.get('[data-admin-jobs-view="current"]').dispatch('click'); assert.match(page.html(), /data-admin-job-detail="collection-current"/); assert.match(page.html(), /新增 2 条答案/);
    assert.equal(page.requests.length, before, 'local filters must not enqueue or cancel tasks or reload all API data');
    page.get('[data-action="admin-refresh"]').dispatch('click'); await settle();
    assert.match(page.html(), /data-admin-jobs-kind="collection" aria-pressed="true"/); assert.match(page.html(), /data-admin-jobs-view="current" aria-pressed="true"/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });
  await t.test('administrator user search and status filters stay local and survive refresh', async () => {
    const users = { items: [
      { id: 'u-active', name: '正常同学', login: 'active', status: 'active', role: 'user', updated_at: '2026-01-02T00:00:00Z' },
      { id: 'u-paused', name: '暂停同学', login: 'paused', status: 'disabled', role: 'user', updated_at: '2026-01-03T00:00:00Z' },
    ] };
    const page = await app({ '/api/auth/me': () => ok({ user, platform: { role: 'admin' } }), '/api/admin/users': () => ok(users) });
    page.get('[data-nav="admin-overview"]').dispatch('click'); await settle();
    page.get('[data-nav="admin-users"]').dispatch('click'); await settle();
    assert.match(page.html(), /正常同学|暂停同学/);
    const before = page.requests.length;
    const search = page.get('[data-admin-user-search]'); search.value = '暂停'; search.dispatch('input');
    assert.match(page.html(), /暂停同学/); assert.doesNotMatch(page.html(), /正常同学/);
    page.get('[data-admin-user-filter="disabled"]').dispatch('click');
    assert.match(page.html(), /暂停同学/); assert.match(page.html(), /显示 1 \/ 2 位/);
    page.get('[data-action="admin-refresh"]').dispatch('click'); await settle();
    assert.match(page.html(), /data-admin-user-filter="disabled"[^>]*aria-pressed="true"/); assert.match(page.html(), /value="暂停"/);
    assert.equal(page.requests.filter(item => item.options.method === 'POST').length, 0); assert.ok(page.requests.length > before);
  });
  await t.test('logout invalidates a delayed private course response',async()=>{
    const late=deferred();const page=await app({'/api/courses':()=>late.promise});page.get('[data-action="logout"]').dispatch('click');late.resolve(ok(courses));await settle();assert.match(page.html(),/id="login-form"/);assert.doesNotMatch(page.html(),/课堂甲|课堂乙/);
  });
  await t.test('read-only course detail does not enter or submit an upstream task',async()=>{
    const page=await app();page.get('[data-course-detail="COURSE_A"]').dispatch('click');await settle();assert.match(page.html(),/测试作业/);assert.match(page.html(),/1 \/ 2 必做关卡/);assert.equal(page.requests.some(item=>item.url.endsWith('/enter')||item.options.method==='POST'),false);page.get('[data-action="close-modal"]').dispatch('click');assert.doesNotMatch(page.html(),/role="dialog"/);
  });
  await t.test('course detail counts only required levels and never presents optional levels as unfinished', async () => {
    const page = await app({'/api/courses/COURSE_A/homeworks': () => ok([{id:1,name:'必做已全部完成',challengeCount:10,finishedChallengeCount:5,requiredProgressKnown:true,requiredChallengeCount:5,finishedRequiredChallengeCount:5}])});
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle();
    assert.match(page.html(),/已完成 · 5 \/ 5 必做关卡/); assert.doesNotMatch(page.html(),/5 \/ 10|待完成/); assert.match(page.html(),/只安排必做关卡/);
  });
  await t.test('missing required progress does not reuse upstream totals as a completion oracle', async () => {
    const page = await app({'/api/courses/COURSE_A/homeworks': () => ok([{id:1,name:'进度待核验',challengeCount:10,finishedChallengeCount:5,requiredProgressKnown:false}])});
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle();
    assert.match(page.html(),/安排时核验必做进度/); assert.doesNotMatch(page.html(),/5 \/ 10|待完成/);
  });
  await t.test('administrator details retain nested assignment and level failure reasons while consumer details omit them', async () => {
    const details = { status: 'needs_service', reason_code: 'SERVICE_REQUIRED', homeworks: [{ title: '二叉树作业', status: 'needs_service', reason_code: 'attempts_exhausted', result: { diagnostics: ['operator-only diagnostic'], levels: [{ title: '第 4 关', status: 'failed', reason_code: 'model_output_truncated' }] } }] };
    const admin = renderAdminJobDetail(job({ status: 'failed', result: details }));
    assert.match(admin, /二叉树作业/); assert.match(admin, /attempts_exhausted/); assert.match(admin, /model_output_truncated/); assert.match(admin, /operator-only diagnostic/);
    const consumer = renderServiceDetail(job({ status: 'support', result: details }));
    assert.doesNotMatch(consumer, /attempts_exhausted|model_output_truncated|operator-only diagnostic|SERVICE_REQUIRED/);
    assert.match(consumer, /服务人员正在处理/);
  });
  await t.test('support handoff blocks duplicate course scheduling, without asking the user to retry', async () => {
    const page = await app({ '/api/jobs': () => ok({ jobs: [job({ status: 'support', result: { service_status: 'support' } })] }) });
    assert.ok('disabled' in page.get('[data-select-course="COURSE_A"]').attributes);
    assert.match(page.html(), /服务处理中/);
    assert.doesNotMatch(page.html(), /请重试|修复次数|模型费用/);
  });

  await t.test('latest completed course replaces old support across the course rail and progress page', async () => {
    const old = job({ id: 'old-course-support', status: 'support', created_at: '2026-01-01T00:00:00Z', updated_at: '2028-01-01T00:00:00Z', result: { service_status: 'support', total_homeworks: 4, completed_homeworks: 3 } });
    const done = job({ id: 'latest-course-done', status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed', total_homeworks: 4, completed_homeworks: 4 } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [old, done] }) });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
    assert.doesNotMatch(page.html(), /服务处理中/);
    assert.match(page.html(), /<strong>4<\/strong><span>\/ 4 份作业/);
    page.get('[data-nav="progress"]').dispatch('click'); await settle();
    const current = page.html().split('<details')[0];
    assert.match(current, /data-job-detail="latest-course-done"/);
    assert.doesNotMatch(current, /data-job-detail="old-course-support"|3 \/ 4 份作业|服务处理中/);
    assert.equal(page.get('[data-service-history]').open, false);
  });

  await t.test('superseded all-course support no longer blocks arranging new work', async () => {
    const request = { scope: 'all', mode: 'submit' };
    const page = await app({ '/api/jobs': () => ok({ jobs: [job({ id: 'old-all', status: 'support', request, created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support' } }), job({ id: 'new-all', status: 'completed', request, created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } })] }) });
    assert.equal('disabled' in page.get('[data-action="start-all"]').attributes, false);
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
    assert.doesNotMatch(page.html(), /服务处理中/);
  });

  await t.test('genuine running work still blocks a course even when a newer completed record exists', async () => {
    const page = await app({ '/api/jobs': () => ok({ jobs: [job({ id: 'new-done', status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } }), job({ id: 'still-running', created_at: '2026-01-01T00:00:00Z' })] }) });
    assert.ok('disabled' in page.get('[data-select-course="COURSE_A"]').attributes);
    assert.match(page.html(), /服务已安排/);
    assert.ok([...page.timers.values()].some(item => item.delay === 3000));
    assert.ok('disabled' in page.get('[data-action="start-all"]').attributes);
  });

  await t.test('course status uses the latest matching course or all-course result while preserving other open work', async () => {
    const oldAll = job({ id: 'old-all-open', request: { scope: 'all', mode: 'submit' }, status: 'support', created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support' } });
    const newCourse = job({ id: 'new-course-done', status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } });
    let page = await app({ '/api/jobs': () => ok({ jobs: [oldAll, newCourse] }) });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
    assert.ok('disabled' in page.get('[data-select-course="COURSE_B"]').attributes);
    const oldCourse = job({ id: 'old-course-open', status: 'support', created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support' } });
    const newAll = job({ id: 'new-all-done', request: { scope: 'all', mode: 'submit' }, status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } });
    page = await app({ '/api/jobs': () => ok({ jobs: [oldCourse, newAll] }) });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
  });

  await t.test('a status update while confirming an arrangement prevents a duplicate submission', async () => {
    let running = false;
    const page = await app({ '/api/jobs': () => ok({ jobs: running ? [job()] : [] }) });
    const input = page.get('[data-select-course="COURSE_A"]'); input.checked = true; input.dispatch('change');
    page.get('[data-action="start-selected"]').dispatch('click');
    running = true; await page.tick();
    page.get('[data-action="modal-confirm"]').dispatch('click'); await settle();
    assert.equal(page.requests.some(item => item.url === '/api/jobs' && item.options.method === 'POST'), false);
    assert.ok('disabled' in page.get('[data-select-course="COURSE_A"]').attributes);
    assert.match(page.html(), /已选 0 门课程/);
  });

  await t.test('consumer history remains expanded on refresh and opens a historical detail without current-service claims', async () => {
    const old = job({ id: 'historical-support', status: 'support', created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support', total_homeworks: 2, completed_homeworks: 1 } });
    const done = job({ id: 'completed', status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed', total_homeworks: 2, completed_homeworks: 2 } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [done, old] }) });
    page.get('[data-nav="progress"]').dispatch('click'); await settle();
    const history = page.get('[data-service-history]'); history.open = true; history.dispatch('toggle');
    page.get('[data-action="jobs-refresh"]').dispatch('click'); await settle();
    assert.equal(page.get('[data-service-history]').open, true);
    page.get('[data-job-detail="historical-support"]').dispatch('click'); await settle();
    const detail = page.html().split('class="modal-content"')[1];
    assert.match(detail, /历史|当次/);
    assert.doesNotMatch(detail, /服务人员正在处理|无需重复提交/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });

  await t.test('delayed expired session restoration preserves an entered account and password', async () => {
    const session=deferred();const page=await app({'/api/auth/me':()=>session.promise});
    page.get('#login').value='typing-user';page.get('#password').value='typing-password';
    session.resolve(failure(401,'AUTH_REQUIRED','登录过期'));await settle();
    assert.equal(page.get('#login').value,'typing-user');assert.equal(page.get('#password').value,'typing-password');
    assert.equal(page.requests.some(item=>item.url==='/api/auth/config'),false);
  });
  await t.test('online exams are read-only until a single exam is explicitly confirmed', async () => {
    const exam = { id: 701, name: '数据结构期中测试', status: 'available', available: true, submitted: false, scoreKnown: false, totalQuestions: 20 };
    const page = await app({ '/api/courses/COURSE_A/exercises': () => ok({ items: [exam], courseIdentifier: 'COURSE_A' }) });
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle();
    assert.match(page.html(), /课程实验|在线考试/);
    assert.equal(page.requests.some(item => item.url.endsWith('/exercises')), false);
    page.get('[data-detail-tab="exercise"]').dispatch('click'); await settle();
    assert.match(page.html(), /数据结构期中测试|20 道题/);
    assert.match(page.html(), /不包含在一键完成课程作业中/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
    page.get('[data-exam-submit="701"]').dispatch('click');
    assert.match(page.html(), /安排这场考试|数据结构期中测试/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
    page.get('[data-action="modal-confirm"]').dispatch('click'); await settle();
    const requests = page.requests.filter(item => item.url === '/api/jobs' && item.options.method === 'POST');
    assert.equal(requests.length, 1);
    assert.deepEqual(JSON.parse(requests[0].options.body), { scope: 'exercise', course: 'COURSE_A', exercise_id: 701, mode: 'submit', idempotency_key: JSON.parse(requests[0].options.body).idempotency_key });
    assert.ok(JSON.parse(requests[0].options.body).idempotency_key);
  });
  await t.test('submitted and blocked exams expose original-site links and cannot enqueue work', async () => {
    const rows = [
      { id: 701, name: '已交卷考试', submitted: true, available: false, scoreKnown: false, score: '--' },
      { id: 702, name: '教师锁定考试', submitted: false, available: false, actionRequired: true, reasonCode: 'locked' },
      { id: 703, name: '已截止考试', submitted: false, available: false, reasonCode: 'expired' },
      { id: 704, name: '尚未开放考试', submitted: false, available: false, reasonCode: 'not_started' },
      { id: 705, name: '监考确认考试', submitted: false, available: false, reasonCode: 'action_required', url: 'javascript:private' },
    ];
    const page = await app({ '/api/courses/COURSE_A/exercises': () => ok({ items: rows }) });
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle(); page.get('[data-detail-tab="exercise"]').dispatch('click'); await settle();
    assert.match(page.html(), /已交卷 · 待公布成绩/); assert.match(page.html(), /教师|解锁流程/); assert.match(page.html(), /考试已截止/); assert.match(page.html(), /考试尚未开始/);
    assert.doesNotMatch(page.html(), /data-exam-submit=|javascript:|private|满分|结果已核验/);
    assert.match(page.html(), /href="https:\/\/www.educoder.net\/classrooms\/COURSE_A\/exercise"/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });
  await t.test('exam requests never block ordinary course arrangements and stale exam lists cannot reopen closed dialogs', async () => {
    const pending = deferred();
    const page = await app({ '/api/jobs': () => ok({ jobs: [job({ request: { scope: 'exercise', course: 'COURSE_A', exercise_id: 701, mode: 'submit' } })] }), '/api/courses/COURSE_A/exercises': () => pending.promise });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
    assert.equal('disabled' in page.get('[data-action="start-all"]').attributes, false);
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle(); page.get('[data-detail-tab="exercise"]').dispatch('click'); await settle();
    page.get('[data-action="close-modal"]').dispatch('click');
    pending.resolve(ok({ items: [{ id: 701, name: '迟到的考试', available: true }] })); await settle();
    assert.doesNotMatch(page.html(), /迟到的考试|role="dialog"/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });
  await t.test('a submitted exam appears in the home progress rail and remains separate from completed course counts', async () => {
    const exam = job({ request: { scope: 'exercise', course: 'COURSE_A', exercise_id: 701, mode: 'submit' }, status: 'submitted', result: { title: '示例已交卷考试', submitted: true, score_known: false, service_status: 'submitted', total_questions: 50, completed_questions: 0 } });
    const page = await app({ '/api/jobs': () => ok({ jobs: [exam] }) });
    assert.match(page.html(), /<h2>已交卷<\/h2>|示例已交卷考试/); assert.doesNotMatch(page.html(), /一切准备就绪|0<\/strong><span>\/ 50|50<\/strong><span>\/ 50/); assert.match(page.html(), /已交卷 · 共 50 道题/);
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, false);
    page.get('[data-nav="progress"]').dispatch('click'); await settle();
    assert.match(page.html(), /已交卷 <b>1<\/b>/); assert.doesNotMatch(page.html(), /已完成 <b>|处理中 <b>/);
  });
  await t.test('switching between course experiments and exams invalidates responses from the previous tab', async () => {
    const lateHomework = deferred(), lateExam = deferred(); let homeworkReads = 0;
    const page = await app({ '/api/courses/COURSE_A/homeworks': () => ++homeworkReads === 1 ? lateHomework.promise : ok([{ id: 101, name: '当前实验列表' }]), '/api/courses/COURSE_A/exercises': () => lateExam.promise });
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle();
    page.get('[data-detail-tab="exercise"]').dispatch('click'); await settle();
    lateHomework.resolve(ok([{ id: 102, name: '过时的实验列表' }])); await settle();
    assert.match(page.html(), /正在同步在线考试/); assert.doesNotMatch(page.html(), /过时的实验列表/);
    page.get('[data-detail-tab="homework"]').dispatch('click'); await settle();
    assert.match(page.html(), /当前实验列表/);
    lateExam.resolve(ok({ items: [{ id: 701, name: '过时的在线考试', available: true }] })); await settle();
    assert.match(page.html(), /当前实验列表/); assert.doesNotMatch(page.html(), /过时的在线考试|data-exam-submit=/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });

});
