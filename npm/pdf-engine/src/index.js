// Node.js wrapper for the Go PDF engine.
// Lazily spawns one long-lived `pdf-engine serve` on a private localhost port and
// talks to it over the same HTTP API used everywhere else. Server-side only.
import { spawn } from "node:child_process";
import { accessSync, chmodSync, constants, existsSync } from "node:fs";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

// A global brand instead of class identity: bundlers, hot reload and duplicate
// installs can load this module twice, and the shared engine (globalThis) may
// throw errors built by the other copy's class. `instanceof` must still match.
const BRAND = Symbol.for("@documentmanager/pdf-engine/PdfEngineError");

export class PdfEngineError extends Error {
  static [Symbol.hasInstance](value) {
    return value?.[BRAND] === true;
  }

  constructor(code, message, details, status) {
    super(message);
    this[BRAND] = true;
    this.name = "PdfEngineError";
    this.code = code;
    this.details = details;
    this.status = status;
  }
}

// Binary lookup: $PDF_ENGINE_BIN, then the platform package npm installed
// (@documentmanager/pdf-engine-<platform>-<arch>), then a local dev build in ../bin.
function defaultBinary() {
  if (process.env.PDF_ENGINE_BIN) return process.env.PDF_ENGINE_BIN;
  const target = `${process.platform}-${process.arch}`;
  const exe = process.platform === "win32" ? "pdf-engine.exe" : "pdf-engine";
  let p;
  try {
    p = fileURLToPath(import.meta.resolve(`@documentmanager/pdf-engine-${target}/bin/${exe}`));
  } catch {}
  if (!p || !existsSync(p)) p = fileURLToPath(new URL(`../bin/${target}/${exe}`, import.meta.url));
  if (!existsSync(p)) {
    throw new Error(
      `pdf-engine: no binary for ${target}. Supported: darwin-arm64, darwin-x64, linux-x64, linux-arm64, win32-x64. ` +
        `Reinstall without --no-optional, or set PDF_ENGINE_BIN.`,
    );
  }
  if (process.platform !== "win32") {
    try {
      accessSync(p, constants.X_OK);
    } catch {
      try { chmodSync(p, 0o755); } catch {} // read-only filesystems: spawn will report the real error
    }
  }
  return p;
}

function freePort() {
  return new Promise((resolve, reject) => {
    const s = createServer();
    s.unref();
    s.on("error", reject);
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * @param {import("./index.d.ts").EngineOptions} [options]
 * @returns {import("./index.d.ts").PdfEngine}
 */
export function createEngine(options = {}) {
  let ready; // Promise<baseURL> while the child is alive
  let child;

  function start() {
    ready ??= (async () => {
      const port = await freePort(); // ponytail: tiny window between close and spawn; retry on failure is the upgrade path
      const args = ["serve", "--addr", `127.0.0.1:${port}`, "--parent-pipe"];
      if (options.maxMb) args.push("--max-mb", String(options.maxMb));
      if (options.workers) args.push("--workers", String(options.workers));
      if (options.timeout) args.push("--timeout", options.timeout);

      // stdin is a pipe we never write to: the engine exits when it closes (i.e. when we die).
      child = spawn(options.binaryPath ?? defaultBinary(), args, {
        stdio: ["pipe", options.logs ? "inherit" : "ignore", "inherit"],
      });
      child.unref();
      child.stdin.unref?.();
      let exited = false;
      child.once("error", () => { exited = true; });
      child.once("exit", () => {
        exited = true;
        ready = undefined; // next call respawns
      });

      const base = `http://127.0.0.1:${port}`;
      for (let i = 0; i < 150 && !exited; i++) {
        try {
          if ((await fetch(`${base}/healthz`)).ok) return base;
        } catch {}
        await sleep(100);
      }
      child.kill();
      ready = undefined;
      throw new Error("pdf-engine failed to start");
    })();
    return ready;
  }

  async function post(op, pdf, opts = {}) {
    const { filename = "document.pdf", signal, ...fields } = opts;
    const base = await start();
    const fd = new FormData();
    fd.append("file", pdf instanceof Blob ? pdf : new Blob([pdf], { type: "application/pdf" }), filename);
    for (const [k, v] of Object.entries(fields)) if (v != null) fd.append(k, String(v));
    const res = await fetch(`${base}/v1/${op}`, { method: "POST", body: fd, signal });
    if (!res.ok) {
      const e = (await res.json().catch(() => null))?.error ?? { code: "INTERNAL_ERROR", message: res.statusText };
      throw new PdfEngineError(e.code, e.message, e.details, res.status);
    }
    return res;
  }

  return {
    async inspect(pdf, opts) {
      return (await (await post("inspect", pdf, opts)).json()).document;
    },
    async text(pdf, opts) {
      return (await (await post("text", pdf, opts)).json()).pages;
    },
    async layout(pdf, opts) {
      return (await (await post("layout", pdf, opts)).json()).pages;
    },
    async render(pdf, opts) {
      const res = await post("render", pdf, opts);
      const h = (k) => Number(res.headers.get(k));
      return {
        image: Buffer.from(await res.arrayBuffer()),
        mimeType: res.headers.get("content-type"),
        width: h("x-image-width"),
        height: h("x-image-height"),
        pageNumber: h("x-page-number"),
        dpi: h("x-dpi"),
      };
    },
    async capture(pdf, opts) {
      const { reference, image } = await (await post("capture", pdf, opts)).json();
      return {
        image: Buffer.from(image, "base64"),
        mimeType: reference.mimeType,
        width: reference.imageWidth,
        height: reference.imageHeight,
        reference,
      };
    },
    close() {
      child?.kill();
      ready = undefined;
    },
  };
}

// Process-wide shared engine. Stored on globalThis so Next.js dev hot reloads don't spawn duplicates.
export const engine = (globalThis.__pdfEngine ??= createEngine());
