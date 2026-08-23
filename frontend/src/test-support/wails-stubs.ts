// @ts-nocheck
export async function GetFileList() {
  return [];
}

export async function SearchFiles(_query, page, limit) {
  return { items: [], page, limit, total: 0, hasMore: false };
}

export async function SearchResources(request) {
  return {
    items: [],
    query: String(request?.query || '').trim().toLowerCase(),
    kinds: request?.kinds || [],
    page: request?.page || 1,
    limit: request?.limit || 50,
    total: 0,
    hasMore: false,
  };
}

export async function LoadFile() {
  return '';
}

export async function SaveFile() {
  return true;
}

export async function PreviewMarkdown(content) {
  return content;
}

export async function PreviewMarkdownForPath(_path, content) {
  return content;
}

export async function GetGraphData() {
  return { nodes: [], edges: [], meta: {} };
}

export async function CreateNewFile() {
  return true;
}

export async function ExportPDF() {
  return '';
}

export async function ExportPreviewHTML() {
  return '';
}

export async function GetCustomCSS() {
  return '';
}

export async function SetCustomCSS() {
  return '';
}

export async function ClearCustomCSS() {
  return true;
}

export async function ResolveConflict() {
  return true;
}

export async function BeginMediaImport(_kind, _filename, declaredSize) {
  return { id: 'stub-media-import', chunkSize: 256 * 1024, maxBytes: Math.max(declaredSize, 1) };
}

export async function AppendMediaImportChunk(_sessionId, expectedOffset, encodedChunk) {
  return expectedOffset + atob(encodedChunk).length;
}

export async function FinishMediaImport() {
  return '';
}

export async function AbortMediaImport() {}

export async function ImportAudioFile() {
  return '';
}

export async function ImportAudioBase64() {
  return '';
}

export async function ImportImageFile() {
  return '';
}

export async function ImportImageBase64() {
  return '';
}

export async function ImportPdfFile() {
  return '';
}

export async function ImportPdfBase64() {
  return '';
}

export async function ImportCsvFile() {
  return '';
}

export async function ImportCsvBase64() {
  return '';
}

export async function GetASRStatus() {
  return { initialized: false, initializing: false };
}

export async function GetAudioFileURL(path) {
  return path;
}

export async function GetImageFileURL(path) {
  return path;
}

export async function GetPdfFileURL(path) {
  return path;
}

export async function GetImageList() {
  return [];
}

export async function LoadBoard() {
  return null;
}

export async function SaveBoard(_path, board) {
  return board;
}

export async function CreateBoardForResource() {
  return null;
}

export async function GetBoardResourceCandidates() {
  return [];
}

export async function GetImageMetadata() {
  return '';
}

export async function SaveImageMetadata() {
  return true;
}

export async function GetImageSystemMetadata() {
  return '';
}

export async function SaveImageSystemMetadata() {
  return true;
}

export async function StartRecording() {
  return true;
}

export async function StopRecording() {
  return '';
}

export async function IsRecording() {
  return false;
}

export async function LogJS() {
  return true;
}

export async function RenamePdfFile() {
  return true;
}

export async function CaptureScreenInteractive() {
  return '';
}

export async function AllowClose() {
  return true;
}

export async function GetCsvList() {
  return [];
}

export async function GetCsvFile() {
  return [];
}

export async function SaveCsvFile() {
  return true;
}

export async function GetCsvPage(request) {
  return {
    path: request.path,
    header: ['column'],
    rows: [],
    page: request.page,
    limit: request.limit,
    totalRows: 0,
    hasMore: false,
    revision: 'stub-csv-revision',
  };
}

export async function SaveCsvPage(request) {
  return { path: request.path, revision: 'stub-csv-revision', totalRows: request.rows.length };
}

export async function SaveEventLogs(_logsJson?: string) {
  return true;
}
