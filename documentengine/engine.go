// Package documentengine is a stateless PDF inspection, text extraction,
// rendering and region-capture engine. Nothing is persisted: documents live in
// memory only between OpenBytes and Close.
//
// PDFium runs as WebAssembly (via wazero) with no filesystem mounted, so a
// malicious PDF is confined to the wasm sandbox and cannot touch the host.
package documentengine

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/enums"
	pdfiumerrors "github.com/klippa-app/go-pdfium/errors"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	wazeroapi "github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// Config holds resource limits. Zero values fall back to defaults.
type Config struct {
	MaxFileSize int64         // bytes, default 100 MiB
	MaxPages    int           // default 5000
	DefaultDPI  int           // default 150
	MaxDPI      int           // default 600
	MaxPixels   int           // per rendered image, default 60M (~240 MB RGBA)
	Timeout     time.Duration // per operation, default 30s
	Workers     int           // concurrently open documents, default 4
}

func (c Config) withDefaults() Config {
	c.MaxFileSize = cmp.Or(c.MaxFileSize, 100<<20)
	c.MaxPages = cmp.Or(c.MaxPages, 5000)
	c.DefaultDPI = cmp.Or(c.DefaultDPI, 150)
	c.MaxDPI = cmp.Or(c.MaxDPI, 600)
	c.MaxPixels = cmp.Or(c.MaxPixels, 60_000_000)
	c.Workers = cmp.Or(c.Workers, 4)
	c.Timeout = cmp.Or(c.Timeout, 30*time.Second)
	return c
}

// Engine owns the PDFium wasm pool. Create once, share across goroutines.
type Engine struct {
	cfg  Config
	pool pdfium.Pool
}

func New(cfg Config) (*Engine, error) {
	cfg = cfg.withDefaults()
	pool, err := webassembly.Init(webassembly.Config{
		MinIdle:  0,
		MaxIdle:  cfg.Workers,
		MaxTotal: cfg.Workers,
		// No directory mounts: PDFium only ever sees bytes we hand it.
		FSConfig: wazero.NewFSConfig(),
		Stdout:   io.Discard,
		Stderr:   io.Discard,
		RuntimeConfig: wazero.NewRuntimeConfig().
			WithCoreFeatures(wazeroapi.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
			WithCloseOnContextDone(true),
	})
	if err != nil {
		return nil, fmt.Errorf("init pdfium: %w", err)
	}
	return &Engine{cfg: cfg, pool: pool}, nil
}

func (e *Engine) Config() Config { return e.cfg }

func (e *Engine) Close() error { return e.pool.Close() }

// OpenBytes validates and opens a PDF. Callers must Close the returned Document.
func (e *Engine) OpenBytes(ctx context.Context, filename string, data []byte) (*Document, error) {
	if int64(len(data)) > e.cfg.MaxFileSize {
		return nil, newErr(ErrFileTooLarge, "file exceeds maximum size", "maxBytes", e.cfg.MaxFileSize, "size", len(data))
	}
	// Spec allows junk before the header, readers accept it within the first 1 KiB.
	if !bytes.Contains(data[:min(len(data), 1024)], []byte("%PDF-")) {
		return nil, newErr(ErrInvalidPDF, "missing %PDF- signature")
	}

	ctx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	inst, err := e.pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, newErr(ErrResourceLimit, "no PDF worker available", "cause", err.Error())
	}

	d := &Document{
		ID:       rand.Text(),
		Filename: filename,
		Size:     int64(len(data)),
		engine:   e,
		inst:     inst,
		turn:     make(chan struct{}, 1),
	}
	err = d.do(ctx, func() error {
		res, err := inst.OpenDocument(&requests.OpenDocument{File: &data})
		if err != nil {
			return openErr(err)
		}
		d.doc = res.Document
		return d.loadMetadata()
	})
	if err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func openErr(err error) error {
	switch {
	case errors.Is(err, pdfiumerrors.ErrPassword):
		return newErr(ErrPDFEncrypted, "PDF is password protected")
	case errors.Is(err, pdfiumerrors.ErrSecurity):
		return newErr(ErrPDFUnsupported, "unsupported PDF security handler")
	case errors.Is(err, pdfiumerrors.ErrFormat), errors.Is(err, pdfiumerrors.ErrFile):
		return newErr(ErrInvalidPDF, "file is not a valid PDF")
	default:
		return newErr(ErrPDFUnsupported, "PDF cannot be processed", "cause", err.Error())
	}
}

// Document is an open PDF. Methods are safe for concurrent use, but calls on one
// Document run one at a time. For parallel work, open one Document per goroutine.
type Document struct {
	ID       string           `json:"id"`
	Filename string           `json:"filename"`
	Size     int64            `json:"size"`
	Metadata DocumentMetadata `json:"metadata"`

	engine *Engine
	inst   pdfium.Pdfium
	doc    references.FPDF_DOCUMENT
	turn   chan struct{} // capacity 1: one PDFium operation at a time (see do)
	mu     sync.Mutex    // guards dead
	dead   bool
}

func (d *Document) loadMetadata() error {
	pc, err := d.inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: d.doc})
	if err != nil || pc.PageCount < 1 {
		return newErr(ErrInvalidPDF, "PDF has no pages")
	}
	if pc.PageCount > d.engine.cfg.MaxPages {
		return newErr(ErrResourceLimit, "too many pages", "maxPages", d.engine.cfg.MaxPages, "pageCount", pc.PageCount)
	}
	m := DocumentMetadata{PageCount: pc.PageCount, FileSize: d.Size, MIMEType: "application/pdf"}

	if sec, err := d.inst.FPDF_GetSecurityHandlerRevision(&requests.FPDF_GetSecurityHandlerRevision{Document: d.doc}); err == nil {
		m.Encrypted = sec.SecurityHandlerRevision != -1 // opened without password: owner-password-only PDF
	}
	if meta, err := d.inst.GetMetaData(&requests.GetMetaData{Document: d.doc}); err == nil {
		for _, t := range meta.Tags {
			v := strings.TrimSpace(t.Value)
			switch t.Tag {
			case "Title":
				m.Title = v
			case "Author":
				m.Author = v
			case "Subject":
				m.Subject = v
			case "Creator":
				m.Creator = v
			case "Producer":
				m.Producer = v
			case "CreationDate":
				m.CreationDate = parsePDFDate(v)
			case "ModDate":
				m.ModifiedDate = parsePDFDate(v)
			}
		}
	}
	d.Metadata = m
	return nil
}

