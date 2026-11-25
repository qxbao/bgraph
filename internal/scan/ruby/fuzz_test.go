package ruby

import (
	"reflect"
	"strings"
	"testing"
)

var fuzzSeeds = []string{
	"require 'a'\nrequire_relative \"../b\"\nload File.expand_path('x.rb', __dir__)\nautoload :X, 'x/y'\n",
	"Dir[File.join(__dir__, 'support/**/*.rb')].sort.each { |f| require f }\nDir.glob('a/*.rb').each do |f|\n  load f\nend\n",
	"s = <<~A + <<-'B' + <<C\n  #{x}\nA\n  y\n  B\nz\nC\nrequire 'q'",
	"x = %w[a b] + %i(c d) + %q{e {f}} + %Q<g #{h}> + %r!i!m + %s(j) + %x(k) + %(l)",
	"a = b / c; d = /e/; f = g /h/; i = ?j + ?\\n; k = :l; m = :\"n#{o}\"; p = :+",
	"=begin\nx\n=end\n__END__\nrequire 'no'",
	"class A < Rails::Application\nend\nloader = Zeitwerk::Loader.for_gem\nloader.push_dir(\"#{__dir__}/app\")",
	"Rake::TestTask.new { |t| t.pattern = ENV['T'] || 'test/**/*_test.rb'; t.libs << 'test'; t.ruby_opts << '-rhelper' }",
	"gem 'a', require: false\ngemspec path: 'x'\nBundler.require\nGem.bin_path('a', 'b')\nfixtures :all\n$LOAD_PATH << 'x'",
	"def /(x); end\ndef `(c); end\nclass << self; end\nx = y.empty? ? 1 : 2\n$\" << 1",
	"\xef\xbb\xbfrequire 'a'\r\nrequire 'b'\r\n",
	"'unterminated", "\"#{", "\"#{\"#{", "<<~A", "<<A\n", "%w(", "%", "?", ":", "/", "=begin", "\\", "#{}", "}", "{", "((((", "))))",
	"require", "require(", "require 'a',", "Dir[", "Dir.glob(", "File.join(", "File.expand_path(", "\x00", "\xff\xfe", "é",
	"def(A=", `?\C-C-C-`, "x = \"#{\"#{\"#{\"#{a}\"}\"}\"}\"", "foo(<<A, <<B)\na\nA\nb\nB\n", "eval(File.read(x))", "send(:require, 'a')",
}

func FuzzScan(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		a := Scan("a.rb", []byte(src))
		b := Scan("a.rb", []byte(src))
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
		for _, r := range a.Imports {
			if r.Kind == "" || r.Line < 1 || r.Line > maxLine {
				t.Fatalf("bad ref %+v (max line %d)", r, maxLine)
			}
			if strings.Contains(r.Spec, selfMark) {
				t.Fatalf("internal marker leaked: %+v", r)
			}
		}
		if miss := siteMisses(src); len(miss) > 0 {
			t.Fatalf("require-like calls at lines %v yield no reference: %+v", miss, a.Imports)
		}
		names, fallback := AutoloadReferences([]byte(src))
		if fallback < -1 || fallback > maxLine || (fallback == -1) != a.Uncertain {
			t.Fatalf("bad autoload fallback line %d (max line %d)", fallback, maxLine)
		}
		for name, line := range names {
			if line < 1 || line > maxLine {
				t.Fatalf("bad autoload reference %s at line %d (max line %d)", name, line, maxLine)
			}
		}
		GemspecFingerprint([]byte(src))
	})
}

// Every seed must run without panicking (this also runs when -fuzz is not enabled).
func TestFuzzSeeds(t *testing.T) {
	for _, s := range fuzzSeeds {
		Scan("a.rb", []byte(s))
		AutoloadReferences([]byte(s))
		GemspecFingerprint([]byte(s))
	}
}

const propertyBase = `require "a/b"
require_relative "../c"
load File.expand_path("d.rb", __dir__)
x = a / b + 2
r = /re'/ =~ s
autoload :E, "e/f"
s = "g #{h} i" + 'j' + %w[k l].join
Dir[File.join(__dir__, "support", "*.rb")].each { |f| require f }
t = <<~EOS
  body '
EOS
loader = Zeitwerk::Loader.for_gem
c = y ? 'z' : ?w
require "m/#{n}"
`

// Property: inserting spaces where white space already is, or a comment line at a statement start, must not change what is found.
func TestCommentInsertionInvariant(t *testing.T) {
	want := Scan("a.rb", []byte(propertyBase))
	wantNames, wantFallback := AutoloadReferences([]byte(propertyBase))
	if want.Uncertain || len(want.Imports) < 8 {
		t.Fatalf("%+v", want)
	}
	lx, why := lex([]byte(propertyBase))
	if why != "" {
		t.Fatal(why)
	}
	for k, tk := range lx.toks {
		var ins []string
		if tk.sp {
			ins = append(ins, " ", "\t ")
		}
		if k == 0 || lx.toks[k-1].kind == tNL {
			ins = append(ins, "# Ignored.constantize; c'\"/ =begin\n", "\n", "=begin\nrequire 'no'\n=end\n")
		}
		for _, in := range ins {
			s := propertyBase[:tk.start] + in + propertyBase[tk.start:]
			got := Scan("a.rb", []byte(s))
			if got.Uncertain || len(got.Imports) != len(want.Imports) {
				t.Fatalf("insert %q at %d changed the result:\n%+v\nvs\n%+v", in, tk.start, got, want)
			}
			for i := range got.Imports {
				g, w := got.Imports[i], want.Imports[i]
				g.Line, w.Line = 0, 0
				if g != w {
					t.Fatalf("insert %q at %d changed ref %d: %+v vs %+v", in, tk.start, i, g, w)
				}
			}
			names, fallback := AutoloadReferences([]byte(s))
			if len(names) != len(wantNames) || (fallback > 0) != (wantFallback > 0) {
				t.Fatalf("insert %q at %d changed autoload references: %v, %d", in, tk.start, names, fallback)
			}
			for name := range wantNames {
				if _, ok := names[name]; !ok {
					t.Fatalf("insert %q at %d hid constant %s", in, tk.start, name)
				}
			}
		}
	}
}

func BenchmarkScan(b *testing.B) {
	src := []byte(strings.Repeat(propertyBase, 200))
	b.SetBytes(int64(len(src)))
	for i := 0; i < b.N; i++ {
		Scan("a.rb", src)
	}
}
