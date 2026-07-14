package corrector

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Prompt is a versioned prompt loaded from packages/tamil-rules/prompts/.
//
// Prompts live in files, not string literals, so a prompt change is a reviewable
// diff and the version that produced any given correction is recorded on the
// ai_requests row (§8.3). When you change one, bump the cache version too: the
// cache is keyed on engine version, and a new prompt produces different output
// for the same input.
type Prompt struct {
	ID      string
	Version int
	Model   string
	Body    string
}

// PromptsDir overrides where prompts are read from (PROMPTS_DIR). Set in the
// container, where the repo layout does not exist.
const promptsSubdir = "packages/tamil-rules/prompts"

// promptSearchPaths walks up from the working directory looking for the repo's
// prompts directory, then falls back to the container mount.
//
// Walking up rather than using fixed relative paths: `go test` runs with the
// package directory as CWD, `go run` uses the module root, and the binary in
// production uses /. A fixed "../../" works in exactly one of those.
func promptSearchPaths() []string {
	var dirs []string

	if custom := os.Getenv("PROMPTS_DIR"); custom != "" {
		dirs = append(dirs, custom)
	}

	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; {
			dirs = append(dirs, filepath.Join(dir, promptsSubdir))
			parent := filepath.Dir(dir)
			if parent == dir { // reached /
				break
			}
			dir = parent
		}
	}

	return append(dirs, "/data/tamil-rules/prompts") // container mount
}

// LoadPrompt reads `<id>.v<n>.md`, splitting the YAML-ish front matter from the
// body. Only the fields the runtime needs are parsed; the rest is documentation.
func LoadPrompt(id string, version int) (*Prompt, error) {
	name := fmt.Sprintf("%s.v%d.md", id, version)
	searched := promptSearchPaths()

	var raw []byte
	for _, dir := range searched {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			raw = b
			break
		}
	}
	if raw == nil {
		return nil, fmt.Errorf("prompt %s not found (searched %d locations, incl. %s)",
			name, len(searched), searched[len(searched)-1])
	}

	text := string(raw)
	p := &Prompt{ID: id, Version: version, Body: text}

	// Front matter is delimited by --- ... ---
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "\n---"); end >= 0 {
			front := text[3 : 3+end]
			p.Body = strings.TrimSpace(text[3+end+4:])

			for _, line := range strings.Split(front, "\n") {
				key, val, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				key = strings.TrimSpace(key)
				val = strings.TrimSpace(val)
				switch key {
				case "model":
					p.Model = val
				case "version":
					if v, err := strconv.Atoi(val); err == nil {
						p.Version = v
					}
				}
			}
		}
	}

	if strings.TrimSpace(p.Body) == "" {
		return nil, fmt.Errorf("prompt %s has an empty body", name)
	}
	return p, nil
}

// userMessage renders the target sentence and its context for the corrector.
// The context is clearly labelled and explicitly marked do-not-correct, because
// the most common way this prompt fails is a model "fixing" a context sentence
// and returning offsets that do not exist in the target.
func userMessage(req Request) string {
	var b strings.Builder
	if req.ContextBefore != "" {
		b.WriteString("CONTEXT BEFORE (do not correct): ")
		b.WriteString(req.ContextBefore)
		b.WriteString("\n")
	}
	b.WriteString("TARGET (correct only this): ")
	b.WriteString(req.Target)
	if req.ContextAfter != "" {
		b.WriteString("\nCONTEXT AFTER (do not correct): ")
		b.WriteString(req.ContextAfter)
	}
	return b.String()
}
