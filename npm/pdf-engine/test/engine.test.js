import assert from "node:assert/strict";
import { after, test } from "node:test";
import { PdfEngineError, engine } from "../src/index.js";

// One-page Letter PDF with a heading near the top.
function fixturePdf() {
  const content = "BT /F1 24 Tf 72 720 Td (Revenue increased by 18%) Tj ET\n";
  const objs = [
    "<< /Type /Catalog /Pages 2 0 R >>",
    "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
    `<< /Length ${content.length} >>\nstream\n${content}endstream`,
    "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
  ];
  let out = "%PDF-1.4\n";
  const offsets = objs.map((o, i) => {
    const off = out.length;
    out += `${i + 1} 0 obj\n${o}\nendobj\n`;
    return off;
  });
  const xref = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n`;
  for (const off of offsets) out += `${String(off).padStart(10, "0")} 00000 n \n`;
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
  return new TextEncoder().encode(out);
}

const pdf = fixturePdf();
after(() => engine.close());

test("inspect, text, render, capture", async () => {
  const doc = await engine.inspect(pdf, { filename: "report.pdf" });
  assert.equal(doc.pageCount, 1);
  assert.equal(doc.filename, "report.pdf");

  const [page] = await engine.text(pdf, { page: 1 });
  assert.match(page.text, /Revenue increased by 18%/);

  const r = await engine.render(pdf, { page: 1, dpi: 72 });
  assert.equal(r.mimeType, "image/png");
  assert.equal(r.width, 612);
  assert.equal(r.image.subarray(1, 4).toString(), "PNG");

  const c = await engine.capture(pdf, { page: 1, x: 0, y: 0, width: 1, height: 0.15, dpi: 150 });
  assert.equal(c.reference.excerpt, "Revenue increased by 18%");
  assert.equal(c.width, 1275);
  assert.equal(c.image.subarray(1, 4).toString(), "PNG");
});

test("layout boxes plug into capture", async () => {
  const [page] = await engine.layout(pdf, { page: 1 });
  assert.equal(page.pageNumber, 1);
  assert.equal(page.lines.length, 1);
  const [line] = page.lines;
  assert.equal(line.index, 0);
  assert.equal(line.text, "Revenue increased by 18%");
  assert.ok(line.region.x > 0.1 && line.region.x < 0.13, `x ${line.region.x}`);

  const c = await engine.capture(pdf, { page: 1, ...line.region, dpi: 72 });
  assert.match(c.reference.excerpt, /Revenue increased by 18%/);
});

// Bundlers and hot reload can evaluate the wrapper twice while the engine stays
// shared on globalThis; instanceof must work against either copy's class.
test("PdfEngineError instanceof across module copies", async () => {
  const copy = await import("../src/index.js?second-copy");
  assert.notEqual(copy.PdfEngineError, PdfEngineError, "test needs two module instances");
  await assert.rejects(engine.inspect(new TextEncoder().encode("not a pdf")), (e) => {
    assert.ok(e instanceof copy.PdfEngineError);
    assert.ok(e instanceof PdfEngineError);
    assert.ok(e instanceof Error);
    return true;
  });
  assert.ok(!(new Error("plain") instanceof PdfEngineError));
});

test("structured errors", async () => {
  await assert.rejects(engine.capture(pdf, { page: 1, x: 0.5, y: 0, width: 0.8, height: 0.1 }), (e) => {
    assert.ok(e instanceof PdfEngineError);
    assert.equal(e.code, "INVALID_REGION");
    assert.equal(e.status, 400);
    return true;
  });
  await assert.rejects(engine.inspect(new TextEncoder().encode("not a pdf")), { code: "INVALID_PDF" });
});
