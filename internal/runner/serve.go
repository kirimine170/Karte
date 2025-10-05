package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"karte/internal/site"

	"github.com/fsnotify/fsnotify"
)

type sseHub struct{ ch chan struct{} }

func newHub() *sseHub { return &sseHub{ch: make(chan struct{}, 1)} }

func (h *sseHub) notify() {
	select {
	case h.ch <- struct{}{}:
	default:
	}
}

func (h *sseHub) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.ch:
			fmt.Fprintf(w, "data: reload\n\n")
			flusher.Flush()
		}
	}
}

func Serve(root string, port int) error {
	// convert to absolute path
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absRoot

	// initial build
	if err := Build(root); err != nil {
		return err
	}

	hub := newHub()

	mux := http.NewServeMux()
	publicDir := filepath.Join(root, "public")
	mux.Handle("/", http.FileServer(http.Dir(publicDir)))
	mux.HandleFunc("/__livereload", hub.serve)

	// editor endpoints
	mux.HandleFunc("/__edit", func(w http.ResponseWriter, r *http.Request) { serveEditor(root, w, r) })
	mux.HandleFunc("/__raw", func(w http.ResponseWriter, r *http.Request) { handleRaw(root, w, r) })
	mux.HandleFunc("/__save", func(w http.ResponseWriter, r *http.Request) { handleSave(root, hub, w, r) })
	mux.HandleFunc("/__list", func(w http.ResponseWriter, r *http.Request) { handleList(root, w, r) })
	// live preview (draft) endpoints
	mux.HandleFunc("/__draft", func(w http.ResponseWriter, r *http.Request) { handleDraft(root, w, r) })
	mux.HandleFunc("/__preview", func(w http.ResponseWriter, r *http.Request) { handlePreview(root, w, r) })

	// health
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// watch content/data/themes
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	for _, d := range []string{"content", "data", "themes"} {
		if err := w.Add(filepath.Join(root, d)); err != nil {
			return err
		}
	}
	go func() {
		for {
			select {
			case ev := <-w.Events:
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					log.Println("change:", ev.Name)
					_ = Build(root) // TODO: incremental
					hub.notify()
				}
			case err := <-w.Errors:
				log.Println("watch error:", err)
			}
		}
	}()

	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: injectLiveReload(publicDir, mux)}
	log.Printf("karte serve http://localhost:%d\n", port)
	return srv.ListenAndServe()
}

