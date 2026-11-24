package agent

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"

	"github.com/larsn03/genisys/internal/model"
)

//go:embed templates/system.tmpl
var systemTmplText string

var systemTmpl = template.Must(template.New("system").Parse(systemTmplText))

// PromptEnv is the live state the system prompt renders from (SPEC §8:
// re-rendered every request, never cached in history).
type PromptEnv struct {
	Cwd        string
	Scratchpad string
	ToolsMode  string // native | xml | none (SPEC §13.4)
	Tools      []model.Tool
	Date       string
	Timezone   string
	OS         string
	OSVersion  string
	Username   string
	Home       string
	Desktop    string
}

// RenderSystem renders the system prompt (SPEC §8 budget: ≤ 6k tokens —
// enforced by TestPromptTokenBudget).
func RenderSystem(env PromptEnv) (string, error) {
	if env.Date == "" {
		env.Date = time.Now().Format("2006-01-02")
	}
	if env.OS == "" {
		env.OS = runtime.GOOS
	}
	if env.OSVersion == "" {
		env.OSVersion = runtime.GOARCH + "/" + runtime.Version()
	}
	if env.Username == "" {
		env.Username = os.Getenv("USER")
	}
	if env.Home == "" {
		env.Home, _ = os.UserHomeDir()
	}
	if env.Desktop == "" && env.Home != "" {
		env.Desktop = filepath.Join(env.Home, "Desktop")
	}
	if env.Timezone == "" {
		if loc, err := time.LoadLocation(""); err == nil && loc != nil {
			env.Timezone = loc.String()
		} else {
			env.Timezone = "UTC"
		}
	}
	if env.ToolsMode == "" {
		env.ToolsMode = "native"
	}
	var b strings.Builder
	if err := systemTmpl.Execute(&b, env); err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}
	return b.String(), nil
}

// osVersionGuess shells out (used by the CLI; cheap, cached by caller).
func OSVersionString() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		if data, err := os.ReadFile("/etc/os-release"); err == nil {
			for _, l := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(l, "PRETTY_NAME=") {
					return strings.Trim(strings.TrimPrefix(l, "PRETTY_NAME="), `"`)
				}
			}
		}
	case "windows":
		out, err := exec.Command("cmd", "/C", "ver").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return runtime.GOOS
}
