package ruby

import (
	"path"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/model"
	rbscan "github.com/qxbao/bgraph/internal/scan/ruby"
	"github.com/qxbao/bgraph/internal/tree"
)

// harmlessDotfiles: linter and documentation tool configurations at a project root.
var harmlessDotfiles = map[string]bool{".rubocop.yml": true, ".rubocop_todo.yml": true, ".reek.yml": true, ".yardopts": true,
	".document": true, ".standard.yml": true}

var nativeSrc = map[string]bool{".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true,
	".java": true, ".m": true, ".y": true}

func hasRuby(a *eco.Analysis) bool {
	if a == nil {
		return false
	}
	for _, p := range a.Projects {
		if p.Eco == Name {
			return true
		}
	}
	return false
}

// projectOf returns the deepest Ruby project containing p.
func projectOf(a *eco.Analysis, p string) (string, bool) {
	best, depth, ok := "", -2, false
	for _, pr := range a.Projects {
		if pr.Eco != Name || !eco.Within(p, pr.Dir) {
			continue
		}
		d := strings.Count(pr.Dir, "/")
		if pr.Dir == "." {
			d = -1
		}
		if d > depth {
			best, depth, ok = pr.Dir, d, true
		}
	}
	return best, ok
}

func isProject(a *eco.Analysis, dir string) bool {
	for _, pr := range a.Projects {
		if pr.Eco == Name && pr.Dir == dir {
			return true
		}
	}
	return false
}

func w(reason, p, scope, detail string) model.Widening {
	return model.Widening{Tier: model.TierModule, Reason: reason, Path: p, Scope: scope, Eco: Name, Detail: detail}
}

func suggestRule(p string) string {
	return "[[rule]]\npaths = [\"" + p + "\"]\naffects = [\"<project or glob test>\"]  # or \"none\" if certainly harmless"
}

// ClassifyChange classifies Bundler manifests and lockfiles, gemspecs and test-runner configuration (see the package
// documentation). Files that look like these but do not sit at a Ruby project (test data) are left to the general rules.
func (e *Eco) ClassifyChange(p string, base, head tree.Tree, a *eco.Analysis) ([]model.Widening, bool) {
	if !hasRuby(a) || skipPath(p) {
		return nil, false
	}
	if why := e.autoloadChange(p, base, head); why != "" {
		return []model.Widening{w(model.ReasonConfigIsCode, p, ".", why)}, true
	}
	name := path.Base(p)
	dir := path.Dir(p)
	proj, inProj := projectOf(a, p)
	switch {
	case name == "Gemfile" || name == "gems.rb":
		if !isProject(a, dir) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonManifestChanged, p, dir, name+" is code: dependencies, groups or sources may have changed")}, true
	case strings.HasSuffix(name, ".gemspec"):
		if !isProject(a, dir) {
			return nil, false
		}
		if harmlessGemspec(p, base, head) {
			return nil, true
		}
		return []model.Widening{w(model.ReasonManifestChanged, p, dir, "gemspec changed beyond descriptive metadata")}, true
	case name == "Gemfile.lock" || name == "gems.locked":
		if !isProject(a, dir) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonLockChanged, p, dir, "Bundler lockfile at level L0 (resolved versions not compared)")}, true
	case inProj && (name == "cucumber.yml" || name == "cucumber.yaml") && isCucumberProfile(p, proj) && hasCucumber(a, proj):
		wd := w(model.ReasonTestConfig, p, proj, "Cucumber profiles can change feature paths and loaded support")
		wd.Exact = true
		return []model.Widening{wd}, true
	case name == ".rspec" || name == ".rspec-local" || name == ".rspec-parallel" || name == "spec.opts" && path.Base(dir) == "spec":
		d := dir
		if name == "spec.opts" {
			d = path.Dir(dir)
		}
		if !isProject(a, d) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonTestConfig, p, d, "RSpec options changed")}, true
	case name == "Rakefile" || name == "rakefile" || name == "Rakefile.rb":
		if !isProject(a, dir) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonTestConfig, p, dir, "the Rakefile defines how tests are run")}, true
	case isTaskFile(p):
		if !inProj || underDataDir(p, true) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonTestConfig, p, proj, "rake task file (may define test tasks)")}, true
	case name == ".simplecov":
		if !inProj || underDataDir(p, true) {
			return nil, false
		}
		return []model.Widening{w(model.ReasonTestConfig, p, proj, ".simplecov is code loaded when the tests start")}, true
	case harmlessDotfiles[name]:
		if isProject(a, dir) {
			return nil, true
		}
	case strings.HasPrefix(name, "CHANGELOG") || strings.HasSuffix(name, ".rdoc"):
		if inProj && !underDataDir(p, true) {
			return nil, true
		}
	}
	if inProj && inExt(p) && (nativeSrc[strings.ToLower(path.Ext(name))] || name == "extconf.rb" || name == "depend") {
		wd := w(model.ReasonUnknownFile, p, proj, "native extension source of Ruby project "+proj+": the compiled library is loaded by tests")
		wd.Suggest = suggestRule(p)
		return []model.Widening{wd}, true
	}
	return nil, false
}

// inExt: p lies below a directory named ext.
func inExt(p string) bool {
	for _, s := range strings.Split(path.Dir(p), "/") {
		if s == "ext" {
			return true
		}
	}
	return false
}

// harmlessGemspec: both versions exist and differ only in harmless attributes.
func harmlessGemspec(p string, base, head tree.Tree) bool {
	if base == nil || head == nil {
		return false
	}
	bb, err1 := base.Read(p)
	hb, err2 := head.Read(p)
	if err1 != nil || err2 != nil {
		return false
	}
	fb, ok1 := rbscan.GemspecFingerprint(bb)
	fh, ok2 := rbscan.GemspecFingerprint(hb)
	return ok1 && ok2 && fb == fh
}