func injectLiveReload(root string, next http.Handler) http.Handler {
	// very small middleware: if serving .html, inject livereload script
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Normalize URL path and map "/" or "/dir/" to "index.html"
		reqPath := r.URL.Path
		var rel string
		if strings.HasSuffix(reqPath, "/") { // e.g. "/" or "/foo/" -> "index.html"
			rel = strings.TrimPrefix(reqPath, "/") + "index.html"
		} else if strings.HasSuffix(reqPath, ".html") {
			rel = strings.TrimPrefix(reqPath, "/")
		} else {
			next.ServeHTTP(w, r)
			return
		}
		p := filepath.Join(root, filepath.Clean(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			// Fallback to default handler if not found
			next.ServeHTTP(w, r)
			return
		}
		snippet := `<script>const es=new EventSource('/__livereload');es.onmessage=()=>location.reload();</script>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(append(b, []byte(snippet)...))
	})
}

// ---------- Live editor ----------

// sanitize and resolve path under content/
func resolveContentPath(root, rel string) (abs string, ok bool) {
	log.Printf("DEBUG: resolveContentPath - input: %q, root: %q", rel, root)
	rel = filepath.ToSlash(rel)
	// 許容: 先頭に '/' が付いた形式（例: '/content/foo.md'）
	rel = strings.TrimPrefix(rel, "/")
	log.Printf("DEBUG: resolveContentPath - after trim: %q", rel)
	if !strings.HasPrefix(rel, "content/") {
		log.Printf("DEBUG: resolveContentPath - path does not start with content/: %q", rel)
		return "", false
	}
	abs = filepath.Join(root, filepath.FromSlash(rel))
	canonical, err := filepath.Abs(abs)
	if err != nil {
		return "", false
	}
	contentRoot, _ := filepath.Abs(filepath.Join(root, "content"))
	// ensure inside content/ using Rel to avoid case/volume issues on Windows
	relToContent, err := filepath.Rel(contentRoot, canonical)
	if err != nil {
		return "", false
	}
	if filepath.IsAbs(relToContent) {
		return "", false
	}
	// prevent traversal: no leading .. segment
	slashRel := filepath.ToSlash(relToContent)
	if slashRel == ".." || strings.HasPrefix(slashRel, "../") {
		log.Printf("DEBUG: resolveContentPath - path traversal detected: %q", slashRel)
		return "", false
	}
	log.Printf("DEBUG: resolveContentPath - resolved to: %q", canonical)
	return canonical, true
}

func mdToHTMLURL(rel string) string {
	if strings.HasSuffix(rel, ".md") {
		return "/" + strings.TrimSuffix(rel, ".md") + ".html"
	}
	return "/" + rel
}

func serveEditor(root string, w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "missing ?path=content/xxx.md", http.StatusBadRequest)
		return
	}
	if _, ok := resolveContentPath(root, rel); !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}

	// htmlURL は JS 側で算出する（サーバ側の埋め込みはしない）
	page := `<!doctype html>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Karte — Editor</title>
<style>
  /* Theme system variables */
  :root {
    --main-background: #ffffff;
    --text-color: #1f2937;
    --browsername-color: #111827;
    --backgroundcolor: #f9fafb;
    --backgroundcolor-unhover: #f3f4f6;
    --opened-tab-backgroundcolor: #e5e7eb;
    --border-color: #e5e7eb;
    --border-color-unhover: #eaecef;
    --borderline-color: #d1d5db;
    --shadow-color: rgba(17,24,39,0.18);
    --shadow-color-unhover: rgba(17,24,39,0.08);
    --input-color-unhover: #ffffff;
    --loading-color: #7c3aed;
    --closebutton-color: #7c3aed;
  }
  :root[data-theme="dark"]{
    --main-background: #0b0f14;
    --text-color: #e5e7eb;
    --browsername-color: #f9fafb;
    --backgroundcolor: #0f1720;
    --backgroundcolor-unhover: #111827;
    --opened-tab-backgroundcolor: #1f2937;
    --border-color: #243041;
    --border-color-unhover: #1e293b;
    --borderline-color: #324155;
    --shadow-color: rgba(0,0,0,0.5);
    --shadow-color-unhover: rgba(0,0,0,0.3);
    --input-color-unhover: #111827;
    --loading-color: #a78bfa;
    --closebutton-color: #a78bfa;
  }
  :root[data-theme="hc"]{
    --main-background: #ffffff;
    --text-color: #000000;
    --browsername-color: #000000;
    --backgroundcolor: #ffffff;
    --backgroundcolor-unhover: #f2f2f2;
    --opened-tab-backgroundcolor: #e6e6e6;
    --border-color: #111111;
    --border-color-unhover: #4d4d4d;
    --borderline-color: #000000;
    --shadow-color: rgba(0,0,0,0.25);
    --shadow-color-unhover: rgba(0,0,0,0.15);
    --input-color-unhover: #ffffff;
    --loading-color: #7c3aed;
    --closebutton-color: #7c3aed;
  }
  /* Light(default) */
  :root{
    --bg:var(--main-background); --panel:var(--backgroundcolor); --ink:var(--text-color); --mute:var(--mute, #6b7280); --line:var(--border-color); --brand:var(--browsername-color);
    --accent:var(--loading-color);
    --radius:10px; --gap:12px; --gap2:16px;
  }
  /* Dark */
  :root[data-theme="dark"]{
    --bg:var(--main-background); --panel:var(--backgroundcolor); --ink:var(--text-color); --mute:var(--mute, #8a8a8a); --line:var(--border-color); --brand:var(--browsername-color);
    --accent:var(--loading-color);
  }
  /* High Contrast */
  :root[data-theme="hc"]{
    --bg:var(--main-background); --panel:var(--backgroundcolor); --ink:var(--text-color); --mute:var(--mute, #4d4d4d); --line:var(--border-color); --brand:var(--browsername-color);
    --accent:var(--loading-color);
  }
  @media (prefers-color-scheme: dark){ :root:not([data-theme]){ color-scheme: dark; } }
  @media (prefers-color-scheme: light){ :root:not([data-theme]){ color-scheme: light; } }
  *{box-sizing:border-box}
  html,body{height:100%}
  body{margin:0;background:var(--bg);color:var(--ink);font:13px/1.6 ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Hiragino Kaku Gothic ProN", "Noto Sans JP", "Yu Gothic UI", "Meiryo", sans-serif;}
  a{color:inherit;text-decoration:none}
  .bar{height:44px;display:flex;align-items:center;gap:10px;padding:0 12px;border-bottom:1px solid var(--line);background:linear-gradient(180deg, color-mix(in srgb, var(--main-background) 98%, transparent), transparent)}
  .logo{font-weight:700;letter-spacing:0.4px}
  .hint{color:var(--mute);font-size:12px}
  .status{margin-left:auto;color:var(--mute)}
  .layout{display:grid;grid-template-columns:260px 1fr; height:calc(100% - 44px);}
  .side{border-right:1px solid var(--line);padding:12px;background:var(--panel)}
  .search{display:flex;gap:6px;margin-bottom:10px}
  .search input{flex:1;background:var(--panel);border:1px solid var(--line);color:var(--ink);padding:8px 10px;border-radius:8px;outline:0}
  .tree{overflow:auto;height:calc(100% - 44px)}
  .item{padding:6px 8px;border-radius:8px;color:var(--ink);border:1px solid transparent; display:block}
  .item:hover{background:var(--backgroundcolor-unhover);border-color:var(--line)}
  .item.active{background:var(--opened-tab-backgroundcolor);border-color:var(--borderline-color)}
  .main{display:grid;grid-template-rows:1fr 1fr; gap:var(--gap); padding:var(--gap2)}
  textarea{width:100%;height:100%;border:1px solid var(--line);background:var(--panel);color:var(--ink);padding:12px;border-radius:var(--radius);resize:none;outline:none; font:13px/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace;}
  textarea:focus{box-shadow:0 0 0 2px color-mix(in srgb, var(--accent) 35%, transparent) inset}
  iframe{width:100%;height:100%;border:1px solid var(--line);border-radius:var(--radius);background:var(--bg)}
  .row{display:grid;grid-template-columns:1fr 1fr; gap:var(--gap)}
  .btn{background:var(--accent);color:var(--browsername-color);border:0;border-radius:8px;padding:6px 10px;cursor:pointer}
  .btn[disabled]{opacity:.6}
  .toolbar{display:flex;gap:8px;margin-left:8px}
  .pill{border:1px solid var(--line);padding:4px 8px;border-radius:999px;color:var(--mute);font-size:12px}
  ::selection{background-color:color-mix(in srgb, var(--accent) 25%, transparent)}
  
  /* Scrollbar styling */
  ::-webkit-scrollbar{width:8px;height:8px}
  ::-webkit-scrollbar-track{background:var(--panel)}
  ::-webkit-scrollbar-thumb{background:var(--border-color);border-radius:4px}
  ::-webkit-scrollbar-thumb:hover{background:var(--borderline-color)}
  ::-webkit-scrollbar-corner{background:var(--panel)}
  
  @media (max-width: 1100px){ .layout{grid-template-columns:1fr} .side{display:none} .row{grid-template-columns:1fr} }
</style>
<div class="bar">
  <div class="logo">Karte</div>
  <div class="pill">Editor</div>
  <div class="hint">Ctrl/Cmd+S: 保存・再ビルド</div>
  <div class="toolbar">
    <label class="hint" for="theme">Theme</label>
    <select id="theme">
      <option value="light">Light</option>
      <option value="dark">Dark</option>
      <option value="hc">High Contrast</option>
    </select>
    <label class="hint" for="hardwrap" style="display:flex;align-items:center;gap:6px">
      <input id="hardwrap" type="checkbox"/> Hard wrap (単一改行→<br>)
    </label>
    <button class="btn" id="saveBtn">保存</button>
    <a class="btn" id="openBtn" target="_blank" rel="noopener">表示</a>
  </div>
  <div class="status" id="status"></div>
</div>
<div class="layout">
  <aside class="side">
    <div class="search">
      <input id="q" placeholder="ファイル検索 (content/)"/>
    </div>
    <div class="tree" id="tree"></div>
  </aside>
  <main class="main">
    <div class="row">
      <textarea id="editor" spellcheck="false"></textarea>
      <iframe id="preview"></iframe>
    </div>
  </main>
</div>
<script>
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

// path=content/xxx.md -> preview via /__preview?path=content/xxx.md
const previewURLBase = '/__preview?path=' + encodeURIComponent(path);
function previewURL(){
  const params = new URLSearchParams();
  params.set('path', path);
  const theme = document.documentElement.getAttribute('data-theme') || 'light';
  if(theme && theme !== 'light') params.set('theme', theme);
  if(hardwrapChk.checked) params.set('hardwrap','1');
  return '/__preview?' + params.toString();
}
openBtn.href = '/' + path.replace(/^content\//,'').replace(/\.md$/, '.html'); // 公開用
pv.src = previewURL();

// live reload (server-sent events)
try {
  const es = new EventSource('/__livereload');
  es.onmessage = () => {
    // ビルド完了後にのみプレビューを更新（404レースを回避）
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

// theme toggle (shared across whole app via localStorage)
const THEME_KEY = 'karte-theme';
function setThemeAttr(name){
  if(name === 'dark') document.documentElement.setAttribute('data-theme','dark');
  else if(name === 'hc') document.documentElement.setAttribute('data-theme','hc');
  else document.documentElement.removeAttribute('data-theme');
}
function applyTheme(name){
  if(name === 'dark' || name === 'hc') {
    setThemeAttr(name);
  } else {
    document.documentElement.removeAttribute('data-theme');
    name = 'light';
  }
  try{ localStorage.setItem(THEME_KEY, name); }catch{}
  // プレビューに即時反映
  pv.src = previewURL();
}
// 初期化: 保存値 > OS設定
(function(){
  let init;
  try{ init = localStorage.getItem(THEME_KEY); }catch{}
  if(!init){
    init = (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) ? 'dark' : 'light';
  }
  themeSel.value = init;
  setThemeAttr(init);
})();
// 変更時
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

(async () => {
    await listFiles();
    await load();
    // 入力のたびにドラフトを送信し、プレビューを即時更新
    let t;
    ta.addEventListener('input', () => {
      clearTimeout(t);
      t = setTimeout(async () => {
        try {
          await fetch('/__draft?path=' + encodeURIComponent(path), { method:'POST', body: ta.value });
          const src = previewURL(); pv.src = src; // 即時再読込（オプション反映）
        } catch {}
      }, 150);
    });
})();

// Theme switching (unified with above)
const root = document.documentElement;
const KEY = 'karte-theme';

// Initial theme
(() => {
  const saved = localStorage.getItem(KEY);
  if (saved) {
    applyTheme(saved);
  } else {
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    applyTheme(prefersDark ? 'dark' : 'light');
  }
})();

// Theme selector
document.getElementById('theme').addEventListener('change', (e) => applyTheme(e.target.value));
</script>`

	// Append input assistance (keyboard shortcuts and auto-pairing) to the editor page
	page += `
<script>
// --- Markdown editor input assistance ---
(function(){
  const ta = document.getElementById('editor');
  if(!ta) return;
  const pv = document.getElementById('preview');

  // --- caret-aware scrolling ---
  function getLineHeight(){
    const cs = window.getComputedStyle(ta);
    const lh = parseFloat(cs.lineHeight);
    return Number.isFinite(lh) && lh > 0 ? lh : 20;
  }
  function caretLine(startIdx){
    const v = ta.value;
    let line = 0;
    for(let i=0;i<startIdx;i++){ if(v.charCodeAt(i)===10) line++; }
    return line;
  }
  function keepCaretInView(){
    const lh = getLineHeight();
    const pad = lh * 2;
    const start = ta.selectionStart;
    const line = caretLine(start);
    const targetTop = line * lh;
    const viewTop = ta.scrollTop;
    const viewBottom = viewTop + ta.clientHeight;
    if(targetTop < viewTop + pad){
      ta.scrollTop = Math.max(0, targetTop - pad);
    }else if(targetTop > viewBottom - pad){
      ta.scrollTop = targetTop - ta.clientHeight + pad;
    }
    syncPreviewScroll();
  }

  // --- preview scroll sync ---
  let lastRatio = 0;
  function totalLines(){
    // number of lines = number of '\n' + 1
    const v = ta.value;
    let n = 1;
    for(let i=0;i<v.length;i++){ if(v.charCodeAt(i)===10) n++; }
    return n;
  }
  function caretRatio(){
    const line = caretLine(ta.selectionStart);
    const tl = totalLines();
    if(tl <= 1) return 0;
    const r = Math.min(1, Math.max(0, line / (tl - 1)));
    return r;
  }
  function scrollPreviewToRatio(r){
    try{
      const doc = pv.contentDocument;
      if(!doc) return;
      const el = doc.scrollingElement || doc.documentElement || doc.body;
      const max = Math.max(0, el.scrollHeight - el.clientHeight);
      el.scrollTo({ top: Math.round(max * r), behavior: 'auto' });
    }catch{}
  }
  function syncPreviewScroll(){
    lastRatio = caretRatio();
    scrollPreviewToRatio(lastRatio);
  }
  pv.addEventListener('load', ()=>{ scrollPreviewToRatio(lastRatio); });

  function getSelection(){
    return { start: ta.selectionStart, end: ta.selectionEnd, value: ta.value };
  }
  function setSelection(start, end){
    ta.selectionStart = start; ta.selectionEnd = end;
    keepCaretInView();
  }
  function replaceRange(text, start, end, insert){
    ta.value = text.slice(0,start) + insert + text.slice(end);
  }
  function wrapSelection(prefix, suffix){
    const {start, end, value} = getSelection();
    const sel = value.slice(start, end);
    // Toggle if already wrapped
    const hasWrap = value.slice(Math.max(0,start-prefix.length), start) === prefix && value.slice(end, end+suffix.length) === suffix;
    if(hasWrap){
      replaceRange(value, start - prefix.length, end + suffix.length, sel);
      setSelection(start - prefix.length, end - prefix.length);
      return;
    }
    replaceRange(value, start, end, prefix + sel + suffix);
    setSelection(start + prefix.length, end + prefix.length);
  }
  function currentLineInfo(){
    const {start, end, value} = getSelection();
    let ls = value.lastIndexOf('\n', start - 1);
    if(ls === -1) ls = 0; else ls += 1;
    let le = value.indexOf('\n', start);
    if(le === -1) le = value.length;
    return { lineStart: ls, lineEnd: le, start, end, value };
  }
  function togglePrefix(prefix){
    const info = currentLineInfo();
    const line = info.value.slice(info.lineStart, info.lineEnd);
    if(line.startsWith(prefix)){
      const newLine = line.slice(prefix.length);
      replaceRange(info.value, info.lineStart, info.lineEnd, newLine);
      const delta = -prefix.length;
      setSelection(info.start+delta, info.end+delta);
    }else{
      replaceRange(info.value, info.lineStart, info.lineEnd, prefix + line);
      const delta = prefix.length;
      setSelection(info.start+delta, info.end+delta);
    }
  }
  function toggleHeading(level){
    const hashes = '#'.repeat(level) + ' ';
    const info = currentLineInfo();
    const line = info.value.slice(info.lineStart, info.lineEnd).replace(/^#+\s+/, '');
    replaceRange(info.value, info.lineStart, info.lineEnd, hashes + line);
    setSelection(info.start + hashes.length, info.end + hashes.length);
  }
  function insertAtCursor(text){
    const {start, end, value} = getSelection();
    replaceRange(value, start, end, text);
    setSelection(start + text.length, start + text.length);
  }
  function handleEnter(e){
    const info = currentLineInfo();
    const line = info.value.slice(info.lineStart, info.start);
    const m = line.match(/^(\s*)(?:([>]+)\s*)?(?:(-|\*|\+) |(\d+)\. |- \[(?: |x)\] )?/);
    if(!m) return false;
    const indent = m[1]||'';
    const quote = m[2] ? m[2] + ' ' : '';
    const bullet = m[3] ? (m[3] + ' ') : (m[4] ? (String(parseInt(m[4],10)+1) + '. ') : (/- \[(?: |x)\] /.test(line) ? '- [ ] ' : ''));
    const marker = indent + quote + bullet;
    const lineTrim = line.replace(/^\s+/, '');
    const onlyMarker = /^([> ]|(-|\*|\+)|\d+\.|- \[(?: |x)\])\s*$/.test(lineTrim);
    e.preventDefault();
    if(onlyMarker){
      // remove marker and start new empty line (stay within list context)
      const before = info.value.slice(0, info.lineStart);
      const mid = indent + quote; // keep indent/quote, drop list symbol
      const after = info.value.slice(info.start);
      const newVal = before + mid + after;
      ta.value = newVal;
      const insAt = info.lineStart + mid.length;
      // insert newline at precise position
      ta.value = newVal.slice(0, insAt) + '\n' + newVal.slice(insAt);
      setSelection(insAt + 1, insAt + 1);
      syncPreviewScroll();
      return true;
    }
    // continue list with next marker at exact caret position
    const s = info.start;
    const v = info.value;
    ta.value = v.slice(0, s) + '\n' + marker + v.slice(s);
    const newCaret = s + 1 + marker.length;
    setSelection(newCaret, newCaret);
    syncPreviewScroll();
    return true;
  }
  function handleBackspace(e){
    const info = currentLineInfo();
    const line = info.value.slice(info.lineStart, info.start);
    const m = line.match(/^(\s*)(?:([>]+)\s*)?(?:(-|\*|\+)|\d+\.|- \[(?: |x)\])\s$/);
    if(m){
      e.preventDefault();
      // Remove the trailing marker on empty item
      const newLine = info.value.slice(info.lineStart, info.start - (m[0].length - m[1].length));
      replaceRange(info.value, info.lineStart, info.start, newLine);
      setSelection(info.lineStart + newLine.length, info.lineStart + newLine.length);
      return true;
    }
    return false;
  }
  function autoPair(e){
    const pairs = { '(':')', '[':']', '{':'}', '"':'"', "'":"'" };
    const ch = e.key;
    if(!(ch in pairs)) return false;
    const {start, end, value} = getSelection();
    const sel = value.slice(start, end);
    e.preventDefault();
    const insert = ch + sel + pairs[ch];
    replaceRange(value, start, end, insert);
    if(sel){ setSelection(start + 1, start + 1 + sel.length); }
    else{ setSelection(start + 1, start + 1); }
    return true;
  }
  function inlineFence(e){
    // backquote cannot appear in Go raw string. Build from char code 96
    const bt = String.fromCharCode(96);
    if((e.ctrlKey||e.metaKey) && e.key === bt){
      e.preventDefault();
      wrapSelection(bt, bt);
      return true;
    }
    return false;
  }
  function codeBlockShortcut(e){
    // Ctrl+Shift+Backquote => code fence block
    const bt = String.fromCharCode(96);
    if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key === bt){
      e.preventDefault();
      const fence = bt+bt+bt;
      insertAtCursor('\n'+fence+'\n\n'+fence+'\n');
      return true;
    }
    return false;
  }
  function toggleTask(){
    const info = currentLineInfo();
    const line = info.value.slice(info.lineStart, info.lineEnd);
    const toggled = line.replace(/^(-\s\[\s\]\s)/, '- [x] ').replace(/^(-\s\[x\]\s)/, '- [ ] ');
    if(toggled !== line){
      replaceRange(info.value, info.lineStart, info.lineEnd, toggled);
      return true;
    }
    // If not a task, make it one
    replaceRange(info.value, info.lineStart, info.lineEnd, '- [ ] ' + line.replace(/^\s+/, ''));
    return true;
  }

  ta.addEventListener('keydown', function(e){
    // Save remains handled globally
    // Formatting shortcuts
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && e.key.toLowerCase()==='b'){ e.preventDefault(); wrapSelection('**','**'); return; }
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && e.key.toLowerCase()==='i'){ e.preventDefault(); wrapSelection('*','*'); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && (e.key.toLowerCase()==='x')){ e.preventDefault(); wrapSelection('~~','~~'); return; }
    if(inlineFence(e)) return;
    if(codeBlockShortcut(e)) return;

    // Structure shortcuts
    if((e.ctrlKey||e.metaKey) && !e.shiftKey && /^[1-6]$/.test(e.key)){ e.preventDefault(); toggleHeading(parseInt(e.key,10)); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key === '-'){ e.preventDefault(); insertAtCursor('\n---\n'); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && (e.key === '>' || e.key === '.')){ e.preventDefault(); togglePrefix('> '); return; }
    if((e.ctrlKey||e.metaKey) && e.shiftKey && e.key.toLowerCase()==='l'){ e.preventDefault(); toggleTask(); return; }

    // Auto-pair ()[]{} ' "
    if(autoPair(e)) return;

    // Enter/Backspace behaviors for lists/quotes
    if(e.key === 'Enter'){ if(handleEnter(e)) return; }
    if(e.key === 'Backspace'){ if(handleBackspace(e)) return; }
  });
  ta.addEventListener('input', function(){
    // after content change, keep preview roughly in sync
    syncPreviewScroll();
  });
  ta.addEventListener('click', function(){ syncPreviewScroll(); });
  ta.addEventListener('keyup', function(){ syncPreviewScroll(); });
})();
</script>
`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}

func handleRaw(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(b)
}

// Store draft content under .mdsys/drafts/<path relative to root>
func draftPath(root, rel string) (string, string, bool) {
	log.Printf("DEBUG: draftPath - input: %q, root: %q", rel, root)
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		log.Printf("DEBUG: draftPath - resolveContentPath failed for: %q", rel)
		return "", "", false
	}
	log.Printf("DEBUG: draftPath - resolved abs path: %q", abs)
	// turn abs content path back to project-relative slash path
	projRel, err := filepath.Rel(root, abs)
	if err != nil {
		log.Printf("DEBUG: draftPath - filepath.Rel failed: %v", err)
		return "", "", false
	}
	projRel = filepath.ToSlash(projRel) // e.g. content/foo.md
	log.Printf("DEBUG: draftPath - projRel: %q", projRel)
	draftAbs := filepath.Join(root, ".mdsys", "drafts", filepath.FromSlash(projRel))
	log.Printf("DEBUG: draftPath - final draft path: %q", draftAbs)
	return draftAbs, projRel, true
}

// POST /__draft?path=content/xxx.md  (body: draft markdown)
func handleDraft(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	log.Printf("DEBUG: handleDraft - received path: %q", rel)
	// 手動でURLデコードを試す
	if decoded, err := url.QueryUnescape(rel); err == nil {
		rel = decoded
		log.Printf("DEBUG: handleDraft - decoded path: %q", rel)
	}
	draftAbs, _, ok := draftPath(root, rel)
	if !ok {
		log.Printf("DEBUG: handleDraft - path resolution failed for: %q", rel)
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	log.Printf("DEBUG: handleDraft - resolved draft path: %q", draftAbs)
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(draftAbs), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(draftAbs, buf.Bytes(), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /__preview?path=content/xxx.md -> render draft if exists otherwise source
func handlePreview(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	// choose content: draft if exists
	draftAbs, _, _ := draftPath(root, rel)
	srcPath := abs
	if draftAbs != "" {
		if b, err := os.Stat(draftAbs); err == nil && !b.IsDir() {
			srcPath = draftAbs
		}
	}
	// hardwrap option: single newline -> <br>
	hardwrap := r.URL.Query().Get("hardwrap") == "1"
	html, _, err := site.RenderMarkdownWithOptions(root, srcPath, hardwrap)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func handleSave(root string, hub *sseHub, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// 保存時に該当ドラフトを削除して公開HTMLとプレビューの差分を解消
	if draftAbs, _, ok := draftPath(root, rel); ok {
		_ = os.Remove(draftAbs)
	}
	// Rebuild and notify for immediate UX
	_ = Build(root)
	hub.notify()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- List markdown files (for sidebar) ----------
func handleList(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := filepath.Join(root, "content")
	type item struct{ Path, Title string }
	var out []item
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			rel, _ := filepath.Rel(root, p)
			title := info.Name()
			// Best-effort: YAML frontmatter の title を先頭数行から拾う
			if b, err := os.ReadFile(p); err == nil {
				s := string(b)
				if strings.HasPrefix(s, "---") {
					if i := strings.Index(s, "\n---"); i > 0 {
						fm := s[:i]
						for _, ln := range strings.Split(fm, "\n") {
							if strings.HasPrefix(strings.TrimSpace(ln), "title:") {
								title = strings.TrimSpace(strings.TrimPrefix(ln, "title:"))
								title = strings.Trim(title, `"' `)
								break
							}
						}
					}
				}
			}
			out = append(out, item{Path: filepath.ToSlash(rel), Title: title})
		}
		return nil
	})
	// sort by path
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
