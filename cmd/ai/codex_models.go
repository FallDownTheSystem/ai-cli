package main

import (
	"cmp"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// codexAlias is a model family ai offers. Codex has no aliases of its own, so
// codexCatalog.slug resolves them to the newest model of the family.
type codexAlias struct {
	name string
	// fallbackSlug is the model used before Codex has written its model cache.
	fallbackSlug string
}

var codexAliases = []codexAlias{
	{"luna", "gpt-6-luna"},
	{"terra", "gpt-6.1-terra"},
	{"sol", "gpt-6.1-sol"},
	{"astra", "gpt-6-astra"},
}

func codexAliasNames() []string {
	names := make([]string, len(codexAliases))
	for i, a := range codexAliases {
		names[i] = a.name
	}
	return names
}

// codexCatalog is the list of models Codex keeps fresh in its cache, newest
// first, so new models and efforts are picked up without a release of ai.
type codexCatalog []codexModelInfo

type codexModelInfo struct {
	Slug       string `json:"slug"`
	Visibility string `json:"visibility"`
	Priority   int    `json:"priority"`
	Levels     []struct {
		Effort string `json:"effort"`
	} `json:"supported_reasoning_levels"`
	// raw is the whole entry, including the fields ai does not read, so it can
	// go back to Codex unchanged apart from the fields ai overrides.
	raw json.RawMessage
}

func (m *codexModelInfo) UnmarshalJSON(data []byte) error {
	type fields codexModelInfo
	if err := json.Unmarshal(data, (*fields)(m)); err != nil {
		return err
	}
	m.raw = slices.Clone(data)
	return nil
}

// loadCodexCatalog reads the cache once per process; the settings picker asks
// for efforts on every frame.
var loadCodexCatalog = sync.OnceValue(readCodexCatalog)

func readCodexCatalog() codexCatalog {
	data, err := os.ReadFile(filepath.Join(codexHome(), "models_cache.json"))
	if err != nil {
		return nil
	}
	var cache struct {
		Models codexCatalog `json:"models"`
	}
	if json.Unmarshal(data, &cache) != nil {
		return nil
	}
	slices.SortStableFunc(cache.Models, func(a, b codexModelInfo) int { return cmp.Compare(a.Priority, b.Priority) })
	return slices.DeleteFunc(cache.Models, func(m codexModelInfo) bool { return m.Visibility != "list" })
}

// slug turns an alias into the newest listed model of that family. Full model
// names pass through unchanged.
func (c codexCatalog) slug(model string) string {
	i := slices.IndexFunc(codexAliases, func(a codexAlias) bool { return a.name == model })
	if i < 0 {
		return model
	}
	for _, m := range c {
		if strings.HasSuffix(m.Slug, "-"+model) {
			return m.Slug
		}
	}
	return codexAliases[i].fallbackSlug
}

// efforts are the efforts a model accepts, which differ by model: some add
// max, and some add ultra on top of it.
func (c codexCatalog) efforts(model string) []string {
	slug := c.slug(model)
	for _, m := range c {
		if m.Slug == slug && len(m.Levels) > 0 {
			efforts := make([]string, len(m.Levels))
			for i, l := range m.Levels {
				efforts[i] = l.Effort
			}
			return efforts
		}
	}
	return []string{"low", "medium", "high", "xhigh"}
}

// directCatalog is a model catalog that holds only the model, changed to send
// no tool that ai does not ask for. As the catalog sets it, a model can wrap
// every tool in a JavaScript exec tool, which costs over 2,000 input tokens on
// every request, even with no tool enabled; that setting overrides every
// feature flag. It reports false for a model the cache does not list.
func (c codexCatalog) directCatalog(slug string) ([]byte, bool) {
	i := slices.IndexFunc(c, func(m codexModelInfo) bool { return m.Slug == slug })
	if i < 0 {
		return nil, false
	}
	var entry map[string]any
	if json.Unmarshal(c[i].raw, &entry) != nil {
		return nil, false
	}
	entry["tool_mode"] = "direct"
	entry["apply_patch_tool_type"] = nil
	entry["experimental_supported_tools"] = []string{}
	data, err := json.Marshal(map[string]any{"models": []any{entry}})
	return data, err == nil
}

func codexHome() string {
	return cmp.Or(os.Getenv("CODEX_HOME"), filepath.Join(homeDir(), ".codex"))
}
