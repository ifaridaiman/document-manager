// Package httpapi exposes the document engine over stateless multipart HTTP endpoints.
// Each request uploads the PDF; nothing outlives the request.
package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	de "github.com/ifaridaiman/document-manager/documentengine"
)

type Server struct {
	Engine *de.Engine
	Log    *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/inspect", s.wrap("inspect", s.inspect))
	mux.HandleFunc("POST /v1/text", s.wrap("text", s.text))
	mux.HandleFunc("POST /v1/layout", s.wrap("layout", s.layout))
	mux.HandleFunc("POST /v1/render", s.wrap("render", s.render))
	mux.HandleFunc("POST /v1/capture", s.wrap("capture", s.capture))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	return mux
}

type call struct {
	w   http.ResponseWriter
	r   *http.Request
	doc *de.Document
	log []any // extra structured log fields; never document content
}

func (s *Server) wrap(op string, h func(*call) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" || len(reqID) > 64 {
			reqID = rand.Text()
		}
		w.Header().Set("X-Request-ID", reqID)

		c := &call{w: w, r: r}
		err := s.open(c)
		if err == nil {
			defer c.doc.Close()
			err = h(c)
		}

		attrs := append([]any{"requestId", reqID, "op", op, "durationMs", time.Since(start).Milliseconds()}, c.log...)
		if err != nil {
			e := asErr(err)
			writeJSON(w, status(e.Code), map[string]any{"error": e})
			s.Log.Warn("request failed", append(attrs, "success", false, "errorCode", e.Code)...)
			return
		}
		s.Log.Info("request ok", append(attrs, "success", true)...)
	}
}

func (s *Server) open(c *call) error {
	maxSize := s.Engine.Config().MaxFileSize
	c.r.Body = http.MaxBytesReader(c.w, c.r.Body, maxSize+1<<20)
	// Parts over 32 MiB spill to OS temp files; RemoveAll deletes them on every path.
	if err := c.r.ParseMultipartForm(32 << 20); err != nil {
		if mbe := (*http.MaxBytesError)(nil); errors.As(err, &mbe) {
			return &de.Error{Code: de.ErrFileTooLarge, Message: "upload exceeds maximum size", Details: map[string]any{"maxBytes": maxSize}}
		}
		return badRequest("expected multipart/form-data with a 'file' field")
	}
	defer c.r.MultipartForm.RemoveAll()
	f, hdr, err := c.r.FormFile("file")
	if err != nil {
		return badRequest("missing 'file' field")
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return badRequest("cannot read upload")
	}
	c.log = append(c.log, "fileSize", len(data))
	// Filename is display-only metadata, never used as a path.
	c.doc, err = s.Engine.OpenBytes(c.r.Context(), filepath.Base(hdr.Filename), data)
	return err
}

func (s *Server) inspect(c *call) error {
	m, _ := c.doc.Inspect()
	return writeJSON(c.w, 200, map[string]any{"document": map[string]any{
		"id": c.doc.ID, "filename": c.doc.Filename, "size": c.doc.Size, "pageCount": m.PageCount, "metadata": m,
	}})
}

// text accepts page=N or pages=A-B.
// layout accepts page=N or pages=A-B and returns each page's text lines with
// normalized boxes, ready to pass to /v1/capture.
func (s *Server) layout(c *call) error {
	from, to, err := pageRange(c.r, c.doc.Metadata.PageCount)
	if err != nil {
		return err
	}
	c.log = append(c.log, "pageFrom", from, "pageTo", to)
	pages := []de.PageLayout{}
	for p := from; p <= to; p++ {
		l, err := c.doc.Layout(c.r.Context(), p)
		if err != nil {
			return err
		}
		pages = append(pages, l)
	}
	return writeJSON(c.w, 200, map[string]any{"pages": pages})
}

func (s *Server) text(c *call) error {
	from, to, err := pageRange(c.r, c.doc.Metadata.PageCount)
	if err != nil {
		return err
	}
	c.log = append(c.log, "pageFrom", from, "pageTo", to)
	type pageText struct {
		PageNumber int    `json:"pageNumber"`
		Text       string `json:"text"`
	}
	pages := []pageText{}
	for p := from; p <= to; p++ {
		t, err := c.doc.ExtractText(c.r.Context(), p)
		if err != nil {
			return err
		}
		pages = append(pages, pageText{p, t})
	}
	return writeJSON(c.w, 200, map[string]any{"pages": pages})
}

