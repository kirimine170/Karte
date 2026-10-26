// Browser-only virtual workspace for developing the native host UI．It never accesses
// the real Karte data directory．Native builds call the Go APIs instead．
export function researchWallPreview(api: any): any {
    const key = 'karte.research-wall.preview.v1';
    let state: any;
    try { state = JSON.parse(localStorage.getItem(key) || 'null'); } catch { /* empty preview */ }
    state ||= {boards:{},documents:{},media:{},metadata:{}};
    const persist = () => localStorage.setItem(key, JSON.stringify(state));
    const fileList = api.GetFileList.bind(api), loadFile = api.LoadFile.bind(api), loadBoard = api.LoadBoard.bind(api);
    api.GetFileList = async () => {
        const all = await fileList();
        for (const [path, value] of Object.entries({...state.documents,...state.boards,...state.media}) as [string,any][]) {
            if (!path.startsWith('content/')) continue;
            const index = all.findIndex((f: any) => f.path === path);
            const item = {path,title:typeof value === 'string'?path.split('/').pop():value.title,modTime:new Date().toISOString()};
            if (index >= 0) all[index] = item; else all.push(item);
        }
        return all;
    };
    api.LoadFile = async (path: string) => state.documents[path] ?? loadFile(path);
    api.SaveFile = async (path: string, content: string) => { state.documents[path] = content; persist(); return true; };
    api.LoadBoard = async (path: string) => structuredClone(state.boards[path] || await loadBoard(path));
    api.SaveBoard = async (path: string, board: any) => { state.boards[path] = structuredClone({...board,path}); persist(); return structuredClone(state.boards[path]); };
    api.CreateResearchBoard = async (title: string) => {
        const path = `content/research-${crypto.randomUUID()}.board.md`;
        return api.SaveBoard(path,{path,title,docId:`board:${crypto.randomUUID()}`,type:'karte-board',version:1,created:'',updated:'',tags:[],cards:[],edges:[],layout:{cards:{},viewport:{x:0,y:0,zoom:1}},notes:'',rawContent:''});
    };
    api.ListResearchResources = async () => {
        const files = (await api.GetFileList()).filter((f: any) => !f.path.endsWith('.board.md') && !state.media[f.path]);
        return [...files.map((f: any) => ({...f,kind:'markdown'})),...Object.values(state.media)];
    };
    api.LoadResearchResource = async (path: string) => {
        const media = state.media[path];
        if (media?.kind === 'audio') {
            const transcript = `${path.replace(/^data\/audio\//,'content/')}.md`;
            return {...media,body:state.documents[transcript] || '',editPath:state.documents[transcript]?transcript:''};
        }
        if (media) return {...media,body:state.metadata[path] || media.body};
        const body = await api.LoadFile(path);
        return {path,title:path.split('/').pop(),kind:'markdown',body,url:'',editPath:path,revision:body};
    };
    api.ImportResearchResource = async (filename: string, encoded: string) => {
        const ext = filename.split('.').pop()?.toLowerCase();
        const kind = ext === 'pdf'?'pdf':['png','jpg','jpeg','gif','webp'].includes(ext || '')?'image':['md','txt','markdown'].includes(ext || '')?'markdown':'audio';
        const path = `${kind === 'markdown' || kind === 'pdf'?'content':`data/${kind === 'image'?'image':'audio'}`}/${crypto.randomUUID()}-${filename}`;
        if (kind === 'markdown') {
            const body = new TextDecoder().decode(Uint8Array.from(atob(encoded), char => char.charCodeAt(0)));
            await api.SaveFile(path, body); return api.LoadResearchResource(path);
        }
        const mime = kind === 'pdf'?'application/pdf':kind === 'image'?`image/${ext === 'jpg'?'jpeg':ext}`:`audio/${ext === 'mp3'?'mpeg':ext}`;
        state.media[path] = {path,title:filename,kind,body:'',url:`data:${mime};base64,${encoded}`,editPath:'',revision:crypto.randomUUID()}; persist(); return state.media[path];
    };
    api.EditResearchResource = async (path: string) => {
        if (state.media[path]?.kind === 'audio') {
            const transcript = `${path.replace(/^data\/audio\//,'content/')}.md`;
            if (!state.documents[transcript]) await api.SaveFile(transcript, `---\naudio_path: ${path}\n---\n\n# 音声メモ\n`);
            return transcript;
        }
        return path;
    };
    api.GetImageMetadata = async (path: string) => state.metadata[path] || '';
    api.SaveImageMetadata = async (path: string, text: string) => { state.metadata[path] = text; persist(); return true; };
    api.GetImageFileURL = async (path: string) => state.media[path]?.url || path;
    api.GetAudioFileURL = async (path: string) => state.media[path]?.url || path;
    api.GetPdfFileURL = async (path: string) => state.media[path]?.url || path;
    api.SaveResearchBoardExport = async (filename: string, content: string) => {
        const url = URL.createObjectURL(new Blob([content], {type:'application/json'}));
        const anchor = document.createElement('a'); anchor.href = url; anchor.download = filename; anchor.click();
        setTimeout(() => URL.revokeObjectURL(url), 1000); return filename;
    };
    return api;
}
