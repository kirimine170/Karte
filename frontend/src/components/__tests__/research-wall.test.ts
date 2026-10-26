import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { ResearchWall } from '../research-wall';
import { useBoardStore, useDocStore, useUIStore } from '../../stores/index';
import type { BoardDocument, WailsAppAPI, WailsRuntimeAPI } from '../../types/wails-api';

const original: BoardDocument = {path:'content/study.board.md',title:'研究',docId:'board:study',type:'karte-board',version:1,created:'2026-10-26',updated:'2026-10-26',tags:['research'],notes:'既存ノート',rawContent:'original',cards:[{id:'md',type:'resource',title:'メモ',source:'content/note.md',body:'old',meta:{custom:'preserved'}}],edges:[],layout:{cards:{md:{x:0,y:0,width:280,height:330}},viewport:{x:0,y:0,zoom:1}}};
const wall = {schema:'karte-research-wall/1',managedPath:original.path,title:'更新した研究',cards:[{id:'md',kind:'markdown',source:'content/note.md',title:'メモ',body:'actual text',x:10,y:20,w:280,h:330}],edges:[]};
let component: ResearchWall;
let api: WailsAppAPI;
let frame: HTMLIFrameElement;
function message(operation: string, value?: unknown, origin = location.origin) {
    window.dispatchEvent(new MessageEvent('message',{source:frame.contentWindow,origin,data:{channel:'karte.research-wall.v1',id:crypto.randomUUID(),operation,value}}));
}
beforeEach(() => {
    document.body.innerHTML = '<button id="saveBtn">保存</button><div id="board-tab"></div>';
    useBoardStore.setState({board:structuredClone(original)});
    useDocStore.setState({currentPath:original.path,markdownContent:'original',hasUnsavedChanges:false,files:[]});
    useUIStore.setState({activeTab:'board'});
    api = {
        GetFileList: vi.fn().mockResolvedValue([]),
        SaveBoard: vi.fn().mockImplementation(async (path, doc) => ({...doc,path,rawContent:'saved'})),
        LoadResearchResource: vi.fn().mockResolvedValue({path:'content/note.md',title:'メモ',kind:'markdown',body:'actual text',url:'',editPath:'content/note.md',revision:'1'}),
    } as unknown as WailsAppAPI;
    component = new ResearchWall(api,{EventsOn: () => () => {}} as WailsRuntimeAPI);
    component.init(); frame = document.querySelector('iframe')!;
    message('load');
});
afterEach(() => { component.destroy(); vi.restoreAllMocks(); });

describe('ResearchWall native host', () => {
    it('keeps native identity and metadata when saving the renderer projection', async () => {
        message('save',wall);
        await vi.waitFor(() => expect(api.SaveBoard).toHaveBeenCalledOnce());
        const saved = vi.mocked(api.SaveBoard).mock.calls[0]![1];
        expect(saved.docId).toBe(original.docId); expect(saved.tags).toEqual(['research']);
        expect(saved.notes).toBe('既存ノート'); expect(saved.cards[0]!.meta?.custom).toBe('preserved');
        expect(saved.layout.cards.md?.x).toBe(10);
    });
    it('does not discard an unsaved source editor when the wall autosaves', async () => {
        useDocStore.setState({currentPath:'content/note.md',markdownContent:'unsaved edit',hasUnsavedChanges:true});
        // Returning to the board is allowed without discarding the editor buffer．
        message('save',wall);
        await vi.waitFor(() => expect(api.SaveBoard).toHaveBeenCalledOnce());
        await vi.waitFor(() => expect(useBoardStore.getState().board?.title).toBe(wall.title));
        expect(useDocStore.getState().currentPath).toBe('content/note.md');
        expect(useDocStore.getState().markdownContent).toBe('unsaved edit');
        expect(useDocStore.getState().hasUnsavedChanges).toBe(true);
    });
    it('saves a queued older board into its own path after a board switch', async () => {
        await vi.waitFor(() => expect(api.LoadResearchResource).toHaveBeenCalled());
        const newer = {...structuredClone(original),path:'content/newer.board.md',title:'別のボード'};
        useBoardStore.getState().setBoard(newer);useDocStore.getState().setCurrentPath(newer.path);
        message('save',wall);
        await vi.waitFor(() => expect(api.SaveBoard).toHaveBeenCalledOnce());
        expect(vi.mocked(api.SaveBoard).mock.calls[0]![0]).toBe(original.path);
        expect(useBoardStore.getState().board?.path).toBe(newer.path);
    });
    it('rejects operations from a different origin', async () => {
        message('save',wall,'https://example.invalid');
        await Promise.resolve(); await Promise.resolve();
        expect(api.SaveBoard).not.toHaveBeenCalled();
    });
});

 it('provides a route back from Karte PDF view to the wall', async () => {
    vi.mocked(api.LoadResearchResource).mockResolvedValue({path:'content/paper.pdf',title:'Paper',kind:'pdf',body:'',url:'/pdf/content/paper.pdf',editPath:'content/paper.pdf',revision:'1'});
    api.EditResearchResource=vi.fn().mockResolvedValue('content/paper.pdf');api.LoadFile=vi.fn().mockResolvedValue('');
    message('edit','content/paper.pdf');
    await vi.waitFor(()=>expect(useDocStore.getState().currentPath).toBe('content/paper.pdf'));
    expect(useUIStore.getState().activeTab).toBe('editor');
    const back=document.getElementById('returnToResearchWall') as HTMLButtonElement;
    expect(back.hidden).toBe(false);back.click();expect(useUIStore.getState().activeTab).toBe('board');
 });

it('restores image metadata through the existing managed image API', async () => {
    const image={path:'data/image/import.webp',title:'Sketch',kind:'image' as const,body:'',url:'/image/data/image/import.webp',editPath:'',revision:'1'};
    api.ImportResearchResource=vi.fn().mockResolvedValue(image);api.SaveImageMetadata=vi.fn().mockResolvedValue(true);
    vi.mocked(api.LoadResearchResource).mockResolvedValue(image);
    message('import',{filename:'sketch.png',data:'AA==',metadata:'tags: [research]\nnotes: 根拠を確認\n'});
    await vi.waitFor(()=>expect(api.SaveImageMetadata).toHaveBeenCalledWith(image.path,'tags: [research]\nnotes: 根拠を確認\n'));
});
it('restores portable audio notes while retaining the newly imported audio path', async () => {
    const audio={path:'data/audio/new.wav',title:'Audio',kind:'audio' as const,body:'',url:'/audio/data/audio/new.wav',editPath:'content/new-audio.md',revision:'1'};
    api.ImportResearchResource=vi.fn().mockResolvedValue(audio);api.EditResearchResource=vi.fn().mockResolvedValue(audio.editPath);
    const header='---\ndoc_id: new-id\naudio_path: data/audio/new.wav\n---\n';
    api.LoadFile=vi.fn().mockResolvedValue(`${header}\n# 音声メモ\n`);api.SaveFile=vi.fn().mockResolvedValue(true);
    vi.mocked(api.LoadResearchResource).mockResolvedValue(audio);
    message('import',{filename:'audio.wav',data:'AA==',transcript:'# 聞き取り\n研究メモを残す．'});
    await vi.waitFor(()=>expect(api.SaveFile).toHaveBeenCalledWith(audio.editPath,`${header}\n# 聞き取り\n研究メモを残す．`));
});
