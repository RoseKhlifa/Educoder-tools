import assert from 'node:assert/strict';
import { test } from 'node:test';
import { renderAdminJobDetail } from './dist/admin.js';
import { renderServiceJob, renderServiceDetail } from './dist/jobs.js';
class Element {
  constructor(tag, attributes, document) {
    this.tag = tag; this.attributes = attributes; this.document = document; this.id = attributes.id || ''; this.value = attributes.value ?? ''; this.checked = 'checked' in attributes; this.required = 'required' in attributes; this.open = 'open' in attributes; this.isConnected = true; this.disabled = 'disabled' in attributes;
    this.dataset = Object.fromEntries(Object.entries(attributes).filter(([key]) => key.startsWith('data-')).map(([key,value]) => [key.slice(5).replace(/-([a-z])/g, (_, letter) => letter.toUpperCase()), value]));
    this.listeners = new Map(); this.controls = []; this.elements = { namedItem: name => this.controls.find(item => item.attributes.name === name) ?? null };
  }
  addEventListener(name, handler) { this.listeners.set(name, [...this.listeners.get(name) ?? [], handler]); }
  dispatch(name) { if ('disabled' in this.attributes) return; for (const handler of this.listeners.get(name) ?? []) handler({currentTarget:this,target:this,preventDefault(){}}); }
  reset() { for (const item of this.controls) if (['input','textarea','select'].includes(item.tag)) item.value = item.initialValue ?? ''; }
  focus() { this.document.activeElement = this; }
  setSelectionRange(start, end, direction = 'none') { this.selectionStart = start; this.selectionEnd = end; this.selectionDirection = direction; }
}
class Input extends Element {}
class Document {
  items = []; html = ''; listeners = new Map(); activeElement = null;
  constructor() { const document = this; this.root = { get innerHTML() { return document.html; }, set innerHTML(value) {
    document.html = value; document.items.forEach(item => { item.isConnected = false; });
    const modalStart = value.indexOf('role="dialog"');
    document.items = [...value.matchAll(/<(button|input|form|img|details|textarea|select|section|a)\b([^>]*)>/g)].map(match => {
      const [,tag,attrs] = match, start = match.index;
      const item = new (tag === 'input' ? Input : Element)(tag,Object.fromEntries([...attrs.matchAll(/([\w-]+)(?:="([^"]*)")?/g)].map(([,key,value])=>[key,value??''])),document);
      item.start = start; item.inModal = modalStart >= 0 && start >= modalStart;
      if (tag === 'select') item.value = value.slice(start,value.indexOf('</select>',start)).match(/<option value="([^"]*)"/)?.[1] ?? '';
      item.initialValue = item.value; return item;
    });
    for (const form of document.items.filter(item=>item.tag==='form')) {
      const end=value.indexOf('</form>',form.start);form.controls=document.items.filter(item=>item.start>form.start&&item.start<end&&'name'in item.attributes);
    }
  } }; }
  querySelector(selector) { return selector === '#app' ? this.root : this.querySelectorAll(selector)[0] ?? null; }
  querySelectorAll(selector) {
    if(selector.includes(',')) return [...new Set(selector.split(',').flatMap(part=>this.querySelectorAll(part.trim())))].sort((a,b)=>a.start-b.start);
    if(selector.startsWith('.modal ')) return this.items.filter(item=>item.inModal&&(item.tag===selector.slice(7)||selector.slice(7)==='a[href]'&&item.tag==='a'&&'href'in item.attributes));
    if(selector.startsWith('#')) return this.items.filter(item => item.attributes.id === selector.slice(1)); const match=selector.match(/^\[([^=\]]+)(?:="([^"]*)")?\]$/);return match?this.items.filter(item => match[1] in item.attributes && (match[2]===undefined||item.attributes[match[1]]===match[2])):[];
  }
  addEventListener(name, handler) { this.listeners.set(name,handler); }
}
const ok = data => new Response(JSON.stringify({data,error:null}));
const failure=(status,code,message)=>new Response(JSON.stringify({data:null,error:{code,message}}),{status});
const user={login:'demo',name:'<测试同学>',avatarUrl:'/api/avatar'};
const courses=[{identifier:'COURSE_A',name:'课堂甲',homeworkCount:2,teacherName:'合成老师',studentCount:25},{identifier:'COURSE_B',name:'课堂乙',homeworkCount:3,teacherName:'<老师>',studentCount:0}];
const serviceDate=()=>new Date(Date.now()+8*60*60*1000).toISOString().slice(0,10);
const job=(extra={})=>({id:'job-1',status:'processing',phase:'processing',request:{scope:'course',course:'COURSE_A',mode:'submit'},created_at:'2026-10-31T00:00:00Z',updated_at:'2026-10-31T00:00:00Z',result:{service_status:'processing',total_homeworks:2,completed_homeworks:1},...extra});
const settle=async()=>{for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));};
const deferred=()=>{let resolve;const promise=new Promise(done=>{resolve=done;});return{promise,resolve};};
const supportTicket=(extra={})=>({id:'ticket-one',subject:'需要核对课程进度',category:'task',status:'open',created_at:'2026-11-02T01:00:00Z',updated_at:'2026-11-02T02:00:00Z',message_count:1,last_author_role:'user',...extra});
const supportDetail=(ticket=supportTicket(),body='请帮忙核对课程完成情况')=>({ticket,messages:[{id:'message-one',author_role:'user',body,created_at:'2026-11-02T01:00:00Z'}]});
const supportList=(items,next_cursor='')=>({items,next_cursor,limit:30});
const fillSupport=(page,subject='测试问题',body='测试反馈内容',category='task')=>{page.get('#ticket-subject').value=subject;page.get('#ticket-category').value=category;page.get('#ticket-body').value=body;};
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
    if(url==='/api/service/today-summary')return ok({date:serviceDate(),timezone:'Asia/Shanghai',completed_students:7,completed_questions:103,total_served_students:28,total_completed_questions:1042});
    if(url.startsWith('/api/tickets')||url.startsWith('/api/admin/tickets'))return ok({items:[],next_cursor:'',limit:30});
    if(url==='/api/question-bank/summary')return ok({verified_objective_questions:272,verified_programming_problems:8,total_verified_questions:280,contributed_questions:12});
    if(url==='/api/jobs')return options.method==='POST'?ok(job({request:JSON.parse(options.body)})):ok({jobs:[]});
    if(url.endsWith('/homeworks'))return ok([{id:1,name:'测试作业',challengeCount:2,finishedChallengeCount:1,requiredProgressKnown:true,requiredChallengeCount:2,finishedRequiredChallengeCount:1}]);
    if(url.endsWith('/exercises'))return ok({items:[]});
    if(url.startsWith('/api/admin/'))return ok(url==='/api/admin/overview'?{total_users:3,running_jobs:1,capacity:{model_concurrency:8}}:{items:[]});
    throw new Error(`Unexpected URL ${url}`);
  };
  await import(`./dist/app.js?test=${++version}`);await settle();
  return {requests,timers,document,html:()=>document.html,get(selector){const element=document.querySelector(selector);assert.ok(element,`Missing ${selector}`);return element;},async tick(){const tasks=[...timers.entries()];for(const[id,timer]of tasks){timers.delete(id);timer.handler();}await settle();}};
}
await test('consumer course service and administrator boundary',async t=>{
  await t.test('retry keeps the original server scope, guards duplicate clicks and moves the old attempt into history', async () => {
    const source=job({id:'retry-source',status:'support',can_retry:true,can_cancel:false,result:{service_status:'support',total_homeworks:2,completed_homeworks:1}});
    const child=job({id:'retry-child',status:'queued',can_retry:false,can_cancel:true,created_at:'2026-11-02T12:00:00Z'}), pending=deferred();
    let rows=[source];
    const page=await app({'/api/jobs':()=>ok({jobs:rows}),'/api/jobs/retry-source/retry':()=>pending.promise});
    assert.equal(page.get('[data-select-course="COURSE_A"]').disabled,false);
    assert.equal(page.get('[data-action="start-all"]').disabled,false);
    assert.match(page.html(),/可重新执行/); assert.doesNotMatch(page.html(),/放心，服务人员正在处理，无需重复提交/);
    page.get('[data-nav="progress"]').dispatch('click'); await settle();
    page.get('[data-job-detail="retry-source"]').dispatch('click');
    assert.match(page.html().slice(page.html().indexOf('role="dialog"')),/data-job-retry="retry-source"/);
    page.get('[data-job-retry="retry-source"]').dispatch('click');
    assert.match(page.html(),/继续原来的安排范围/);assert.match(page.html(),/已核验通过的关卡会自动跳过/);
    assert.equal(page.requests.some(item=>item.options.method==='POST'),false);
    const confirm=page.get('[data-action="modal-confirm"]');confirm.dispatch('click');confirm.dispatch('click');await settle();
    const posted=page.requests.filter(item=>item.url==='/api/jobs/retry-source/retry');
    assert.equal(posted.length,1);assert.equal(posted[0].options.method,'POST');assert.deepEqual(JSON.parse(posted[0].options.body),{});
    assert.equal(page.requests.some(item=>item.url==='/api/jobs'&&item.options.method==='POST'),false);
    assert.match(page.html(),/disabled>正在重新安排/);assert.doesNotMatch(page.html(),/已重新安排，/);
    rows=[child,source];pending.resolve(ok(child));await settle();
    assert.match(page.html(),/已重新安排，已完成的关卡会自动跳过/);
    const current=page.html().split('data-service-history')[0];assert.match(current,/data-job-detail="retry-child"/);assert.doesNotMatch(current,/data-job-detail="retry-source"/);
    assert.match(page.html(),/data-job-detail="retry-source" data-job-history="true"/);
    assert.doesNotMatch(page.html(),/data-job-retry="retry-source"/);
    assert.ok(page.requests.filter(item=>item.url==='/api/courses/COURSE_A/homeworks').length>=2);
    page.get('[data-nav="courses"]').dispatch('click');assert.equal(page.get('[data-select-course="COURSE_A"]').disabled,true);
  });
  await t.test('retry failure keeps the original job and never announces acceptance or leaks provider errors', async () => {
    const source=job({status:'support',can_retry:true,result:{service_status:'support'}});
    for (const fail of [()=>{throw new Error('PRIVATE_NETWORK_TRACE');},()=>failure(409,'JOB_RETRY_CONFLICT','PRIVATE_CONFLICT')]) {
      const page=await app({'/api/jobs':()=>ok({jobs:[source]}),'/api/jobs/job-1/retry':fail});
      page.get('[data-nav="progress"]').dispatch('click');await settle();page.get('[data-job-retry="job-1"]').dispatch('click');page.get('[data-action="modal-confirm"]').dispatch('click');await settle();
      assert.match(page.html(),/暂未确认重新执行结果，请刷新进度后重试/);assert.match(page.html(),/data-job-retry="job-1"/);
      assert.doesNotMatch(page.html(),/PRIVATE_|已重新安排，/);
    }
    const expired=await app({'/api/jobs':()=>ok({jobs:[source]}),'/api/jobs/job-1/retry':()=>failure(401,'UPSTREAM_AUTH_EXPIRED','PRIVATE_AUTH')});
    expired.get('[data-nav="progress"]').dispatch('click');await settle();expired.get('[data-job-retry="job-1"]').dispatch('click');expired.get('[data-action="modal-confirm"]').dispatch('click');await settle();
    assert.match(expired.html(),/id="login-form"/);assert.match(expired.html(),/登录已过期/);assert.doesNotMatch(expired.html(),/PRIVATE_AUTH|已重新安排，/);
  });
  await t.test('stop waits for confirmed termination before offering retry and updates an open detail dialog', async () => {
    const running=job({can_cancel:true,can_retry:false}), stopping={...running,can_cancel:false}, stopped=job({status:'canceled',can_retry:true,can_cancel:false,result:{service_status:'support'}});
    const child=job({id:'after-stop',status:'queued',can_retry:false,can_cancel:true,created_at:'2026-11-02T12:00:00Z'});let rows=[running];
    const page=await app({'/api/jobs':()=>ok({jobs:rows}),'/api/jobs/job-1/cancel':()=>{rows=[stopping];return ok(stopping);},'/api/jobs/job-1/retry':()=>{rows=[child,stopped];return ok(child);}});
    page.get('[data-nav="progress"]').dispatch('click');await settle();page.get('[data-job-cancel="job-1"]').dispatch('click');
    assert.match(page.html(),/等待任务确认停止/);assert.equal(page.requests.some(item=>item.url.endsWith('/cancel')),false);
    page.get('[data-action="modal-confirm"]').dispatch('click');await settle();assert.match(page.html(),/正在停止…/);
    assert.doesNotMatch(page.html(),/data-job-retry/);assert.equal(page.requests.some(item=>item.url.endsWith('/retry')),false);
    await page.tick();assert.match(page.html(),/正在停止…/);assert.doesNotMatch(page.html(),/data-job-retry/);
    page.get('[data-job-detail="job-1"]').dispatch('click');rows=[stopped];await page.tick();
    const dialog=page.html().slice(page.html().indexOf('role="dialog"'));assert.match(dialog,/data-job-retry="job-1"/);assert.doesNotMatch(dialog,/正在停止/);
    page.get('[data-job-retry="job-1"]').dispatch('click');page.get('[data-action="modal-confirm"]').dispatch('click');await settle();
    assert.equal(page.requests.filter(item=>item.url==='/api/jobs/job-1/cancel').length,1);assert.equal(page.requests.filter(item=>item.url==='/api/jobs/job-1/retry').length,1);
    assert.equal(page.requests.some(item=>item.url==='/api/jobs'&&item.options.method==='POST'),false);assert.match(page.html(),/data-job-detail="after-stop"/);
  });
  await t.test('a current detail becomes read-only history when a newer attempt arrives during polling', async () => {
    const source=job({id:'source-dialog',status:'support',can_retry:true,result:{service_status:'support'}}),child=job({id:'new-dialog',created_at:'2026-11-02T12:00:00Z'});let rows=[source];
    const page=await app({'/api/jobs':()=>ok({jobs:rows})});page.get('[data-nav="progress"]').dispatch('click');await settle();
    page.get('[data-job-detail="source-dialog"]').dispatch('click');assert.match(page.html().slice(page.html().indexOf('role="dialog"')),/data-job-retry="source-dialog"/);
    rows=[child,source];await page.tick();const dialog=page.html().slice(page.html().indexOf('role="dialog"'));
    assert.match(dialog,/历史记录：以下为当次安排/);assert.doesNotMatch(dialog,/data-job-retry|data-job-cancel/);
  });
  await t.test('retryable terminal jobs unlock selection while active and unresolved support remain blocked', async () => {
    for (const [status,can_retry,blocked] of [['support',true,false],['failed',true,false],['support',false,true],['support',undefined,true],['running',true,true]]) {
      const source=job({status,can_retry,result:{service_status:'support'}}),page=await app({'/api/jobs':()=>ok({jobs:[source]})});
      assert.equal(page.get('[data-select-course="COURSE_A"]').disabled,blocked,`${status}/${can_retry}`);
      assert.equal(page.get('[data-action="start-all"]').disabled,blocked,`${status}/${can_retry}`);
    }
  });

  await t.test('different homework attempts cannot mask another current blocker in the same course or a global arrangement', async () => {
    const first={scope:'homework',course:'COURSE_A',homework_id:11,mode:'submit'},second={...first,homework_id:12};
    const newer=job({id:'newer-retryable',request:second,status:'support',can_retry:true,created_at:'2026-11-02T12:00:00Z',result:{service_status:'support'}});
    for (const prior of [
      job({id:'older-support',request:first,status:'support',can_retry:false,result:{service_status:'support'}}),
      job({id:'older-running',request:first,status:'running',can_retry:false,result:{service_status:'processing'}}),
      job({id:'global-support',request:{scope:'all',mode:'submit'},status:'support',can_retry:false,result:{service_status:'support'}}),
    ]) {
      const page=await app({'/api/jobs':()=>ok({jobs:[newer,prior]})});
      assert.equal(page.get('[data-select-course="COURSE_A"]').disabled,true,prior.id);
      assert.equal(page.get('[data-action="start-all"]').disabled,true,prior.id);
      assert.equal(page.get('[data-select-course="COURSE_B"]').disabled,prior.request.scope==='all',prior.id);
      page.get('[data-action="select-all"]').dispatch('click');
      assert.equal(page.get('[data-select-course="COURSE_A"]').checked,false,prior.id);
      assert.equal(page.requests.some(item=>item.url==='/api/jobs'&&item.options.method==='POST'),false);
    }
    const completed=job({id:'older-completed',request:first,status:'completed',result:{service_status:'completed'}});
    const page=await app({'/api/jobs':()=>ok({jobs:[newer,completed]})});
    assert.equal(page.get('[data-select-course="COURSE_A"]').disabled,false);
    assert.equal(page.get('[data-action="start-all"]').disabled,false);
  });
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
    assert.match(page.html(), /您已为题库贡献 <strong>12<\/strong> 道题，感谢您的付出。/);assert.doesNotMatch(page.html(), /bank-count-note|bank-reuse-note/);assert.match(page.html(), /不承诺成绩或完成时限/);
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
  await t.test('missing contribution evidence stays unknown while verified totals remain usable', async () => {
    const page=await app({'/api/question-bank/summary':()=>ok({verified_objective_questions:272,verified_programming_problems:8,total_verified_questions:280})});
    const bank=page.html().split('aria-label="题库概况"')[1].split('</section>')[0];
    assert.match(bank,/bank-total"><strong>280<\/strong>/);assert.match(bank,/正在核对您的题库贡献，感谢您的支持。/);
    assert.doesNotMatch(bank,/您已为题库贡献.*(?:<strong>)?0/);
  });
  await t.test('a bank response after logout never restores private account UI', async () => {
    const late=deferred();const page=await app({'/api/question-bank/summary':()=>late.promise});
    page.get('[data-action="logout"]').dispatch('click');late.resolve(ok({verified_objective_questions:900,verified_programming_problems:1,total_verified_questions:901,contributed_questions:77}));await settle();
    assert.match(page.html(), /id="login-form"/);assert.doesNotMatch(page.html(), /题库概况|901|77|课堂甲/);
    assert.equal(page.timers.size,0);
  });
  await t.test('daily completions replace connection labels and use real teacher/student fields', async () => {
    const page=await app();
    assert.match(page.html(), /aria-label="累计服务"/);assert.match(page.html(), /已为 <strong>28<\/strong> 位同学/);assert.match(page.html(), /完成 <strong>1,042<\/strong> 道题/);
    assert.match(page.html(), /其中今天 <b>7<\/b> 位同学 · <b>103<\/b> 道题/);
    assert.match(page.html(), /北京时间.*按可核验记录累计/);assert.doesNotMatch(page.html(), /头歌已连接|头歌账号已连接|<h3>服务状态<\/h3>|class="connection"/);
    const meta=page.html().match(/class="course-meta">([\s\S]*?)<\/td>/)?.[1] || '';
    assert.match(meta,/授课教师/);assert.match(meta,/合成老师/);assert.match(meta,/学生人数/);assert.match(meta,/25 人/);assert.doesNotMatch(meta,/COURSE_A/);
    assert.match(page.html(), /&lt;老师&gt;/);assert.match(page.html(), /0 人/);
    assert.equal(page.requests.filter(item=>item.url==='/api/service/today-summary').length,1);await page.tick();assert.equal(page.requests.filter(item=>item.url==='/api/service/today-summary').length,1);
  });
  await t.test('missing people metadata and unavailable daily totals stay explicitly unknown', async () => {
    const page=await app({'/api/courses':()=>ok([{identifier:'COURSE_A',name:'信息待同步',membersCount:99}]),'/api/service/today-summary':()=>failure(503,'SERVICE_SUMMARY_UNAVAILABLE','private-stat-error')});
    assert.match(page.html(),/教师待同步/);assert.match(page.html(),/人数待同步/);assert.match(page.html(),/服务统计暂未同步/);assert.doesNotMatch(page.html(),/99 人|private-stat-error|完成 <strong>0/);
    const old=await app({'/api/service/today-summary':()=>ok({date:'2020-01-01',timezone:'Asia/Shanghai',completed_students:999,completed_questions:999,total_served_students:999,total_completed_questions:999})});
    assert.match(old.html(),/服务统计暂未同步/);assert.doesNotMatch(old.html(),/完成 <strong>999|已为 <strong>999/);
  });
  await t.test('login prefetches both lists and opening cached details does not wait or request again', async () => {
    const page=await app();
    for(const id of ['COURSE_A','COURSE_B'])for(const kind of ['homeworks','exercises'])assert.equal(page.requests.filter(item=>item.url===`/api/courses/${id}/${kind}`).length,1);
    const before=page.requests.length;page.get('[data-course-detail="COURSE_A"]').dispatch('click');
    assert.match(page.html(),/测试作业/);assert.doesNotMatch(page.html(),/正在同步列表/);
    page.get('[data-detail-tab="exercise"]').dispatch('click');assert.match(page.html(),/暂无在线考试/);
    page.get('[data-detail-tab="homework"]').dispatch('click');assert.match(page.html(),/测试作业/);
    assert.equal(page.requests.length,before);assert.equal(page.requests.some(item=>item.url.endsWith('/enter')||item.options.method==='POST'),false);
  });
  await t.test('the detail SVG refresh revalidates both tabs and retains previous results on failure', async () => {
    let step=1,failed=false;
    const page=await app({
      '/api/courses/COURSE_A/homeworks':()=>failed?failure(503,'UPSTREAM','private-refresh-error'):ok([{id:1,name:`实验版本${step}`}]),
      '/api/courses/COURSE_A/exercises':()=>failed?failure(503,'UPSTREAM','private-refresh-error'):ok({items:[{id:701,name:`考试版本${step}`,available:false,submitted:true,scoreKnown:false}]}),
    });
    page.get('[data-course-detail="COURSE_A"]').dispatch('click');assert.match(page.html(),/实验版本1/);
    step=2;page.get('[data-action="detail-refresh"]').dispatch('click');assert.match(page.html(),/实验版本1/);await settle();assert.match(page.html(),/实验版本2/);
    page.get('[data-detail-tab="exercise"]').dispatch('click');assert.match(page.html(),/考试版本2/);
    failed=true;page.get('[data-action="detail-refresh"]').dispatch('click');await settle();assert.match(page.html(),/考试版本2/);assert.match(page.html(),/暂未更新，继续显示上次结果/);assert.doesNotMatch(page.html(),/private-refresh-error/);
    assert.equal(page.requests.filter(item=>item.url==='/api/courses/COURSE_A/homeworks').length,3);assert.equal(page.requests.filter(item=>item.url==='/api/courses/COURSE_A/exercises').length,3);
    assert.equal(page.requests.some(item=>item.options.method==='POST'),false);
  });
  await t.test('old prefetched details cannot populate a subsequent login session', async () => {
    const old=deferred();let fresh=false;
    const page=await app({'/api/courses/COURSE_A/homeworks':()=>fresh?ok([{id:2,name:'新会话作业'}]):old.promise});
    page.get('[data-action="logout"]').dispatch('click');await settle();fresh=true;
    page.get('#login').value='synthetic-other';page.get('#password').value='synthetic-only';page.get('#login-form').dispatch('submit');await settle();
    old.resolve(ok([{id:1,name:'旧会话私有作业'}]));await settle();page.get('[data-course-detail="COURSE_A"]').dispatch('click');
    assert.match(page.html(),/新会话作业/);assert.doesNotMatch(page.html(),/旧会话私有作业/);
    assert.equal(page.requests.some(item=>item.url==='/api/jobs'&&item.options.method==='POST'),false);
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

  await t.test('different course and all-course scopes preserve each current unresolved blocker', async () => {
    const oldAll = job({ id: 'old-all-open', request: { scope: 'all', mode: 'submit' }, status: 'support', created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support' } });
    const newCourse = job({ id: 'new-course-done', status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } });
    let page = await app({ '/api/jobs': () => ok({ jobs: [oldAll, newCourse] }) });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, true);
    assert.ok('disabled' in page.get('[data-select-course="COURSE_B"]').attributes);
    const oldCourse = job({ id: 'old-course-open', status: 'support', created_at: '2026-01-01T00:00:00Z', result: { service_status: 'support' } });
    const newAll = job({ id: 'new-all-done', request: { scope: 'all', mode: 'submit' }, status: 'completed', created_at: '2026-01-02T00:00:00Z', result: { service_status: 'completed' } });
    page = await app({ '/api/jobs': () => ok({ jobs: [oldCourse, newAll] }) });
    assert.equal('disabled' in page.get('[data-select-course="COURSE_A"]').attributes, true);
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
    assert.equal(page.requests.filter(item => item.url==='/api/courses/COURSE_A/exercises').length, 1, 'exam catalog should already be prefetched without starting the exam');
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
    const page = await app({ '/api/courses/COURSE_A/homeworks': () => ++homeworkReads === 1 ? lateHomework.promise : ok([{ id: 101, name: '再次读取的实验列表' }]), '/api/courses/COURSE_A/exercises': () => lateExam.promise });
    page.get('[data-course-detail="COURSE_A"]').dispatch('click'); await settle();
    page.get('[data-detail-tab="exercise"]').dispatch('click'); await settle();
    lateHomework.resolve(ok([{ id: 102, name: '后台预取的实验列表' }])); await settle();
    assert.match(page.html(), /正在同步列表/); assert.doesNotMatch(page.html(), /后台预取的实验列表/);
    page.get('[data-detail-tab="homework"]').dispatch('click'); await settle();
    assert.match(page.html(), /后台预取的实验列表/);
    lateExam.resolve(ok({ items: [{ id: 701, name: '过时的在线考试', available: true }] })); await settle();
    assert.match(page.html(), /后台预取的实验列表/); assert.doesNotMatch(page.html(), /过时的在线考试|data-exam-submit=/);
    assert.equal(page.requests.some(item => item.options.method === 'POST'), false);
  });

  await t.test('support is available to users and draft values survive polling refresh filters and page navigation', async () => {
    const page=await app();page.get('[data-nav="support"]').dispatch('click');await settle();
    assert.match(page.html(),/问题反馈|id="ticket-create-form"/);assert.doesNotMatch(page.html(),/data-nav="admin-support"/);
    fillSupport(page,'会话内私密草稿','第一行\n第二行','data');
    await page.tick();assert.equal(page.get('#ticket-body').value,'第一行\n第二行');assert.equal(page.get('#ticket-category').value,'data');
    page.get('[data-ticket-refresh]').dispatch('click');await settle();assert.equal(page.get('#ticket-subject').value,'会话内私密草稿');
    page.get('[data-ticket-filter="open"]').dispatch('click');await settle();assert.equal(page.get('#ticket-body').value,'第一行\n第二行');
    page.get('[data-nav="courses"]').dispatch('click');page.get('[data-nav="support"]').dispatch('click');await settle();
    assert.equal(page.get('#ticket-subject').value,'会话内私密草稿');assert.equal(page.get('#ticket-category').value,'data');assert.equal(page.get('#ticket-body').value,'第一行\n第二行');
    assert.doesNotMatch(page.html(),/会话内私密草稿|第一行/,'drafts must be restored through DOM values, not interpolated into HTML');
    assert.equal(page.requests.some(item=>item.options.method==='POST'),false);
    assert.ok(page.requests.some(item=>item.url==='/api/tickets?limit=30&status=open'));
  });
  await t.test('ticket creation prevents duplicate submission and ambiguous retry keeps the same payload key and draft', async () => {
    const pending=deferred();let attempts=0;const created=supportDetail(supportTicket({id:'created-ticket',subject:'测试问题'}));
    const page=await app({'/api/tickets':options=>++attempts===1?pending.promise:ok(created)});
    page.get('[data-nav="support"]').dispatch('click');await settle();fillSupport(page);
    const form=page.get('#ticket-create-form');form.dispatch('submit');form.dispatch('submit');await settle();
    const posts=()=>page.requests.filter(item=>item.url==='/api/tickets'&&item.options.method==='POST');assert.equal(posts().length,1);
    assert.ok(page.get('#ticket-body').disabled);pending.resolve(failure(503,'TICKETS_UNAVAILABLE','PRIVATE_INTERNAL_MESSAGE'));await settle();
    assert.equal(page.get('#ticket-body').value,'测试反馈内容');assert.doesNotMatch(page.html(),/PRIVATE_INTERNAL_MESSAGE/);
    page.get('#ticket-create-form').dispatch('submit');await settle();assert.equal(posts().length,2);
    assert.deepEqual(JSON.parse(posts()[0].options.body),JSON.parse(posts()[1].options.body));assert.ok(JSON.parse(posts()[0].options.body).idempotency_key);
    assert.match(page.html(),/role="dialog"/);assert.match(page.html(),/data-ticket-detail="created-ticket"/);
    assert.equal(page.get('#ticket-subject').value,'');assert.equal(page.get('#ticket-body').value,'');
    assert.equal(page.requests.some(item=>item.url==='/api/jobs'&&item.options.method==='POST'),false);
  });
  await t.test('users can append messages close and reopen their ticket with fixed error text and no admin mutations', async () => {
    let current=supportDetail(),replyCalls=0;
    const page=await app({
      '/api/tickets?limit=30':()=>ok(supportList([current.ticket])),
      '/api/tickets/ticket-one':()=>ok(current),
      '/api/tickets/ticket-one/messages':options=>{
        if(++replyCalls===1)throw new TypeError('PRIVATE_NETWORK_DETAIL');
        current={ticket:{...current.ticket,message_count:2},messages:[...current.messages,{id:'m2',author_role:'user',body:JSON.parse(options.body).body,created_at:'2026-11-02T03:00:00Z'}]};return ok(current);
      },
      '/api/tickets/ticket-one/status':options=>{current={...current,ticket:{...current.ticket,status:JSON.parse(options.body).status}};return ok(current);},
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    page.get('#ticket-reply-body').value='补充情况\n第二行';page.get('#ticket-reply-form').dispatch('submit');await settle();
    assert.equal(page.get('#ticket-reply-body').value,'补充情况\n第二行');assert.doesNotMatch(page.html(),/PRIVATE_NETWORK_DETAIL/);
    page.get('#ticket-reply-form').dispatch('submit');await settle();
    const posts=page.requests.filter(item=>item.url.endsWith('/messages'));assert.equal(posts.length,2);assert.deepEqual(JSON.parse(posts[0].options.body),JSON.parse(posts[1].options.body));
    assert.match(page.html(),/补充情况<br>第二行/);assert.equal(page.get('#ticket-reply-body').value,'');
    page.get('[data-ticket-status="closed"]').dispatch('click');await settle();assert.doesNotMatch(page.html(),/id="ticket-reply-form"/);assert.match(page.html(),/这条反馈已关闭/);
    page.get('[data-ticket-status="open"]').dispatch('click');await settle();assert.match(page.html(),/id="ticket-reply-form"/);
    assert.equal(page.requests.some(item=>item.url.startsWith('/api/admin/')),false);
  });
  await t.test('admin feedback namespace filters and pagination remain separate from the user ticket list', async () => {
    const first=supportTicket({user:{name:'甲同学'}}),second=supportTicket({id:'ticket-two',subject:'第二条反馈',user:{name:'乙同学'}});let detail=supportDetail(first);
    const page=await app({
      '/api/auth/me':()=>ok({user,platform:{role:'admin'}}),
      '/api/tickets?limit=30':()=>ok(supportList([supportTicket({id:'own-ticket',subject:'我自己的反馈'})])),
      '/api/admin/tickets?limit=30':()=>ok(supportList([first],'next/+=')),
      '/api/admin/tickets?limit=30&cursor=next%2F%2B%3D':()=>ok(supportList([first,second])),
      '/api/admin/tickets?limit=30&status=processing':()=>ok(supportList([{...second,status:'processing'}])),
      '/api/admin/tickets/ticket-two':()=>ok({...detail,ticket:second}),
      '/api/admin/tickets/ticket-two/messages':options=>{detail={ticket:{...second,status:'processing',message_count:2,last_author_role:'admin'},messages:[...detail.messages,{id:'m2',author_role:'admin',body:JSON.parse(options.body).body,created_at:'2026-11-02T03:00:00Z'}]};return ok(detail);},
      '/api/admin/tickets/ticket-two/status':options=>{detail={...detail,ticket:{...detail.ticket,status:JSON.parse(options.body).status}};return ok(detail);},
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();assert.match(page.html(),/我自己的反馈/);
    page.get('[data-nav="admin-overview"]').dispatch('click');await settle();page.get('[data-nav="admin-support"]').dispatch('click');await settle();
    assert.match(page.html(),/甲同学/);assert.doesNotMatch(page.html(),/我自己的反馈|id="ticket-create-form"/);
    page.get('[data-ticket-more="next/+="]').dispatch('click');await settle();assert.match(page.html(),/乙同学/);
    assert.equal(page.document.querySelectorAll('[data-ticket-open="ticket-one"]').length,2,'one title and one action for the deduplicated row');
    page.get('[data-ticket-filter="processing"]').dispatch('click');await settle();assert.doesNotMatch(page.html(),/甲同学/);
    page.get('[data-ticket-open="ticket-two"]').dispatch('click');await settle();page.get('#ticket-reply-body').value='已收到，正在核对。';page.get('#ticket-reply-form').dispatch('submit');await settle();
    assert.match(page.html(),/已收到，正在核对。/);page.get('[data-ticket-status="resolved"]').dispatch('click');await settle();assert.match(page.html(),/已解决/);
    assert.ok(page.requests.some(item=>item.url==='/api/admin/tickets/ticket-two/messages'&&item.options.method==='POST'));
    assert.deepEqual(JSON.parse(page.requests.find(item=>item.url==='/api/admin/tickets/ticket-two/status').options.body),{status:'resolved'});
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-nav="courses"]').dispatch('click');page.get('[data-nav="support"]').dispatch('click');await settle();assert.match(page.html(),/我自己的反馈/);assert.doesNotMatch(page.html(),/甲同学|乙同学/);
  });
  await t.test('new list filters invalidate older results and closing a loading ticket prevents a late dialog reopening', async () => {
    const list=deferred(),detail=deferred();const current=supportTicket({id:'current-ticket',subject:'当前处理中反馈',status:'processing'});
    const page=await app({
      '/api/tickets?limit=30':()=>list.promise,
      '/api/tickets?limit=30&status=processing':()=>ok(supportList([current])),
      '/api/tickets/current-ticket':()=>detail.promise,
    });
    page.get('[data-nav="support"]').dispatch('click');page.get('[data-ticket-filter="processing"]').dispatch('click');await settle();
    list.resolve(ok(supportList([supportTicket({subject:'过时的旧列表'})])));await settle();assert.match(page.html(),/当前处理中反馈/);assert.doesNotMatch(page.html(),/过时的旧列表/);
    page.get('[data-ticket-open="current-ticket"]').dispatch('click');page.get('[data-action="close-modal"]').dispatch('click');detail.resolve(ok(supportDetail(current,'不应显示的迟到详情')));await settle();
    assert.doesNotMatch(page.html(),/role="dialog"|不应显示的迟到详情/);
  });
  await t.test('an old detail refresh cannot overwrite the later successful reply and draft remains through refresh', async () => {
    const stale=deferred();let reads=0;const original=supportDetail();const replied={ticket:{...original.ticket,message_count:2},messages:[...original.messages,{id:'m2',author_role:'user',body:'已成功保存的新补充',created_at:'2026-11-02T03:00:00Z'}]};
    const page=await app({
      '/api/tickets?limit=30':()=>ok(supportList([original.ticket])),
      '/api/tickets/ticket-one':()=>++reads===1?ok(original):stale.promise,
      '/api/tickets/ticket-one/messages':()=>ok(replied),
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    page.get('#ticket-reply-body').value='待发送的草稿';page.get('[data-ticket-detail-refresh="ticket-one"]').dispatch('click');assert.equal(page.get('#ticket-reply-body').value,'待发送的草稿');
    page.get('#ticket-reply-form').dispatch('submit');await settle();assert.match(page.html(),/已成功保存的新补充/);
    stale.resolve(ok({...original,messages:[{...original.messages[0],body:'旧GET详情不应覆盖'}]}));await settle();assert.match(page.html(),/已成功保存的新补充/);assert.doesNotMatch(page.html(),/旧GET详情不应覆盖/);
  });
  await t.test('reply drafts stay isolated by ticket and a late send never reopens or locks a different dialog', async () => {
    const pending=deferred();const one=supportDetail(),two=supportDetail(supportTicket({id:'ticket-two',subject:'另一条反馈'}),'另一条对话');
    const page=await app({
      '/api/tickets?limit=30':()=>ok(supportList([one.ticket,two.ticket])),
      '/api/tickets/ticket-one':()=>ok(one),'/api/tickets/ticket-two':()=>ok(two),'/api/tickets/ticket-one/messages':()=>pending.promise,
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();page.get('#ticket-reply-body').value='第一条草稿';
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-ticket-open="ticket-two"]').dispatch('click');await settle();assert.equal(page.get('#ticket-reply-body').value,'');page.get('#ticket-reply-body').value='第二条草稿';
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();assert.equal(page.get('#ticket-reply-body').value,'第一条草稿');
    page.get('#ticket-reply-form').dispatch('submit');page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-ticket-open="ticket-two"]').dispatch('click');await settle();assert.equal(page.get('#ticket-reply-body').value,'第二条草稿');assert.ok(page.get('#ticket-reply-body').disabled);
    pending.resolve(ok({...one,ticket:{...one.ticket,message_count:2}}));await settle();assert.match(page.html(),/另一条对话/);assert.equal(page.get('#ticket-reply-body').value,'第二条草稿');assert.equal(page.get('#ticket-reply-body').disabled,false);
    assert.doesNotMatch(page.html(),/data-ticket-detail="ticket-one"/);
  });
  await t.test('logout clears all support drafts keys and late private responses before another login', async () => {
    const late=deferred();let post=0;const page=await app({'/api/tickets':()=>++post===1?late.promise:failure(503,'TICKETS_UNAVAILABLE','private')});
    page.get('[data-nav="support"]').dispatch('click');await settle();fillSupport(page);page.get('#ticket-create-form').dispatch('submit');await settle();
    const first=JSON.parse(page.requests.find(item=>item.url==='/api/tickets').options.body).idempotency_key;
    page.get('[data-action="logout"]').dispatch('click');late.resolve(ok(supportDetail(supportTicket({subject:'迟到的私有工单'}))));await settle();assert.match(page.html(),/id="login-form"/);assert.doesNotMatch(page.html(),/迟到的私有工单|ticket-create-form|role="dialog"/);
    page.get('#login').value='demo';page.get('#password').value='fixture-password';page.get('#login-form').dispatch('submit');await settle();page.get('[data-nav="support"]').dispatch('click');await settle();
    assert.equal(page.get('#ticket-subject').value,'');assert.equal(page.get('#ticket-body').value,'');fillSupport(page);page.get('#ticket-create-form').dispatch('submit');await settle();
    const posts=page.requests.filter(item=>item.url==='/api/tickets');assert.notEqual(JSON.parse(posts[1].options.body).idempotency_key,first);
  });
  await t.test('support typing keeps the connected field focus and caret through periodic updates', async () => {
    const page=await app();page.get('[data-nav="support"]').dispatch('click');await settle();
    fillSupport(page,'正在编辑的标题','前半段后半段');const field=page.get('#ticket-body');field.focus();field.setSelectionRange(3,5,'backward');
    await page.tick();const current=page.get('#ticket-body');
    assert.equal(page.document.activeElement,current);assert.equal(current.isConnected,true);assert.equal(current.value,'前半段后半段');
    assert.equal(current.selectionStart,3);assert.equal(current.selectionEnd,5);assert.equal(current.selectionDirection,'backward');
  });
  await t.test('ambiguous attempts retain each unchanged payload key even after editing and restoring an earlier draft', async () => {
    const page=await app({'/api/tickets':()=>{throw new TypeError('synthetic network interruption');}});
    page.get('[data-nav="support"]').dispatch('click');await settle();
    for(const text of ['反馈版本甲','反馈版本乙','反馈版本甲']) {fillSupport(page,'同一反馈',text);page.get('#ticket-create-form').dispatch('submit');await settle();}
    const bodies=page.requests.filter(item=>item.url==='/api/tickets'&&item.options.method==='POST').map(item=>JSON.parse(item.options.body));
    assert.equal(bodies.length,3);assert.notEqual(bodies[0].idempotency_key,bodies[1].idempotency_key);assert.equal(bodies[0].idempotency_key,bodies[2].idempotency_key);
    assert.equal(page.get('#ticket-body').value,'反馈版本甲');
  });
  await t.test('ambiguous replies keep each draft key and a confirmed send after closing stays closed', async () => {
    const detail=supportDetail(),pending=deferred();let calls=0;
    const page=await app({
      '/api/tickets?limit=30':()=>ok(supportList([detail.ticket])), '/api/tickets/ticket-one':()=>ok(detail),
      '/api/tickets/ticket-one/messages':()=>++calls<4?Promise.reject(new TypeError('synthetic interrupted response')):pending.promise,
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    for(const text of ['补充甲','补充乙','补充甲']) {page.get('#ticket-reply-body').value=text;page.get('#ticket-reply-form').dispatch('submit');await settle();}
    const posts=()=>page.requests.filter(item=>item.url==='/api/tickets/ticket-one/messages').map(item=>JSON.parse(item.options.body));
    assert.equal(posts().length,3);assert.notEqual(posts()[0].idempotency_key,posts()[1].idempotency_key);assert.equal(posts()[0].idempotency_key,posts()[2].idempotency_key);
    page.get('#ticket-reply-form').dispatch('submit');await settle();assert.equal(posts().length,4);assert.equal(posts()[2].idempotency_key,posts()[3].idempotency_key);
    page.get('[data-action="close-modal"]').dispatch('click');pending.resolve(ok({...detail,ticket:{...detail.ticket,message_count:2},messages:[...detail.messages,{id:'late-reply',author_role:'user',body:'关闭后的成功响应',created_at:'2026-11-02T03:00:00Z'}]}));await settle();
    assert.doesNotMatch(page.html(),/role="dialog"|关闭后的成功响应/);assert.match(page.html(),/id="ticket-create-form"/);assert.equal(page.get('#ticket-body').disabled,false);
  });
  await t.test('reply drafts remain separate for the same ticket ID in user and administrator views', async () => {
    const detail=supportDetail();const page=await app({
      '/api/auth/me':()=>ok({user,platform:{role:'admin'}}),
      '/api/tickets?limit=30':()=>ok(supportList([detail.ticket])), '/api/admin/tickets?limit=30':()=>ok(supportList([detail.ticket])),
      '/api/tickets/ticket-one':()=>ok(detail), '/api/admin/tickets/ticket-one':()=>ok(detail),
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();page.get('#ticket-reply-body').value='用户视图草稿';
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-nav="admin-overview"]').dispatch('click');await settle();page.get('[data-nav="admin-support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    assert.equal(page.get('#ticket-reply-body').value,'');page.get('#ticket-reply-body').value='管理员视图草稿';
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-nav="courses"]').dispatch('click');page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    assert.equal(page.get('#ticket-reply-body').value,'用户视图草稿');assert.doesNotMatch(page.html(),/管理员视图草稿/);
  });
  await t.test('old ticket list and detail errors never expire a subsequent account session', async () => {
    const list=deferred(),detail=deferred();let listCalls=0;
    const current=supportDetail(supportTicket({subject:'新会话反馈'}));
    const page=await app({
      '/api/tickets?limit=30':()=>++listCalls===1?ok(supportList([current.ticket])):listCalls===2?list.promise:ok(supportList([current.ticket])),
      '/api/tickets/ticket-one':()=>detail.promise,
    });
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');
    page.get('[data-action="close-modal"]').dispatch('click');page.get('[data-ticket-refresh]').dispatch('click');
    page.get('[data-action="logout"]').dispatch('click');await settle();page.get('#login').value='new-session';page.get('#password').value='synthetic-password';page.get('#login-form').dispatch('submit');await settle();
    page.get('[data-nav="support"]').dispatch('click');await settle();
    list.resolve(failure(401,'AUTH_REQUIRED','old list expired'));detail.resolve(failure(401,'AUTH_REQUIRED','old detail expired'));await settle();
    assert.match(page.html(),/新会话反馈/);assert.doesNotMatch(page.html(),/id="login-form"|role="dialog"|old list expired|old detail expired/);
  });
  await t.test('support detail traps keyboard focus across editable message fields and buttons', async () => {
    const detail=supportDetail();const page=await app({'/api/tickets?limit=30':()=>ok(supportList([detail.ticket])),'/api/tickets/ticket-one':()=>ok(detail)});
    page.get('[data-nav="support"]').dispatch('click');await settle();page.get('[data-ticket-open="ticket-one"]').dispatch('click');await settle();
    const controls=page.document.querySelectorAll('.modal button, .modal input, .modal textarea, .modal select, .modal a[href]').filter(item=>!item.disabled);
    assert.ok(controls.some(item=>item.tag==='textarea'));
    controls[0].focus();let prevented=false;page.document.listeners.get('keydown')({key:'Tab',shiftKey:true,preventDefault(){prevented=true;}});assert.ok(prevented);assert.equal(page.document.activeElement,controls.at(-1));
    prevented=false;page.document.listeners.get('keydown')({key:'Tab',shiftKey:false,preventDefault(){prevented=true;}});assert.ok(prevented);assert.equal(page.document.activeElement,controls[0]);
  });

});
