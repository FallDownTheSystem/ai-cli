package main

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// config is the file at configPath, keyed by the names ai --config takes, such
// as "effort" or "codex.model". It holds preferences only, never conversation
// state.
type config map[string]string

// keySpec describes one key of the config file.
type keySpec struct {
	key  string
	kind valueKind
	// provider and field are set for a key that each provider keeps for itself,
	// such as "codex.model": provider "codex", field "model".
	provider string
	field    string
	// env fills in when the file leaves the key unset, and def after that.
	env string
	def string
	// step is how far ←/→ moves a number in the settings picker.
	step int
}

type valueKind int

const (
	textValue valueKind = iota
	numberValue
	boolValue
	providerValue
)

const defaultProvider = "codex"

// modelEnv names a model of the provider that AI_PROVIDER selects.
const modelEnv = "AI_MODEL"

// keySpecs lists every key, in the order the settings picker shows them.
var keySpecs = func() []keySpec {
	list := []keySpec{{key: "provider", kind: providerValue, env: "AI_PROVIDER", def: defaultProvider}}
	for _, p := range providers {
		list = append(list, providerKeySpec(p.name, "model", modelEnv, p.defaultModel))
	}
	list = append(list,
		keySpec{key: "effort", env: "AI_EFFORT", def: "low"},
		keySpec{key: "instructions", env: "AI_INSTRUCTIONS"},
		keySpec{key: "shell", env: "AI_SHELL"},
		keySpec{key: "style", env: "GLAMOUR_STYLE", def: "dark"},
		keySpec{key: "stream", kind: boolValue, env: "AI_STREAM", def: "true"},
		keySpec{key: "context_commands", kind: numberValue, env: "AI_CONTEXT_COMMANDS", def: "5", step: 1},
		keySpec{key: "context_lines", kind: numberValue, env: "AI_CONTEXT_LINES", def: "500", step: 100},
	)
	for _, p := range providers {
		list = append(list, providerKeySpec(p.name, "path", "AI_"+strings.ToUpper(p.name)+"_PATH", ""))
	}
	return list
}()

func providerKeySpec(provider, field, env, def string) keySpec {
	return keySpec{key: providerKey(provider, field), provider: provider, field: field, env: env, def: def}
}

// providerKey names a key that each provider keeps for itself, such as
// "codex.model", so that switching provider never sends one vendor's model
// name to the other.
func providerKey(provider, field string) string { return provider + "." + field }

func lookupKeySpec(key string) (keySpec, error) {
	i := slices.IndexFunc(keySpecs, func(k keySpec) bool { return k.key == key })
	if i < 0 {
		return keySpec{}, fmt.Errorf("unknown setting %q", key)
	}
	return keySpecs[i], nil
}

// parse checks a value against the key's kind and returns it in the form the
// file keeps. "" means unset and is always valid.
func (k keySpec) parse(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch k.kind {
	case numberValue:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return "", fmt.Errorf("%s must be a positive number", k.key)
		}
		return strconv.Itoa(n), nil
	case boolValue:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return "", fmt.Errorf("%s must be true or false", k.key)
		}
		return strconv.FormatBool(b), nil
	case providerValue:
		return strings.ToLower(value), nil
	}
	return value, nil
}

// envVar is the variable that fills in for the key. AI_MODEL belongs to one
// provider only, so it never sends one vendor's model name to the other.
func (k keySpec) envVar() string {
	if k.field == "model" && k.provider != unsetValue("provider") {
		return ""
	}
	return k.env
}

// set applies one key; the same keys work in the file and in ai --config key=value.
func (c config) set(key, value string) error {
	k, err := lookupKeySpec(key)
	if err != nil {
		return err
	}
	if value, err = k.parse(value); err != nil {
		return err
	}
	if value == "" {
		delete(c, key)
		return nil
	}
	if k.kind == providerValue {
		if _, err := lookupProvider(value); err != nil {
			return err
		}
	}
	c[key] = value
	return nil
}