// Inspect returns document metadata (read once at open).
func (d *Document) Inspect() (DocumentMetadata, error) { return d.Metadata, nil }

func (d *Document) GetPageInfo(ctx context.Context, pageNumber int) (PageInfo, error) {
	var info PageInfo
	err := d.do(ctx, func() error {
		return d.withPage(pageNumber, func(p requests.Page) error {
			size, err := d.inst.GetPageSize(&requests.GetPageSize{Page: p})
			if err != nil {
				return newErr(ErrInternal, "cannot read page size")
			}
			info = PageInfo{PageNumber: pageNumber, Width: size.Width, Height: size.Height}
			if rot, err := d.inst.FPDFPage_GetRotation(&requests.FPDFPage_GetRotation{Page: p}); err == nil {
				info.Rotation = int(rot.PageRotation) * 90
			}
			if tp, err := d.inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: p}); err == nil {
				if n, err := d.inst.FPDFText_CountChars(&requests.FPDFText_CountChars{TextPage: tp.TextPage}); err == nil {
					info.TextExists = n.Count > 0
				}
				d.inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: tp.TextPage})
			}
			return nil
		})
	})
	return info, err
}

// ExtractText returns the plain text of one page. Scanned pages return "".
func (d *Document) ExtractText(ctx context.Context, pageNumber int) (string, error) {
	var text string
	err := d.do(ctx, func() error {
		return d.withPage(pageNumber, func(p requests.Page) error {
			res, err := d.inst.GetPageText(&requests.GetPageText{Page: p})
			if err != nil {
				return newErr(ErrTextExtractionFailed, "text extraction failed", "pageNumber", pageNumber)
			}
			text = res.Text
			return nil
		})
	})
	return text, err
}

func (d *Document) RenderPage(ctx context.Context, req RenderRequest) (RenderResult, error) {
	var out RenderResult
	err := d.do(ctx, func() error {
		return d.withPage(req.PageNumber, func(p requests.Page) error {
			r, err := d.rasterize(p, nil, req.DPI, req.Format, req.Options, ErrRenderFailed)
			if err != nil {
				return err
			}
			out = RenderResult{Image: r.data, MIMEType: r.mime, Width: r.w, Height: r.h, PageNumber: req.PageNumber, DPI: r.dpi}
			return nil
		})
	})
	return out, err
}

