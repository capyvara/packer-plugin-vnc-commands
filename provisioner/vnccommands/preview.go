package vnccommands

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

type previewServer struct {
	server   *http.Server
	url      string
	done     chan struct{}
	serveErr error
	mu       sync.Mutex
	jpeg     []byte
	changed  chan struct{}
}

func newPreview() (*previewServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	// A per-step URL keeps arbitrary local web pages from guessing the stream.
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		listener.Close()
		return nil, err
	}
	path := fmt.Sprintf("/%x/", token)
	p := &previewServer{url: "http://" + listener.Addr().String() + path, done: make(chan struct{}), changed: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path+"{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'")
		io.WriteString(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>VNC preview</title><style>body{margin:0;background:#171717;color:#eee;font:16px system-ui;text-align:center}header{padding:16px}img{display:block;max-width:100%;max-height:calc(100vh - 72px);margin:auto;object-fit:contain}</style><header>VNC preview · Observe only</header><img src="stream" alt="Waiting for the guest screen"></html>`)
	})
	mux.HandleFunc("GET "+path+"stream", p.stream)
	p.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go func() {
		p.serveErr = p.server.Serve(listener)
		close(p.done)
	}()
	return p, nil
}

func (p *previewServer) publish(jpeg []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jpeg = jpeg // Immutable; slow viewers never hold up capture or typing.
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *previewServer) stream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	controller := http.NewResponseController(w)
	for {
		p.mu.Lock()
		jpeg, changed := p.jpeg, p.changed
		p.mu.Unlock()
		if len(jpeg) > 0 {
			if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpeg)); err != nil {
				return
			}
			if _, err := w.Write(jpeg); err != nil {
				return
			}
			if _, err := io.WriteString(w, "\r\n"); err != nil {
				return
			}
			if err := controller.Flush(); err != nil {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}

func (p *previewServer) Close() error {
	err := p.server.Close()
	<-p.done
	return err
}

func openPreviewBrowser(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.CommandContext(ctx, "open", url)
	case "linux":
		command = exec.CommandContext(ctx, "xdg-open", url)
	case "windows":
		command = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("opening a browser is unsupported on %s", runtime.GOOS)
	}
	return command.Run()
}
