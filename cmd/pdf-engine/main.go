// pdf-engine: CLI and HTTP server for the PDF document reference engine.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	de "github.com/ifaridaiman/document-manager/documentengine"
	"github.com/ifaridaiman/document-manager/httpapi"
)

const usage = `usage:
  pdf-engine inspect <file.pdf> [--json]
  pdf-engine text    <file.pdf> --page N [--json]
  pdf-engine render  <file.pdf> --page N [--dpi 150] [--format png|jpeg] [--output page.png]
  pdf-engine capture <file.pdf> --page N --x X --y Y --width W --height H [--dpi 300] [--format png] [--output evidence.png]
  pdf-engine serve   [--addr :8080] [--max-mb 100] [--workers 4] [--timeout 30s]

render/capture also take: --grayscale --rotate 90 --max-width PX
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		var e *de.Error
		if errors.As(err, &e) {
			json.NewEncoder(os.Stderr).Encode(map[string]any{"error": e})
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		os.Exit(1)
	}
}

func run(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	page := fs.Int("page", 1, "page number (1-based)")
	dpi := fs.Int("dpi", 0, "render DPI (default 150)")
	format := fs.String("format", "png", "png or jpeg")
	output := fs.String("output", "", "output image path")
	asJSON := fs.Bool("json", false, "machine-readable output")
	x := fs.Float64("x", 0, "region left (0-1)")
	y := fs.Float64("y", 0, "region top (0-1)")
	w := fs.Float64("width", 0, "region width (0-1)")
	h := fs.Float64("height", 0, "region height (0-1)")
	gray := fs.Bool("grayscale", false, "grayscale output")
	rot := fs.Int("rotate", 0, "rotate output clockwise (90/180/270)")
	maxW := fs.Int("max-width", 0, "downscale to at most this many pixels wide")
	addr := fs.String("addr", ":8080", "listen address")
	maxMB := fs.Int64("max-mb", 100, "max upload size in MiB")
	workers := fs.Int("workers", 4, "concurrent documents")
	timeout := fs.Duration("timeout", 30*time.Second, "per-operation timeout")
	parentPipe := fs.Bool("parent-pipe", false, "exit when stdin closes (used when spawned by the npm wrapper)")
	pos := parseInterspersed(fs, args)

	eng, err := de.New(de.Config{MaxFileSize: *maxMB << 20, Workers: *workers, Timeout: *timeout})
	if err != nil {
		return err
	}
	defer eng.Close()

	if cmd == "serve" {
		return serve(eng, *addr, *parentPipe)
	}
	if len(pos) != 1 {
		fs.Usage()
		os.Exit(2)
	}
	ctx := context.Background()
	data, err := os.ReadFile(pos[0])
	if err != nil {
		return err
	}
	doc, err := eng.OpenBytes(ctx, filepath.Base(pos[0]), data)
	if err != nil {
		return err
	}
	defer doc.Close()
	opts := de.ImageOptions{Grayscale: *gray, Rotate: *rot, MaxWidth: *maxW}

	switch cmd {
	case "inspect":
		m, _ := doc.Inspect()
		_ = asJSON // inspect output is always JSON
		return printJSON(map[string]any{"filename": doc.Filename, "pageCount": m.PageCount, "size": doc.Size, "metadata": m})
	case "text":
		t, err := doc.ExtractText(ctx, *page)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(map[string]any{"pageNumber": *page, "text": t})
		}
		fmt.Println(t)
		return nil
	case "render":
		res, err := doc.RenderPage(ctx, de.RenderRequest{PageNumber: *page, DPI: *dpi, Format: de.ImageFormat(*format), Options: opts})
		if err != nil {
			return err
		}
		out := orDefault(*output, fmt.Sprintf("page-%d.%s", *page, *format))
		if err := os.WriteFile(out, res.Image, 0o644); err != nil {
			return err
		}
		return printJSON(map[string]any{"output": out, "render": res})
	case "capture":
		res, err := doc.CaptureRegion(ctx, de.CaptureRequest{PageNumber: *page, Region: de.Region{X: *x, Y: *y, Width: *w, Height: *h}, DPI: *dpi, Format: de.ImageFormat(*format), Options: opts})
		if err != nil {
			return err
		}
		out := orDefault(*output, "evidence."+*format)
		if err := os.WriteFile(out, res.Image, 0o644); err != nil {
			return err
		}
		return printJSON(map[string]any{"output": out, "reference": res.Reference})
	}
	fs.Usage()
	os.Exit(2)
	return nil
}

func serve(eng *de.Engine, addr string, parentPipe bool) error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	srv := &http.Server{
		Addr:              addr,
		Handler:           (&httpapi.Server{Engine: eng, Log: log}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if parentPipe {
		// The parent holds our stdin open; EOF means it died, even via SIGKILL.
		go func() { io.Copy(io.Discard, os.Stdin); stop() }()
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	log.Info("listening", "addr", addr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// parseInterspersed lets flags come after the file argument (stdlib flag stops at the first positional).
func parseInterspersed(fs *flag.FlagSet, args []string) []string {
	var pos []string
	for {
		fs.Parse(args)
		if fs.NArg() == 0 {
			return pos
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
