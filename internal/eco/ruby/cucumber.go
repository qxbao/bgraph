package ruby

import (
	"bytes"
	"path"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/qxbao/bgraph/internal/classify"
	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/model"
)

// RunnerCucumber is the Ruby Cucumber runner.
const RunnerCucumber = "cucumber"

const cucumberProfileData = "<rb-cucumber-profile-data>"

// CucumberWholeUnitID identifies a project whose native Cucumber command is the unit.
func CucumberWholeUnitID(dir string) string { return "<rb-all-cucumber:" + dir + ">" }

// ParseCucumberWholeUnitID returns the project of a whole Cucumber unit.
func ParseCucumberWholeUnitID(id string) (string, bool) {
	dir, ok := strings.CutPrefix(id, "<rb-all-cucumber:")
	if !ok || !strings.HasSuffix(dir, ">") {
		return "", false
	}
	return strings.TrimSuffix(dir, ">"), true
}

// Cucumber searches these six paths from the directory where it runs.
var cucumberProfilePaths = []string{
	"cucumber.yml", "cucumber.yaml", ".config/cucumber.yml", ".config/cucumber.yaml", "config/cucumber.yml", "config/cucumber.yaml",
}

func isCucumberProfile(p, project string) bool {
	for _, rel := range cucumberProfilePaths {
		if p == path.Join(project, rel) {
			return true
		}
	}
	return false
}

func hasCucumber(a *eco.Analysis, project string) bool {
	for _, u := range a.Tests {
		if u.Runner == RunnerCucumber && u.Project == project {
			return true
		}
	}
	return false
}

func cucumberGem(name string) bool {
	return name == "cucumber" || name == "cucumber-rails"
}

func (b *builder) javascriptCucumber(dir string, profiles []string) bool {
	if b.foreign[dir] {
		if pkg, err := b.t.Read(path.Join(dir, "package.json")); err == nil {
			if bytes.Contains(pkg, []byte(`"@cucumber/cucumber"`)) ||
				bytes.Contains(pkg, []byte(`"cucumber"`)) || bytes.Contains(pkg, []byte(`"@wdio/cucumber-framework"`)) {
				return true
			}
		}
	}
	if len(profiles) == 0 {
		return false
	}
	// Cucumber.js accepts an options mapping as its default profile.
	// Ruby Cucumber expects a command-line string or array of arguments.
	src, err := b.t.Read(profiles[0])
	var profile struct{ Default any }
	if err != nil || yaml.Unmarshal(src, &profile) != nil {
		return false
	}
	_, mapping := profile.Default.(map[string]any)
	return mapping
}

func (b *builder) bundledCucumber(project string) bool {
	gemfile := b.gemfileFor(project)
	if gemfile == "" {
		return false
	}
	if fi := b.infos[gemfile]; fi != nil {
		for _, im := range fi.scan.Imports {
			if im.Kind == model.ImportRbGem && cucumberGem(im.Spec) {
				return true
			}
		}
	}
	lockfile := gemfile + ".lock"
	if path.Base(gemfile) == "gems.rb" {
		lockfile = path.Join(path.Dir(gemfile), "gems.locked")
	}
	if lock, err := b.t.Read(lockfile); err == nil {
		for _, line := range strings.Split(string(lock), "\n") {
			if name, _, ok := strings.Cut(strings.TrimSpace(line), " ("); ok && cucumberGem(name) {
				return true
			}
		}
	}
	return false
}

func cucumberFeaturesPath(p, project string) bool {
	return strings.HasPrefix(p, path.Join(project, "features")+"/")
}

// The runner globs recursively under features/ and Ruby's Dir glob skips dot files and directories.
func cucumberVisible(p string) bool {
	return !strings.HasPrefix(p, ".") && !strings.Contains(p, "/.")
}

// A Ruby glue file alongside Gherkin identifies an unbundled Cucumber suite.
// With a profile, feature discovery may take place outside the conventional directory.
func standaloneCucumber(files []string) bool {
	var feature, anyFeature, glue, profile bool
	for _, p := range files {
		profile = profile || isCucumberProfile(p, ".")
		if path.Ext(p) == ".feature" && cucumberVisible(p) && !skipPath(p) {
			anyFeature = true
		}
		rel, ok := strings.CutPrefix(p, "features/")
		if !ok || !cucumberVisible(rel) || skipPath(p) {
			continue
		}
		switch path.Ext(p) {
		case ".feature":
			feature = true
		case ".rb":
			glue = true
		}
		if feature && glue {
			return true
		}
	}
	return glue && profile && anyFeature
}

