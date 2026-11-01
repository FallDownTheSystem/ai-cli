package main

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// runConfig handles ai --config. With key=value arguments it sets them
// directly; on a terminal without arguments it opens a picker; otherwise it
// prints the file.
func runConfig(opts options) int {
	c, err := loadConfig()
	if err != nil {
		return fail(err)
	}

	if len(opts.assignments) > 0 {
		if err := applyAssignments(c, resolve(opts, c).provider, opts.assignments); err != nil {
			return fail(err)
		}
		if err := saveConfig(c); err != nil {
			return fail(err)
		}
		lipgloss.Printf("%s\n\n%s", dimStyle.Render(configPath()), c)
		return 0
	}

	if !isTerminal(os.Stdout) || !isTerminal(os.Stderr) {
		fmt.Printf("# %s\n%s", configPath(), c)
		return 0
	}

	m := newConfigModel(c)
	if _, err := tea.NewProgram(m, tea.WithOutput(os.Stderr)).Run(); err != nil {
		return fail(err)
	}
	if !m.saved {
		return exitCancelled
	}
	return 0
}

// applyAssignments sets key=value pairs. A bare model= or path= belongs to the
// provider chosen in the same command, or else to the current provider.
func applyAssignments(c config, current string, assignments []string) error {
	provider := current
	for _, a := range assignments {
		if k, v, ok := strings.Cut(a, "="); ok && strings.TrimSpace(k) == "provider" {
			provider = strings.ToLower(strings.TrimSpace(v))
		}
	}
	for _, a := range assignments {
		key, value, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", a)
		}
		key = strings.TrimSpace(key)
		switch key {
		case "model", "path":
			key = providerKey(provider, key)
		}
		if err := c.set(key, strings.TrimSpace(value)); err != nil {
			return err
		}
	}
	return nil
}

type configModel struct {
	cfg config
	// shell is the shell ai detects when the file names none.
	shell string
	// shells are looked up on PATH once, since a lookup per keypress is slow.
	shells  []string
	row     int
	width   int
	editing bool
	input   textinput.Model
	err     string
	saved   bool
	final   string
}

func newConfigModel(c config) *configModel {
	in := textinput.New()
	in.Prompt = ""
	in.SetWidth(60)
	return &configModel{
		cfg:    c,
		shell:  selectShell(unsetValue("shell")).name,
		shells: shellChoices(),
		input:  in,
		width:  terminalWidth(),
	}
}

// rows are the picker's rows: every key, with only the selected provider's model.
func (m *configModel) rows() []keySpec {
	selected := m.provider().name
	return slices.DeleteFunc(slices.Clone(keySpecs), func(k keySpec) bool {
		return k.field == "model" && k.provider != selected
	})
}

func (m *configModel) current() keySpec { return m.rows()[m.row] }

// provider is the provider the file selects, which the model and effort rows
// follow. An unknown name from the environment shows as the default here; a
// run reports it.
func (m *configModel) provider() providerSpec {
	spec, err := lookupProvider(m.cfg.value("provider"))
	if err != nil {
		spec, _ = lookupProvider(defaultProvider)
	}
	return spec
}

func (m *configModel) model() string {
	return m.cfg.value(providerKey(m.provider().name, "model"))
}

// effortSupported reports whether the selected model accepts the effort, which
// can stop being true when the model changes after the effort was picked.
func (m *configModel) effortSupported() bool {
	return slices.Contains(m.provider().efforts(m.model()), m.cfg.value("effort"))
}

// choices are the values ←/→ cycles through, or nil for a row that is typed.
func (m *configModel) choices(k keySpec) []string {
	var list []string
	switch {
	case k.kind == providerValue:
		list = providerNames()
	case k.kind == boolValue:
		list = []string{"true", "false"}
	case k.field == "model":
		list = m.provider().models
	case k.key == "effort":
		list = m.provider().efforts(m.model())
	case k.key == "shell":
		list = m.shells
	case k.key == "style":
		list = markdownStyles
	default:
		return nil
	}
	// "" is the default; a hand-written value stays reachable while cycling.
	list = append([]string{""}, list...)
	if current := m.cfg[k.key]; !slices.Contains(list, current) {
		list = append(list, current)
	}
	return list
}

// rowDefault is the value a row shows as its default when the file leaves it unset.
func (m *configModel) rowDefault(k keySpec) string {
	switch {
	case k.key == "shell":
		return m.shell
	case k.field == "path":
		return cmp.Or(unsetValue(k.key), "found on PATH")
	}
	return cmp.Or(unsetValue(k.key), "none")
}

func (m *configModel) display(k keySpec) string {
	if v := m.cfg[k.key]; v != "" {
		return v
	}
	return "default (" + m.rowDefault(k) + ")"
}

func (m *configModel) Init() tea.Cmd { return nil }