func (d *Document) CaptureRegion(ctx context.Context, req CaptureRequest) (CaptureResult, error) {
	if err := req.Region.Validate(); err != nil {
		return CaptureResult{}, err
	}
	var out CaptureResult
	err := d.do(ctx, func() error {
		return d.withPage(req.PageNumber, func(p requests.Page) error {
			r, err := d.rasterize(p, &req.Region, req.DPI, req.Format, req.Options, ErrCaptureFailed)
			if err != nil {
				return err
			}
			out = CaptureResult{Image: r.data, MIMEType: r.mime, Width: r.w, Height: r.h, Reference: EvidenceReference{
				DocumentID:  d.ID,
				Filename:    d.Filename,
				PageNumber:  req.PageNumber,
				Region:      req.Region,
				Excerpt:     d.regionText(p, req.Region),
				MIMEType:    r.mime,
				ImageWidth:  r.w,
				ImageHeight: r.h,
				DPI:         r.dpi,
				CapturedAt:  time.Now().UTC(),
			}}
			return nil
		})
	})
	return out, err
}

type raster struct {
	data      []byte
	mime      string
	w, h, dpi int
}

func (d *Document) rasterize(p requests.Page, region *Region, dpi int, format ImageFormat, opt ImageOptions, failCode ErrorCode) (raster, error) {
	cfg := d.engine.cfg
	if dpi == 0 {
		dpi = cfg.DefaultDPI
	}
	if dpi < 1 || dpi > cfg.MaxDPI {
		return raster{}, newErr(ErrInvalidRequest, "dpi out of range", "min", 1, "max", cfg.MaxDPI)
	}
	if opt.Rotate%90 != 0 {
		return raster{}, newErr(ErrInvalidRequest, "rotate must be a multiple of 90")
	}
	opt.Rotate = ((opt.Rotate % 360) + 360) % 360
	mime, err := format.mime()
	if err != nil {
		return raster{}, err
	}

	size, err := d.inst.GetPageSize(&requests.GetPageSize{Page: p})
	if err != nil {
		return raster{}, newErr(failCode, "cannot read page size")
	}
	// Page size is already rotation-aware, so normalized coords map to the page as displayed.
	wPt, hPt := size.Width, size.Height
	var crop *requests.RenderPageCrop
	if region != nil {
		crop = &requests.RenderPageCrop{X: region.X * wPt, Y: region.Y * hPt, Width: region.Width * wPt, Height: region.Height * hPt}
		wPt, hPt = crop.Width, crop.Height
	}

	// Resize = render at a lower DPI: cheaper and sharper than scaling pixels afterwards.
	outW := wPt
	if opt.Rotate == 90 || opt.Rotate == 270 {
		outW = hPt
	}
	if opt.MaxWidth > 0 && outW*float64(dpi)/72 > float64(opt.MaxWidth) {
		dpi = max(1, int(float64(opt.MaxWidth)*72/outW))
	}
	if px := (wPt * float64(dpi) / 72) * (hPt * float64(dpi) / 72); px > float64(cfg.MaxPixels) {
		return raster{}, newErr(ErrResourceLimit, "rendered image would be too large, lower the dpi", "pixels", int64(px), "maxPixels", cfg.MaxPixels)
	}

	imgFormat := requests.RenderImageFormatRGBA
	if opt.Grayscale {
		imgFormat = requests.RenderImageFormatGrayscale
	}
	res, err := d.inst.RenderPageInDPI(&requests.RenderPageInDPI{Page: p, DPI: dpi, Crop: crop, ImageFormat: imgFormat, RenderFlags: enums.FPDF_RENDER_FLAG_ANNOT})
	if err != nil {
		return raster{}, newErr(failCode, "render failed")
	}
	defer res.Cleanup() // pixel buffer lives in wasm memory; encode before this runs

	img := rotate(res.Result.RenderedImage, opt.Rotate)
	data, err := encode(img, format, opt.Quality)
	if err != nil {
		return raster{}, newErr(failCode, "image encoding failed")
	}
	b := img.Bounds()
	return raster{data: data, mime: mime, w: b.Dx(), h: b.Dy(), dpi: dpi}, nil
}

