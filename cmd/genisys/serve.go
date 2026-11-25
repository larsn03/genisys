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
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/larsn03/genisys/internal/api"
	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/app"
	"github.com/larsn03/genisys/ui"
)

type serverOpts struct {
	cfgPath, dbFlag, addr string
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

	url := fmt.Sprintf("http://%s/?token=%s", o.addr, srv.Token)
	fmt.Printf("Genisys %s serving %s\n", version, url)
	fmt.Println("press ctrl-c to stop")

	var quit <-chan struct{}
	if open != nil {
		quit = open(url)
	}

	ctx, stop := signalContext()
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
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

// signalContext: ctrl-c / SIGTERM aware context.
func signalContext() (context.Context, func()) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}
