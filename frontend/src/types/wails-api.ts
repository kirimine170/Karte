// Wails API型定義

export interface FileItem {
    path: string;
    title?: string;
    modTime?: string;
    size?: number;
    // Legacy callers may still provide this field，but GetFileList no longer emits it．
    searchText?: string;
    [key: string]: unknown;
}

export interface FileSearchResult {
    items: FileItem[];
    page: number;
    limit: number;
    total: number;
    hasMore: boolean;
}

export type ResourceKind = 'markdown' | 'pdf' | 'image' | 'csv';

export interface ResourceSearchRequest {
    query: string;
    kinds: ResourceKind[];
    excludePaths?: string[];
    page: number;
    limit: number;
}

export interface ResourceSearchMetadata {
    name: string;
    extension: string;
    size: number;
    modTime: string;
}

export interface ResourceSearchItem {
    kind: ResourceKind;
    path: string;
    title: string;
    metadata: ResourceSearchMetadata;
}

export interface ResourceSearchResult {
    items: ResourceSearchItem[];
    query: string;
    kinds: ResourceKind[];
    page: number;
    limit: number;
    total: number;
    hasMore: boolean;
}

export interface GraphNode {
    id: string;
    label: string;
    kind: string;
    exists: boolean;
    degIn: number;
    degOut: number;
    tags: string[];
    [key: string]: unknown;
}

export interface GraphEdge {
    id: string;
    source: string;
    target: string;
    kind: string;
    weight: number;
    [key: string]: unknown;
}

export interface GraphData {
    nodes: GraphNode[];
    edges: GraphEdge[];
    meta: {
        directed?: boolean;
        [key: string]: unknown;
    };
}

export interface ASRStatus {
    initialized: boolean;
    initializing: boolean;
}

export type MediaImportKind = 'audio' | 'image' | 'pdf' | 'csv';

export interface MediaImportSession {
    id: string;
    chunkSize: number;
    maxBytes: number;
}

export interface ImageInfo {
    path: string;
    name: string;
    size: number;
    modTime: string;
}

export interface CsvInfo {
    path: string;
    name: string;
    size: number;
    modTime: string;
}

export interface CsvPageRequest {
    path: string;
    page: number;
    limit: number;
}

export interface CsvPageResult {
    path: string;
    header: string[];
    rows: string[][];
    page: number;
    limit: number;
    totalRows: number;
    hasMore: boolean;
    revision: string;
    // Frontend compatibility marker．The current backend never emits it．
    legacy?: boolean;
}

export interface CsvSavePageRequest {
    path: string;
    revision: string;
    page: number;
    limit: number;
    header: string[];
    rows: string[][];
}

export interface CsvSaveResult {
    path: string;
    revision: string;
    totalRows: number;
}

