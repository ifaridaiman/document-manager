// Compiled (not run) by the smoke test: a TypeScript consumer must get the package's
// types through its export map with Node's module resolution.
import { PdfEngineError, createEngine, engine, type CaptureResult, type PdfEngine } from "@documentmanager/pdf-engine";

const custom: PdfEngine = createEngine({ workers: 1, timeout: "10s" });
const pending: Promise<CaptureResult> = engine.capture(new Uint8Array(), { page: 1, x: 0, y: 0, width: 1, height: 1 });

// @ts-expect-error page is required
void engine.render(new Uint8Array(), {});

export function codeOf(e: unknown): string | undefined {
  return e instanceof PdfEngineError ? e.code : undefined;
}
export { custom, pending };
