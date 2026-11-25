package dart

import (
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/glob"
	"github.com/qxbao/bgraph/internal/tree"
)

// pkg is one directory holding a pubspec.yaml.
type pkg struct {
	dir string
	// raw is the parsed pubspec (nil when it does not parse; err says why).
	raw  map[string]any
	err  string
	name string
	// flutter: the package depends on an SDK package of the Flutter SDK (flutter, flutter_test, integration_test...).
	flutter bool
	// depNames: names of the dependencies, dev_dependencies and dependency_overrides (also from pubspec_overrides.yaml).
	depNames []string
	// pathDeps: directories named by path: dependencies (repo-relative, cleaned; outside the repo is dropped).
	pathDeps []string
	// workspace: the entries of `workspace:` (nil when absent); member: `resolution: workspace`.
	workspace  []string
	hasWsKey   bool
	member     bool
	overridden map[string]any // pubspec_overrides.yaml dependency_overrides (nil when absent or unreadable)
}

// repo is the Dart view of one tree.
type repo struct {
	pkgs    map[string]*pkg
	byName  map[string][]string // package name → directories (every directory with that name)
	unnamed []string            // directories whose pubspec has no readable name (it may be any package)
	members map[string][]string // workspace root → direct member directories
	root    map[string]string   // member → its workspace root (direct parent workspace)
}

// localPackage proves that a package: name is the current package or an explicit in-tree path dependency.
// A same-named package in the checkout can otherwise differ from the one that Pub actually loads.
func (r *repo) localPackage(p *pkg, name string) bool {
	if p == nil || name == "" {
		return false
	}
	if p.name == name {
		return true
	}
	if p.member || p.hasWsKey {
		return false // other workspace members can override this name
	}
	sections := []any{p.overridden, p.raw["dependency_overrides"], p.raw["dev_dependencies"], p.raw["dependencies"]}
	for _, section := range sections {
		if specs, ok := section.(map[string]any); ok {
			if spec, exists := specs[name]; exists {
				details, _ := spec.(map[string]any)
				depPath, _ := details["path"].(string)
				dir, valid := resolveRel(p.dir, depPath)
				return depPath != "" && valid && r.pkgs[dir] != nil && r.pkgs[dir].name == name
			}
		}
	}
	return false
}

// skipSegment: a directory whose pubspec files are tool caches or generated platform links, never packages of the repo.
func skipSegment(seg string) bool {
	return strings.HasPrefix(seg, ".") && seg != "." && seg != ".."
}

func isPubspec(f string) bool {
	if path.Base(f) != "pubspec.yaml" {
		return false
	}
	for _, seg := range strings.Split(path.Dir(f), "/") {
		if skipSegment(seg) {
			return false
		}
	}
	return true
}

// loadRepo reads every pubspec.yaml of t; nil when there is none.
func loadRepo(t tree.Tree) *repo {
	if t == nil {
		return nil
	}
	var r *repo
	for _, f := range t.Files() {
		if !isPubspec(f) {
			continue
		}
		if r == nil {
			r = &repo{pkgs: map[string]*pkg{}, byName: map[string][]string{}, members: map[string][]string{}, root: map[string]string{}}
		}
		p := parsePubspec(t, path.Dir(f))
		r.pkgs[p.dir] = p
		if p.name != "" {
			r.byName[p.name] = append(r.byName[p.name], p.dir)
		} else {
			r.unnamed = append(r.unnamed, p.dir)
		}
	}
	if r == nil {
		return nil
	}
	dirs := slices.Sorted(maps.Keys(r.pkgs))
	for _, d := range dirs {
		p := r.pkgs[d]
		for _, w := range p.workspace {
			for _, m := range r.resolveDirs(d, w) {
				if m != d && !slices.Contains(r.members[d], m) {
					r.members[d] = append(r.members[d], m)
					if _, ok := r.root[m]; !ok {
						r.root[m] = d
					}
				}
			}
		}
	}
	return r
}

// resolveDirs resolves a workspace entry (a path or, since Dart 3.11, a glob) of the package at dir to package directories.
func (r *repo) resolveDirs(dir, entry string) []string {
	entry = strings.TrimSuffix(strings.ReplaceAll(entry, "\\", "/"), "/")
	if strings.ContainsAny(entry, "*?[{") {
		g, err := glob.Compile(joinDir(dir, entry))
		if err != nil {
			return nil
		}
		var out []string
		for d := range r.pkgs {
			if g.Match(d) {
				out = append(out, d)
			}
		}
		slices.Sort(out)
		return out
	}
	d, ok := resolveRel(dir, entry)
	if ok && r.pkgs[d] != nil {
		return []string{d}
	}
	return nil
}

// allMembers returns the members of the workspace rooted at dir, recursively (nested workspaces).
func (r *repo) allMembers(dir string) []string {
	if r == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{dir: true}
	var walk func(string)
	walk = func(d string) {
		for _, m := range r.members[d] {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
				walk(m)
			}
		}
	}
	walk(dir)
	return out
}

