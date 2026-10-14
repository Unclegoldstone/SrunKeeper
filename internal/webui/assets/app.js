'use strict';
const $=id=>document.getElementById(id);
let token=location.hash.slice(1)||sessionStorage.getItem('srunkeeper-token')||'';
if(location.hash){sessionStorage.setItem('srunkeeper-token',token);history.replaceState(null,'',location.pathname);}
let loaded=false;
function notice(text){$('notice').textContent=text;}
const scrollKey='srunkeeper-scroll:'+location.pathname;
function scrollPositions(){return {
 sidebar:document.querySelector('.sidebar').scrollTop,
 operations:document.querySelector('.operations').scrollTop,
 window:window.scrollY,
 logs:Object.fromEntries([...document.querySelectorAll('.log-stream')].map(el=>[el.dataset.ip,{top:el.scrollTop,bottom:el.scrollHeight-el.scrollTop-el.clientHeight<24}]))
};}
function restoreScroll(position){
 if(!position)return;
 document.querySelector('.sidebar').scrollTop=position.sidebar||0;
 document.querySelector('.operations').scrollTop=position.operations||0;
 window.scrollTo(0,position.window||0);
 for(const el of document.querySelectorAll('.log-stream')){const old=position.logs?.[el.dataset.ip];if(old)el.scrollTop=old.bottom?el.scrollHeight:old.top;}
}
let initialScroll=null;
try{initialScroll=JSON.parse(sessionStorage.getItem(scrollKey));}catch{}
function rememberScroll(){try{sessionStorage.setItem(scrollKey,JSON.stringify(scrollPositions()));}catch{}}

