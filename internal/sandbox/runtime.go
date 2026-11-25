// runtime.go — one-time extraction of the embedded CPython-WASI guest
// into the user's runtime cache (~/.genisys/runtime/<version>/).

package sandbox

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// EnsureGuest extracts the embedded guest build into root/<version>/ if
// not present yet and returns the install dir (contains python.wasm +
// lib/python3.13).
func EnsureGuest(root string) (string, error) {
	dir := filepath.Join(root, guestVersion())
	marker := filepath.Join(dir, ".genisys-ok")
	if _, err := os.Stat(marker); err == nil {
		return dir, nil
	}
	data, err := cpythonZip()
	if err != nil {
		return "", err
	}
	tmp := dir + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return "", err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", err
	}
	if err := extractZip(tmp, data); err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	// paperworks rides along inside the guest install dir
	pws, err := PaperworksFiles()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(tmp, "paperworks"), 0o755); err != nil {
		return "", err
	}
	for name, src := range pws {
		if err := os.WriteFile(filepath.Join(tmp, "paperworks", name), src, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil { // atomic-ish publish
		return "", err
	}
	return dir, os.WriteFile(marker, []byte("ok"), 0o644)
}

func extractZip(dst string, data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		// no traversal, no absolute paths
		name := filepath.Clean(f.Name)
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			continue
		}
		p := filepath.Join(dst, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return fmt.Errorf("extract %s: %w", f.Name, err)
		}
	}
	return nil
}
