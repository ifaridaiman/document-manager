export type PdfInput = Uint8Array | ArrayBuffer | Blob;

export interface EngineOptions {
  /** Path to the pdf-engine binary. Defaults to $PDF_ENGINE_BIN or the bundled one. */
  binaryPath?: string;
  /** Max upload size in MiB (default 100). */
  maxMb?: number;
  /** Concurrent documents (default 4). */
  workers?: number;
  /** Per-operation timeout as a Go duration, e.g. "30s" (default 30s). */
  timeout?: string;
  /** Forward the engine's JSON logs to stdout. */
  logs?: boolean;
}

interface CallOptions {
  filename?: string;
  signal?: AbortSignal;
}

export interface ImageOptions {
  /** Default 150. */
  dpi?: number;
  /** Default "png". */
  format?: "png" | "jpeg";
  grayscale?: boolean;
  /** Clockwise degrees: 0, 90, 180, 270. */
  rotate?: number;
  /** Downscale so output width <= maxWidth px. */
  maxWidth?: number;
  /** JPEG quality 1-100 (default 90). */
  quality?: number;
}

export interface DocumentMetadata {
  title?: string;
  author?: string;
  subject?: string;
  creator?: string;
  producer?: string;
  creationDate?: string;
  modifiedDate?: string;
  pageCount: number;
  encrypted: boolean;
  fileSize: number;
  mimeType: string;
}

export interface InspectResult {
  id: string;
  filename: string;
  size: number;
  pageCount: number;
  metadata: DocumentMetadata;
}

export interface PageText {
  pageNumber: number;
  text: string;
}

/** Normalized 0..1, top-left origin, relative to the page as displayed. */
export interface Region {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** One run of text as PDFium groups it (usually a line). */
export interface TextLine {
  /** 0-based position in the page's line list; stable for the same PDF. */
  index: number;
  text: string;
  /** Normalized box, ready to pass to capture(). */
  region: Region;
}

export interface PageLayout {
  pageNumber: number;
  /** Points, as displayed (rotation applied). */
  width: number;
  height: number;
  rotation: number;
  /** Empty for pages without a text layer (scanned pages). */
  lines: TextLine[];
}

export interface EvidenceReference {
  documentId: string;
  filename: string;
  pageNumber: number;
  region: Region;
  /** Text inside the region, if the page has a text layer. */
  excerpt?: string;
  mimeType: string;
  imageWidth: number;
  imageHeight: number;
  dpi: number;
  capturedAt: string;
}

export interface RenderResult {
  image: Buffer;
  mimeType: string;
  width: number;
  height: number;
  pageNumber: number;
  dpi: number;
}

export interface CaptureResult {
  image: Buffer;
  mimeType: string;
  width: number;
  height: number;
  reference: EvidenceReference;
}

export interface PdfEngine {
  inspect(pdf: PdfInput, opts?: CallOptions): Promise<InspectResult>;
  /** Pass `page` for one page or `pages: "3-7"` for a range. */
  text(pdf: PdfInput, opts: CallOptions & ({ page: number } | { pages: string })): Promise<PageText[]>;
  /** Text lines with exact boxes. Pass `page` for one page or `pages: "3-7"` for a range. */
  layout(pdf: PdfInput, opts: CallOptions & ({ page: number } | { pages: string })): Promise<PageLayout[]>;
  render(pdf: PdfInput, opts: CallOptions & ImageOptions & { page: number }): Promise<RenderResult>;
  capture(pdf: PdfInput, opts: CallOptions & ImageOptions & Region & { page: number }): Promise<CaptureResult>;
  /** Stop the engine process. The next call starts it again. */
  close(): void;
}

export class PdfEngineError extends Error {
  /** INVALID_PDF, PAGE_NOT_FOUND, INVALID_REGION, ... */
  code: string;
  details?: Record<string, unknown>;
  /** HTTP status the engine returned. */
  status: number;
}

export function createEngine(options?: EngineOptions): PdfEngine;

/** Shared lazily-started engine for the whole process. */
export const engine: PdfEngine;
