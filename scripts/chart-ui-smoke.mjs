// v0.6 isolated desktop integration. All prices and history below are explicitly
// SYNTHETIC, seeded only into a disposable profile. No external or paid service.
import {spawn,spawnSync} from 'node:child_process';
import {mkdtemp,mkdir,writeFile,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import http from 'node:http';
import net from 'node:net';
import {setTimeout as delay} from 'node:timers/promises';
const root=fileURLToPath(new URL('../',import.meta.url)),binary=process.argv[2];
const profile=await mkdtemp(path.join(tmpdir(),'cornwatch-chart-v6-')),userData=path.join(profile,'cornwatch');
const artifacts=path.join(root,'artifacts','v6-chart-ui');
const symbol='c2701',requests=[],checks=[],rendererErrors=[];
let inflight=0,maxInflight=0,failed=false,bridgeDelay=20;
const server=http.createServer((req,res)=>{
 if(req.url!=='/quotes'){res.writeHead(404);res.end();return}
 requests.push(Date.now());inflight++;maxInflight=Math.max(maxInflight,inflight);
 setTimeout(()=>{inflight--;res.writeHead(failed?503:200,{'Content-Type':'application/json'});if(failed){res.end(JSON.stringify({error:'SYNTHETIC local failure'}));return}
 const price=2250+(requests.length%50),now=new Date();
 res.end(JSON.stringify({generated_at:now.toISOString(),quotes:[{symbol,product:'c',name:'隔离验收合成玉米',exchange:'DCE',trade_date:new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(now),last:price,open:2245,high:2300,low:2200,previous_settlement:2240,volume:90000+requests.length,open_interest:160000,observed_at:now.toISOString(),received_at:now.toISOString()}]}));},bridgeDelay);
});
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const endpoint=`http://127.0.0.1:${server.address().port}/quotes`;
const now=new Date(),available=now.toISOString(),meta={};
for(const [id,interval] of [['sina',.5],['market_bridge',.5],['liaoning',43200],['moa',43200],['boyar_prices',43200],['boyar_arrivals',43200],['import_auction',86400],['weather',3600],['industry_bridge',300]])meta[`datahub_config_${id}`]={revision:1,enabled:id==='market_bridge',interval_seconds:interval,endpoint:id==='market_bridge'?endpoint:''};
const dayBars=[];
for(const interval of ['day','1m','5m','15m','30m','60m']){
 const bars=Array.from({length:240},(_,i)=>{
  const close=Math.round(2200+Math.sin(i/7)*24+i*.15),open=close+Math.round(Math.cos(i/3)*5);
  const timestamp=interval==='day'?new Date(Date.UTC(now.getUTCFullYear(),now.getUTCMonth(),now.getUTCDate()-241+i,7)):new Date(Math.floor(now.getTime()/60000)*60000-(242-i)*parseInt(interval)*60000);
  const bar={symbol,timestamp:timestamp.toISOString(),trade_date:interval==='day'?timestamp.toISOString().slice(0,10):'',open,high:Math.max(open,close)+7,low:Math.min(open,close)-7,close,settlement:close,volume:10000+i*53,open_interest:60000+i*100,limit_up:2500,limit_down:1900,is_demo:false,available_at:available};
  if(interval==='day')dayBars.push(bar);return bar;
 });
 meta[`market_chart_v1_${symbol}_${interval}`]={report:{symbol,interval,bars,source:'SYNTHETIC isolated UI fixture · 非真实行情',source_url:'https://example.invalid/isolated-fixture',available_at:available,has_open_interest:true,has_settlement:true,warnings:['SYNTHETIC fixture，仅验证图表和交互，不代表真实市场历史。']},next_attempt:new Date(now.getTime()+3600000).toISOString()};
}
await mkdir(userData,{recursive:true});await mkdir(artifacts,{recursive:true});
const seedPath=path.join(profile,'seed.json'),goPath=path.join(profile,'seed.go');
await writeFile(seedPath,JSON.stringify(meta));
await writeFile(goPath,`package main
import("encoding/json";"os";bolt "go.etcd.io/bbolt")
func main(){raw,e:=os.ReadFile(os.Args[2]);if e!=nil{panic(e)};var data map[string]json.RawMessage;if e=json.Unmarshal(raw,&data);e!=nil{panic(e)};db,e:=bolt.Open(os.Args[1],0600,nil);if e!=nil{panic(e)};defer db.Close();e=db.Update(func(tx *bolt.Tx)error{b,e:=tx.CreateBucketIfNotExists([]byte("meta"));if e!=nil{return e};for k,v:=range data{if e=b.Put([]byte(k),v);e!=nil{return e}};return nil});if e!=nil{panic(e)}}`);
const seeded=spawnSync('go',['run',goPath,path.join(userData,'cornwatch.db'),seedPath],{cwd:path.join(root,'backend'),encoding:'utf8',timeout:60000});
if(seeded.status!==0)throw new Error(seeded.stderr);
const listener=net.createServer();await new Promise(r=>listener.listen(0,'127.0.0.1',r));const port=listener.address().port;await new Promise(r=>listener.close(r));
const env={...process.env,XDG_CONFIG_HOME:profile};for(const key of Object.keys(env))if(key.startsWith('LLM_')||key.startsWith('CORNWATCH_')||key==='ELECTRON_RUN_AS_NODE')delete env[key];
env.CORNWATCH_REPORT_AUTO='false';env.CORNWATCH_WEATHER_ENABLED='false';
const electron=path.join(root,'node_modules/electron/dist/electron');
const child=spawn(binary?path.resolve(binary):electron,[...(binary?[]:['.']),`--remote-debugging-port=${port}`,`--user-data-dir=${userData}`],{cwd:root,env,detached:true,stdio:['ignore','pipe','pipe']});
let logs='',socket,seq=0;const pending=new Map();child.stdout.on('data',c=>logs=(logs+c).slice(-15000));child.stderr.on('data',c=>logs=(logs+c).slice(-15000));
function assert(v,m){if(!v)throw new Error(m)}
function command(method,params={}){return new Promise((resolve,reject)=>{const id=++seq;const timer=setTimeout(()=>{pending.delete(id);reject(new Error(`CDP timeout ${method}`))},20000);pending.set(id,{resolve,reject,timer});socket.send(JSON.stringify({id,method,params}))})}
async function evaluate(expression){const r=await command('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw new Error(JSON.stringify(r.exceptionDetails));return r.result?.value}
async function until(expression,timeout=20000){const start=Date.now();while(Date.now()-start<timeout){if(await evaluate(`Boolean(${expression})`))return;await delay(100)}throw new Error(`UI timed out: ${expression}`)}
async function click(label,scope='document'){assert(await evaluate(`(()=>{const b=[...${scope}.querySelectorAll('button')].find(x=>x.textContent.trim()===${JSON.stringify(label)});if(!b||b.disabled)return false;b.click();return true})()`),`Cannot click ${label}`)}
async function setInput(selector,value){assert(await evaluate(`(()=>{const x=document.querySelector(${JSON.stringify(selector)});if(!x||x.disabled)return false;Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(x,${JSON.stringify(value)});x.dispatchEvent(new Event('input',{bubbles:true}));x.dispatchEvent(new Event('change',{bubbles:true}));return true})()`),`Cannot edit ${selector}`)}
async function api(url,options){return evaluate(`window.cornwatch.request(${JSON.stringify(url)}${options?','+JSON.stringify(options):''})`)}
async function screenshot(name,selector='.market-price-panel'){await evaluate(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});if(e)window.scrollTo({top:Math.max(0,window.scrollY+e.getBoundingClientRect().top-85),behavior:'instant'})})()`);await delay(180);const r=await command('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});await writeFile(path.join(artifacts,name),Buffer.from(r.data,'base64'))}
async function viewport(width){await command('Emulation.setDeviceMetricsOverride',{width,height:960,deviceScaleFactor:1,mobile:false});await delay(150);assert(!await evaluate('document.documentElement.scrollWidth>innerWidth+2'),`Overflow at ${width}`)}
try{
 let tab;for(let i=0;i<150;i++){if(child.exitCode!==null)throw new Error(`Electron exited ${logs}`);try{const tabs=await(await fetch(`http://127.0.0.1:${port}/json`)).json();tab=tabs.find(t=>t.type==='page');if(tab)break}catch{}await delay(150)}assert(tab,'No renderer CDP');
 socket=new WebSocket(tab.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{socket.onopen=resolve;socket.onerror=reject});socket.onmessage=e=>{const message=JSON.parse(e.data);if(message.id){const p=pending.get(message.id);if(p){clearTimeout(p.timer);pending.delete(message.id);message.error?p.reject(new Error(JSON.stringify(message.error))):p.resolve(message.result)}}else if(message.method==='Runtime.exceptionThrown')rendererErrors.push(JSON.stringify(message.params))};
 await command('Runtime.enable');await command('Page.enable');await until("window.cornwatch && document.querySelector('.market-current')?.textContent.includes('2,2')");
 const started=Date.now(),initialCount=requests.length;
 console.log('Synthetic bridge running at target500ms. Measuring at least61seconds while exercising charts.');
 await until("document.querySelector('.mc-plot') && document.querySelector('.mc-indicator-legend')?.textContent.includes('MA60')");
 const initial=await api(`/api/market/history?symbol=${symbol}&interval=day&limit=6000`);assert(initial.bars.length===240&&initial.cached&&initial.source.includes('SYNTHETIC'),'Did not load isolated historycache');
 const expectedMA=initial.bars.slice(-5).reduce((s,b)=>s+b.close,0)/5;
 assert(await evaluate(`document.querySelector('.mc-indicator-legend').textContent.includes(${JSON.stringify(expectedMA.toLocaleString('zh-CN',{minimumFractionDigits:2,maximumFractionDigits:2}))})`),'MA5 disagrees with fixture');
 checks.push('Cached historicalOHLC and independently recomputedMA5 render correctly');
 for(const label of ['1分','5分','15分','30分','60分','日K']){await click(label,"document.querySelector('.mc-periods')");await until(`document.querySelector('.mc-periods button.active')?.textContent===${JSON.stringify(label)} && !!document.querySelector('.mc-plot') && ![...document.querySelector('.mc-toolbar').querySelectorAll('button')].find(x=>x.textContent.trim()==='刷新历史')?.disabled`)}
 checks.push('All six genuine-history period controls use isolated cachedfixture');
 await click('指标参数');await setInput('[aria-label="MA周期"]','3,7,21');await setInput('[aria-label="BOLL周期"]','10');await setInput('[aria-label="BOLL标准差倍数"]','2.5');await click('应用指标');
 await until("document.querySelector('.mc-indicator-legend')?.textContent.includes('MA21')&&document.querySelector('.mc-indicator-legend')?.textContent.includes('BOLL(10,2.5)')");
 await evaluate("document.querySelector('[aria-label=放大K线]').click()");await until("document.querySelector('.mc-navigation').textContent.includes('63 / 240')");
 await evaluate("document.querySelector('[aria-label=查看更早行情]').click()");await until("document.querySelector('.mc-navigation').textContent.includes('正在查看历史')");
 await click('最近 / 重置');
 const startDate=dayBars.at(-30).trade_date,endDate=dayBars.at(-1).trade_date;
 await setInput('[aria-label="历史起始日期"]',startDate);await setInput('[aria-label="历史结束日期"]',endDate);await until("document.querySelector('.mc-navigation').textContent.includes('30 / 30')");
 await click('查看历史数据表');assert(await evaluate("document.querySelectorAll('.mc-history-table tbody tr').length===30"),'Date table mismatch');
 await command('Browser.setDownloadBehavior',{behavior:'allow',downloadPath:artifacts});await click('导出当前范围CSV');await delay(300);
 const filename=`${symbol}-day-${new Intl.DateTimeFormat('en-CA',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date())}.csv`;
 const csv=await readFile(path.join(artifacts,filename),'utf8');assert(csv.trim().split('\n').length===31,'CSV did not export entire filteredrange');
 checks.push('ConfigurableMA/BOLL, zoom/pan/reset, datefilter, datatable andCSV export work');
 await click('收起数据表');await click('最近 / 重置');
 for(const width of [1400,1080]){await viewport(width);await screenshot(`chart-${width}-synthetic.png`);await screenshot(`chart-plot-${width}-synthetic.png`,'.mc-toolbar')}
 await viewport(1400);await click('实时采样');await until("document.querySelector('.mc-provenance').textContent.includes('采样点') && document.querySelector('.mc-plot')");
 checks.push('Realtime samples separate from historicalcandles with explicit sampling-unit indicators');
 const importCSV=['symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down',...dayBars.slice(-40).map(b=>[b.symbol,b.timestamp,b.trade_date,b.open,b.high,b.low,b.close,b.settlement,b.volume,b.open_interest,b.limit_up,b.limit_down].join(','))].join('\n');
 await api('/api/import/bars',{method:'POST',body:{csv:importCSV,source:'SYNTHETIC isolated chart import, not realmarket'}});
 await click('本地导入日线');await until("document.querySelector('.mc-navigation')?.textContent.includes('40 / 40')");
 assert(await evaluate("document.querySelector('.mc-provenance')?.textContent.includes('本地导入')"),'Importedhistory missingprovenance');
 await click('实时采样');await until("document.querySelector('.mc-provenance')?.textContent.includes('采样点')");
 checks.push('Imported realbar keyspace canbeviewed separately, with syntheticfixtures clearlymarked');
 await screenshot('samples-1400-synthetic.png');
 await click('数据与设置',"document.querySelector('.sidebar nav')");await until("document.querySelectorAll('.ds-card').length===9");
 const selector='.ds-card[aria-label="授权行情 · TqSdk / JSON桥接数据源"]';
 await evaluate(`document.querySelector(${JSON.stringify(selector)}).querySelector('.ds-card-header button').click()`);await until(`document.querySelector(${JSON.stringify(selector+' .ds-config-form')})`);
 await until(`document.querySelector(${JSON.stringify(selector+' .ds-interval-label input')})?.value==='0.5'`);
 assert(await evaluate(`document.querySelector(${JSON.stringify(selector+' .ds-polling')}).textContent.includes('120')`),'Missing actualfrequency UI');
 await setInput(`${selector} .ds-interval-label input`,'1');await click('保存配置',`document.querySelector(${JSON.stringify(selector)})`);await until(`!document.querySelector(${JSON.stringify(selector+' .ds-unsaved')})`);
 await click('设为 120 次 / 分钟（500 ms）',`document.querySelector(${JSON.stringify(selector)})`);await click('保存配置',`document.querySelector(${JSON.stringify(selector)})`);await until(`!document.querySelector(${JSON.stringify(selector+' .ds-unsaved')}) && document.querySelector(${JSON.stringify(selector+' .ds-interval-label input')})?.value==='0.5'`);

 await screenshot('frequency-settings-1400-synthetic.png',selector);await viewport(1080);await screenshot('frequency-settings-1080-synthetic.png',selector);
 checks.push('500ms persisted configuration and target/actual request/latency shown in sourceconsole');
 while(Date.now()-started<61000){console.log(`Frequencymeasurement ${Math.round((Date.now()-started)/1000)}s, localrequests ${requests.length-initialCount}`);await delay(Math.min(15000,61000-(Date.now()-started)))}
 const ended=Date.now(),windowRequests=requests.filter(t=>t>=started&&t<=ended),elapsed=(ended-started)/1000,rpm=windowRequests.length/elapsed*60;
 const deltas=windowRequests.slice(1).map((t,i)=>t-windowRequests[i]);
 assert(rpm>=100&&rpm<=122,`Measured rate ${rpm.toFixed(2)} is outside scheduler tolerance`);assert(Math.min(...deltas)>=450,'Requests burst faster than target');assert(maxInflight===1,'Quote requests overlap');
 const live=await api('/api/market/live');assert(live.polling.target_interval_ms===500&&live.polling.target_requests_per_minute===120,'Backend target does notmatch500ms');assert(live.polling.requests_last_minute>=100&&live.polling.requests_last_minute<=121,'Rolling actualrequestcount inconsistent');
 checks.push(`At least61s local bridge measurement: ${rpm.toFixed(2)}requests/min, nooverlap orcatchupburst`);
 await click('市场总览',"document.querySelector('.sidebar nav')");await until("document.querySelector('.market-current')");
 const beforeText=await evaluate("document.querySelector('.market-current').textContent");await delay(1200);const afterText=await evaluate("document.querySelector('.market-current').textContent");assert(beforeText!==afterText,'Realtime displayedprice didnotupdate');
 failed=true;await until("document.querySelector('.market-error')?.textContent.includes('503')",10000);const failedCount=requests.length;await delay(2000);assert(requests.length===failedCount,'Failure didnotenter cooldown');
 checks.push('Displayedprice updates independently; failedprovider retainsquote andenterscooldown');
 failed=false;bridgeDelay=700;await click('数据与设置',"document.querySelector('.sidebar nav')");await until(`document.querySelector(${JSON.stringify(selector)})`);
 await evaluate(`(()=>{const c=document.querySelector(${JSON.stringify(selector)});if(!c.querySelector('.ds-config-form'))c.querySelector('.ds-card-header button').click()})()`);
 const waitStarted=Date.now();while(!inflight&&Date.now()-waitStarted<20000)await delay(30);assert(inflight===1,'Didnotobserve slow in-flightbridge');
 await evaluate(`document.querySelector(${JSON.stringify(selector+' .ds-toggle-label input')}).click()`);
 await click('保存配置',`document.querySelector(${JSON.stringify(selector)})`);
 await until(`!document.querySelector(${JSON.stringify(selector+' .ds-unsaved')}) && !document.querySelector(${JSON.stringify(selector+' .ds-toggle-label input')})?.checked`);
 const disabled=await api('/api/data-sources');assert(!disabled.sources.find(s=>s.id==='market_bridge').config.enabled,'Queuedconfigdidnotdisable source');
 const stopCount=requests.length;await delay(1300);assert(requests.length===stopCount,'Disabledsourcekeptpolling');
 checks.push('Slow700ms collection can be disabled through UI save; queuedconfiguration wins withoutoverlap');

 assert(rendererErrors.length===0,`Rendererexceptions ${rendererErrors.join(';')}`);
 await writeFile(path.join(artifacts,'report.json'),JSON.stringify({passed:true,fixtures:'SYNTHETIC loopback-only quotes and seeded historical cache in disposableprofile; no externalcalls',checks,measurement:{elapsed_seconds:elapsed,requests:windowRequests.length,rpm,min_gap_ms:Math.min(...deltas),max_inflight:maxInflight,reported_rolling:live.polling.requests_last_minute},viewport_widths:[1400,1080],renderer_errors:rendererErrors,created_at:new Date().toISOString()},null,2));
 console.log(`PASS ${checks.length} v0.6chart integration groups`);
}catch(e){try{await screenshot('unexpected-failure-synthetic.png');await writeFile(path.join(artifacts,'failure-state.json'),JSON.stringify(await api('/api/data-sources'),null,2))}catch{}throw new Error(`${e.stack||e}\n${logs.slice(-2000)}`)}finally{
 if(child.exitCode===null&&socket?.readyState===WebSocket.OPEN){try{await command('Browser.close')}catch{}await delay(400)}socket?.close();for(const p of pending.values())clearTimeout(p.timer);try{if(child.pid)process.kill(-child.pid,'SIGKILL')}catch{}server.closeAllConnections();await new Promise(r=>server.close(r));await delay(300);await rm(profile,{recursive:true,force:true,maxRetries:10,retryDelay:200});
}