async function api(path,method='GET',data){
 const r=await fetch('/api/'+path,{method,headers:{'X-SrunKeeper-Token':token,...(data?{'Content-Type':'application/json'}:{})},body:data?JSON.stringify(data):undefined});
 if(!r.ok)throw new Error(await r.text());return r.json();
}
function input(label,value,type='text',placeholder=''){
 const l=document.createElement('label');l.append(document.createTextNode(label));const i=document.createElement('input');i.type=type;i.value=value||'';i.placeholder=placeholder;i.required=type!=='password';i.autocomplete=type==='password'?'new-password':'off';l.append(i);return l;
}
function refreshAccounts(){
 const names=[...new Set([...$('accounts').children].map(row=>row.querySelector('input').value).filter(name=>name.trim()!==''))];
 for(const select of $('devices').querySelectorAll('select')){
  const selected=select.value;select.replaceChildren();
  const placeholder=document.createElement('option');placeholder.value='';placeholder.textContent=names.length?'请选择账号':'请先添加账号';select.append(placeholder);
  for(const name of names){const option=document.createElement('option');option.value=name;option.textContent=name;select.append(option);}
  select.value=names.includes(selected)?selected:'';
 }
}
function remove(row){const b=document.createElement('button');b.type='button';b.className='remove';b.textContent='删除';b.onclick=()=>{row.remove();refreshAccounts();};return b;}
function account(name='',pass='',existing=false){const row=document.createElement('div');row.className='row';row.append(input('账号',name),input('密码',pass,'password',existing?'已保存，留空保留':'填写密码'),remove(row));$('accounts').append(row);refreshAccounts();}
function device(d={}){
 const row=document.createElement('div');row.className='row device';
 const label=document.createElement('label');label.append(document.createTextNode('关联账号'));
 const select=document.createElement('select');select.required=true;label.append(select);
 row.append(input('IP 地址',d.ip),input('设备描述',d.description),label,remove(row));$('devices').append(row);refreshAccounts();select.value=d.account||'';
}
let savedAccounts=new Set();
function renderAccounts(c){$('accounts').replaceChildren();for(const [name,pass] of Object.entries(c.user.account))account(name,pass,true);savedAccounts=new Set(Object.keys(c.user.account));refreshAccounts();}
function renderDevices(c){$('devices').replaceChildren();c.user.device.forEach(device);}
function renderSettings(c){$('school-description').value=c.school.description||'';$('host').value=c.school.host;$('acid').value=c.school.ac_id;$('n').value=c.school.n;$('type').value=c.school.type;$('enc').value=c.school.enc_ver;$('seconds').value=c.system.check_interval_seconds;}
async function load(){const c=await api('config');renderAccounts(c);renderDevices(c);renderSettings(c);loaded=true;}
function node(tag,className,text){const n=document.createElement(tag);if(className)n.className=className;if(text!==undefined)n.textContent=text;return n;}
let serviceAction=false;
async function status(){
 const s=await api('status');$('service').textContent=s.running?(s.busy?'正在检测':'服务运行中'):'服务已停止';$('service').classList.toggle('running',s.running);
 $('service-help').textContent=s.running?'按设置的间隔检查所有设备，离线时自动重新认证。':'服务尚未运行。保存设置后，点击启动服务。';
 $('start').disabled=serviceAction||s.running||!s.devices.length;$('stop').disabled=serviceAction||!s.running;$('check').disabled=serviceAction||!s.running;
 $('total').textContent=s.devices.length;$('online').textContent=s.devices.filter(d=>d.state==='online').length;$('interval').textContent=s.interval+' 秒';
 const container=$('device-logs');const pagePosition=scrollPositions();const fragment=document.createDocumentFragment();const positions=new Map([...container.querySelectorAll('.log-stream')].map(el=>[el.dataset.ip,{top:el.scrollTop,bottom:el.scrollHeight-el.scrollTop-el.clientHeight<24}]));
 if(!s.devices.length)fragment.append(node('div','empty','还没有设备。请在左侧添加账号和设备，并分别保存。'));
 for(const d of s.devices){
  const card=node('article','device-card'),head=node('div','device-head'),info=node('div');info.append(node('div','device-name',d.description),node('div','device-meta',d.ip+' · '+d.account));
  const state=node('span','state '+d.state,({online:'认证在线',error:'检测失败',pending:'等待检测'})[d.state]||'待确认');head.append(info,state);card.append(head);
  const stream=node('div','log-stream');stream.dataset.ip=d.ip;stream.setAttribute('aria-label',d.description+'的日志');
  const entries=s.logs[d.ip]||[];
  if(!entries.length)stream.append(node('div','log-empty',s.running?'等待首次检测…':'启动服务后，检测与认证记录将显示在这里。'));
  for(const entry of entries){const row=node('div','log-line '+entry.level);row.append(node('span','log-message',entry.line));stream.append(row);}
  card.append(stream);fragment.append(card);
 }
 // Replace the whole list in one operation. Clearing and appending cards one at
 // a time temporarily shrinks the scroll area and clamps its scroll offset.
 container.replaceChildren(fragment);
 restoreScroll(pagePosition);
 for(const stream of container.querySelectorAll('.log-stream')){const previous=positions.get(stream.dataset.ip);stream.scrollTop=!previous||previous.bottom?stream.scrollHeight:previous.top;}
}
async function control(action){if(serviceAction)return;serviceAction=true;try{await api('service/'+action,'POST');notice(action==='start'?'服务已启动，开始检测所有设备。':'服务已停止，已有日志会保留。');}catch(err){notice(err.message);}finally{serviceAction=false;await status();}}
$('start').onclick=()=>control('start');$('stop').onclick=()=>control('stop');
$('accounts').addEventListener('input',refreshAccounts);
$('add-account').onclick=()=>account();$('add-device').onclick=()=>device();
$('check').onclick=async()=>{try{await api('check','POST');notice('已安排检测，将在当前轮次结束后执行。');}catch(e){notice(e.message);}};
let saving=false;
async function saveSection(section,body,render,label){
 if(!loaded||saving)return;
 saving=true;const buttons=['save-accounts','save-devices','save-settings'];buttons.forEach(id=>$(id).disabled=true);
 try{await api('config/'+section,'POST',body);const c=await api('config');render(c);notice(label+'已保存并应用，其他区域的未保存内容会保留。');await status();}
 catch(err){notice(err.message);}finally{saving=false;buttons.forEach(id=>$(id).disabled=false);}
}
$('accounts-form').onsubmit=async e=>{e.preventDefault();const accounts=Object.create(null);try{
 for(const row of $('accounts').children){const [name,pass]=row.querySelectorAll('input');if(Object.hasOwn(accounts,name.value))throw new Error('账号重复，请检查。');accounts[name.value]=pass.value;}
 await saveSection('accounts',{account:accounts},renderAccounts,'账号');
 }catch(err){notice(err.message);}};
$('devices-form').onsubmit=async e=>{e.preventDefault();try{
 const devices=[...$('devices').children].map(row=>{const [ip,description]=row.querySelectorAll('input');const account=row.querySelector('select').value;if(!savedAccounts.has(account))throw new Error('设备关联的账号尚未保存，请先点击“保存账号”。');return {ip:ip.value,description:description.value,account};});
 await saveSection('devices',{device:devices},renderDevices,'设备');
 }catch(err){notice(err.message);}};
$('settings').onsubmit=async e=>{e.preventDefault();await saveSection('settings',{school:{description:$('school-description').value,host:$('host').value,ac_id:$('acid').value,n:$('n').value,type:$('type').value,enc_ver:$('enc').value},system:{check_interval_seconds:Number($('seconds').value)}},renderSettings,'学校与系统设置');};
async function poll(){try{await status();}catch(e){$('service').textContent='服务连接中断';notice(e.message);}setTimeout(poll,3000);}
load().then(async()=>{
 await status();restoreScroll(initialScroll);
 let scheduled=false;
 document.addEventListener('scroll',()=>{if(!scheduled){scheduled=true;requestAnimationFrame(()=>{scheduled=false;rememberScroll();});}},true);
 window.addEventListener('pagehide',rememberScroll);
 setTimeout(poll,3000);
}).catch(e=>{notice(e.message);$('service').textContent='无法连接';});
