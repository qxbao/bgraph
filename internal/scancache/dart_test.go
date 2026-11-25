package scancache

import (
	"reflect"
	"testing"

	"github.com/qxbao/bgraph/internal/model"
	"github.com/qxbao/bgraph/internal/tree"
)

func TestDartScanRoundTrip(t *testing.T) {
	tr := tree.NewMapTree(map[string]string{
		"lib/a.dart":    "import 'package:x/y.dart' if (dart.library.io) 'io.dart';\nexport 'b.dart';\npart 'a.g.dart';\nimport augment 'c.dart';\n",
		"test/t.dart":   "void main() { File('test/fixtures/$n.json'); Isolate.spawnUri(u, [], null); Process.run('dart', ['bin/x.dart']); }\n",
		"consumer.dart": "import 'barrel.dart';\nvoid main() { print('$alpha'); }\n",
		"barrel.dart":   "export 'leaf.dart' hide other;\n",
		"leaf.dart":     "int alpha() => 1;\n",
		"bad.dart":      "var s = 'x",
	})
	c := New()
	for _, p := range tr.Files() {
		r, err := c.Scan(tr, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		got, err := decode(encode(r))
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(r.Imports) == 0 {
			r.Imports = nil
		}
		if !reflect.DeepEqual(got, r) {
			t.Fatalf("%s: round trip mismatch\n got %+v\nwant %+v", p, got, r)
		}
	}
	if r, _ := c.Scan(tr, "lib/a.dart"); len(r.Imports) != 5 || r.Imports[0].Kind != model.ImportDartImport {
		t.Fatalf("unexpected scan: %+v", r.Imports)
	}
	if r, _ := c.Scan(tr, "bad.dart"); !r.Uncertain {
		t.Fatal("bad.dart must be uncertain")
	}
	// The Dart kinds were appended: existing indexes do not move, so rows written before keep decoding.
	if kindIdx[model.ImportRbLoadPath] != 30 || kindIdx[model.ImportDartImport] != 31 || kindIdx[model.ImportDartProcess] != 36 {
		t.Fatalf("kind indexes moved: %d %d %d", kindIdx[model.ImportRbLoadPath], kindIdx[model.ImportDartImport], kindIdx[model.ImportDartProcess])
	}
}
