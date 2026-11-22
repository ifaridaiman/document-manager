# Document-Manager

A stateless PDF engine for showing **visual evidence** next to AI answers.

Give it a PDF and it can inspect the document, extract page text, render pages and crop any region of a page into an image, with a reference that says exactly where the image came from. It is built so chatbots and LLM agents can answer a question _and show the part of the document that supports it_.

- **Stateless.** Nothing is stored. Documents live in memory for one call and are gone.
- **Sandboxed.** PDFium runs as WebAssembly with no filesystem access. A malicious PDF cannot touch the host.
- **No native dependencies.** Pure Go, one static binary, no cgo, no system libraries.
- **Use it four ways:** Go library, CLI, HTTP server, or npm package for Node.js / Next.js.

```text
"What was revenue growth last year?"
   → extract text → find page 14 → capture region (x 0.12, y 0.25, w 0.76, h 0.32)
   → PNG of that region + { page: 14, region, excerpt: "Revenue increased by 18%..." }
```

## Contents

- [How it works](#how-it-works)
- [Quick start](#quick-start)
- [Go library](#go-library)
- [CLI](#cli)
- [HTTP API](#http-api)
- [npm package](#npm-package-nodejs--nextjs)
- [Coordinates](#coordinates)
- [Errors](#errors)
- [Configuration and limits](#configuration-and-limits)
- [Security](#security)
- [Development](#development)
- [Roadmap](#roadmap)
- [License](#license)

## How it works

```text
            Go library ─┐
                 CLI ───┤
 HTTP API (pdf-engine serve) ──► documentengine ──► PDFium (WebAssembly, via wazero)
 npm package ─► spawns serve ┘
```

All PDF logic lives in one Go package, [`documentengine`](documentengine/). The CLI, the HTTP server and the npm package are thin adapters over it; none of them renders or crops anything on its own.

PDFium comes from [go-pdfium](https://github.com/klippa-app/go-pdfium) in WebAssembly mode. Regions are rendered directly by PDFium (only the requested area is rasterized), so a small capture from a large page stays fast.

## Quick start

Requires Go 1.27 or newer.

```bash
git clone https://github.com/ifaridaiman/document-manager.git
cd document-manager
go run ./cmd/pdf-engine inspect report.pdf
go run ./cmd/pdf-engine capture report.pdf --page 1 --x 0 --y 0 --width 1 --height 0.2 --dpi 200 --output evidence.png
```

## Go library

```bash
go get github.com/ifaridaiman/document-manager
```

```go
package main

import (
	"context"
	"fmt"
	"os"

	de "github.com/ifaridaiman/document-manager/documentengine"
)

func main() {
	ctx := context.Background()

	engine, err := de.New(de.Config{}) // zero values use the defaults
	if err != nil {
		panic(err)
	}
	defer engine.Close()

	data, _ := os.ReadFile("annual-report.pdf")
	doc, err := engine.OpenBytes(ctx, "annual-report.pdf", data)
	if err != nil {
		panic(err) // *de.Error with a Code such as INVALID_PDF
	}
	defer doc.Close()

	meta, _ := doc.Inspect()
	fmt.Println(meta.PageCount, meta.Title)

	text, _ := doc.ExtractText(ctx, 14)
	fmt.Println(text)

	capture, err := doc.CaptureRegion(ctx, de.CaptureRequest{
		PageNumber: 14,
		Region:     de.Region{X: 0.12, Y: 0.25, Width: 0.76, Height: 0.32},
		DPI:        300,
		Format:     de.PNG,
	})
	if err != nil {
		panic(err)
	}
	os.WriteFile("evidence.png", capture.Image, 0o644)
	fmt.Printf("%+v\n", capture.Reference) // page, region, excerpt, image size, dpi
}
```

Create one `Engine` per process and share it. It is safe for concurrent use; each open `Document` holds one worker until you `Close` it.

| Method                                   | Returns                                                                        |
| ---------------------------------------- | ------------------------------------------------------------------------------ |
| `engine.OpenBytes(ctx, filename, data)`  | `*Document`, after validating the file                                         |
| `doc.Inspect()`                          | Title, author, subject, creator, producer, dates, page count, encryption, size |
| `doc.GetPageInfo(ctx, page)`             | Width and height in points, rotation, whether the page has a text layer        |
| `doc.ExtractText(ctx, page)`             | Plain text of one page (`""` for scanned pages)                                |
| `doc.Layout(ctx, page)`                  | Text lines with exact normalized boxes (empty for scanned pages)               |
| `doc.RenderPage(ctx, RenderRequest)`     | Whole page as PNG or JPEG                                                      |
| `doc.CaptureRegion(ctx, CaptureRequest)` | Cropped image + `EvidenceReference` (including the text inside the region)     |

Image options (`ImageOptions`, on both render and capture): `Grayscale`, `Rotate` (90/180/270), `MaxWidth` (downscale), `Quality` (JPEG). Page numbers are **1-based**.

## CLI

```bash
go install github.com/ifaridaiman/document-manager/cmd/pdf-engine@latest
```

```bash
pdf-engine inspect report.pdf
pdf-engine text    report.pdf --page 14 [--json]
pdf-engine layout  report.pdf --page 14
pdf-engine render  report.pdf --page 14 --dpi 150 --output page-14.png
pdf-engine capture report.pdf --page 14 --x 0.12 --y 0.25 --width 0.76 --height 0.32 \
                   --dpi 300 --format png --output evidence.png
pdf-engine serve   --addr :8080
```

`render` and `capture` also take `--grayscale`, `--rotate 90` and `--max-width PX`. Results are printed as JSON; errors go to stderr as JSON with a non-zero exit code.

## HTTP API

```bash
pdf-engine serve --addr :8080 --max-mb 100 --workers 4 --timeout 30s
```

Every endpoint takes `multipart/form-data` with the PDF in a `file` field. The server keeps nothing between requests.

| Endpoint           | Fields                                                                      | Response                                                        |
| ------------------ | --------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `POST /v1/inspect` | `file`                                                                      | `{ document: { id, filename, size, pageCount, metadata } }`     |
| `POST /v1/text`    | `file`, `page` or `pages=3-7`                                               | `{ pages: [{ pageNumber, text }] }`                             |
| `POST /v1/layout`  | `file`, `page` or `pages=3-7`                                               | `{ pages: [{ pageNumber, width, height, rotation, lines: [{ index, text, region }] }] }` |
| `POST /v1/render`  | `file`, `page`, `dpi`, `format`, image options                              | Image bytes; size in `X-Image-Width` / `X-Image-Height` headers |
| `POST /v1/capture` | `file`, `page`, `x`, `y`, `width`, `height`, `dpi`, `format`, image options | `{ reference, image }` (image is base64)                        |
| `GET /healthz`     |                                                                             | `ok`                                                            |

Image options as form fields: `grayscale=true`, `rotate=90`, `maxWidth=800`, `quality=85`.

```bash
curl -F file=@report.pdf -F page=14 -F x=0.12 -F y=0.25 -F width=0.76 -F height=0.32 -F dpi=300 \
  http://localhost:8080/v1/capture
```

```json
{
  "reference": {
    "documentId": "X2BRQKDYXEUC6LVJTLPJXUQZLF",
    "filename": "report.pdf",
    "pageNumber": 14,
    "region": { "x": 0.12, "y": 0.25, "width": 0.76, "height": 0.32 },
    "excerpt": "Revenue increased by 18% compared to the previous year.",
    "mimeType": "image/png",
    "imageWidth": 1938,
    "imageHeight": 1056,
    "dpi": 300,
    "capturedAt": "2026-11-21T11:22:24Z"
  },
  "image": "iVBORw0KGgo..."
}
```

Each request is logged as one structured JSON line (request ID, operation, duration, file size, page, DPI, output size, error code). Document text and image data are never logged. Send `X-Request-ID` to correlate logs with your own.

The server has **no authentication**. Run it on a private network or behind your own gateway.

## npm package (Node.js / Next.js)

[`@documentmanager/pdf-engine`](https://www.npmjs.com/package/@documentmanager/pdf-engine) ships the compiled engine with a small typed wrapper. npm installs only the binary for your platform (macOS arm64/x64, Linux x64/arm64, Windows x64). On first use it starts one `pdf-engine serve` process on a random `127.0.0.1` port and reuses it; the process exits automatically when Node exits.

```bash
npm install @documentmanager/pdf-engine
```

```js
import { engine, PdfEngineError } from "@documentmanager/pdf-engine";

const pdf = await fs.promises.readFile("report.pdf");

const doc = await engine.inspect(pdf, { filename: "report.pdf" });
const pages = await engine.text(pdf, { pages: "1-5" });
const [layout] = await engine.layout(pdf, { page: 14 }); // lines: [{ index, text, region }]
const { image, reference } = await engine.capture(pdf, {
  page: 14,
  x: 0.12,
  y: 0.25,
  width: 0.76,
  height: 0.32,
  dpi: 300,
});

try {
  await engine.capture(pdf, { page: 99, x: 0, y: 0, width: 1, height: 1 });
} catch (e) {
  if (e instanceof PdfEngineError) console.log(e.code, e.status); // PAGE_NOT_FOUND 422
}
```

- Server-side only: Node route handlers and server actions, not the browser or Edge runtime.
- In Next.js, add `serverExternalPackages: ["@documentmanager/pdf-engine"]` to `next.config`.
- Set `PDF_ENGINE_BIN` to use your own binary, or `createEngine({ maxMb, workers, timeout, logs })` for a custom instance.
- TypeScript types are included.

## Coordinates

Regions are **normalized**: every value is between `0` and `1`, measured from the **top-left** of the page **as displayed**, so rotated pages need no special handling.

```json
{ "x": 0.12, "y": 0.25, "width": 0.76, "height": 0.32 }
```

This region starts 12% from the left and 25% from the top, and covers 76% of the page width and 32% of its height. A region is valid when `x, y ≥ 0`, `width, height > 0`, `x + width ≤ 1` and `y + height ≤ 1`.

Normalized coordinates do not depend on DPI or screen size. To draw a highlight in a browser, position a box at `left: x%`, `top: y%`, `width: w%`, `height: h%` over the rendered page image.

## Errors

Every failure is a structured error with a stable code:

```json
{
  "error": {
    "code": "PAGE_NOT_FOUND",
    "message": "page not found",
    "details": { "pageNumber": 9, "pageCount": 1 }
  }
}
```

| Code                                                                          | HTTP | Meaning                                                   |
| ----------------------------------------------------------------------------- | ---- | --------------------------------------------------------- |
| `INVALID_REQUEST`                                                             | 400  | Missing or malformed field                                |
| `INVALID_REGION`                                                              | 400  | Region outside 0–1 or zero-sized                          |
| `FILE_TOO_LARGE`                                                              | 413  | Upload over the size limit                                |
| `INVALID_PDF`                                                                 | 422  | No `%PDF-` signature, or the file is corrupt              |
| `PDF_ENCRYPTED`                                                               | 422  | Needs a password                                          |
| `PDF_UNSUPPORTED`                                                             | 422  | Unsupported security handler or unreadable page           |
| `PAGE_NOT_FOUND`                                                              | 422  | Page number outside the document                          |
| `RESOURCE_LIMIT`                                                              | 422  | Too many pages, or the image would exceed the pixel limit |
| `PROCESSING_TIMEOUT`                                                          | 504  | Operation took longer than the timeout                    |
| `ENGINE_BUSY`                                                                 | 503  | Every PDF worker is in use; retry after `Retry-After`     |
| `RENDER_FAILED`, `CAPTURE_FAILED`, `TEXT_EXTRACTION_FAILED`, `INTERNAL_ERROR` | 500  | Processing failed                                         |

## Configuration and limits

| Setting               | Default    | Go `Config`   | CLI flag           |
| --------------------- | ---------- | ------------- | ------------------ |
| Max file size         | 100 MiB    | `MaxFileSize` | `--max-mb`         |
| Max pages             | 5000       | `MaxPages`    |                    |
| Default DPI           | 150        | `DefaultDPI`  | `--dpi` (per call) |
| Max DPI               | 600        | `MaxDPI`      |                    |
| Max pixels per image  | 60,000,000 | `MaxPixels`   |                    |
| Timeout per operation | 30 s       | `Timeout`     | `--timeout`        |
| Concurrent documents  | 4          | `Workers`     | `--workers`        |

## Security

PDFs are treated as untrusted input:

- PDFium runs inside a WebAssembly sandbox with **no filesystem mounted**; it only sees the bytes it is given.
- Files are checked by signature and parsed by PDFium, never trusted by extension.
- Size, page count, DPI and output pixel count are capped before rendering starts.
- An operation that exceeds the timeout has its worker killed, so a hostile PDF cannot keep consuming CPU.
- Uploaded filenames are display metadata only and are never used as paths. Multipart temp files are removed after each request.
- Document text, images and metadata are never logged.

The HTTP server has no authentication and no rate limiting; put it behind something that does before exposing it.

Found a vulnerability? Please report it privately through [GitHub security advisories](https://github.com/ifaridaiman/document-manager/security/advisories/new) instead of opening a public issue.

## Development

```text
documentengine/   core engine (the only package that touches PDFium)
httpapi/          HTTP adapter
cmd/pdf-engine/   CLI and server entry point
npm/pdf-engine/   npm wrapper, binary build and release scripts
```

```bash
go test ./...   # engine tests, using PDFs generated in the test
go vet ./...
cd npm/pdf-engine && npm run build:local && npm test
```

Releases are published by GitHub Actions ([release.yml](.github/workflows/release.yml)) when a `v*` tag is pushed.

Contributions are welcome. Please open an issue to discuss larger changes first, keep PDF logic inside `documentengine`, and add a test for any new behavior.

## Roadmap

- `pages`: size, rotation and text availability of every page in one call
- OCR for scanned pages
- MCP server adapter for LLM agents

## License

[MIT](LICENSE)

Third-party components keep their own licenses, including [go-pdfium](https://github.com/klippa-app/go-pdfium) and [PDFium](https://pdfium.googlesource.com/pdfium/).
