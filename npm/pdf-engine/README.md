# @documentmanager/pdf-engine

Inspect PDFs, extract page text, render pages and capture any region of a page as an image, from Node.js. Built for showing visual evidence next to AI answers.

It wraps the Go [document-manager](https://github.com/ifaridaiman/document-manager) engine (PDFium, sandboxed in WebAssembly). npm installs only the binary for your platform: macOS (arm64, x64), Linux (x64, arm64) or Windows (x64).

```bash
npm install @documentmanager/pdf-engine
```

```js
import { readFile } from "node:fs/promises";
import { engine, PdfEngineError } from "@documentmanager/pdf-engine";

const pdf = await readFile("report.pdf");

const doc = await engine.inspect(pdf, { filename: "report.pdf" }); // { pageCount, metadata, ... }
const [page] = await engine.text(pdf, { page: 14 });               // { pageNumber, text }
const { image, reference } = await engine.capture(pdf, {
  page: 14, x: 0.12, y: 0.25, width: 0.76, height: 0.32, dpi: 300,
});
// image: PNG Buffer; reference: { pageNumber, region, excerpt, imageWidth, imageHeight, dpi, ... }
```

Regions are normalized (0–1) from the top-left of the page as displayed. Errors are `PdfEngineError` with a stable `code` (`INVALID_PDF`, `PAGE_NOT_FOUND`, `INVALID_REGION`, ...) and the HTTP-style `status`.

## Notes

- Server-side only (Node.js). Not for the browser or the Edge runtime.
- The first call starts one engine process on a private `127.0.0.1` port; it exits with your Node process.
- Next.js: add `serverExternalPackages: ["@documentmanager/pdf-engine"]` to `next.config`.
- `createEngine({ maxMb, workers, timeout, logs })` for custom limits; `PDF_ENGINE_BIN` to use your own binary.

Full documentation, the HTTP API and the Go library: [github.com/ifaridaiman/document-manager](https://github.com/ifaridaiman/document-manager).

MIT licensed.
