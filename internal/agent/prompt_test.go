package agent

import (
	"strings"
	"testing"

	tiktoken "github.com/pkoukk/tiktoken-go"

	"github.com/larsn03/genisys/internal/skills"
	"github.com/larsn03/genisys/internal/tools"
	exectool "github.com/larsn03/genisys/internal/tools/exec"
	"github.com/larsn03/genisys/internal/tools/fileio"
	"github.com/larsn03/genisys/internal/tools/search"
)

func sixTools() *tools.Registry {
	r := tools.NewRegistry()
	r.Add(fileio.ReadLines{})
	r.Add(fileio.ReadChars{})
	r.Add(fileio.ListDir{})
	r.Add(search.SearchLine{})
	r.Add(search.FindFiles{})
	r.Add(exectool.Shell{})
	return r
}

func TestPromptStructure(t *testing.T) {
	env := PromptEnv{Cwd: "/home/u/proj", Scratchpad: "/tmp/scratch", ToolsMode: "native"}
	env.Tools = sixTools().Definitions()
	got, err := RenderSystem(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"You are an agent running inside Genisys",
		"# Coding", "## Git", "# Communication",
		"<workspace>", "<location>", "genisys/",
		"<environment>", "<cwd>/home/u/proj</cwd>",
		"#### `read_file_lines`", "#### `shell_command`",
		"function-calling interface",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// XML mode swaps the invocation section
	xml, _ := RenderSystem(PromptEnv{Cwd: "/x", ToolsMode: "xml", Tools: env.Tools})
	if !strings.Contains(xml, "<tool_name>") || strings.Contains(xml, "function-calling interface") {
		t.Error("xml mode invocation section wrong")
	}
}

// P1-7 DoD: rendered prompt ≤ 6k tokens including tool schemas (SPEC §8).
func TestPromptTokenBudget(t *testing.T) {
	env := PromptEnv{Cwd: "/home/u/proj", Scratchpad: "/tmp/scratch", ToolsMode: "xml"}
	env.Tools = sixTools().Definitions()
	got, err := RenderSystem(env)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := tiktoken.GetEncoding("cl100k_base")
	if err != nil {
		t.Skipf("tiktoken unavailable: %v", err)
	}
	n := len(enc.Encode(got, nil, nil))
	t.Logf("system prompt tokens (xml mode, 6 tools): %d", n)
	if n > 6000 {
		t.Errorf("prompt budget exceeded: %d > 6000 (SPEC §8)", n)
	}
}

func TestWorkPersonaBlock(t *testing.T) {
	env := PromptEnv{Cwd: "/home/u/proj", Scratchpad: "/tmp/scratch", ToolsMode: "native"}
	env.Tools = sixTools().Definitions()
	env.Persona = "work"
	out, err := RenderSystem(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"paperworks", "add_assumptions", "deliver_file_to_user"} {
		if !strings.Contains(out, want) {
			t.Errorf("work persona missing %q", want)
		}
	}
	// coder default must NOT carry the work block (budget + focus)
	env.Persona = ""
	out2, err := RenderSystem(env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "add_assumptions") {
		t.Error("coder persona unexpectedly carries the work block")
	}
}

func TestAvailableSkillsBlock(t *testing.T) {
	env := PromptEnv{Cwd: "/p", Scratchpad: "/s", ToolsMode: "native"}
	env.Tools = sixTools().Definitions()
	if out, _ := RenderSystem(env); strings.Contains(out, "available-skills") {
		t.Error("empty skills must render no block")
	}
	env.Skills = []skills.Skill{
		{Name: "pdf-forms", Description: "Fill PDF forms.", Path: "/home/u/.genisys/skills/pdf-forms/SKILL.md"},
		{Name: "skill-management", Description: "Create skills.", Path: "/b/SKILL.md", Builtin: true},
	}
	out, err := RenderSystem(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<available-skills>", "pdf-forms: Fill PDF forms. (/home/u/.genisys/skills/pdf-forms/SKILL.md)", "read_file_lines FIRST"} {
		if !strings.Contains(out, want) {
			t.Errorf("skills block missing %q", want)
		}
	}
}
