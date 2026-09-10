package attention

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nccapo/stvena/internal/session"
)

// RecordHook stores only lifecycle metadata and explicit edit paths, never
// prompts or command output. Atomic event files avoid losing rapid transitions.
func RecordHook(input io.Reader) error {
	dir, root := os.Getenv("STVENA_ATTENTION_DIR"), os.Getenv("STVENA_ROOT")
	if dir == "" || root == "" {
		return nil
	}
	var h struct {
		Kind         string `json:"hook_event_name"`
		ID           string `json:"tool_use_id"`
		CWD          string `json:"cwd"`
		Tool         string `json:"tool_name"`
		Notification string `json:"notification_type"`
		Input        struct {
			File    string `json:"file_path"`
			Path    string `json:"path"`
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.NewDecoder(io.LimitReader(input, 1024*1024)).Decode(&h); err != nil {
		return err
	}
	e := Event{Kind: h.Kind, At: time.Now().UTC()}
	switch h.Kind {
	case "SessionStart", "UserPromptSubmit", "Stop", "StopFailure", "PreToolUse", "PostToolUse", "PostToolUseFailure":
	case "Notification":
		if h.Notification != "permission_prompt" && h.Notification != "idle_prompt" {
			return nil
		}
		e.Kind = "waiting"
	default:
		return nil
	}
	var paths []string
	switch h.Tool {
	case "Edit", "Write", "MultiEdit":
		p := h.Input.File
		if p == "" {
			p = h.Input.Path
		}
		if p != "" {
			paths = append(paths, p)
		}
	case "apply_patch":
		for _, line := range strings.Split(h.Input.Command, "\n") {
			for _, prefix := range []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "} {
				if strings.HasPrefix(line, prefix) {
					paths = append(paths, strings.TrimPrefix(line, prefix))
				}
			}
		}
	}
	beforePath := filepath.Join(dir, fmt.Sprintf("tool-%x.json", sha256.Sum256([]byte(h.ID))))
	if h.ID != "" && h.Kind == "PreToolUse" && len(paths) > 0 {
		before := map[string]string{}
		for _, p := range paths {
			if rel := safePath(root, h.CWD, p); rel != "" && !ignored(root, rel) {
				if v, err := fileVersion(root, rel); err == nil {
					before[rel] = v
				}
			}
		}
		if err := session.AtomicJSON(beforePath, before); err != nil {
			return err
		}
	}
	if h.ID != "" && (h.Kind == "PostToolUse" || h.Kind == "PostToolUseFailure") {
		var before map[string]string
		if data, err := os.ReadFile(beforePath); h.Kind == "PostToolUse" && err == nil && json.Unmarshal(data, &before) == nil {
			for p, old := range before {
				if v, err := fileVersion(root, p); err == nil && v != old {
					e.Changes = append(e.Changes, Change{p, v})
				}
			}
		}
		_ = os.Remove(beforePath)
	}
	file, err := os.CreateTemp(dir, ".event-*.json")
	if err != nil {
		return err
	}
	temp := file.Name()
	name := filepath.Join(dir, strings.TrimPrefix(filepath.Base(temp), "."))
	_ = file.Close()
	_ = os.Remove(temp)
	return session.AtomicJSON(name, e)
}

func safePath(root, cwd, path string) string {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	if cwd == "" {
		cwd = root
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	// Resolve the closest existing ancestor to support additions and deletions.
	tail := ""
	current := filepath.Clean(path)
	for {
		resolved, e := filepath.EvalSymlinks(current)
		if e == nil {
			path = filepath.Join(resolved, tail)
			break
		}
		if !os.IsNotExist(e) || filepath.Dir(current) == current {
			return ""
		}
		tail = filepath.Join(filepath.Base(current), tail)
		current = filepath.Dir(current)
	}
	rel, err := filepath.Rel(realRoot, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}
func ignored(root, path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", "-C", root, "check-ignore", "--quiet", "--", path).Run() == nil
}

func fileVersion(root, path string) (string, error) {
	if safePath(root, root, path) != path {
		return "", fmt.Errorf("path outside workspace")
	}
	info, err := os.Lstat(filepath.Join(root, path))
	if os.IsNotExist(err) {
		return "deleted", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "hash-object", "--", path).Output()
	return strings.TrimSpace(string(out)), err
}

// Drain is run off the render loop. Tool baselines stay until their post event.
func Drain(dir string) ([]Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var events []Event
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "event-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var e Event
		if json.Unmarshal(data, &e) == nil {
			events = append(events, e)
		}
		_ = os.Remove(name)
	}
	// Filename randomness is deliberate; lifecycle order comes from hook time.
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	return events, nil
}
