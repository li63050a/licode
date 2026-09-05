/* helpers */
const $=id=>document.getElementById(id);
const esc=s=>{const d=document.createElement('div');d.textContent=s;return d.innerHTML};

/* state */
let ws,busy=false,lastAsst=null,tools=[],curAsk=null,sessions=[],curSess='',prevSess='';
let curSettings={};

/* theme */
let theme=localStorage.getItem('licode_theme')||'light';
function applyTheme(){document.documentElement.setAttribute('data-theme',theme);$('themeIcon').innerHTML=theme==='dark'?'<circle cx="12" cy="12" r="5"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/>':'<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>';}
function toggleTheme(){theme=theme==='dark'?'light':'dark';localStorage.setItem('licode_theme',theme);applyTheme();}
applyTheme();

/* toggle sidebar/right */
function toggle(id){$(id).classList.toggle('collapsed');}

/* sessions */
function renderSessions(){
  const list=$('slist');list.innerHTML='';
  for(const s of sessions){
    const d=document.createElement('div');
    d.className='sitem'+(s.id===curSess?' active':'');
    d.innerHTML='<span class="t">'+esc(s.title||'新对话')+'</span><span class="x" data-id="'+s.id+'">✕</span>';
    d.querySelector('.t').onclick=()=>{if(s.id!==curSess)ws.send(JSON.stringify({type:'session_switch',sessionId:s.id}));};
    d.querySelector('.x').onclick=e=>{e.stopPropagation();ws.send(JSON.stringify({type:'session_delete',sessionId:s.id}));};
    list.appendChild(d);
  }
}
function newSession(){ws.send(JSON.stringify({type:'session_new'}));}
function newBranch(){ws.send(JSON.stringify({type:'session_branch',sessionId:curSess,index:-1}));toast('已复制为分支对话');}

/* chat */
function renderEmpty(){$('chatWrap').innerHTML='<div class="empty"><div class="title">licode</div><div>有什么可以帮你的？</div></div>';}
function addMsg(role,text){
  const last=$('chatWrap').querySelector('.empty');if(last)last.remove();
  const d=document.createElement('div');d.className='msg '+role;
  const av=role==='user'?'你':'AI';
  d.innerHTML='<div class="avatar">'+av+'</div><div class="body"><div class="content"></div></div>';
  d.querySelector('.content').innerHTML=renderMd(text);
  $('chatWrap').appendChild(d);scrollBottom();
  return d.querySelector('.content');
}
function appendTo(el,chunk){
  if(!el)return;
  el.innerHTML=renderMd(el.dataset.full=(el.dataset.full||'')+chunk);
  scrollBottom();
}
function scrollBottom(){$('chat').scrollTop=$('chat').scrollTop+999;}