// resolutionRoot is the directory whose pubspec.lock resolves the package at dir: the outermost workspace root above a
// member, else dir itself.
func (r *repo) resolutionRoot(dir string) string {
	seen := map[string]bool{}
	for !seen[dir] {
		seen[dir] = true
		p := r.pkgs[dir]
		up, ok := r.root[dir]
		if !ok || p == nil || !p.member {
			return dir
		}
		dir = up
	}
	return dir
}

// enclosing returns the nearest package directory strictly above dir ("." when there is none).
func (r *repo) enclosing(dir string) string {
	if dir == "." {
		return "."
	}
	if p := r.nearest(path.Dir(dir)); p != nil {
		return p.dir
	}
	return "."
}

// nearest returns the package containing directory dir (the deepest package directory at or above it).
func (r *repo) nearest(dir string) *pkg {
	if r == nil {
		return nil
	}
	for ; ; dir = path.Dir(dir) {
		if p := r.pkgs[dir]; p != nil {
			return p
		}
		if dir == "." || dir == "/" || dir == "" {
			return nil
		}
	}
}

func joinDir(dir, rel string) string {
	if dir == "." {
		return path.Clean(rel)
	}
	return path.Join(dir, rel)
}

// resolveRel joins a relative path to dir; false when it is absolute or leaves the repository.
func resolveRel(dir, rel string) (string, bool) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || path.IsAbs(rel) || strings.Contains(rel, ":") || strings.HasPrefix(rel, "~") {
		return "", false
	}
	d := joinDir(dir, rel)
	return d, d != ".." && !strings.HasPrefix(d, "../")
}

func parsePubspec(t tree.Tree, dir string) *pkg {
	p := &pkg{dir: dir}
	src, err := t.Read(path.Join(dir, "pubspec.yaml"))
	if err != nil {
		p.err = err.Error()
		return p
	}
	raw, err := parseYAMLMap(src)
	if err != nil {
		p.err = "pubspec.yaml does not parse: " + err.Error()
		return p
	}
	p.raw = raw
	if s, ok := raw["name"].(string); ok {
		p.name = s
	}
	if s, ok := raw["resolution"].(string); ok && s == "workspace" {
		p.member = true
	}
	if w, ok := raw["workspace"]; ok {
		p.hasWsKey = true
		if l, ok := w.([]any); ok {
			for _, e := range l {
				if s, ok := e.(string); ok {
					p.workspace = append(p.workspace, s)
				}
			}
		}
	}
	sections := []any{raw["dependencies"], raw["dev_dependencies"], raw["dependency_overrides"]}
	if src, err := t.Read(path.Join(dir, "pubspec_overrides.yaml")); err == nil {
		if o, err := parseYAMLMap(src); err == nil {
			if m, ok := o["dependency_overrides"].(map[string]any); ok {
				p.overridden = m
				sections = append(sections, m)
			}
		}
	}
	for _, sec := range sections {
		m, ok := sec.(map[string]any)
		if !ok {
			continue
		}
		for name, spec := range m {
			p.depNames = append(p.depNames, name)
			sm, ok := spec.(map[string]any)
			if !ok {
				continue
			}
			if sdk, ok := sm["sdk"].(string); ok && sdk == "flutter" {
				p.flutter = true
			}
			if pth, ok := sm["path"].(string); ok {
				if d, ok := resolveRel(dir, pth); ok {
					p.pathDeps = append(p.pathDeps, d)
				}
			}
		}
	}
	slices.Sort(p.depNames)
	p.depNames = slices.Compact(p.depNames)
	slices.Sort(p.pathDeps)
	p.pathDeps = slices.Compact(p.pathDeps)
	return p
}

// parseYAMLMap decodes a YAML document whose top level is a mapping (an empty document is an empty mapping).
func parseYAMLMap(src []byte) (map[string]any, error) {
	var m map[string]any
	if err := yaml.Unmarshal(src, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// dependsOn: package q names a package of dirs (by path, or by the name of one of them in either tree).
func dependsOn(q *pkg, dirs map[string]bool, names map[string]bool) bool {
	if q.raw == nil {
		return true // its dependencies cannot be read
	}
	for _, d := range q.pathDeps {
		if dirs[d] {
			return true
		}
	}
	for _, n := range q.depNames {
		if names[n] {
			return true
		}
	}
	return false
}

// harmlessKeys are pubspec keys that change neither resolution nor the build: metadata for pub.dev. `version` is not one of
// them: pub checks path and workspace packages against their dependents' constraints (and a dev_dependency cycle through a
// hosted package checks the package's own version), so a bump can make a version solve fail.
var harmlessKeys = map[string]bool{
	"description": true, "homepage": true, "repository": true, "issue_tracker": true, "documentation": true, "topics": true,
	"funding": true, "screenshots": true, "false_secrets": true, "platforms": true, "publish_to": true,
}

// changedKeys lists the top-level keys whose values differ between two parsed pubspecs.
func changedKeys(a, b map[string]any) []string {
	var out []string
	for k, v := range a {
		if w, ok := b[k]; !ok || !reflect.DeepEqual(v, w) {
			out = append(out, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// within reports whether p lies inside dir.
func within(p, dir string) bool { return eco.Within(p, dir) }