// value is what a key resolves to without flags: the file, then unsetValue.
func (c config) value(key string) string {
	return cmp.Or(c[key], unsetValue(key))
}

// unsetValue is what a key takes when the file leaves it unset: its
// environment variable, then the built-in default. A variable that does not
// parse counts as unset; an unknown provider name is left for newProvider to report.
func unsetValue(key string) string {
	k, err := lookupKeySpec(key)
	if err != nil {
		return ""
	}
	if v, err := k.parse(os.Getenv(k.envVar())); err == nil && v != "" {
		return v
	}
	return k.def
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// configHome is where programs keep their settings on Unix, and where ai keeps
// its own on every system.
func configHome() string {
	return cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(homeDir(), ".config"))
}

func configPath() string { return filepath.Join(configHome(), "ai", "config.toml") }

func loadConfig() (config, error) {
	c := config{}
	var table map[string]any
	_, err := toml.DecodeFile(configPath(), &table)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err == nil {
		err = c.setTable("", table)
	}
	if err != nil {
		return c, fmt.Errorf("%s: %w", configPath(), err)
	}
	return c, nil
}

// setTable applies a decoded TOML table through set, so the file and
// ai --config key=value share one set of keys and checks.
func (c config) setTable(prefix string, table map[string]any) error {
	for _, name := range slices.Sorted(maps.Keys(table)) {
		key := prefix + name
		var err error
		switch v := table[name].(type) {
		case map[string]any:
			if prefix != "" || !slices.Contains(providerNames(), name) {
				return fmt.Errorf("unknown section [%s]", key)
			}
			err = c.setTable(key+".", v)
		case string:
			err = c.set(key, v)
		case int64, bool:
			err = c.set(key, fmt.Sprint(v))
		default:
			err = fmt.Errorf("%s has a value of the wrong type", key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// String writes the file: top-level keys first, then a section per provider.
func (c config) String() string {
	var b strings.Builder
	b.WriteString("# Settings for ai. Flags override these; environment variables fill in what is unset.\n")
	write := func(name string, k keySpec) {
		value := c[k.key]
		if k.kind == textValue || k.kind == providerValue {
			value = tomlString(value)
		}
		fmt.Fprintf(&b, "%s = %s\n", name, value)
	}
	set := func(provider string) []keySpec {
		return slices.DeleteFunc(slices.Clone(keySpecs), func(k keySpec) bool {
			_, ok := c[k.key]
			return !ok || k.provider != provider
		})
	}
	for _, k := range set("") {
		write(k.key, k)
	}
	for _, p := range providers {
		section := set(p.name)
		if len(section) > 0 {
			fmt.Fprintf(&b, "\n[%s]\n", p.name)
		}
		for _, k := range section {
			write(k.field, k)
		}
	}
	return b.String()
}

func saveConfig(c config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(c.String()), 0o644)
}

// settings is the resolved configuration for one run.
type settings struct {
	provider        string
	model           string
	binPath         string
	effort          string
	instructions    string
	shell           string
	style           string
	stream          bool
	contextLines    int
	contextCommands int
}

// resolve picks each value from the first source that sets it: the flag, the
// file, the environment, then the built-in default.
func resolve(opts options, c config) settings {
	provider := cmp.Or(opts.provider, c.value("provider"))
	number := func(key string) int {
		n, _ := strconv.Atoi(c.value(key))
		return n
	}
	return settings{
		provider:        provider,
		model:           cmp.Or(opts.model, c.value(providerKey(provider, "model"))),
		binPath:         c.value(providerKey(provider, "path")),
		effort:          c.value("effort"),
		instructions:    c.value("instructions"),
		shell:           c.value("shell"),
		style:           c.value("style"),
		stream:          cmp.Or(opts.stream, c.value("stream")) == "true",
		contextLines:    number("context_lines"),
		contextCommands: number("context_commands"),
	}
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
