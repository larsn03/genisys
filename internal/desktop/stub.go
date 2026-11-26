//go:build !wails

package desktop

import "fmt"

// Controls are the native window actions (see desktop.go); the stub
// never fills them.
type Controls struct {
	Minimize       func()
	ToggleMaximize func()
	IsMaximized    func() bool
	Close          func()
}

// Options configures the main window (see desktop.go).
type Options struct {
	Title string
	URL   string
	Ctl   *Controls
}

// ErrNotBuilt is returned by Run in binaries compiled without the
// `wails` build tag — those ship the browser flow only (SPEC G12:
// CGO-free single binary).
var ErrNotBuilt = fmt.Errorf("built without native window support: rebuild with `go build -tags wails` (CGO on) or use `genisys serve`")

// Run always fails in the stub build.
func Run(o Options) error { return ErrNotBuilt }
