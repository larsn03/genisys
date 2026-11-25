// Package sandbox runs guest Python for run_python (SPEC §17.1).
// Tier 1: wazero + embedded CPython-WASI (this file's assets);
// Tier 2: OS-sandboxed subprocess (proc.go). Same /inputs /outputs
// contract for both.
package sandbox

import (
	"embed"
	"io/fs"
)

//go:embed all:assets
var assets embed.FS

//go:embed all:paperworks
var paperworks embed.FS

// cpythonZip returns the embedded CPython-WASI guest build
// (python.wasm + lib/python3.13, periareon/cpython-wasi-build v3.13.17,
// wasi_sdk-24 stock variant; pinned by IMPL P4-1).
func cpythonZip() ([]byte, error) {
	return assets.ReadFile("assets/cpython-wasi.zip")
}

func guestVersion() string { return "cpython-3.13.17-wasi_sdk-24" }

// PaperworksFiles returns the embedded Python library (name → source)
// mounted at /lib/paperworks in the guest (SPEC §17.1).
func PaperworksFiles() (map[string][]byte, error) {
	entries, err := fs.ReadDir(paperworks, "paperworks")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(paperworks, "paperworks/"+e.Name())
		if err != nil {
			return nil, err
		}
		out[e.Name()] = b
	}
	return out, nil
}
