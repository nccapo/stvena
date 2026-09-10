package editor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Activity struct {
	Session   string    `json:"session"`
	ID        string    `json:"id"`
	Agent     string    `json:"agent"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	EndLine   int       `json:"endLine,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func ActivityPath(saved *session.Session) string {
	return filepath.Join(saved.Dir, saved.ID+"-activity.json")
}

// HookArgs adds invocation-local observers, leaving persistent agent settings alone.
// Codex still applies its normal hook trust review. Explicit Claude settings are
// left intact; users can add the documented hook to those settings themselves.
func HookArgs(args []string, executable string) []string {
	if len(args) == 0 {
		return args
	}
	name := filepath.Base(args[0])
	command := "'" + strings.ReplaceAll(executable, "'", "'\"'\"'") + "' editor-hook"
	extra := []string{}
	events := []string{"PostToolUse", "PreToolUse", "SessionStart", "UserPromptSubmit", "Stop"}
	switch name {
	case "codex":
		for _, event := range events {
			extra = append(extra, "-c", "hooks."+event+"=[{hooks=[{type=\"command\",command="+strconv.Quote(command)+",timeout=2}]}]")
		}
	case "claude":
		for _, arg := range args[1:] {
			if arg == "--settings" || strings.HasPrefix(arg, "--settings=") {
				return args
			}
		}
		hooks := map[string]any{}
		for _, event := range append(events, "Notification", "StopFailure", "PostToolUseFailure") {
			hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 2}}}}
		}
		data, _ := json.Marshal(map[string]any{"hooks": hooks})
		extra = []string{"--settings", string(data)}
	default:
		return args
	}

	return append(append([]string{args[0]}, extra...), args[1:]...)
}

// RecordHook is an observational hook: it never returns an approval decision,
// emits context to the agent, or executes any of the reported command text.
func RecordHook(input io.Reader) error {
	target, root, sessionID := os.Getenv("STVENA_ACTIVITY_PATH"), os.Getenv("STVENA_ROOT"), os.Getenv("STVENA_SESSION")
	if target == "" || root == "" || sessionID == "" {
		return nil
	}
	var event struct {
		Event string `json:"hook_event_name"`
		CWD   string `json:"cwd"`
		Tool  string `json:"tool_name"`
		ID    string `json:"tool_use_id"`
		Input struct {
			File    string `json:"file_path"`
			Path    string `json:"path"`
			Offset  int    `json:"offset"`
			Limit   int    `json:"limit"`
			Command string `json:"command"`
			Cmd     string `json:"cmd"`
			Workdir string `json:"workdir"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(io.LimitReader(input, 1024*1024)).Decode(&event); err != nil {
		return err
	}
	if event.Event != "PostToolUse" {
		return nil
	}
	file, line, end := "", 1, 0
	switch event.Tool {
	case "Read":
		if event.Input.Offset < 0 || event.Input.Offset > 1000000 || event.Input.Limit < 0 || event.Input.Limit > 1000000 {
			return nil
		}
		file = event.Input.File
		if file == "" {
			file = event.Input.Path
		}
		line = max(1, event.Input.Offset)
		if event.Input.Limit > 0 && event.Input.Limit <= 1000000 && line <= 1000000 {
			end = line + event.Input.Limit - 1
		}
	case "Bash", "exec_command", "shell_command":
		command := event.Input.Command
		if command == "" {
			command = event.Input.Cmd
		}
		file, line, end = readCommand(command)
	default:
		return nil
	}
	if file == "" {
		return nil
	}
	cwd := event.CWD
	if cwd == "" {
		cwd = root
	}
	if event.Input.Workdir != "" {
		if filepath.IsAbs(event.Input.Workdir) {
			cwd = event.Input.Workdir
		} else {
			cwd = filepath.Join(cwd, event.Input.Workdir)
		}
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	// Resolve symlinks too: never advertise a file outside the workspace.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	file, err = filepath.EvalSymlinks(file)
	if err != nil {
		return nil
	}
	relative, err := filepath.Rel(realRoot, file)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	now := time.Now().UTC()
	return session.AtomicJSON(target, Activity{Session: sessionID, ID: fmt.Sprintf("%s:%d", event.ID, now.UnixNano()),
		Agent: os.Getenv("STVENA_AGENT"), Path: filepath.ToSlash(relative), Line: line, EndLine: end, UpdatedAt: now})
}

// Recognize only unambiguous single-file read commands. Complex shell programs
// and searches do not provide a reliable source range and are deliberately skipped.
func readCommand(command string) (string, int, int) {
	args, ok := readWords(strings.TrimSpace(command))
	if !ok || len(args) < 2 {
		return "", 0, 0
	}
	switch args[0] {
	case "cat":
		if len(args) == 2 && !strings.HasPrefix(args[1], "-") {
			return args[1], 1, 0
		}
	case "head":
		if len(args) == 4 && args[1] == "-n" && !strings.HasPrefix(args[3], "-") {
			n, err := strconv.Atoi(args[2])
			if err == nil && n > 0 && n <= 1000000 {
				return args[3], 1, n
			}
		}
	case "sed":
		if len(args) == 4 && args[1] == "-n" && strings.HasSuffix(args[2], "p") && !strings.HasPrefix(args[3], "-") {
			bounds := strings.Split(strings.TrimSuffix(args[2], "p"), ",")
			if len(bounds) == 1 {
				bounds = append(bounds, bounds[0])
			}
			if len(bounds) == 2 {
				a, e1 := strconv.Atoi(bounds[0])
				b, e2 := strconv.Atoi(bounds[1])
				if e1 == nil && e2 == nil && a > 0 && b >= a && b <= 1000000 {
					return args[3], a, b
				}
			}
		}
	}
	return "", 0, 0
}

func readWords(command string) ([]string, bool) {
	var words []string
	var word strings.Builder
	var quote rune
	started := false
	for _, c := range command {
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				if quote == '"' && strings.ContainsRune("$`\\", c) {
					return nil, false
				}
				word.WriteRune(c)
			}
		} else {
			switch c {
			case '\'', '"':
				quote = c
				started = true
			case ' ', '\t':
				if started {
					words = append(words, word.String())
					word.Reset()
					started = false
				}
			default:
				if strings.ContainsRune("\n\r;&|<>$`\\()*?{}[]~", c) {
					return nil, false
				}
				word.WriteRune(c)
				started = true
			}
		}
	}
	if quote != 0 {
		return nil, false
	}
	if started {
		words = append(words, word.String())
	}
	return words, true
}

func changedRanges(lines []string) []LineRange {
	var ranges []LineRange
	line, inHunk := 1, false
	for _, text := range lines {
		if strings.HasPrefix(text, "@@ ") {
			fields := strings.Fields(text)
			if len(fields) >= 3 {
				_, _ = fmt.Sscanf(strings.Split(fields[2], ",")[0], "+%d", &line)
				inHunk = true
			}
			continue
		}
		if !inHunk || text == "" {
			continue
		}
		switch text[0] {
		case '+', '-':
			current := max(1, line)
			if len(ranges) > 0 && current <= ranges[len(ranges)-1].End+1 {
				ranges[len(ranges)-1].End = max(current, ranges[len(ranges)-1].End)
			} else {
				ranges = append(ranges, LineRange{current, current})
			}
			if text[0] == '+' {
				line++
			}
		case ' ':
			line++
		}
	}
	return ranges
}
