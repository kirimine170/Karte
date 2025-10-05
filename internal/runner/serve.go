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

// path=content/xxx.md -> preview via /__preview?path=content/xxx.md
const previewURL = '/__preview?path=' + encodeURIComponent(path);
openBtn.href = '/' + path.replace(/^content\//,'').replace(/\.md$/, '.html'); // 公開用
pv.src = previewURL;

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
  const theme = document.documentElement.getAttribute('data-theme') || 'light';
  pv.src = '/__preview?path=' + encodeURIComponent(path) + '&theme=' + theme;
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
          const src = pv.src; pv.src = src; // 即時再読込
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
	html, _, err := site.RenderMarkdown(root, srcPath)
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
