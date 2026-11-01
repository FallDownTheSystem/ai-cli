package main

import (
	"regexp"
	"strings"
)

// promptEnds are the characters prompts commonly end with.
const promptEnds = ">$#%❯➜λ»"

var aiCommand = regexp.MustCompile(`^(?:\S*[\\/])?ai(?:\.exe)?(?:\s|$)`)

// recentHistory trims a terminal capture to what a follow-up needs: everything
// since the depth-th most recent ai command that started a new topic. An
// ai -c command only continued an earlier one, so the search goes past it and
// a chain of follow-ups stays whole. Each level of depth that finds no ai
// topic adds commandsPerLevel plain commands instead. The current invocation is
// dropped, because its question is sent separately.
func recentHistory(text string, depth, commandsPerLevel int) string {
	lines := strings.Split(text, "\n")

	current, shellPrompt := findCurrent(lines)
	if shellPrompt == nil {
		return text
	}

	// commandLines holds the line of every command, newest first; topics counts
	// the commands that started an ai topic, and lastTopic is where the oldest
	// of those sits in commandLines.
	var commandLines []int
	topics, lastTopic := 0, -1
	for i := current - 1; i >= 0; i-- {
		cmd, ok := commandOn(shellPrompt, lines[i])
		if !ok {
			continue
		}
		commandLines = append(commandLines, i)
		if ai, ok := aiInvocation(cmd); ok && !continues(ai) {
			topics++
			lastTopic = len(commandLines) - 1
			if topics == depth {
				return join(lines[i:current])
			}
		}
	}

	if len(commandLines) == 0 {
		return join(lines[:current])
	}
	older := commandLines[lastTopic+1:]
	if len(older) == 0 {
		return join(lines[commandLines[lastTopic]:current])
	}
	want := min((depth-topics)*commandsPerLevel, len(older))
	return join(lines[older[want-1]:current])
}

func join(lines []string) string {
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// findCurrent locates the line where the user typed this ai command. It sits
// near the bottom, but a long command can wrap onto the following lines.
func findCurrent(lines []string) (int, *regexp.Regexp) {
	for i, seen := len(lines)-1, 0; i >= 0 && seen < 6; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if prefix, ok := currentPrompt(lines[i]); ok {
			return i, promptPattern(prefix)
		}
	}
	return len(lines), nil
}

// currentPrompt finds the invocation with a loose rule: the first prompt-ending
// character whose remaining text runs ai, such as "PS C:\x> git diff | ai -c".
// The prefix then teaches the exact prompt shape, which is far less likely to
// match lines of program output.
func currentPrompt(line string) (string, bool) {
	for i, r := range line {
		if !strings.ContainsRune(promptEnds, r) {
			continue
		}
		end := i + len(string(r))
		if _, ok := aiInvocation(strings.TrimSpace(line[end:])); ok {
			return line[:end], true
		}
	}
	return "", false
}

// promptPattern generalises one prompt into a pattern for every prompt: the
// first word stays fixed and the end is the first terminator after it, so a
// prompt that shows the working directory still matches after a cd.
func promptPattern(prefix string) *regexp.Regexp {
	prefix = strings.TrimRight(prefix, " ")
	runes := []rune(prefix)
	terminator := regexp.QuoteMeta(string(runes[len(runes)-1]))
	if len(runes) == 1 {
		return regexp.MustCompile(`^` + terminator + `\s?(.*)$`)
	}
	// "PS C:\x>" keeps "PS"; "user@host:~/dir$" keeps "user@host".
	head := prefix
	if i := strings.IndexAny(prefix, " :"); i > 0 {
		head = prefix[:i]
	}
	if head == prefix {
		head = string(runes[0])
	}
	return regexp.MustCompile(`^` + regexp.QuoteMeta(head) + `.*?` + terminator + `\s?(.*)$`)
}

// commandOn is the command typed after the shell prompt on line, if line has one.
func commandOn(shellPrompt *regexp.Regexp, line string) (string, bool) {
	m := shellPrompt.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

// aiInvocation finds ai anywhere in a command line, such as "cd x; ai why".
func aiInvocation(cmd string) (string, bool) {
	for _, part := range commandParts(cmd) {
		if aiCommand.MatchString(part) {
			return part, true
		}
	}
	return "", false
}

// commandParts splits a command line at ;, |, || and &&, except inside quotes,
// so a prompt such as ai "why does a | b fail" stays one part.
func commandParts(cmd string) []string {
	var parts []string
	var quote byte
	start := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ';' || c == '|' || c == '&':
			parts = append(parts, strings.TrimSpace(cmd[start:i]))
			for i+1 < len(cmd) && (cmd[i+1] == '|' || cmd[i+1] == '&') {
				i++
			}
			start = i + 1
		}
	}
	return append(parts, strings.TrimSpace(cmd[start:]))
}

func continues(cmd string) bool {
	fields := strings.Fields(cmd)
	opts, err := parseArgs(fields[1:])
	return err == nil && opts.depth > 0
}
