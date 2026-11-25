// attach.go — load_file_attachment (SPEC §9.6): pull an attached or
// scratchpad file into the conversation as text. docx is extracted
// natively (zip+XML); xlsx/pdf point at run_python's paperworks.

package py

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/larsn03/genisys/internal/tools"
)

// LoadAttachment implements tools.Tool.
type LoadAttachment struct{}

// Name is the model-visible tool name.
func (LoadAttachment) Name() string { return "load_file_attachment" }

// Description explains what the tool returns.
func (LoadAttachment) Description() string {
	return `Load an attached or scratchpad file as text. Text/markdown/csv/json are read directly; .docx paragraphs are extracted; for .xlsx/.pdf/.pptx the result tells you to use run_python (paperworks) instead.`
}

// Schema returns the JSON schema.
func (LoadAttachment) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
		"path":{"type":"string","description":"Attachment path (as shown in [attached files]) or any scratchpad/workspace path"},
		"max_chars":{"type":"integer","description":"Cap on returned characters (default 40000)"}
	},"required":["path"]}`)
}

// Execute loads the file.
func (LoadAttachment) Execute(_ context.Context, raw json.RawMessage, w *tools.World) tools.Result {
	var a struct {
		Path     string `json:"path"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal(raw, &a); err != nil || a.Path == "" {
		return errRes("load_file_attachment: path is required")
	}
	if a.MaxChars <= 0 {
		a.MaxChars = 40000
	}
	p, err := resolveAny(w, a.Path)
	if err != nil {
		return errRes("load_file_attachment: " + err.Error())
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".docx":
		text, err := docxText(p)
		if err != nil {
			return errRes("load_file_attachment: " + err.Error())
		}
		return clipRes(text, a.MaxChars)
	case ".xlsx", ".pdf", ".pptx":
		return tools.Result{Content: fmt.Sprintf(
			"%s is a %s document (%d bytes). Read it with run_python: "+
				"xlsx → from xlsx_helpers import read_xlsx; read_xlsx(%q); "+
				"pdf → pypdf is unavailable in the WASM guest, so ask for a text/pdf variant. "+
				"Stage it first: stage_files=[%q], then open /inputs/%s",
			filepath.Base(p), strings.TrimPrefix(ext, "."), fileSizeOf(p), p, p, filepath.Base(p))}
	default:
		data, err := os.ReadFile(p)
		if err != nil {
			return errRes("load_file_attachment: " + err.Error())
		}
		if !isLikelyText(data) {
			return errRes(fmt.Sprintf("load_file_attachment: %s is binary (%s); use run_python", filepath.Base(p), ext))
		}
		return clipRes(string(data), a.MaxChars)
	}
}

func clipRes(text string, max int) tools.Result {
	if len(text) > max {
		return tools.Result{Content: text[:max] + fmt.Sprintf("\n[… truncated at %d chars]", max)}
	}
	return tools.Result{Content: text}
}

// docxText extracts paragraph text from word/document.xml.
func docxText(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		data, err := io.ReadAll(io.LimitReader(rc, 32<<20))
		if err != nil {
			return "", err
		}
		return xmlParagraphs(string(data)), nil
	}
	return "", fmt.Errorf("no word/document.xml — not a .docx")
}

// xmlParagraphs is a tag-soup extraction: <w:p> → newline, <w:t> kept.
func xmlParagraphs(doc string) string {
	var b strings.Builder
	var inT bool
	for i := 0; i < len(doc); {
		if doc[i] != '<' {
			if inT {
				b.WriteByte(doc[i])
			}
			i++
			continue
		}
		end := strings.IndexByte(doc[i:], '>')
		if end < 0 {
			break
		}
		tag := doc[i+1 : i+end]
		switch {
		case strings.HasPrefix(tag, "w:t"):
			inT = !strings.HasSuffix(tag, "/")
		case strings.HasPrefix(tag, "/w:t"):
			inT = false
		case strings.HasPrefix(tag, "w:p") && !strings.HasPrefix(tag, "w:pPr") && !strings.HasPrefix(tag, "w:pict"):
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
				b.WriteByte('\n')
			}
		}
		i += end + 1
	}
	return strings.TrimSpace(b.String())
}

func isLikelyText(b []byte) bool {
	n := min(len(b), 1024)
	bad := 0
	for _, c := range b[:n] {
		if c < 9 || (c > 13 && c < 32) {
			bad++
		}
	}
	return n == 0 || bad*20 < n // <5% control chars
}

func fileSizeOf(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return -1
}
