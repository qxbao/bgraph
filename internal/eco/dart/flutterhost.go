package dart

import (
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/qxbao/bgraph/internal/eco"
	"github.com/qxbao/bgraph/internal/graph"
	"github.com/qxbao/bgraph/internal/model"
)

type flutterHostFiles struct {
	owned, uncertain []string
}

// flutterAncestors collects the package itself and its Flutter ancestors once
// so each platform file can be assigned to its owner and a possible outer app.
func (r *repo) flutterAncestors() map[string][]*pkg {
	out := map[string][]*pkg{}
	for d, p := range r.pkgs {
		for ; p != nil; p = r.nearest(path.Dir(p.dir)) {
			if p.flutter {
				out[d] = append(out[d], p)
			}
			if p.dir == "." {
				break
			}
		}
	}
	return out
}

func addFlutterHostPath(hosts map[string]*flutterHostFiles, ancestors []*pkg, owner *pkg, f string, link bool) {
	for _, p := range ancestors {
		platform, _, hasSubpath := strings.Cut(rel(p.dir, f), "/")
		if !isFlutterPlatform(p, platform) || !link && !hasSubpath {
			continue
		}
		files := hosts[p.dir]
		if files == nil {
			files = &flutterHostFiles{}
			hosts[p.dir] = files
		}
		if !link && p == owner {
			files.owned = append(files.owned, f)
		} else {
			files.uncertain = append(files.uncertain, f)
		}
	}
}

func isFlutterPlatform(p *pkg, platform string) bool {
	switch platform {
	case "android", "ios", "linux", "macos", "windows", "web":
		return true
	case ".android", ".ios":
		return flutterDeclaration(p, "module")
	}
	return false
}

func flutterDeclaration(p *pkg, key string) bool {
	flutter, _ := p.raw["flutter"].(map[string]any)
	_, ok := flutter[key]
	return ok
}

// flutterToolingInput identifies platform metadata Flutter can read or update
// during command setup, before it chooses the widget or device test runner.
func flutterToolingInput(file string) bool {
	if file == "android/local.properties" || file == "android/gradle.properties" {
		return true
	}
	platform, _, _ := strings.Cut(file, "/")
	if platform != "ios" && platform != "macos" {
		return false
	}
	switch path.Base(file) {
	case "Podfile", "Podfile.lock", "project.pbxproj", "contents.xcworkspacedata":
		return true
	}
	return strings.HasSuffix(file, ".xcconfig") || strings.HasSuffix(file, ".xcscheme")
}

// conventionalFlutterHostInput recognizes known host source and build inputs.
// Extra payloads and fixtures under the same directories retain normal ownership.
func conventionalFlutterHostInput(file string) bool {
	if flutterToolingInput(file) {
		return true
	}
	platform, sub, _ := strings.Cut(file, "/")
	switch platform {
	case "android":
		switch sub {
		case "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", "app/build.gradle", "app/build.gradle.kts":
			return true
		}
		if !strings.HasPrefix(sub, "app/src/") {
			return false
		}
		_, source, _ := strings.Cut(strings.TrimPrefix(sub, "app/src/"), "/")
		return source == "AndroidManifest.xml" || strings.HasPrefix(source, "java/") && strings.HasSuffix(source, ".java") ||
			strings.HasPrefix(source, "kotlin/") && strings.HasSuffix(source, ".kt")
	case "ios", "macos":
		if sub == "Runner/Info.plist" {
			return true
		}
		return strings.HasPrefix(sub, "Runner/") && nativeHostSource(sub)
	case "linux", "windows":
		return sub == "CMakeLists.txt" || sub == "runner/CMakeLists.txt" ||
			strings.HasPrefix(sub, "runner/") && nativeHostSource(sub)
	case "web":
		return sub == "index.html" || sub == "flutter_bootstrap.js"
	}
	return false
}

func nativeHostSource(file string) bool {
	switch path.Ext(file) {
	case ".c", ".cc", ".cpp", ".h", ".hpp", ".m", ".mm", ".swift":
		return true
	}
	return false
}

