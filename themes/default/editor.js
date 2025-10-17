(function(){
  const q = new URLSearchParams(location.search);
  const path = q.get('path');
  const statusEl = document.getElementById('status');
  const ta = document.getElementById('editor');
  const pv = document.getElementById('preview');
  const tree = document.getElementById('tree');
  const inp = document.getElementById('q');
  const saveBtn = document.getElementById('saveBtn');
  const openBtn = document.getElementById('openBtn');
  const themeSel = document.getElementById('theme');
  const hardwrapChk = document.getElementById('hardwrap');

  function previewURL(){
    const params = new URLSearchParams();
    params.set('path', path);
    const theme = document.documentElement.getAttribute('data-theme') || 'light';
    if(theme && theme !== 'light') params.set('theme', theme);
    if(hardwrapChk.checked) params.set('hardwrap','1');
    return '/__preview?' + params.toString();
  }

  openBtn.href = '/' + path.replace(/^content\//,'').replace(/\.md$/, '.html');
  pv.src = previewURL();

  try {
    const es = new EventSource('/__livereload');
    es.onmessage = () => {
      const src = pv.src; pv.src = src;
    };
  } catch {}

  async function load() {
    const r = await fetch('/__raw?path=' + encodeURIComponent(path));
    if (!r.ok) { ta.value = '# Load failed: ' + r.status; return; }
    ta.value = await r.text();
  }
  async function save() {
    statusEl.textContent = 'Saving...';
    const r = await fetch('/__save?path=' + encodeURIComponent(path), { method:'POST', body: ta.value });
    if (!r.ok) { statusEl.textContent = 'Save failed ' + r.status; return; }
    statusEl.textContent = 'Saved';
  }
  addEventListener('keydown', (e)=>{
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase()==='s') {
      e.preventDefault(); save();
    }
  });
  saveBtn.onclick = save;

  // theme toggle
  const THEME_KEY = 'karte-theme';
  function setThemeAttr(name){
    if(name === 'dark') document.documentElement.setAttribute('data-theme','dark');
    else if(name === 'hc') document.documentElement.setAttribute('data-theme','hc');
    else document.documentElement.removeAttribute('data-theme');
  }
  function applyTheme(name){
    if(name === 'dark' || name === 'hc') setThemeAttr(name);
    else { document.documentElement.removeAttribute('data-theme'); name = 'light'; }
    try{ localStorage.setItem(THEME_KEY, name); }catch{}
    pv.src = previewURL();
  }
  (function(){
    let init; try{ init = localStorage.getItem(THEME_KEY); }catch{}
    if(!init){ init = (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light'; }
    themeSel.value = init; setThemeAttr(init);
  })();
  themeSel.addEventListener('change', (e)=> applyTheme(e.target.value));
  hardwrapChk.addEventListener('change', ()=>{ pv.src = previewURL(); });

  // file list
  async function listFiles() {
    const r = await fetch('/__list');
    if (!r.ok) return;
    const items = await r.json();
    tree.innerHTML = '';
    const frag = document.createDocumentFragment();
    const qq = (inp.value||'').toLowerCase();
    for (const it of items) {
      if (qq && !(it.Path.toLowerCase().includes(qq) || it.Title.toLowerCase().includes(qq))) continue;
      const a = document.createElement('a');
      a.className = 'item' + (it.Path === path ? ' active':'');
      a.textContent = it.Title + '  —  ' + it.Path.replace(/^content\//,'');
      a.href = '/__edit?path=' + encodeURIComponent(it.Path);
      frag.appendChild(a);
    }
    tree.appendChild(frag);
  }
  inp.addEventListener('input', listFiles);

  // caret-aware scrolling and preview sync
  function getLineHeight(){ const cs = window.getComputedStyle(ta); const lh = parseFloat(cs.lineHeight); return Number.isFinite(lh)&&lh>0 ? lh : 20; }
  function caretLine(startIdx){ const v = ta.value; let line = 0; for(let i=0;i<startIdx;i++){ if(v.charCodeAt(i)===10) line++; } return line; }
  function keepCaretInView(){ const lh=getLineHeight(); const pad=lh*2; const start=ta.selectionStart; const line=caretLine(start); const targetTop=line*lh; const viewTop=ta.scrollTop; const viewBottom=viewTop+ta.clientHeight; if(targetTop<viewTop+pad){ ta.scrollTop=Math.max(0,targetTop-pad);} else if(targetTop>viewBottom-pad){ ta.scrollTop=targetTop-ta.clientHeight+pad;} syncPreviewScroll(); }
  function totalLines(){ const v=ta.value; let n=1; for(let i=0;i<v.length;i++){ if(v.charCodeAt(i)===10) n++; } return n; }
  function caretRatio(){ const line=caretLine(ta.selectionStart); const tl=totalLines(); if(tl<=1) return 0; return Math.min(1, Math.max(0, line/(tl-1))); }
  let lastRatio=0;
  function scrollPreviewToRatio(r){ try{ const doc=pv.contentDocument; if(!doc) return; const el=doc.scrollingElement||doc.documentElement||doc.body; const max=Math.max(0, el.scrollHeight-el.clientHeight); el.scrollTo({top:Math.round(max*r), behavior:'auto'});}catch{} }
  function syncPreviewScroll(){ lastRatio=caretRatio(); scrollPreviewToRatio(lastRatio); }
  pv.addEventListener('load', ()=>{ scrollPreviewToRatio(lastRatio); });

  function getSelection(){ return { start: ta.selectionStart, end: ta.selectionEnd, value: ta.value }; }
  function setSelection(start, end){ ta.selectionStart = start; ta.selectionEnd = end; keepCaretInView(); }
  function replaceRange(text, start, end, insert){ ta.value = text.slice(0,start) + insert + text.slice(end); }
  function wrapSelection(prefix, suffix){ const {start,end,value}=getSelection(); const sel=value.slice(start,end); const hasWrap = value.slice(Math.max(0,start-prefix.length), start)===prefix && value.slice(end, end+suffix.length)===suffix; if(hasWrap){ replaceRange(value, start-prefix.length, end+suffix.length, sel); setSelection(start-prefix.length, end-prefix.length); return; } replaceRange(value, start, end, prefix+sel+suffix); setSelection(start+prefix.length, end+prefix.length); }
  function currentLineInfo(){ const {start,end,value}=getSelection(); let ls=value.lastIndexOf('\n', start-1); if(ls===-1) ls=0; else ls+=1; let le=value.indexOf('\n', start); if(le===-1) le=value.length; return { lineStart:ls, lineEnd:le, start, end, value }; }
  function togglePrefix(prefix){ const info=currentLineInfo(); const line=info.value.slice(info.lineStart, info.lineEnd); if(line.startsWith(prefix)){ const newLine=line.slice(prefix.length); replaceRange(info.value, info.lineStart, info.lineEnd, newLine); const delta=-prefix.length; setSelection(info.start+delta, info.end+delta);} else { replaceRange(info.value, info.lineStart, info.lineEnd, prefix+line); const delta=prefix.length; setSelection(info.start+delta, info.end+delta);} }
  function toggleHeading(level){ const hashes='#'.repeat(level)+' '; const info=currentLineInfo(); const line=info.value.slice(info.lineStart, info.lineEnd).replace(/^#+\s+/, ''); replaceRange(info.value, info.lineStart, info.lineEnd, hashes+line); setSelection(info.start+hashes.length, info.end+hashes.length); }
  function insertAtCursor(text){ const {start,end,value}=getSelection(); replaceRange(value, start, end, text); setSelection(start+text.length, start+text.length); }
  function handleEnter(e){ const info=currentLineInfo(); const line=info.value.slice(info.lineStart, info.start); const m=line.match(/^(\s*)(?:([>]+)\s*)?(?:(-|\*|\+) |(\d+)\. |- \[(?: |x)\] )?/); if(!m) return false; const indent=m[1]||''; const quote=m[2]?m[2]+' ':''; const bullet=m[3]?(m[3]+' '):(m[4]?(String(parseInt(m[4],10)+1)+'. '):(/- \[(?: |x)\] /.test(line)?'- [ ] ':'')); const marker=indent+quote+bullet; const lineTrim=line.replace(/^\s+/, ''); const onlyMarker=/^([> ]|(-|\*|\+)|\d+\.|- \[(?: |x)\])\s*$/.test(lineTrim); e.preventDefault(); if(onlyMarker){ const before=info.value.slice(0, info.lineStart); const mid=indent+quote; const after=info.value.slice(info.start); const newVal=before+mid+after; ta.value=newVal; const insAt=info.lineStart+mid.length; ta.value=newVal.slice(0, insAt)+'\n'+newVal.slice(insAt); setSelection(insAt+1, insAt+1); syncPreviewScroll(); return true; } const s=info.start; const v=info.value; ta.value=v.slice(0,s)+'\n'+marker+v.slice(s); const newCaret=s+1+marker.length; setSelection(newCaret, newCaret); syncPreviewScroll(); return true; }
  function handleBackspace(e){ const info=currentLineInfo(); const line=info.value.slice(info.lineStart, info.start); const m=line.match(/^(\s*)(?:([>]+)\s*)?(?:(-|\*|\+)|\d+\.|- \[(?: |x)\])\s$/); if(m){ e.preventDefault(); const newLine=info.value.slice(info.lineStart, info.start - (m[0].length - m[1].length)); replaceRange(info.value, info.lineStart, info.start, newLine); setSelection(info.lineStart + newLine.length, info.lineStart + newLine.length); return true; } return false; }
  function autoPair(e){ const pairs={ '(':')', '[':']', '{':'}', '"':'"', "'":"'" }; const ch=e.key; if(!(ch in pairs)) return false; const {start,end,value}=getSelection(); const sel=value.slice(start,end); e.preventDefault(); const insert=ch+sel+pairs[ch]; replaceRange(value, start, end, insert); if(sel){ setSelection(start+1, start+1+sel.length);} else { setSelection(start+1, start+1);} return true; }
  function inlineFence(e){ const bt=String.fromCharCode(96); if((e.ctrlKey||e.metaKey) && e.key === bt){ e.preventDefault(); wrapSelection(bt, bt); return true; } return false; }
  function codeBlockShortcut(e){ const bt=String.fromCharCode(96); if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key === bt){ e.preventDefault(); const fence=bt+bt+bt; insertAtCursor('\n'+fence+'\n\n'+fence+'\n'); return true; } return false; }
  function toggleTask(){ const info=currentLineInfo(); const line=info.value.slice(info.lineStart, info.lineEnd); const toggled=line.replace(/^(-\s\[\s\]\s)/,'- [x] ').replace(/^(-\s\[x\]\s)/,'- [ ] '); if(toggled!==line){ replaceRange(info.value, info.lineStart, info.lineEnd, toggled); return true; } replaceRange(info.value, info.lineStart, info.lineEnd, '- [ ] '+line.replace(/^\s+/,'')); return true; }

  ta.addEventListener('keydown', function(e){
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && e.key.toLowerCase()==='b'){ e.preventDefault(); wrapSelection('**','**'); return; }
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && e.key.toLowerCase()==='i'){ e.preventDefault(); wrapSelection('*','*'); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && (e.key.toLowerCase()==='x')){ e.preventDefault(); wrapSelection('~~','~~'); return; }
    if(inlineFence(e)) return;
    if(codeBlockShortcut(e)) return;
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && /^[1-6]$/.test(e.key)){ e.preventDefault(); toggleHeading(parseInt(e.key,10)); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key === '-'){ e.preventDefault(); insertAtCursor('\n---\n'); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && (e.key === '>' || e.key === '.')){ e.preventDefault(); togglePrefix('> '); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key.toLowerCase()==='l'){ e.preventDefault(); toggleTask(); return; }
    if(autoPair(e)) return;
    if(e.key === 'Enter'){ if(handleEnter(e)) return; }
    if(e.key === 'Backspace'){ if(handleBackspace(e)) return; }
  });
  ta.addEventListener('input', function(){ syncPreviewScroll(); });
  ta.addEventListener('click', function(){ syncPreviewScroll(); });
  ta.addEventListener('keyup', function(){ syncPreviewScroll(); });

  (async () => {
    await listFiles();
    await load();
    let t;
    ta.addEventListener('input', () => {
      clearTimeout(t);
      t = setTimeout(async () => {
        try {
          await fetch('/__draft?path=' + encodeURIComponent(path), { method:'POST', body: ta.value });
          const src = previewURL(); pv.src = src;
        } catch {}
      }, 150);
    });
  })();
})();