export interface BoardCardLayout {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface BoardViewport {
    x: number;
    y: number;
    zoom: number;
}

export interface BoardCard {
    id: string;
    type: string;
    title: string;
    source?: string;
    tags?: string[];
    createdBy?: string;
    updatedBy?: string;
    reviewed?: boolean;
    reviewedBy?: string;
    model?: string;
    body: string;
    meta?: Record<string, unknown>;
}

export interface BoardEdgeRecord {
    id: string;
    from: string;
    to: string;
    relation: string;
    label?: string;
    description?: string;
}

export interface BoardDocument {
    path: string;
    title: string;
    docId: string;
    type: string;
    version: number;
    created: string;
    updated: string;
    tags: string[];
    cards: BoardCard[];
    edges: BoardEdgeRecord[];
    layout: {
        cards: Record<string, BoardCardLayout>;
        viewport: BoardViewport;
    };
    notes: string;
    rawContent: string;
}

export type ConflictResolutionStrategy = 'local' | 'remote' | 'merge';

export type ClipImageMode = 'download' | 'link' | 'none';

export interface ClipRequest {
    url: string;
    mode: 'article';
    imageMode: ClipImageMode;
    outputDir?: string;
}

export interface ClipResult {
    markdownPath: string;
    assetDir: string;
    title: string;
    sourceUrl: string;
    warnings: string[];
}

// Wails App API
export interface WailsAppAPI {
    GetFileList(): Promise<FileItem[]>;
    SearchFiles?(query: string, page: number, limit: number): Promise<FileSearchResult>;
    SearchResources?(request: ResourceSearchRequest): Promise<ResourceSearchResult>;
    LoadFile(path: string): Promise<string>;
    LoadBoard(path: string): Promise<BoardDocument>;
    SaveBoard(path: string, board: BoardDocument): Promise<BoardDocument>;
    CreateBoardForResource(path: string): Promise<BoardDocument>;
    GetBoardResourceCandidates(boardPath: string): Promise<FileItem[]>;
    SaveFile(path: string, content: string): Promise<boolean>;
    PreviewMarkdown(content: string): Promise<string>;
    PreviewMarkdownForPath?(path: string, content: string): Promise<string>;
    GetGraphData(): Promise<GraphData>;
    CreateNewFile(filename: string): Promise<boolean>;
    ExportPDF(html: string): Promise<string>;
    ExportPreviewHTML(html: string): Promise<string>;
    GetCustomCSS(): Promise<string>;
    SetCustomCSS(css: string): Promise<boolean>;
    ClearCustomCSS(): Promise<boolean>;
    ResolveConflict(path: string, strategy: ConflictResolutionStrategy): Promise<boolean>;
    ImportAudioFile(path: string): Promise<string>;
    ImportAudioBase64(name: string, data: string): Promise<string>;
    ImportImageFile(path: string): Promise<string>;
    ImportImageBase64(name: string, data: string): Promise<string>;
    ImportPdfFile(path: string): Promise<string>;
    ImportPdfBase64(name: string, data: string): Promise<string>;
    BeginMediaImport?(kind: MediaImportKind, filename: string, declaredSize: number): Promise<MediaImportSession>;
    AppendMediaImportChunk?(sessionId: string, expectedOffset: number, encodedChunk: string): Promise<number>;
    FinishMediaImport?(sessionId: string): Promise<string>;
    AbortMediaImport?(sessionId: string): Promise<void>;
    GetASRStatus(): Promise<ASRStatus>;
    GetAudioFileURL(audioPath: string): Promise<string>;
    GetImageFileURL(imagePath: string): Promise<string>;
    GetPdfFileURL(pdfPath: string): Promise<string>;
    GetImageList(): Promise<ImageInfo[]>;
    GetImageMetadata(path: string): Promise<string>;
    SaveImageMetadata(path: string, yaml: string): Promise<boolean>;
    GetImageSystemMetadata(path: string): Promise<string>;
    SaveImageSystemMetadata(path: string, yaml: string): Promise<boolean>;
    StartRecording(): Promise<boolean>;
    StopRecording(): Promise<string>;
    IsRecording(): Promise<boolean>;
    LogJS(level: string, msg: string): Promise<void>;
    RenamePdfFile?(oldPath: string, newPath: string): Promise<boolean>;
    RenameFile?(oldPath: string, newPath: string): Promise<boolean>;
    UpdateLinkToLatest?(sourceDocID: string, targetDocID: string): Promise<boolean>;
    CaptureScreenInteractive(): Promise<string>;
    AllowClose(): Promise<boolean>;
    GetCsvList(): Promise<CsvInfo[]>;
    GetCsvFile(path: string): Promise<string[][]>;
    SaveCsvFile(path: string, data: string[][]): Promise<boolean | void>;
    GetCsvPage?(request: CsvPageRequest): Promise<CsvPageResult>;
    SaveCsvPage?(request: CsvSavePageRequest): Promise<CsvSaveResult>;
    ImportCsvFile(src: string): Promise<string>;
    ImportCsvBase64(filename: string, base64Data: string): Promise<string>;
    SaveEventLogs(logsJson: string): Promise<boolean>;
    ClipURL(request: ClipRequest): Promise<ClipResult>;
}

// Wails Runtime API
export interface WailsRuntimeAPI {
    EventsOn(eventName: string, callback: (...args: unknown[]) => void): () => void;
    BrowserOpenURL(url: string): void;
}
