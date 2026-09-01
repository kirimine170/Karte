// Wails API型定義

export interface FileItem {
    path: string;
    title?: string;
    modTime?: string;
    searchText?: string;
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

export interface EphySourceRef {
    type: string;
    reference: string;
    sha256?: string;
}

export interface EphyProposal {
    schema_version: '1.1';
    candidate_id: string;
    operation: 'create' | 'append';
    target_doc_id: string | null;
    target_relative_path: string | null;
    base_sha256: string | null;
    append_position: 'document_end' | null;
    proposed_frontmatter: Record<string, unknown>;
    proposed_body: string;
    placement: {
        project: string;
        kind: 'note' | 'meeting' | 'decision' | 'plan' | 'task' | 'research' | 'reference' | 'report' | 'person' | 'organization' | 'journal';
        year_month: string;
        confidence: number;
        preferred_filename: string;
        candidates: Array<{ project: string; kind: string; confidence: number; reason: string }>;
        consultation_required: boolean;
        consultation_question: string | null;
    };
    source_refs: EphySourceRef[];
    sensitivity: 'public' | 'internal' | 'confidential' | 'restricted';
    created_at: string;
}

export interface EphyProposalReview {
    proposal: EphyProposal;
    current_content: string;
    proposed_content: string;
    diff: string;
    current_sha256: string | null;
    resolved_doc_id: string;
    resolved_relative_path: string;
    routing_reason: string;
    placement_alternatives: string[];
    content_warnings: string[];
}

export interface EphyProposalError {
    filename: string;
    candidate_id?: string;
    code: string;
    message: string;
}

export interface EphyInbox {
    proposals: EphyProposalReview[];
    errors: EphyProposalError[];
}

export interface EphyReceipt {
    schema_version: '1.1';
    candidate_id: string;
    result: 'accepted' | 'rejected' | 'conflict' | 'invalid';
    doc_id: string | null;
    relative_path: string | null;
    resulting_sha256: string | null;
    processed_at: string;
    error_code: string | null;
    message: string | null;
}

// Wails App API
export interface WailsAppAPI {
    GetFileList(): Promise<FileItem[]>;
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
    ListEphyProposals(): Promise<EphyInbox>;
    AcceptEphyProposal(candidateId: string, editedFrontmatter: Record<string, unknown>, editedBody: string): Promise<EphyReceipt>;
    RejectEphyProposal(candidateId: string, message: string): Promise<EphyReceipt>;
}

// Wails Runtime API
export interface WailsRuntimeAPI {
    EventsOn(eventName: string, callback: (...args: unknown[]) => void): () => void;
    BrowserOpenURL(url: string): void;
}