// addCucumber discovers the conventional features tree. A single shared graph
// node represents Cucumber's support and step definitions and nearby data.
func (b *builder) addCucumber(files []string) {
	type suite struct {
		features, support, profiles []string
		whole                       bool
		rubyGlue, foreignGlue       bool
	}
	suites := map[string]*suite{}
	for _, project := range b.a.Projects {
		s := &suite{}
		for _, rel := range cucumberProfilePaths {
			p := path.Join(project.Dir, rel)
			if b.t.Has(p) {
				s.profiles = append(s.profiles, p)
			}
		}
		s.whole = len(s.profiles) != 0
		if s.whole || b.t.IsDir(path.Join(project.Dir, "features")) {
			suites[project.Dir] = s
		}
	}
	if len(suites) == 0 {
		return
	}
	for _, p := range files {
		project := b.project(path.Dir(p))
		s := suites[project]
		isFeature := path.Ext(p) == ".feature"
		if isFeature {
			// Native discovery can also find a feature through an enclosing
			// project's features directory.
			for d := project; d != "."; {
				d = b.project(path.Dir(d))
				rel, underFeatures := strings.CutPrefix(p, path.Join(d, "features")+"/")
				if outer := suites[d]; outer != nil && underFeatures && cucumberVisible(rel) {
					outer.features = append(outer.features, p)
					outer.whole = true
				}
			}
		}
		if s == nil {
			continue
		}
		rel := strings.TrimPrefix(p, path.Join(project, "features")+"/")
		if rel != p {
			visible := cucumberVisible(rel)
			if visible {
				switch path.Ext(p) {
				case ".rb":
					s.rubyGlue = s.rubyGlue || !skipPath(p)
				case ".js", ".cjs", ".mjs", ".ts", ".cts", ".mts", ".jsx", ".tsx", ".java", ".kt", ".scala", ".cs", ".fs", ".go", ".py", ".php":
					s.foreignGlue = true
				}
			}
			if isFeature && visible {
				s.features = append(s.features, p)
			} else {
				s.support = append(s.support, p)
				if path.Ext(p) == ".rb" && skipPath(p) && visible {
					s.whole = true
				}
			}
			continue
		}
		// Native Cucumber can also load vendor/{gems,plugins}/*/cucumber
		// when no --require is passed. Preserve that implicit search.
		if rel, vendor := strings.CutPrefix(p, path.Join(project, "vendor")+"/"); vendor {
			group, subdir, _ := strings.Cut(rel, "/")
			_, rest, _ := strings.Cut(subdir, "/")
			if (group == "gems" || group == "plugins") && strings.HasPrefix(rest, "cucumber/") {
				s.whole = true
			}
		}
	}
	for _, project := range b.a.Projects {
		if project.Dir == "." {
			continue
		}
		// Glob discovery can cross a nested Ruby project rooted under
		// features/. Let Cucumber own the overlapping discovery.
		parent := b.project(path.Dir(project.Dir))
		if s := suites[parent]; s != nil && cucumberFeaturesPath(project.Dir, parent) {
			s.whole = true
		}
	}
	whole := map[string]string{}
	profiled := false
	for _, project := range b.a.Projects {
		dir := project.Dir
		s := suites[dir]
		if s == nil || len(s.features) == 0 && len(s.profiles) == 0 ||
			!s.rubyGlue && (s.foreignGlue || b.javascriptCucumber(dir, s.profiles)) && !b.bundledCucumber(dir) {
			continue
		}
		for _, p := range s.profiles {
			b.a.Roles[p] = model.RoleConfig
		}
		if s.whole {
			id := CucumberWholeUnitID(dir)
			if len(s.profiles) != 0 {
				profiled = true
				b.edge(id, cucumberProfileData, model.EdgeImplicit, "Cucumber profile may load data from custom paths", 0)
			} else {
				whole[dir] = id
			}
			b.a.Tests = append(b.a.Tests, model.TestUnit{Eco: Name, ID: id, Project: dir, Runner: RunnerCucumber,
				Config: b.gemfileFor(dir), Files: []string{id}, Whole: true})
			continue
		}
		shared := "<rb-cucumber-support:" + dir + ">"
		for _, p := range s.support {
			if b.a.Roles[p] == "" {
				b.a.Roles[p] = model.RoleData
			}
			b.edge(shared, p, model.EdgeImplicit, "Cucumber shared support under features/", 0)
		}
		for _, p := range s.features {
			b.a.Roles[p] = model.RoleTest
			b.a.Tests = append(b.a.Tests, model.TestUnit{Eco: Name, ID: p, Project: dir, Runner: RunnerCucumber,
				Files: []string{p}, Config: b.gemfileFor(dir)})
			b.edge(p, shared, model.EdgeImplicit, "Cucumber loads shared support", 0)
		}
	}
	if !profiled && len(whole) == 0 {
		return
	}
	for _, p := range files {
		if b.a.Roles[p] != "" {
			continue
		}
		// Whole suites already run on meaningful changes. A profile can
		// also make an apparent document anywhere in the repo test data.
		doc := classify.Doc(p)
		if profiled && (doc || rubyDoc(path.Base(p))) {
			b.edge(cucumberProfileData, p, model.EdgeImplicit, "possible Cucumber profile data", 0)
		}
		if !doc || len(whole) == 0 {
			continue
		}
		dir := b.project(path.Dir(p))
		if id := whole[dir]; id != "" && b.inRuby(p) {
			b.edge(id, p, model.EdgeImplicit, "data read by an unfiltered Cucumber suite", 0)
		}
	}
}
