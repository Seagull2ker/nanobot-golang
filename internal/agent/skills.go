package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ---------- Types ----------

// SkillMetadata is the YAML frontmatter at the top of a SKILL.md file.
type SkillMetadata struct {
	Name        string
	Description string
	Homepage    string
	Always      bool
	License     string
	Metadata    string // raw JSON string
}

// NanobotMeta is the nanobot-specific extension parsed from skill metadata JSON.
type NanobotMeta struct {
	Emoji    string            `json:"emoji,omitempty"`
	Always   bool              `json:"always,omitempty"`
	OS       []string          `json:"os,omitempty"`
	Requires *RequirementsMeta `json:"requires,omitempty"`
	Install  []InstallMeta     `json:"install,omitempty"`
}

// RequirementsMeta lists binaries and env vars a skill depends on.
type RequirementsMeta struct {
	Bins []string `json:"bins,omitempty"`
	Env  []string `json:"env,omitempty"`
}

// InstallMeta describes one installation step for a skill.
type InstallMeta struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // "brew" | "apt" | "pip"
	Label   string `json:"label,omitempty"`
	Formula string `json:"formula,omitempty"`
	Package string `json:"package,omitempty"`
}

// Skill is one loaded skill with parsed metadata and body content.
type Skill struct {
	Meta    SkillMetadata
	Content string // body without frontmatter
	Path    string
	Source  string // "workspace" or "builtin"
}

// ---------- SkillsLoader ----------

// SkillsLoader loads skills from workspace and builtin directories.
type SkillsLoader struct {
	workspaceSkillsDir string
	builtinSkillsDir   string
	skills             map[string]*Skill
}

// NewSkillsLoader creates a SkillsLoader.
// workspaceDir is the agent workspace root (skills go in workspaceDir/skills/).
func NewSkillsLoader(workspaceDir, builtinDir string) *SkillsLoader {
	return &SkillsLoader{
		workspaceSkillsDir: filepath.Join(workspaceDir, "skills"),
		builtinSkillsDir:   builtinDir,
		skills:             make(map[string]*Skill),
	}
}

// LoadSkills scans both directories. Workspace skills shadow builtin ones.
func (l *SkillsLoader) LoadSkills() error {
	l.skills = make(map[string]*Skill)
	l.loadFromDir(l.workspaceSkillsDir, "workspace")
	l.loadFromDir(l.builtinSkillsDir, "builtin")
	return nil
}

func (l *SkillsLoader) loadFromDir(dir, source string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, exists := l.skills[name]; exists {
			continue // workspace shadows builtin
		}
		skillPath := filepath.Join(dir, name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		sk := l.parseSkill(string(data), skillPath, source)
		if sk != nil {
			l.skills[sk.Meta.Name] = sk
		}
	}
}

func (l *SkillsLoader) parseSkill(content, path, source string) *Skill {
	meta, body := parseFrontmatter(content)
	if meta.Name == "" {
		return nil
	}
	return &Skill{
		Meta:    meta,
		Content: strings.TrimSpace(body),
		Path:    path,
		Source:  source,
	}
}

func parseFrontmatter(raw string) (SkillMetadata, string) {
	var meta SkillMetadata
	body := raw

	if strings.HasPrefix(raw, "---") {
		rest := raw[3:]
		if idx := strings.Index(rest, "---"); idx >= 0 {
			fm := strings.TrimSpace(rest[:idx])
			body = strings.TrimSpace(rest[idx+3:])
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				val = strings.Trim(val, `"'`)
				switch key {
				case "name":
					meta.Name = val
				case "description":
					meta.Description = val
				case "homepage":
					meta.Homepage = val
				case "always":
					meta.Always = val == "true"
				case "license":
					meta.License = val
				case "metadata":
					meta.Metadata = val
				}
			}
		}
	}
	return meta, body
}

// ---------- Queries ----------

// GetAlwaysSkills returns skills marked always-on and whose requirements are met.
func (l *SkillsLoader) GetAlwaysSkills() []*Skill {
	var result []*Skill
	for _, sk := range l.skills {
		if !l.isAvailable(sk) {
			continue
		}
		if sk.Meta.Always || l.getNanobotMeta(sk).Always {
			result = append(result, sk)
		}
	}
	return result
}

// ListAvailableSkills returns skills whose requirements are met.
func (l *SkillsLoader) ListAvailableSkills() []*Skill {
	var list []*Skill
	for _, sk := range l.skills {
		if l.isAvailable(sk) {
			list = append(list, sk)
		}
	}
	return list
}