func (s *Server) render(c *call) error {
	page, err := intField(c.r, "page", 0)
	if err != nil {
		return err
	}
	dpi, opts, err := imageFields(c.r)
	if err != nil {
		return err
	}
	res, err := c.doc.RenderPage(c.r.Context(), de.RenderRequest{PageNumber: page, DPI: dpi, Format: de.ImageFormat(c.r.FormValue("format")), Options: opts})
	if err != nil {
		return err
	}
	c.log = append(c.log, "page", page, "dpi", res.DPI, "width", res.Width, "height", res.Height)
	h := c.w.Header()
	h.Set("Content-Type", res.MIMEType)
	h.Set("X-Page-Number", strconv.Itoa(page))
	h.Set("X-Image-Width", strconv.Itoa(res.Width))
	h.Set("X-Image-Height", strconv.Itoa(res.Height))
	h.Set("X-DPI", strconv.Itoa(res.DPI))
	_, err = c.w.Write(res.Image)
	return err
}

// capture returns JSON with the evidence reference and the image as base64,
// ready to drop into an LLM image block or a chat UI <img src="data:...">.
func (s *Server) capture(c *call) error {
	page, err := intField(c.r, "page", 0)
	if err != nil {
		return err
	}
	var region de.Region
	for name, dst := range map[string]*float64{"x": &region.X, "y": &region.Y, "width": &region.Width, "height": &region.Height} {
		if *dst, err = strconv.ParseFloat(c.r.FormValue(name), 64); err != nil {
			return &de.Error{Code: de.ErrInvalidRegion, Message: "'" + name + "' must be a number between 0 and 1"}
		}
	}
	dpi, opts, err := imageFields(c.r)
	if err != nil {
		return err
	}
	res, err := c.doc.CaptureRegion(c.r.Context(), de.CaptureRequest{PageNumber: page, Region: region, DPI: dpi, Format: de.ImageFormat(c.r.FormValue("format")), Options: opts})
	if err != nil {
		return err
	}
	c.log = append(c.log, "page", page, "dpi", res.Reference.DPI, "width", res.Width, "height", res.Height)
	return writeJSON(c.w, 200, map[string]any{"reference": res.Reference, "image": res.Image}) // []byte → base64
}

func imageFields(r *http.Request) (int, de.ImageOptions, error) {
	var o de.ImageOptions
	var err error
	dpi, err := intField(r, "dpi", 0)
	if err == nil {
		o.Rotate, err = intField(r, "rotate", 0)
	}
	if err == nil {
		o.MaxWidth, err = intField(r, "maxWidth", 0)
	}
	if err == nil {
		o.Quality, err = intField(r, "quality", 0)
	}
	o.Grayscale, _ = strconv.ParseBool(r.FormValue("grayscale"))
	return dpi, o, err
}

func intField(r *http.Request, name string, def int) (int, error) {
	v := r.FormValue(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, badRequest("'" + name + "' must be an integer")
	}
	return n, nil
}

func pageRange(r *http.Request, pageCount int) (int, int, error) {
	if p := r.FormValue("pages"); p != "" {
		a, b, ok := strings.Cut(p, "-")
		from, err1 := strconv.Atoi(strings.TrimSpace(a))
		to, err2 := strconv.Atoi(strings.TrimSpace(b))
		if !ok || err1 != nil || err2 != nil || from > to {
			return 0, 0, badRequest("'pages' must look like 3-7")
		}
		if from < 1 || to > pageCount {
			return 0, 0, &de.Error{Code: de.ErrPageNotFound, Message: "page range outside document", Details: map[string]any{"pageCount": pageCount}}
		}
		return from, to, nil
	}
	p, err := intField(r, "page", 0)
	return p, p, err
}

func badRequest(msg string) *de.Error { return &de.Error{Code: de.ErrInvalidRequest, Message: msg} }

func asErr(err error) *de.Error {
	var e *de.Error
	if errors.As(err, &e) {
		return e
	}
	return &de.Error{Code: de.ErrInternal, Message: "internal error"}
}

func status(c de.ErrorCode) int {
	switch c {
	case de.ErrInvalidRequest, de.ErrInvalidRegion:
		return http.StatusBadRequest
	case de.ErrFileTooLarge:
		return http.StatusRequestEntityTooLarge
	case de.ErrInvalidPDF, de.ErrPDFEncrypted, de.ErrPDFUnsupported, de.ErrPageNotFound, de.ErrResourceLimit:
		return http.StatusUnprocessableEntity
	case de.ErrProcessingTimeout:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

func writeJSON(w http.ResponseWriter, code int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	return json.NewEncoder(w).Encode(v)
}
