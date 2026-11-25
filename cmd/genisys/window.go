// window.go — `genisys window`: the native desktop app (SPEC Q1). Same
// server as `serve`, wrapped in a Wails webview window instead of a
// browser tab. Binaries built without `-tags wails` fall back to serve.

package main

import (
	"flag"
	"log"
	"path/filepath"

	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/desktop"
)

func windowCmd(args []string) error {
	fs := flag.NewFlagSet("window", flag.ExitOnError)
	var o serverOpts
	fs.StringVar(&o.cfgPath, "config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	fs.StringVar(&o.dbFlag, "db", "", "state db path (default ~/.genisys/genisys.db)")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8710", "listen address")
	fs.Parse(args)

	// desktop.Run blocks, so it runs on its own goroutine; the returned
	// channel closes when the window quits → runServer shuts the server
	// down. On failure (stub build / no webview) we degrade to the
	// browser flow and never signal quit.
	quit := make(chan struct{})
	return runServer(o, func(url string) <-chan struct{} {
		go func() {
			if err := desktop.Run(desktop.Options{Title: "Genisys", URL: url}); err != nil {
				log.Println("native window unavailable:", err)
				openBrowser(url)
				return
			}
			close(quit)
		}()
		return quit
	})
}
