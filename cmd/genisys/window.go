// window.go — `genisys window`: the native desktop app (SPEC Q1). Same
// server as `serve`, wrapped in a Wails webview window instead of a
// browser tab. Binaries built without `-tags wails` fall back to serve.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/desktop"
)

// openArgs maps `genisys <path>` to (cwd, file): a directory opens as a
// project; a file opens its containing project with the file focused.
func openArgs(arg string) (cwd, file string, err error) {
	abs, err := filepath.Abs(apiconfig.ExpandPath(arg))
	if err != nil {
		return "", "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", arg, err)
	}
	if fi.IsDir() {
		return abs, "", nil
	}
	return filepath.Dir(abs), abs, nil
}

func windowCmd(args []string) error {
	fs := flag.NewFlagSet("window", flag.ExitOnError)
	var o serverOpts
	fs.StringVar(&o.cfgPath, "config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	fs.StringVar(&o.dbFlag, "db", "", "state db path (default ~/.genisys/genisys.db)")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8710", "listen address")
	fs.StringVar(&o.openDir, "cwd", "", "open this dir (or a file's dir) as project")
	fs.StringVar(&o.openFile, "file", "", "open this file in the editor")
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
