package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"harness/internal/tools"
)

// Skill is a directory with a SKILL.md (Agent Skills format: name and
// description in frontmatter). Only the metadata is in context; the body is
// loaded when the model asks for it.
type Skill struct {
	Name, Description, Path string
}

func LoadSkills(dirs ...string) []Skill {
	byName := map[string]Skill{}
	for _, d := range dirs {
		files, _ := filepath.Glob(filepath.Join(d, "*", "SKILL.md"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			meta, _ := frontmatter(string(b))
			if meta["name"] == "" || meta["description"] == "" {
				continue
			}
			byName[meta["name"]] = Skill{Name: meta["name"], Description: meta["description"], Path: f}
		}
	}
	var out []Skill
	for _, s := range byName {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// SkillListing is the block added to the first message.
func SkillListing(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<available_skills>\nLoad a skill with the skill tool when the task matches its description.\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
	}
	b.WriteString("</available_skills>")
	return b.String()
}

type skillArgs struct {
	Name string `json:"name" desc:"Skill name from <available_skills>"`
}

// SkillTool returns a skill's instructions plus the names of its supporting
// files, which the model can read if it needs them.
func SkillTool(skills []Skill) tools.Tool {
	return tools.New("skill", "Load the full instructions of a skill listed in <available_skills>.", true,
		func(ctx context.Context, env *tools.Env, a skillArgs) tools.Result {
			var names []string
			for _, s := range skills {
				names = append(names, s.Name)
				if s.Name != a.Name {
					continue
				}
				b, err := os.ReadFile(s.Path)
				if err != nil {
					return tools.Result{IsError: true, Content: err.Error()}
				}
				_, body := frontmatter(string(b))
				out := fmt.Sprintf("<skill name=%q path=%q>\n%s\n</skill>", s.Name, s.Path, body)
				if extra := supporting(s.Path); len(extra) > 0 {
					out += "\nSupporting files (read them if needed): " + strings.Join(extra, ", ")
				}
				return tools.Result{Content: out}
			}
			return tools.Result{IsError: true, Content: fmt.Sprintf("Unknown skill %q. Available: %s.", a.Name, strings.Join(names, ", "))}
		})
}

func supporting(skillMD string) []string {
	dir := filepath.Dir(skillMD)
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && p != skillMD {
			out = append(out, p)
		}
		return nil
	})
	return out
}