// regionText returns the text inside a normalized region, best effort ("" on failure).
func (d *Document) regionText(p requests.Page, r Region) string {
	size, err := d.inst.GetPageSize(&requests.GetPageSize{Page: p})
	if err != nil {
		return ""
	}
	// Map display-space corners to PDF user space; PDFium applies page rotation for us.
	const scale = 10 // device units per point, for sub-point precision
	sx, sy := int(math.Round(size.Width*scale)), int(math.Round(size.Height*scale))
	toPage := func(nx, ny float64) (float64, float64, bool) {
		res, err := d.inst.FPDF_DeviceToPage(&requests.FPDF_DeviceToPage{
			Page: p, SizeX: sx, SizeY: sy,
			DeviceX: int(math.Round(nx * float64(sx))), DeviceY: int(math.Round(ny * float64(sy))),
		})
		if err != nil {
			return 0, 0, false
		}
		return res.PageX, res.PageY, true
	}
	x1, y1, ok1 := toPage(r.X, r.Y)
	x2, y2, ok2 := toPage(r.X+r.Width, r.Y+r.Height)
	if !ok1 || !ok2 {
		return ""
	}
	tp, err := d.inst.FPDFText_LoadPage(&requests.FPDFText_LoadPage{Page: p})
	if err != nil {
		return ""
	}
	defer d.inst.FPDFText_ClosePage(&requests.FPDFText_ClosePage{TextPage: tp.TextPage})
	res, err := d.inst.FPDFText_GetBoundedText(&requests.FPDFText_GetBoundedText{
		TextPage: tp.TextPage,
		Left:     min(x1, x2), Right: max(x1, x2),
		Top: max(y1, y2), Bottom: min(y1, y2),
	})
	if err != nil {
		return ""
	}
	return truncateRunes(strings.TrimSpace(res.Text), 4000)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (d *Document) withPage(n int, fn func(p requests.Page) error) error {
	if n < 1 || n > d.Metadata.PageCount {
		return newErr(ErrPageNotFound, "page not found", "pageNumber", n, "pageCount", d.Metadata.PageCount)
	}
	res, err := d.inst.FPDF_LoadPage(&requests.FPDF_LoadPage{Document: d.doc, Index: n - 1})
	if err != nil {
		return newErr(ErrPDFUnsupported, "page cannot be loaded", "pageNumber", n)
	}
	defer d.inst.FPDF_ClosePage(&requests.FPDF_ClosePage{Page: res.Page})
	return fn(requests.Page{ByReference: &res.Page})
}

// do runs fn with the configured timeout. On timeout the wasm instance is
// killed (a hostile PDF can't keep burning CPU) and the document becomes unusable.
//
// Operations on one Document run one at a time: PDFium is single-threaded per
// instance, and go-pdfium frees render buffers outside its own lock, so
// overlapping calls race. Waiting for the turn honours ctx but never kills the
// worker: a queued call is not hung work.
func (d *Document) do(ctx context.Context, fn func() error) error {
	select {
	case d.turn <- struct{}{}:
	case <-ctx.Done():
		return newErr(ErrProcessingTimeout, "cancelled while waiting for the document")
	}
	d.mu.Lock()
	dead := d.dead
	d.mu.Unlock()
	if dead {
		<-d.turn
		return newErr(ErrInternal, "document is closed")
	}
	ctx, cancel := context.WithTimeout(ctx, d.engine.cfg.Timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		// Released only when fn really returns, even after a timeout, so the next
		// operation never overlaps work that is still unwinding.
		defer func() { <-d.turn }()
		defer func() {
			if r := recover(); r != nil {
				done <- newErr(ErrInternal, "PDF processing crashed")
			}
		}()
		done <- fn()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		d.mu.Lock()
		if !d.dead {
			d.dead = true
			d.inst.Kill()
		}
		d.mu.Unlock()
		return newErr(ErrProcessingTimeout, "processing timed out or was cancelled")
	}
}

// Close releases the document and its worker. Safe to call more than once and
// concurrently with other methods: it waits for a running operation to finish.
func (d *Document) Close() error {
	d.mu.Lock()
	dead := d.dead
	d.mu.Unlock()
	if dead {
		return nil // already closed, or killed after a timeout (worker already freed)
	}
	d.turn <- struct{}{}
	defer func() { <-d.turn }()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.dead {
		return nil
	}
	d.dead = true
	if d.doc != "" {
		d.inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: d.doc})
	}
	return d.inst.Close()
}