// SkillCount returns the number of loaded skills.
func (l *SkillsLoader) SkillCount() int { return len(l.skills) }

// ---------- Context Assembly ----------

// LoadSkillsForContext concatenates skill bodies for system prompt injection.
func (l *SkillsLoader) LoadSkillsForContext(skills []*Skill) string {
	var parts []string
	for _, sk := range skills {
		if sk.Content == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("### Skill: %s\n\n%s", sk.Meta.Name, sk.Content))
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// BuildSkillsSummary renders an XML block describing all loaded skills.
func (l *SkillsLoader) BuildSkillsSummary() string {
	all := l.listAll()
	if len(all) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "<skills>")
	for _, sk := range all {
		name := sk.Meta.Name
		desc := sk.Meta.Description
		if desc == "" {
			desc = name
		}
		available := l.isAvailable(sk)

		lines = append(lines, fmt.Sprintf(`  <skill available="%v">`, available))
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", name))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", desc))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", sk.Path))

		if !available {
			if missing := l.getMissingRequirements(sk); missing != "" {
				lines = append(lines, fmt.Sprintf("    <requires>%s</requires>", missing))
			}
			if install := l.getInstallInstructions(sk); install != "" {
				lines = append(lines, fmt.Sprintf("    <install>%s</install>", install))
			}
		}
		lines = append(lines, "  </skill>")
	}
	lines = append(lines, "</skills>")
	return strings.Join(lines, "\n")
}

// GetSkillMetadata returns a skill's frontmatter as a map.
func (l *SkillsLoader) GetSkillMetadata(name string) map[string]string {
	sk := l.skills[name]
	if sk == nil {
		return nil
	}
	m := map[string]string{
		"name":        sk.Meta.Name,
		"description": sk.Meta.Description,
	}
	if sk.Meta.Homepage != "" {
		m["homepage"] = sk.Meta.Homepage
	}
	if sk.Meta.Always {
		m["always"] = "true"
	}
	if sk.Meta.License != "" {
		m["license"] = sk.Meta.License
	}
	return m
}

// ---------- Helpers ----------

func (l *SkillsLoader) listAll() []*Skill {
	list := make([]*Skill, 0, len(l.skills))
	for _, s := range l.skills {
		list = append(list, s)
	}
	return list
}

func (l *SkillsLoader) getNanobotMeta(sk *Skill) NanobotMeta {
	if sk.Meta.Metadata == "" {
		return NanobotMeta{}
	}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sk.Meta.Metadata), &wrapper); err != nil {
		return NanobotMeta{}
	}
	raw, ok := wrapper["nanobot"]
	if !ok {
		raw, ok = wrapper["openclaw"]
		if !ok {
			return NanobotMeta{}
		}
	}
	var meta NanobotMeta
	json.Unmarshal(raw, &meta)
	return meta
}

func (l *SkillsLoader) isAvailable(sk *Skill) bool {
	nbm := l.getNanobotMeta(sk)
	if nbm.Requires == nil {
		return true
	}
	for _, bin := range nbm.Requires.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			return false
		}
	}
	for _, env := range nbm.Requires.Env {
		if os.Getenv(env) == "" {
			return false
		}
	}
	return true
}

func (l *SkillsLoader) getMissingRequirements(sk *Skill) string {
	nbm := l.getNanobotMeta(sk)
	if nbm.Requires == nil {
		return ""
	}
	var missing []string
	for _, bin := range nbm.Requires.Bins {
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, "bin:"+bin)
		}
	}
	for _, env := range nbm.Requires.Env {
		if os.Getenv(env) == "" {
			missing = append(missing, "env:"+env)
		}
	}
	return strings.Join(missing, ", ")
}

func (l *SkillsLoader) getInstallInstructions(sk *Skill) string {
	nbm := l.getNanobotMeta(sk)
	if len(nbm.Install) == 0 {
		return ""
	}
	var parts []string
	for _, ins := range nbm.Install {
		switch ins.Kind {
		case "brew":
			parts = append(parts, fmt.Sprintf("brew install %s", ins.Formula))
		case "apt":
			parts = append(parts, fmt.Sprintf("apt install %s", ins.Package))
		default:
			parts = append(parts, ins.Label)
		}
	}
	return strings.Join(parts, "; ")
}
