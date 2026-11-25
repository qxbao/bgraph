package ruby

import "strings"

// AutoloadReferences finds constant names and operations that may reach a constant indirectly.
// dynamicLine is zero for ordinary lookup, positive for a dynamic site and -1 for an uncertain scan.
func AutoloadReferences(src []byte) (names map[string]int, dynamicLine int) {
	lx, why := lex(src)
	if why != "" {
		return nil, -1
	}
	names = map[string]int{}
	visit := func(ts []token) {
		for i, t := range ts {
			switch t.kind {
			case tIdent:
				name := string(src[t.start:t.end])
				if name[0] >= 'A' && name[0] <= 'Z' {
					if _, seen := names[name]; !seen {
						names[name] = int(t.line)
					}
				}
				if dynamicLine == 0 && autoloadReflection(name) {
					// The conventional Rails test setting disables eager loading. A computed value can enable it.
					if name != "eager_load" || !eagerLoadDisabled(ts, i, src) {
						dynamicLine = int(t.line)
					}
				}
			case tSym:
				if dynamicLine == 0 {
					name, static := strings.TrimPrefix(string(src[t.start:t.end]), ":"), true
					if t.aux >= 0 {
						name, static = lx.strs[t.aux].static()
					}
					if !static || autoloadReflection(name) {
						dynamicLine = int(t.line)
					}
				}
			case tStr:
				if dynamicLine == 0 && lx.strs[t.aux].kind == sSymStr {
					if name, _ := lx.strs[t.aux].static(); autoloadReflection(name) {
						dynamicLine = int(t.line)
					}
				}
			}
		}
	}
	visit(lx.toks)
	for _, str := range lx.strs {
		for _, part := range str.parts {
			if part.isCode {
				visit(part.code)
			}
		}
	}
	return names, dynamicLine
}

func autoloadReflection(name string) bool {
	switch name {
	case "const_get", "const_set", "const_defined?", "constants", "const_missing", "const_source_location", "remove_const",
		"constantize", "safe_constantize", "autoload", "autoload?", "autoload_relative", "autoload_paths", "autoload_once_paths",
		"autoload_lib", "autoload_lib_once", "load_file", "eager_load", "eager_load_all", "eager_load_dir", "eager_load_namespace",
		"eval", "class_eval", "module_eval", "instance_eval", "class_exec", "module_exec", "instance_exec", "class_attribute",
		"send", "__send__", "public_send", "method", "public_method", "instance_method", "public_instance_method",
		"descendants", "subclasses", "ObjectSpace", "inflect", "inflector", "collapse", "on_load", "on_unload", "to_prepare",
		"Marshal", "YAML", "Psych", "delegate_missing_to", "method_missing", "define_method", "define_singleton_method":
		return true
	}
	return false
}

func eagerLoadDisabled(ts []token, i int, src []byte) bool {
	if i < 2 || i+2 >= len(ts) {
		return false
	}
	text := func(j int) string { return string(src[ts[j].start:ts[j].end]) }
	return text(i-2) == "config" && text(i-1) == "." && text(i+1) == "=" && text(i+2) == "false" &&
		(i+3 == len(ts) || ts[i+3].kind == tNL || text(i+3) == ";")
}
