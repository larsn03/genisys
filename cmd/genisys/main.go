// Command genisys is the single Genisys binary (SPEC G12): serve (default, P3),
// run (P1 CLI agent), revert (P2), and config tooling (P0-2).
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/larsn03/genisys/internal/apiconfig"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "genisys:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "version":
		fmt.Println("genisys", version)
		return nil
	case "config":
		return configCmd(args[1:])
	case "run", "serve", "revert":
		return fmt.Errorf("%s is not implemented yet (see docs/IMPLEMENTATION.md)", args[0])
	case "help", "-h", "--help":
		return usage()
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() error {
	fmt.Fprint(os.Stderr, `genisys — agent harness for OpenAI-compatible endpoints (SPEC §18)

usage:
  genisys config validate [--config <path>]   lint all YAML levels (file:line)
  genisys config show     [--config <path>]   print effective app config
  genisys version
  genisys run|serve|revert                    (upcoming milestones)
`)
	return nil
}

func configCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: need 'validate' or 'show'")
	}
	sub := args[0]
	fs := flag.NewFlagSet("config "+sub, flag.ExitOnError)
	cfgPath := fs.String("config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "path to config.yaml")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := apiconfig.LoadApp(*cfgPath)
	issues := apiconfig.AsIssues(err)
	if cfg == nil {
		if len(issues) == 0 {
			issues = apiconfig.Issues{apiconfig.Issue{File: *cfgPath, Msg: "config could not be loaded"}}
		}
		for _, is := range issues {
			fmt.Fprintln(os.Stderr, is)
		}
		return fmt.Errorf("config: %d issue(s)", len(issues))
	}

	switch sub {
	case "validate":
		all := apiconfig.ValidateAll(cfg, *cfgPath)
		all = append(issues, all...)
		if len(all) == 0 {
			fmt.Printf("OK: %s (and project/session levels) valid\n", *cfgPath)
			fmt.Printf("  resolved default model: %s\n", cfg.DefaultModel)
			return nil
		}
		for _, is := range all {
			fmt.Fprintln(os.Stderr, is)
		}
		return fmt.Errorf("config: %d issue(s)", len(all))
	case "show":
		out, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		os.Stdout.Write(out)
		fmt.Printf("# effective default model: %s\n", cfg.DefaultModel)
		return nil
	default:
		return fmt.Errorf("config: unknown subcommand %q", sub)
	}
}
