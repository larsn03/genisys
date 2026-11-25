// search.go — `genisys search test`: live SearXNG round-trip
// (SPEC §9.5 reachability classification).
package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/larsn03/genisys/internal/apiconfig"
	"github.com/larsn03/genisys/internal/websearch"
)

func searchCmd(args []string) error {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	cfgPath := fs.String("config", filepath.Join(apiconfig.ExpandPath("~"), ".genisys", "config.yaml"), "app config path")
	// accept `search test --config …` and `search --config … test`
	var rest []string
	sub := ""
	for _, a := range args {
		if a == "test" && sub == "" {
			sub = a
			continue
		}
		rest = append(rest, a)
	}
	if sub == "" {
		return fmt.Errorf("usage: genisys search test [--config <path>]")
	}
	fs.Parse(rest)
	cfg, err := apiconfig.LoadApp(*cfgPath)
	if err != nil {
		return err
	}
	if !cfg.Web.SearchEnabled || cfg.Web.Provider != "searxng" {
		return fmt.Errorf("web search disabled (web.search_enabled: %v, provider: %s)", cfg.Web.SearchEnabled, cfg.Web.Provider)
	}
	c, err := websearch.New(websearch.Config{
		BaseURL:          cfg.Web.Searxng.BaseURL,
		Language:         cfg.Web.Searxng.Language,
		Categories:       cfg.Web.Searxng.Categories,
		TimeoutMs:        cfg.Web.Searxng.TimeoutMs,
		APIKeyEnv:        cfg.Web.Searxng.APIKeyEnv,
		AllowInsecureTLS: cfg.Web.Searxng.AllowInsecureTLS,
	})
	if err != nil {
		return err
	}
	status := c.Probe(context.Background())
	fmt.Printf("instance: %s\nstatus:   %s\n", cfg.Web.Searxng.BaseURL, status)
	switch status {
	case "ok":
		fmt.Println("JSON API works — web_search is ready.")
	case "json-disabled":
		fmt.Println(`fix: add "- json" under search.formats in the instance's settings.yml and restart it`)
	case "auth-required":
		fmt.Println("fix: export the token named by web.searxng.api_key_env")
	case "unreachable":
		fmt.Println("fix: check the instance is running and reachable from this machine")
	default:
		fmt.Println("fix: URL responds but is not a SearXNG JSON API")
	}
	return nil
}