/* markdown (lightweight) */
function renderMd(s){
  if(!s)return'';
  s=esc(s);
  // code blocks
  s=s.replace(/```(\w*)\n([\s\S]*?)```/g,(_,lang,code)=>'<pre><code class="lang-'+lang+'">'+code+'</code><button class="cp" onclick="copyPre(this)">复制</button></pre>');
  // inline code
  s=s.replace(/`([^`]+)`/g,'<code>$1</code>');
  // bold
  s=s.replace(/\*\*(.+?)\*\*/g,'<b>$1</b>');
  // italic
  s=s.replace(/\*(.+?)\*/g,'<i>$1</i>');
  // headers
  s=s.replace(/^### (.+)$/gm,'<h4>$1</h4>');
  s=s.replace(/^## (.+)$/gm,'<h3>$1</h3>');
  s=s.replace(/^# (.+)$/gm,'<h2>$1</h2>');
  // lists
  s=s.replace(/^- (.+)$/gm,'<li>$1</li>');
  s=s.replace(/(<li>.*<\/li>\n?)+/g,m=>'<ul>'+m+'</ul>');
  // links
  s=s.replace(/\[([^\]]+)\]\(([^)]+)\)/g,'<a href="$2" target="_blank">$1</a>');
  // newlines
  s=s.replace(/\n/g,'<br>');
  return s;
}
function copyPre(btn){const code=btn.previousElementSibling;navigator.clipboard.writeText(code.textContent).then(()=>{btn.textContent='已复制';setTimeout(()=>btn.textContent='复制',1500);});}

/* tool call */
function addTool(evt){
  const d=document.createElement('div');d.className='tool open';
  d.innerHTML='<div class="head" onclick="this.parentElement.classList.toggle(\'open\')"><span class="name">⚙ '+esc(evt.toolName)+'</span><span class="status run">执行中...</span></div>'+
    '<div class="body"><div class="tl"><span>参数</span></div><pre class="args">'+esc(evt.toolArgs)+'</pre>'+
    '<div class="tl out" style="display:none"><span>结果</span></div><pre class="out" style="display:none"></pre></div>';
  $('chatWrap').appendChild(d);scrollBottom();return d;
}
function setToolDone(el,evt){
  if(!el)return;
  el.querySelector('.status').className='status';el.querySelector('.status').textContent='✓ 完成';
  el.querySelector('.tl.out').style.display='';el.querySelector('pre.out').style.display='';
  el.querySelector('pre.out').textContent=evt.toolOut||'';
}

/* toast */
function toast(msg){const d=document.createElement('div');d.className='t';d.textContent=msg;$('toast').appendChild(d);setTimeout(()=>d.remove(),3000);}

/* backup */
async function exportBackup(){
  try{
    const r=await fetch('/api/export');
    const blob=await r.blob();
    const url=URL.createObjectURL(blob);
    const a=document.createElement('a');a.href=url;a.download='licode-backup.zip';a.click();
    URL.revokeObjectURL(url);toast('已导出备份');
  }catch(e){toast(e.message);}
}
async function importBackup(file){
  if(!file)return;
  const fd=new FormData();fd.append('file',file);
  try{
    const r=await fetch('/api/import',{method:'POST',body:file});
    const d=await r.json();
    toast(d.ok?'导入成功':'导入失败: '+(d.error||''));
  }catch(e){toast(e.message);}
}

/* settings（HTMX：表单由 /fragment/settings 服务器渲染） */
async function openSettings(){
  if(window.htmx){
    htmx.ajax('GET','/fragment/settings',{target:'#settingsModal',swap:'innerHTML'});
    return;
  }
  try{
    const r=await fetch('/fragment/settings');
    $('settingsModal').innerHTML=await r.text();
    showSettingsModal();
  }catch(e){toast(e.message);}
}
function showSettingsModal(){const m=$('settingsModal');if(m)m.classList.add('on');}
function closeSettings(){$('settingsModal').classList.remove('on');}
function saveSettings(){
  let mcp=[];try{mcp=JSON.parse($('sMCP').value||'[]')}catch(e){}
  let provs=[];try{provs=JSON.parse($('sProvs').value||'[]')}catch(e){}
  ws.send(JSON.stringify({type:'settings_set',settings:{
    provider:$('sProv').value,model:$('sModel').value,api_key:$('sKey').value,base_url:$('sBase').value,
    temperature:parseFloat($('sTemp').value)||0.7,max_tokens:parseInt($('sMaxTok').value)||4096,
    max_iterations:parseInt($('sMaxIter').value)||16,subagents:$('sSub').value==='on',
    auto_allow:$('sAuto').value==='on',streaming:true,
    shell_path:$('sShell').value||'/bin/sh',
    retry_max:parseInt($('sRetry').value)||0,sub_timeout:parseInt($('sSubT').value)||0,
    max_ctx_tokens:parseInt($('sCtx').value)||0,redact_secrets:$('sRedact').value==='on',
    sandbox:$('sSandbox').value==='on',sandbox_image:$('sSandImg').value||'',
    cache_enabled:$('sCache').value==='on',tool_auto_retry:$('sAutoRetry').value==='on',
    rag_enabled:$('sRAG').value==='on',rag_source:$('sRAGSrc').value||'',
    audit_enabled:$('sAudit').value==='on',audit_auto_fix:$('sAuditFix').value==='on',
    audit_exclude:$('sAuditEx').value.split(',').map(x=>x.trim()).filter(Boolean),
    mcp_servers:mcp,providers:provs
  }}));
  closeSettings();toast('设置已保存');
}

/* right panel */
function switchTab(tab,btn){
  $('tabInfo').style.display=tab==='info'?'':'none';
  $('tabFiles').style.display=tab==='files'?'':'none';
  $('tabAudit').style.display=tab==='audit'?'':'none';
  document.querySelectorAll('.rtabs button').forEach(b=>b.classList.remove('active'));
  btn.classList.add('active');
  if(tab==='audit')loadAudit();
  if(tab==='files')loadDir();
}

/* 文件浏览（HTMX：树由 /fragment/files 服务器渲染） */
function loadDir(path){
  const p=(path===undefined||path===null)?($('fPath').value||''):path;
  $('fPath').value=p;
  const url='/fragment/files?path='+encodeURIComponent(p);
  if(window.htmx){
    htmx.ajax('GET',url,{target:'#fTree',swap:'innerHTML'});
  }else{
    fetch(url).then(r=>r.text()).then(t=>{$('fTree').innerHTML=t;if(window.htmx)htmx.process($('fTree'));}).catch(e=>{$('fTree').innerHTML='<div style="color:var(--red)">'+esc(e.message)+'</div>';});
  }
}
document.addEventListener('click',e=>{
  const n=e.target&&e.target.closest?e.target.closest('.fitem'):null;
  if(!n)return;
  if(n.dataset.isdir==='1'){loadDir(n.dataset.path);}
  else{openEditor(n.dataset.path);}
});
async function openEditor(path){
  try{
    const r=await fetch('/api/file?path='+encodeURIComponent(path));const d=await r.json();
    $('editorFile').textContent=path;
    $('editorFile').title=path;
    $('editor').value=d.content;
    $('editor').dataset.path=path;
    $('editorWrap').style.display='';
    updateLineNums();
  }catch(e){toast(e.message);}
}
function closeEditor(){$('editorWrap').style.display='none';}
function updateLineNums(){
  const ta=$('editor'),ln=$('lineNums');
  if(!ta||!ln)return;
  const lines=ta.value.split('\n').length;
  ln.innerHTML=Array.from({length:lines},(_,i)=>'<div>'+(i+1)+'</div>').join('');
}
async function saveFile(){
  const path=$('editor').dataset.path;
  if(!path)return;
  try{
    await fetch('/api/file',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({path,content:$('editor').value})});
    toast('已保存 '+path);
  }catch(e){toast(e.message);}
}
$('editor').addEventListener('input',updateLineNums);
$('editor').addEventListener('scroll',()=>{$('lineNums').scrollTop=$('editor').scrollTop;});
$('editor').addEventListener('keydown',e=>{
  if(e.key==='Tab'){e.preventDefault();const s=e.target.selectionStart,v=e.target.value;e.target.value=v.slice(0,s)+'  '+v.slice(e.target.selectionEnd);e.target.selectionStart=e.target.selectionEnd=s+2;updateLineNums();}
  if(e.key==='s'&&(e.ctrlKey||e.metaKey)){e.preventDefault();saveFile();}
});

/* ask */
function replyAsk(ok,always){
  if(!curAsk)return;
  ws.send(JSON.stringify({type:'ask_reply',askId:curAsk,askApprove:ok,askAlways:!!always}));
  $('askBar').style.display='none';curAsk=null;busy=false;
}

/* input */
const input=$('input');
input.addEventListener('keydown',e=>{if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();send();}});
input.addEventListener('input',()=>{input.style.height='auto';input.style.height=Math.min(input.scrollHeight,180)+'px';});

function send(){
  const text=input.value.trim();
  if(!text||busy||!ws||ws.readyState!==WebSocket.OPEN)return;
  if(text==='/clear'){$('chatWrap').innerHTML='';renderEmpty();ws.send(JSON.stringify({type:'message',content:'/clear'}));input.value='';return;}
  addMsg('user',text);input.value='';input.style.height='auto';
  setBusy(true);
  ws.send(JSON.stringify({type:'message',content:text}));
}
function setBusy(b){
  busy=b;$('send').disabled=b;$('stop').style.display=b?'':'none';
  if(!b){lastAsst=null;}
}
function stopGen(){
  if(!ws||busy){ws.send(JSON.stringify({type:'interrupt'}));busy=false;lastAsst=null;$('stop').style.display='none';$('send').disabled=false;$('statusBar').textContent='已停止';}
}

/* 代码审计（HTMX：面板由 /fragment/audit 服务器渲染；修复预览/确认仍走 /api/audit/fix） */
let auditData={selected:{}};
function loadAudit(){
  const sevEl=$('aSev');
  const sev=(sevEl&&sevEl.value)||'all';
  const url='/fragment/audit?sev='+encodeURIComponent(sev);
  if(window.htmx){
    htmx.ajax('GET',url,{target:'#tabAudit',swap:'innerHTML'});
  }else{
    fetch(url).then(r=>r.text()).then(t=>{$('tabAudit').innerHTML=t;if(window.htmx)htmx.process($('tabAudit'));}).catch(e=>{$('tabAudit').innerHTML='<div style="padding:12px;color:var(--red)">'+esc(e.message)+'</div>';});
  }
}
function updateSelCount(){
  const n=Object.keys(auditData.selected).length;
  const el=$('aSelCount');if(el)el.textContent='已选 '+n;
  const fb=$('aFixBar');if(fb)fb.querySelector('button').disabled=!n;
}
function selAllIssues(checked){
  document.querySelectorAll('#tabAudit input[data-id]').forEach(cb=>{
    cb.checked=checked;
    if(checked)auditData.selected[cb.dataset.id]=true;else delete auditData.selected[cb.dataset.id];
  });
  updateSelCount();
}
document.addEventListener('change',e=>{
  const t=e.target;
  if(!t)return;
  if(t.id==='aSelAll'){selAllIssues(t.checked);return;}
  if(t.type==='checkbox'&&t.dataset&&t.dataset.id){
    if(t.checked)auditData.selected[t.dataset.id]=true;else delete auditData.selected[t.dataset.id];
    updateSelCount();
  }
});
/* 服务器渲染片段 swap 之后：重新套用已勾选状态、设置弹窗显示 */
document.body.addEventListener('htmx:afterSwap',e=>{
  const target=e.detail&&e.detail.target;
  if(!target)return;
  if(target.id==='settingsModal')showSettingsModal();
  if(target.id==='tabAudit'){
    document.querySelectorAll('#tabAudit input[data-id]').forEach(cb=>{cb.checked=!!auditData.selected[cb.dataset.id];});
    updateSelCount();
  }
});
let previewTaskId='',previewIds=[],previewFiles=[];
async function genPreview(){
  const ids=Object.keys(auditData.selected);
  if(!ids.length){toast('请先勾选要修复的问题');return;}
  const el=$('aTaskId');
  const tid=(el&&el.value)||'';
  const st=$('aStatus');if(st)st.textContent='⏳ 正在生成修复预览（调用 LLM）…';
  try{
    const r=await fetch('/api/audit/fix',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({task_id:tid,issue_ids:ids})});
    const d=await r.json();
    if(!r.ok)throw new Error(d.error||'生成失败');
    previewTaskId=tid;previewIds=ids;
    openDiff(d.preview||{});
    if(st)st.textContent='';
  }catch(e){if(st)st.textContent='生成预览失败: '+e.message;}
}
async function confirmFix(){
  const r=await fetch('/api/audit/fix?confirm=true',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({task_id:previewTaskId,issue_ids:previewIds})});
  const d=await r.json();
  if(!r.ok){toast('修复失败: '+(d.error||''));return;}
  closeDiff();
  toast('已修复 '+(d.files||[]).length+' 个文件（已备份 .bak）');
  if(ws&&ws.readyState===WebSocket.OPEN){
    ws.send(JSON.stringify({type:'audit_log',content:'[代码审计] 已修复 '+(d.files||[]).join(', ')+'，原文件已备份为 .bak' }));
  }
  auditData.selected={};
  loadAudit();
}
function openDiff(diffs){
  const body=$('diffBody');body.innerHTML='';
  previewFiles=Object.keys(diffs||{});
  $('diffFileList').textContent=previewFiles.length?('共 '+previewFiles.length+' 个文件'):'';
  let affected=false;
  previewFiles.forEach(path=>{
    const d=diffs[path];
    const file=document.createElement('div');file.className='diff-file';
    file.innerHTML='<div class="fh">'+esc(path)+'</div>';
    const fl=document.createElement('pre');
    d.split('\n').forEach(l=>{
      const span=document.createElement('span');
      if(l.startsWith('@@'))span.className='diff-line hunk';
      else if(l.startsWith('+')&&!l.startsWith('+++')){span.className='diff-line add';affected=true;}
      else if(l.startsWith('-')&&!l.startsWith('---'))span.className='diff-line del';
      else span.className='diff-line ctx';
      span.textContent=l;
      fl.appendChild(span);
    });
    file.appendChild(fl);
    body.appendChild(file);
  });
  if(!affected){$('diffNote').style.display='block';}
  document.getElementById('diffModal').classList.add('on');
}
function closeDiff(){document.getElementById('diffModal').classList.remove('on');}

/* connection */
function connect(){
  const proto=location.protocol==='https:'?'wss':'ws';
  ws=new WebSocket(proto+'://'+location.host+'/ws');
  ws.onopen=()=>{
    ws.send(JSON.stringify({type:'sessions_get'}));
    ws.send(JSON.stringify({type:'settings_get'}));
    $('connStat').innerHTML='<span style="width:6px;height:6px;border-radius:50%;background:var(--green);display:inline-block"></span> 已连接';
  };
  ws.onclose=()=>{
    busy=false;$('send').disabled=true;
    $('connStat').innerHTML='<span style="width:6px;height:6px;border-radius:50%;background:var(--red);display:inline-block"></span> 断开';
    setTimeout(connect,2000);
  };
  ws.onmessage=e=>{
    let evt;try{evt=JSON.parse(e.data)}catch{return;}
    switch(evt.type){
      case 'delta':
        if(busy&&!lastAsst){lastAsst=addMsg('assistant','');}
        if(lastAsst)appendTo(lastAsst,evt.content);
        $('statusBar').textContent='思考中...';
        break;
      case 'tool_start':tools.push(addTool(evt));break;
      case 'tool_done':if(tools.length){setToolDone(tools.pop(),evt);}break;
      case 'status':$('statusBar').textContent=evt.content||'';break;
      case 'settings':curSettings=evt.settings||{};$('modelTag').textContent=curSettings.model||'';break;
      case 'sessions':
        sessions=evt.sessions||[];const newSid=evt.sessionId||'';
        if(newSid&&newSid!==curSess){$('chatWrap').innerHTML='';renderEmpty();}
        curSess=newSid;prevSess=curSess;renderSessions();break;
      case 'stats':
        $('sMsgs').textContent=(evt.stats&&evt.stats.messages)||0;
        $('sTokens').textContent=(evt.stats&&evt.stats.tokens)||0;
        $('sAlways').textContent=((evt.stats&&evt.stats.always_allow)||[]).join(', ')||'-';
        break;
      case 'ask':curAsk=evt.askId;$('askName').textContent=evt.toolName;$('askBar').style.display='';setBusy(true);break;
      case 'done':
        setBusy(false);tools=[];$('statusBar').textContent='';
        ws.send(JSON.stringify({type:'sessions_get'}));
        break;
      case 'error':
        addMsg('assistant','⚠ '+evt.error);setBusy(false);tools=[];
        $('statusBar').textContent='';
        break;
      case 'interrupt':
        setBusy(false);tools=[];
        $('statusBar').textContent='';
        break;
      case 'audit_log':
        // 审计在后台完成时推送；刷新审计面板（无需在聊天重复落盘）
        loadAudit();
        break;
    }
  };
}

/* version */
fetch('/api/version').then(r=>r.json()).then(d=>{$('sVer').textContent=d.version||''}).catch(()=>{});

connect();