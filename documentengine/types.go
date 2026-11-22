package documentengine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type DocumentMetadata struct {
	Title        string     `json:"title,omitempty"`
	Author       string     `json:"author,omitempty"`
	Subject      string     `json:"subject,omitempty"`
	Creator      string     `json:"creator,omitempty"`
	Producer     string     `json:"producer,omitempty"`
	CreationDate *time.Time `json:"creationDate,omitempty"`
	ModifiedDate *time.Time `json:"modifiedDate,omitempty"`
	PageCount    int        `json:"pageCount"`
	Encrypted    bool       `json:"encrypted"`
	FileSize     int64      `json:"fileSize"`
	MIMEType     string     `json:"mimeType"`
}

// PageInfo sizes are in PDF points (1/72 inch), as displayed (rotation applied).
type PageInfo struct {
	PageNumber int     `json:"pageNumber"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	Rotation   int     `json:"rotation"`
	TextExists bool    `json:"textExists"`
}

// Region is normalized (0..1) with a top-left origin, relative to the page as displayed.
type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

func (r Region) Validate() error {
	const eps = 1e-9
	for _, v := range []float64{r.X, r.Y, r.Width, r.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return newErr(ErrInvalidRegion, "region values must be finite numbers")
		}
	}
	if r.X < 0 || r.Y < 0 || r.Width <= 0 || r.Height <= 0 || r.X+r.Width > 1+eps || r.Y+r.Height > 1+eps {
		return newErr(ErrInvalidRegion, "region must satisfy x,y >= 0, width,height > 0, x+width <= 1, y+height <= 1",
			"region", r)
	}
	return nil
}

type ImageFormat string

const (
	PNG  ImageFormat = "png"
	JPEG ImageFormat = "jpeg"
)

func (f ImageFormat) mime() (string, error) {
	switch strings.ToLower(string(f)) {
	case "", "png":
		return "image/png", nil
	case "jpeg", "jpg":
		return "image/jpeg", nil
	}
	return "", newErr(ErrInvalidRequest, "unsupported image format", "format", string(f), "supported", "png, jpeg")
}

// ImageOptions is the post-processing pipeline: crop (Region), resize, rotate, grayscale.
type ImageOptions struct {
	Grayscale bool `json:"grayscale,omitempty"`
	Rotate    int  `json:"rotate,omitempty"`   // clockwise degrees, multiple of 90
	MaxWidth  int  `json:"maxWidth,omitempty"` // downscale so output width <= MaxWidth px
	Quality   int  `json:"quality,omitempty"`  // JPEG only, 1-100, default 90
}

type RenderRequest struct {
	PageNumber int
	DPI        int // 0 = engine default (150)
	Format     ImageFormat
	Options    ImageOptions
}

type RenderResult struct {
	Image      []byte `json:"-"`
	MIMEType   string `json:"mimeType"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	PageNumber int    `json:"pageNumber"`
	DPI        int    `json:"dpi"`
}

// CaptureRequest: use a low DPI (e.g. 72) for a quick preview, a high one for final evidence.
type CaptureRequest struct {
	PageNumber int
	Region     Region
	DPI        int
	Format     ImageFormat
	Options    ImageOptions
}

type CaptureResult struct {
	Image     []byte            `json:"-"`
	MIMEType  string            `json:"mimeType"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Reference EvidenceReference `json:"reference"`
}

type EvidenceReference struct {
	DocumentID  string    `json:"documentId"`
	Filename    string    `json:"filename"`
	PageNumber  int       `json:"pageNumber"`
	Region      Region    `json:"region"`
	Excerpt     string    `json:"excerpt,omitempty"`
	MIMEType    string    `json:"mimeType"`
	ImageWidth  int       `json:"imageWidth"`
	ImageHeight int       `json:"imageHeight"`
	DPI         int       `json:"dpi"` // effective DPI (may be lowered by MaxWidth)
	CapturedAt  time.Time `json:"capturedAt"`
}

// parsePDFDate parses "D:YYYYMMDDHHmmSSOHH'mm'" with any trailing part optional.
func parsePDFDate(s string) *time.Time {
	s = strings.TrimPrefix(strings.TrimSpace(s), "D:")
	n := 0
	for n < len(s) && n < 14 && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	if n < 4 {
		return nil
	}
	n -= n % 2 // layout fields are 2 digits after the year
	t, err := time.Parse("20060102150405"[:n], s[:n])
	if err != nil {
		return nil
	}
	// Metadata is untrusted: a malformed zone keeps the date and drops the zone.
	if rest := s[n:]; len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		tz := strings.ReplaceAll(rest[1:], "'", "")
		if len(tz) < 2 {
			return &t
		}
		hh, err := strconv.Atoi(tz[:2])
		if err != nil || hh > 23 {
			return &t
		}
		mm := 0
		if len(tz) >= 4 {
			if mm, err = strconv.Atoi(tz[2:4]); err != nil || mm > 59 {
				return &t
			}
		}
		off := hh*3600 + mm*60
		if rest[0] == '-' {
			off = -off
		}
		t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.FixedZone("", off))
	}
	return &t
}

// Error is the structured error returned by every engine operation.
type Error struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

type ErrorCode string

const (
	ErrInvalidPDF           ErrorCode = "INVALID_PDF"
	ErrPDFEncrypted         ErrorCode = "PDF_ENCRYPTED"
	ErrPDFUnsupported       ErrorCode = "PDF_UNSUPPORTED"
	ErrFileTooLarge         ErrorCode = "FILE_TOO_LARGE"
	ErrPageNotFound         ErrorCode = "PAGE_NOT_FOUND"
	ErrInvalidRegion        ErrorCode = "INVALID_REGION"
	ErrInvalidRequest       ErrorCode = "INVALID_REQUEST"
	ErrRenderFailed         ErrorCode = "RENDER_FAILED"
	ErrTextExtractionFailed ErrorCode = "TEXT_EXTRACTION_FAILED"
	ErrCaptureFailed        ErrorCode = "CAPTURE_FAILED"
	ErrProcessingTimeout    ErrorCode = "PROCESSING_TIMEOUT"
	ErrResourceLimit        ErrorCode = "RESOURCE_LIMIT"
	ErrEngineBusy           ErrorCode = "ENGINE_BUSY" // every worker in use; retry later
	ErrInternal             ErrorCode = "INTERNAL_ERROR"
)

func newErr(code ErrorCode, msg string, kv ...any) *Error {
	e := &Error{Code: code, Message: msg}
	if len(kv) > 0 {
		e.Details = map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			e.Details[kv[i].(string)] = kv[i+1]
		}
	}
	return e
}
