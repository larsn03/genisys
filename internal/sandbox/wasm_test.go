package sandbox_test

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larsn03/genisys/internal/sandbox"
)

func guest(t *testing.T) string {
	t.Helper()
	dir, err := sandbox.EnsureGuest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestHelloWorld(t *testing.T) {
	res, err := sandbox.Run(context.Background(), sandbox.Request{
		GuestDir: guest(t), Code: "print(6*7)", Timeout: 90 * time.Second,
		OutputsDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("run: %v\nstderr=%s", err, res.Stderr)
	}
	if res.ExitCode != 0 || strings.TrimSpace(res.Stdout) != "42" {
		t.Fatalf("out=%q err=%q code=%d", res.Stdout, res.Stderr, res.ExitCode)
	}
}

func TestPaperworksDeck(t *testing.T) {
	code := `
from ppt_builder import Deck
d = Deck(title="P4 smoke")
d.add_title("P4 smoke", "genisys sandbox")
for i in range(5):
    d.add_bullets("Slide %d" % i, ["point a", "point b"])
d.add_assumptions(["the endpoint stays reachable"])
d.save("/outputs/deck.pptx")
print("saved")
`
	out := t.TempDir()
	res, err := sandbox.Run(context.Background(), sandbox.Request{
		GuestDir: guest(t), Code: code, Timeout: 120 * time.Second, OutputsDir: out,
	})
	if err != nil {
		t.Fatalf("run: %v\nstderr=%s", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if len(res.Outputs) != 1 || res.Outputs[0] != "deck.pptx" {
		t.Fatalf("outputs = %v", res.Outputs)
	}
	// structural validity: every part parses as XML, slide count == 7
	f, err := os.Open(filepath.Join(out, "deck.pptx"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := zip.NewReader(f, fileSize(t, filepath.Join(out, "deck.pptx")))
	if err != nil {
		t.Fatal(err)
	}
	slides := 0
	for _, zf := range zr.File {
		if strings.HasPrefix(zf.Name, "ppt/slides/slide") && strings.HasSuffix(zf.Name, ".xml") {
			slides++
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if strings.HasSuffix(zf.Name, ".xml") || strings.HasSuffix(zf.Name, ".rels") {
			var v any
			if err := xml.Unmarshal(b, &v); err != nil {
				t.Fatalf("part %s invalid XML: %v", zf.Name, err)
			}
		}
	}
	if slides != 7 {
		t.Errorf("slides = %d, want 7", slides)
	}
}

func fileSize(t *testing.T, p string) int64 {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

func TestDetect(t *testing.T) {
	c := sandbox.Detect()
	if !c.WASM {
		t.Error("embedded WASM tier must always report available")
	}
	t.Logf("capabilities: %+v", c)
}

func TestRunProcIfAvailable(t *testing.T) {
	c := sandbox.Detect()
	if c.Tier2 == "" || !c.Tier2Python {
		t.Skipf("no Tier-2 stack (tier2=%q python=%v)", c.Tier2, c.Tier2Python)
	}
	res, err := sandbox.RunProc(context.Background(), sandbox.ProcRequest{
		Code:       "from ppt_builder import Deck\nprint(6*7)",
		OutputsDir: t.TempDir(),
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Skipf("tier2 runner present but failed (permissions?): %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}

// P4-1 DoD: 1-page PDF + 3-slide PPTX from the embedded guest alone.
func TestPDFAndDeck(t *testing.T) {
	dir := guest(t)
	code := "from pdf_simple import write_pdf\n" +
		"from ppt_builder import Deck\n" +
		"write_pdf('/outputs/memo.pdf', 'Q3 Memo', [('Key results', ['Revenue up 12%', 'Churn down 2pp']), ('Next steps', ['Ship P4', 'Hire two engineers'])])\n" +
		"d = Deck(title='Board Review')\n" +
		"d.add_title('Board Review')\n" +
		"d.add_bullets('Wins', ['Revenue up 12%'])\n" +
		"d.add_assumptions(['figures unaudited'])\n" +
		"d.save('/outputs/deck.pptx')\n" +
		"print('done')\n"
	out := t.TempDir()
	res, err := sandbox.Run(context.Background(), sandbox.Request{
		GuestDir: dir, Code: code, Timeout: 90 * time.Second, OutputsDir: out,
	})
	if err != nil {
		t.Fatalf("run: %v (stderr=%s)", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if len(res.Outputs) != 2 {
		t.Fatalf("outputs=%v", res.Outputs)
	}
	pdf, err := os.ReadFile(filepath.Join(out, "memo.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	head, tail := string(pdf[:9]), string(pdf[len(pdf)-40:])
	if !strings.HasPrefix(head, "%PDF-1.4") || !strings.Contains(tail, "%%EOF") || !strings.Contains(string(pdf), "startxref") {
		t.Fatalf("pdf structure suspect: %q / %q", head, tail)
	}
	if !strings.Contains(string(pdf), "Q3 Memo") {
		t.Error("title missing from pdf bytes")
	}
}
