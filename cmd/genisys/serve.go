// serve.go — shared server lifecycle for `genisys serve` (browser flow)
// and `genisys window` (native Wails shell). One command starts the
// API+WS server, announces the URL, and shuts down cleanly on signal or
// when the opener's done-channel closes (SPEC §19, G12).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/larsn03/genisys/internal/api"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/ui"
)

type serverOpts struct {
	cfgPath, dbFlag, addr string
	openDir, openFile     string // `genisys <dir|file>`: project to focus + file to edit
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var o serverOpts
	fs.StringVar(&o.cfgPath, "config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	fs.StringVar(&o.dbFlag, "db", "", "state db path (default ~/.genisys/genisys.db)")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8710", "listen address (pass 0.0.0.0 to expose; warns)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	fs.Parse(args)

	return runServer(o, func(url string) <-chan struct{} {
		if !*noOpen {
			openBrowser(url)
		}
		return nil
	})
}

// runServer starts the HTTP server, calls open(url) once listening, and
// returns when a signal arrives, the listener dies, or the channel open
// returned (if non-nil) closes — e.g. the native window was closed.
func runServer(o serverOpts, open func(url string) <-chan struct{}) error {
	a, err := app.Open(o.cfgPath, o.dbFlag)
	if err != nil {
		return err
	}
	defer a.Close()

	api.Version = version
	srv := api.New(a, o.cfgPath, ui.Dist(), log.Default())

	if host, _, err := net.SplitHostPort(o.addr); err == nil && host == "0.0.0.0" {
		fmt.Fprintln(os.Stderr, "warning: bound to 0.0.0.0 — the bearer token is your only protection (SPEC §22)")
	}
	httpSrv := &http.Server{Addr: o.addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	// desktop UX: a taken port must never kill the app — fall back to an
	// ephemeral port (two windows side by side just work)
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		if !errors.Is(err, syscall.EADDRINUSE) {
			return err
		}
		fmt.Fprintf(os.Stderr, "warning: %s already in use — falling back to 127.0.0.1:0\n", o.addr)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
	}
	url := fmt.Sprintf("http://%s/?token=%s", ln.Addr().String(), srv.Token)
	if extra := openURLParams(a, o.openDir, o.openFile); extra != "" {
		url += extra
	}
	fmt.Printf("Genisys %s serving %s\n", version, url)
	fmt.Println("press ctrl-c to stop")

	var quit <-chan struct{}
	if open != nil {
		quit = open(url)
	}

	ctx, stop := signalContext()
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-quit: // window closed
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Sup.CloseAll()
	_ = httpSrv.Shutdown(shutCtx) // best-effort within the timeout
	return nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Println("could not open browser:", err)
	}
}

// openURLParams turns `genisys <dir|file>` into UI deep-link params:
// the project bound to the dir (created on demand — ProjectFor) and the
// file relative to the workspace root. Empty when nothing to open.
func openURLParams(a *app.App, dir, file string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(apiconfig.ExpandPath(dir))
	if err != nil {
		return ""
	}
	if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
		abs = filepath.Dir(abs)
	}
	proj, err := a.ProjectFor(abs)
	if err != nil {
		return ""
	}
	extra := "&project=" + proj.ID
	if file != "" {
		if fabs, err := filepath.Abs(apiconfig.ExpandPath(file)); err == nil {
			if rel, err := filepath.Rel(proj.WorkspacePath, fabs); err == nil && !strings.HasPrefix(rel, "..") {
				extra += "&file=" + url.QueryEscape(rel)
			}
		}
	}
	return extra
}

// signalContext: ctrl-c / SIGTERM aware context.
func signalContext() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
