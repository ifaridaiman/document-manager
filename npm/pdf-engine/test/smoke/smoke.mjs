// Smoke test for the INSTALLED npm tarballs. CI ("smoke" in .github/workflows/ci.yml)
// copies this folder and ../fixture.js into a scratch project, installs the main and
// platform tarballs there, and runs it on every supported platform. Not run in place.
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { PdfEngineError, createEngine } from "@documentmanager/pdf-engine";
import { fixturePdf } from "./fixture.js";

assert.equal(process.env.PDF_ENGINE_BIN, undefined, "must use the packaged binary");
const target = `${process.platform}-${process.arch}`;
const exe = process.platform === "win32" ? "pdf-engine.exe" : "pdf-engine";

// Platform selection: the binary comes from the platform package npm installed...
const bin = fileURLToPath(import.meta.resolve(`@documentmanager/pdf-engine-${target}/bin/${exe}`));
assert.ok(existsSync(bin), `missing ${bin}`);
// ...and the main package carries no fallback binaries of its own.
assert.ok(!existsSync(new URL("../bin", import.meta.resolve("@documentmanager/pdf-engine"))));

const engine = createEngine({ workers: 1 });
try {
  const pdf = fixturePdf();
  const doc = await engine.inspect(pdf, { filename: "smoke.pdf" });
  assert.equal(doc.pageCount, 1);

  const [layout] = await engine.layout(pdf, { page: 1 });
  assert.equal(layout.lines[0].text, "Revenue increased by 18%");

  const c = await engine.capture(pdf, { page: 1, ...layout.lines[0].region, dpi: 72 });
  assert.equal(c.mimeType, "image/png");
  assert.equal(c.image.subarray(1, 4).toString(), "PNG");
  assert.match(c.reference.excerpt ?? "", /Revenue/);

  await assert.rejects(engine.text(pdf, { page: 9 }), (e) => e instanceof PdfEngineError && e.code === "PAGE_NOT_FOUND");
} finally {
  engine.close();
}
console.log(`smoke ok on ${target} with ${bin}`);
