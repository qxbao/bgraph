package dart

import (
	"slices"
	"testing"
)

func TestStandaloneNamespace(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   []string
	}{
		{`library leaves; int count() => 1; class Item {} const double rate = 1.5; bool ok() { return true; }`, []string{"count", "Item", "rate", "ok"}},
		{`String word() => "value";`, []string{"word"}},
		{`int wrong() => "value";`, nil},
		{`"int" wrong() => 1;`, nil},
		{`String wrong() => 'unescaped $!';`, nil},
		{`String wrong() => '\uZZZZ';`, nil},
		{`int wrong() = > 1;`, nil},
		{`const int wrong = 1fake;`, nil},
		{`int make() => 1; extension X on int { int get a => 0; }`, nil},
		{`library; int make() => 1;`, nil},
		{"// comment\rpart 'other.dart';\r\nint count() => 1;", nil},
	} {
		r := Scan([]byte(tc.source))
		var got []string
		if r.Dart != nil {
			got = r.Dart.Standalone
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q: standalone names %v, want %v", tc.source, got, tc.want)
		}
	}
}

func TestExportNamespaceAndConsumerWords(t *testing.T) {
	barrel := Scan([]byte(`library public.api; export 'a.dart' show alpha, gamma; export 'b.dart' hide beta;`)).Dart
	if barrel == nil || len(barrel.Exports) != 2 || barrel.Exports[0].Hide || !barrel.Exports[1].Hide ||
		!slices.Equal(barrel.Exports[0].Names, []string{"alpha", "gamma"}) || !slices.Equal(barrel.Exports[1].Names, []string{"beta"}) {
		t.Fatalf("unexpected export namespace: %+v", barrel)
	}
	for _, source := range []string{
		`export 'a.dart' if (dart.library.io) 'io.dart';`,
		`export 'a.dart' show alpha show beta;`,
		`export 'a.dart'; part 'b.dart';`,
		`export 'a\u002edart';`,
		`export 'a%2Edart';`,
	} {
		if r := Scan([]byte(source)); r.Dart != nil && len(r.Dart.Exports) != 0 {
			t.Errorf("%q: unexpected export namespace %+v", source, r.Dart.Exports)
		}
	}
	words := Scan([]byte(`import 'api.dart' as p; void show() { print('$alpha ${p.beta()}'); }`)).Dart
	for _, name := range []string{"alpha", "beta", "p"} {
		if words == nil || !slices.Contains(words.Words, name) {
			t.Errorf("%q not found in %+v", name, words)
		}
	}
}
