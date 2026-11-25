package dart

import (
	"reflect"
	"strings"
	"testing"

	"github.com/qxbao/bgraph/internal/model"
)

var fuzzSeeds = []string{
	"import 'a.dart'; export 'b.dart' show c; part 'd.g.dart'; part of 'e.dart'; import augment 'f.dart';",
	"import 'a.dart' if (dart.library.io) 'b.dart' if (dart.library.html == 'true') 'c.dart' deferred as x;",
	"var a = 'x${b('${c}')}y' \"z$d\" r'$e' '''\nf\n''' r\"\"\"g\"\"\";",
	"/* /* nested */ */ /// doc\n// line\nvoid main() { Isolate.spawnUri(Uri.parse('w.dart'), [], null); }",
	"Isolate.spawnUri(u, [], null); final f = Isolate.spawnUri; mirrors.loadUri(Uri.file('x/$y.dart'));",
	"spawnHybridCode('void hybridMain(c) {}'); spawnHybridUri('helper.dart'); final f = spawnHybridCode;",
	"Process.run('dart', ['bin/x.dart']); TestProcess.start(Platform.resolvedExecutable, [p.join('bin', 'y.dart')]);",
	"p.join(dir, 'fixtures', 'a.json'); path.join('test', name); matchesGoldenFile('goldens/$n.png');",
	"#!/usr/bin/env dart\n\xef\xbb\xbfimport 'a.dart';",
	"'unterminated", "'''unterminated", "/* unterminated", "'${unterminated", "${", "}", "'$'", "'\\", "r'", "(((", ")))",
	"1.5e-3 0x1F 1_000 .5 #sym a?.b ?? c ..d ...e",
	"\x00", "\xff\xfe",
}

func FuzzScan(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		a := Scan([]byte(src))
		b := Scan([]byte(src))
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("non-deterministic: %+v vs %+v", a, b)
		}
		if a.Uncertain != (a.UncertainReason != "") {
			t.Fatalf("inconsistent uncertain flag: %+v", a)
		}
		if a.Imports == nil {
			t.Fatal("nil Imports")
		}
		maxLine := strings.Count(src, "\n") + 1
		loads := 0
		for _, r := range a.Imports {
			if r.Line < 1 || r.Line > maxLine {
				t.Fatalf("line %d out of range [1,%d]: %+v", r.Line, maxLine, r)
			}
			if r.Kind == "" {
				t.Fatalf("empty kind: %+v", r)
			}
			if r.Kind == model.ImportDartLoad {
				loads++
				if !r.Dynamic && r.Spec == "" {
					t.Fatalf("static load without a URI: %+v", r)
				}
			}
		}
		// Every load site the lexer sees yields a reference (a URI or a dynamic site).
		if a.UncertainReason != "file_too_large" {
			sites := 0
			for _, seg := range Lex([]byte(src)).Segments {
				for _, tk := range seg {
					if tk.Kind == Word {
						switch tk.Text {
						case "spawnUri", "loadUri", "spawnHybridUri", "spawnHybridCode":
							sites++
						}
					}
				}
			}
			if sites != loads {
				t.Fatalf("%d load sites, %d load refs: %+v", sites, loads, a.Imports)
			}
		}
	})
}

// Every seed must run without panicking (this also runs when -fuzz is not enabled).
func TestFuzzSeeds(t *testing.T) {
	for _, s := range fuzzSeeds {
		Scan([]byte(s))
	}
}

const propertyBase = `@TestOn('vm')
library;

import 'dart:io';
import 'package:a/b.dart' as b;
import 'stub.dart' if (dart.library.io) 'io.dart';
export 'src/c.dart' show C;
part 'd.g.dart';

void main() {
  final s = 'x ${b.f('}')} y';
  final g = matchesGoldenFile('goldens/$name.png');
  final f = File('test/fixtures/data.json');
  final j = p.join('test', 'assets', name);
  Isolate.spawnUri(Uri.parse('worker.dart'), [], null);
  Isolate.spawnUri(uri, [], null);
  Process.run('dart', ['bin/tool.dart']);
  final r = r'raw/$x.txt';
  final t = '''
multi/line.txt
''';
}
`

// Property: inserting white space or a comment between tokens must not change what is found (only line numbers).
func TestCommentInsertionInvariant(t *testing.T) {
	want := Scan([]byte(propertyBase))
	if want.Uncertain || len(want.Imports) < 12 {
		t.Fatalf("%+v", want)
	}
	lx := Lex([]byte(propertyBase))
	for _, seg := range lx.Segments[:1] {
		for _, tk := range seg {
			for _, in := range []string{" ", "\n", "/* c 'x' */", "/* a /* nested */ b */", "// import 'no.dart';\n", "/// doc 'y'\n"} {
				s := propertyBase[:tk.Start] + in + propertyBase[tk.Start:]
				got := Scan([]byte(s))
				if got.Uncertain || len(got.Imports) != len(want.Imports) {
					t.Fatalf("insert %q at %d changed the result:\n%+v\nvs\n%+v", in, tk.Start, got, want)
				}
				for i := range got.Imports {
					g, w := got.Imports[i], want.Imports[i]
					g.Line, w.Line = 0, 0
					if g != w {
						t.Fatalf("insert %q at %d changed ref %d: %+v vs %+v", in, tk.Start, i, g, w)
					}
				}
			}
		}
	}
}

func BenchmarkScan(b *testing.B) {
	src := []byte(strings.Repeat(propertyBase, 200))
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		Scan(src)
	}
}