func (b *builder) flutterHosts(files map[string]*flutterHostFiles) {
	readerInputs := map[string][]string{}
	for dir, paths := range files {
		p := b.r.pkgs[dir]
		// Plugins, add-to-app modules and native asset hooks can contribute to a
		// downstream test build, including builds that do not launch this app.
		shared := flutterDeclaration(p, "plugin") || flutterDeclaration(p, "module") || len(b.hooks[dir]) > 0
		var users []string
		if shared {
			for _, d := range b.dependentsOf(dir) {
				users = append(users, b.units[d]...)
			}
		} else {
			for _, f := range b.units[dir] {
				if within(rel(dir, f), "integration_test") {
					users = append(users, f)
				}
			}
			if len(users) == 0 && !b.t.Has(joinDir(dir, "lib/main.dart")) {
				// A library can have unrelated data in a directory with one of
				// these names. Leave it to the general rules without app evidence.
				continue
			}
		}
		// Analyze each tree's own files. The graph union covers additions,
		// removals and valid symlink aliases without a directory-wide pattern
		// that could match a nested package on either side.
		node := "<darthost:" + dir + ">"
		toolingNode := "<dartplatformtooling:" + dir + ">"
		var hasToolingInput bool
		fallback := map[string][]string{}
		addFallback := func(f string) {
			platform, _, _ := strings.Cut(rel(dir, f), "/")
			fallback[platform] = append(fallback[platform], escapeGlob(f))
		}
		for _, f := range paths.uncertain {
			addFallback(f)
		}
		for _, f := range paths.owned {
			if !shared && !conventionalFlutterHostInput(rel(dir, f)) {
				addFallback(f)
				continue
			}
			if _, ok := b.a.Roles[f]; !ok {
				b.a.Roles[f] = model.RoleData
			}
			b.edge(node, f, model.EdgeImplicit, "Flutter platform file", 0)
			if !shared {
				readerInputs[dir] = append(readerInputs[dir], escapeGlob(f))
			}
			if !shared && flutterToolingInput(rel(dir, f)) {
				b.edge(toolingNode, f, model.EdgeImplicit, "Flutter platform tooling metadata", 0)
				hasToolingInput = true
			}
		}
		for _, f := range users {
			b.edge(f, node, model.EdgeImplicit, "Flutter host build inputs", 0)
		}
		if len(users) > 0 && len(fallback) > 0 {
			fallbackNode := "<darthostadditional:" + dir + ">"
			for _, f := range users {
				b.edge(f, fallbackNode, model.EdgeImplicit, "Flutter host build inputs", 0)
			}
			for _, platform := range slices.Sorted(maps.Keys(fallback)) {
				// Unresolved triggers seed the host without claiming data owned
				// by a nested package or suppressing the normal Dart fallback.
				b.unresolved(model.Unresolved{File: fallbackNode, Spec: platform + "/", Class: model.UnresolvedUnknown,
					ScopeTier: model.TierModule, ScopeDir: joinDir(dir, platform), TriggerGlobs: fallback[platform],
					Detail: "Flutter host build may read additional platform inputs"})
			}
		}
		if hasToolingInput {
			for _, f := range b.units[dir] {
				b.edge(f, toolingNode, model.EdgeImplicit, "Flutter test command setup", 0)
			}
		}
	}
	if len(readerInputs) > 0 && len(b.hostReaders) > 0 {
		b.connectFlutterHostReaders(readerInputs)
	}
}

// Local Dart code using file, process or FFI APIs may read otherwise known
// build inputs through a computed path. A shared reader can be reached from
// multiple apps, each running its tests with a different working directory.
func (b *builder) connectFlutterHostReaders(inputs map[string][]string) {
	seeds := make([]graph.Seed, 0, len(b.hostReaders))
	for _, f := range b.hostReaders {
		seeds = append(seeds, graph.Seed{Path: f})
	}
	reach := graph.Build(b.a, eco.LinkAnalysis(b.t)).Walk(seeds)
	for _, d := range slices.Sorted(maps.Keys(inputs)) {
		node := "<darthostreader:" + d + ">"
		var linked bool
		for _, f := range b.units[d] {
			if _, ok := reach[f]; ok {
				b.edge(f, node, model.EdgeImplicit, "Dart code may read Flutter platform inputs", 0)
				linked = true
			}
		}
		if linked {
			// A raw file reader observes changes to the file's contents. It does not depend on inputs to its native build.
			b.unresolved(model.Unresolved{File: node, Spec: "Flutter platform files", Class: model.UnresolvedUnknown,
				ScopeTier: model.TierModule, ScopeDir: d, TriggerGlobs: inputs[d],
				Detail: "Dart code may read changed platform inputs through a computed path"})
		}
	}
}

func (b *builder) mayReadFlutterHost(from string, ref model.ImportRef, scan *model.DartScan) bool {
	if ref.Kind == model.ImportDartLoad || ref.Kind == model.ImportDartProcess {
		return true
	}
	if ref.Kind != model.ImportDartImport && ref.Kind != model.ImportDartExport {
		return false
	}
	uri, err := url.PathUnescape(ref.Spec)
	if err != nil || strings.ContainsRune(uri, 0) {
		return true
	}
	if strings.HasPrefix(uri, "dart:") {
		switch uri {
		case "dart:async", "dart:collection", "dart:convert", "dart:core", "dart:math", "dart:typed_data":
			return false
		}
		return true
	}
	candidates, external, _ := b.target(from, ref.Spec)
	if pkg, _, isPackage := strings.Cut(strings.TrimPrefix(uri, "package:"), "/"); !strings.HasPrefix(uri, "package:") ||
		isPackage && b.r.localPackage(b.r.nearest(path.Dir(from)), pkg) {
		if external {
			return true
		}
		return len(candidates) == 0 || slices.ContainsFunc(candidates, func(f string) bool { return !b.t.Has(f) })
	}
	// Common SDK entry points are used by most widget tests. Independently
	// preserve the Flutter test APIs that read custom golden files.
	switch uri {
	case "package:flutter/flutter.dart", "package:flutter/foundation.dart", "package:flutter/material.dart",
		"package:flutter/widgets.dart", "package:flutter/cupertino.dart", "package:flutter/services.dart",
		"package:integration_test/integration_test.dart", "package:test/test.dart":
		return false
	case "package:flutter_test/flutter_test.dart":
		return ref.Kind == model.ImportDartExport || scan == nil || slices.ContainsFunc(scan.Words, func(word string) bool {
			switch word {
			case "matchesGoldenFile", "goldenFileComparator", "GoldenFileComparator", "LocalFileComparator":
				return true
			}
			return false
		})
	}
	return true
}
