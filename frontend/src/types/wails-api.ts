// Wails API型定義

export interface FileItem {
    path: string;
    title?: string;
    [key: string]: unknown;
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
    LoadFile(path: string): Promise<string>;
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
    SaveCsvFile(path: string, data: string[][]): Promise<boolean>;
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