func (m *configModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = size.Width
		m.input.SetWidth(max(size.Width-24, 20))
		return m, nil
	}
	key, isKey := msg.(tea.KeyPressMsg)
	if isKey && key.String() == "ctrl+c" {
		return m.cancel()
	}
	if m.editing {
		return m.updateEdit(msg)
	}
	if !isKey {
		return m, nil
	}
	k := m.current()
	rows := len(m.rows())
	m.err = ""
	switch key.String() {
	case "up", "k", "shift+tab":
		m.row = (m.row + rows - 1) % rows
	case "down", "j", "tab":
		m.row = (m.row + 1) % rows
	case "left", "h":
		m.change(k, -1)
	case "right", "l", "space":
		m.change(k, 1)
	case "backspace", "delete":
		m.set(k, "")
	case "enter":
		if m.choices(k) != nil {
			return m.save()
		}
		return m.startEdit(k)
	case "ctrl+s", "s":
		return m.save()
	case "esc", "q":
		return m.cancel()
	}
	return m, nil
}

func (m *configModel) startEdit(k keySpec) (tea.Model, tea.Cmd) {
	m.editing = true
	m.input.SetValue(m.cfg[k.key])
	m.input.Placeholder = m.rowDefault(k)
	m.input.CursorEnd()
	return m, m.input.Focus()
}

func (m *configModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.editing = false
			m.err = ""
			m.input.Blur()
			return m, nil
		case "enter":
			m.set(m.current(), unquote(strings.TrimSpace(m.input.Value())))
			if m.err == "" {
				m.editing = false
				m.input.Blur()
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// unquote drops the quotes around a value. Explorer's "Copy as path" puts them
// around a path, and they would end up in the file.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func (m *configModel) set(k keySpec, value string) {
	m.err = ""
	if err := m.cfg.set(k.key, value); err != nil {
		m.err = err.Error()
	}
}

// change moves a row's value one place in direction, -1 or 1.
func (m *configModel) change(k keySpec, direction int) {
	if list := m.choices(k); list != nil {
		i := slices.Index(list, m.cfg[k.key])
		m.set(k, list[(i+direction+len(list))%len(list)])
		return
	}
	if k.kind == numberValue {
		n, _ := strconv.Atoi(m.cfg.value(k.key))
		m.set(k, strconv.Itoa(max(n+direction*k.step, 1)))
	}
}

func (m *configModel) save() (tea.Model, tea.Cmd) {
	if err := saveConfig(m.cfg); err != nil {
		m.final = errorStyle.Render("ai: " + err.Error())
		return m, tea.Quit
	}
	m.saved = true
	summary := fmt.Sprintf("%s · %s · effort %s", m.provider().name, m.model(), m.cfg.value("effort"))
	m.final = fmt.Sprintf("%s %s %s", promptMark, summary, dimStyle.Render("saved to "+shortPath(configPath())))
	return m, tea.Quit
}

func (m *configModel) cancel() (tea.Model, tea.Cmd) {
	m.final = cancelledNote
	return m, tea.Quit
}

func (m *configModel) View() tea.View {
	if m.final != "" {
		return tea.NewView(m.final + "\n")
	}
	var b strings.Builder
	header := lipgloss.NewStyle().Bold(true).Render("ai settings") + " " + dimStyle.Render(shortPath(configPath()))
	b.WriteString(truncateLine(header, m.width) + "\n")
	for i, k := range m.rows() {
		marker := "  "
		if i == m.row {
			marker = promptMark + " "
		}
		// Instructions can run longer than the terminal is wide, and a wrapped row
		// would push the rows below out of place.
		b.WriteString(truncateLine(fmt.Sprintf("%s%-17s %s", marker, k.key, m.rowValue(k, i == m.row)), m.width) + "\n")
	}
	if m.err != "" {
		b.WriteString(errorStyle.Render(m.err) + "\n")
	}
	b.WriteString(dimStyle.Render(m.help()))
	return tea.NewView(b.String())
}

// rowValue is a row's value as the picker shows it: dim when it is the default,
// and on the selected row as the editor, or with arrows when ←/→ cycles it.
func (m *configModel) rowValue(k keySpec, selected bool) string {
	var value string
	switch {
	case selected && m.editing:
		value = m.input.View()
	case selected && m.choices(k) != nil:
		value = keyStyle.Render("‹ " + m.display(k) + " ›")
	case selected:
		value = keyStyle.Render(m.display(k))
	case m.cfg[k.key] == "":
		value = dimStyle.Render(m.display(k))
	default:
		value = m.display(k)
	}
	if k.key == "effort" && !m.effortSupported() {
		value += " " + warnStyle.Render("not supported by "+m.model())
	}
	return value
}

func (m *configModel) help() string {
	k := m.current()
	switch {
	case m.editing:
		return "enter set · empty for default · esc back"
	case k.kind == numberValue:
		return "←/→ change · enter type · ⌫ default · ↑/↓ move · s save · esc cancel"
	case m.choices(k) == nil:
		return "enter type · ⌫ default · ↑/↓ move · s save · esc cancel"
	}
	return "←/→ change · ⌫ default · ↑/↓ move · enter save · esc cancel"
}
