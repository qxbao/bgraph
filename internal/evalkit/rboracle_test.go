package evalkit

import (
	"reflect"
	"testing"

	"github.com/qxbao/bgraph/internal/tree"
)

func TestParseRbName(t *testing.T) {
	for _, c := range []struct {
		in   string
		want rbName
	}{
		{"archives a directory - ./fastlane/spec/actions_specs/zip_spec.rb[1:1:1:8]", rbName{form: "path", path: "fastlane/spec/actions_specs/zip_spec.rb", desc: "archives a directory - ./fastlane/spec/actions_specs/zip_spec.rb[1:1:1:8]"}},
		{"spec/a/b_spec.rb:12", rbName{form: "path", path: "spec/a/b_spec.rb", desc: "spec/a/b_spec.rb:12"}},
		{"features/post_data.feature:30  Scenario: Use page.name variable", rbName{form: "cucumber", path: "features/post_data.feature", desc: "features/post_data.feature:30  Scenario: Use page.name variable"}},
		{"Minitest::Test::TestAPI::TestConvert#test_should_convert_x", rbName{form: "minitest", class: "Minitest::Test::TestAPI::TestConvert", method: "test_should_convert_x"}},
		{"TestFoo::test_bar", rbName{form: "minitest", class: "TestFoo", method: "test_bar"}},
		{"test_bar(TestFoo)", rbName{form: "minitest", class: "TestFoo", method: "test_bar"}},
		{"test_bar (Mod::TestFoo)", rbName{form: "minitest", class: "Mod::TestFoo", method: "test_bar"}},
		{"test_rpc_server[ipv4]", rbName{form: "method", method: "test_rpc_server[ipv4]"}},
		{"test_password", rbName{form: "method", method: "test_password"}},
		{"TestFilters#test_: filters where_exp filter should filter objects.", rbName{form: "shoulda", class: "TestFilters", desc: "filters where_exp filter should filter objects"}},
		{"test: A site with defaults should affect only the path. ", rbName{form: "shoulda", desc: "A site with defaults should affect only the path"}},
		{"allows \"#@@a\" with freeze", rbName{form: "desc", desc: "allows \"#@@a\" with freeze"}},
		{"strings for rotate_age[weekly]", rbName{form: "desc", desc: "strings for rotate_age[weekly]"}},
		{"Foo::Bar#baz does x", rbName{form: "desc", desc: "Foo::Bar#baz does x"}},
	} {
		if got := parseRbName(c.in); got != c.want {
			t.Errorf("%q: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestReadRbLit(t *testing.T) {
	for _, c := range []struct {
		in   string
		want rbLit
		ok   bool
	}{
		{`'it''s' do`, rbLit{"it"}, true},
		{`'a \'b\' \n' do`, rbLit{`a 'b' \n`}, true},
		{`"a \"b\" \#{c}" do`, rbLit{`a "b" #{c}`}, true},
		{`"reject '#{x}' now" do`, rbLit{"reject '", "' now"}, true},
		{`"x #{a({b: 1})} y"`, rbLit{"x ", " y"}, true},
		{`"allows #@@a and #$b" do`, rbLit{"allows ", " and ", ""}, true},
		{`%q(a (b) c) do`, rbLit{"a (b) c"}, true},
		{`%(a #{b})`, rbLit{"a ", ""}, true},
		{`Foo do`, nil, false},
		{`"unterminated`, nil, false},
	} {
		got, ok := readRbLit(c.in)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q %v want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestReadRbLitConcat(t *testing.T) {
	for in, want := range map[string]rbLit{
		"'a ' \\\n     'b' do":       {"a b"},
		"'a ' 'b' \"c#{x}d\" do":     {"a bc", "d"},
		"'a' do\n  it 'b'":           {"a"},
		"'a', 'b'":                   {"a"},
		"\"x #{y} \" \\\r\n  'z' do": {"x ", " z"},
	} {
		if got, ok := readRbLitConcat(in); !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %q %v want %q", in, got, ok, want)
		}
	}
}

func TestRbMethodKey(t *testing.T) {
	for in, want := range map[string]string{
		"test_:backend option should override": "test_backend_option_should_override",
		"test_backend_option_should_override":  "test_backend_option_should_override",
		"test: Do not retry (when) x":          "test_Do_not_retry_when_x",
		"test_rpc__server":                     "test_rpc_server",
	} {
		if got := rbMethodKey(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestRbIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"spec/a/foo_spec.rb": true, "test/test_foo.rb": true, "test/foo_test.rb": true, "fastlane/spec/x_spec.rb": true,
		"test/unit/tc_x.rb": true, "spec/spec_helper.rb": false, "test/test_helper.rb": true, // test_helper.rb matches test_*.rb (contract)
		"spec/fixtures/foo_spec.rb": false, "lib/foo_spec.rb": false, "spec/support/shared.rb": false, "vendor/spec/x_spec.rb": false,
	} {
		if got := RbIsTestFile(p); got != want {
			t.Errorf("%s: got %v want %v", p, got, want)
		}
	}
}

func TestLocateRuby(t *testing.T) {
	mt := tree.NewMapTree(map[string]string{
		"test/api_test.rb":    "require_relative 'test_helper'\ncontext 'API' do\n  context 'Convert' do\n    test 'should convert (file) to x' do\n    end\n  end\nend\n",
		"test/blocks_test.rb": "context 'Blocks' do\n  test 'should convert (file) to x' do\n  end\n  test \"should handle #{kind} blocks\" do\n  end\nend\n",
		"test/test_filters.rb": "class TestFilters < JekyllUnitTest\n  context \"filters\" do\n    context \"where_exp filter\" do\n      should \"filter objects\" do\n" +
			"      end\n    end\n  end\nend\n",
		"test/test_site.rb":             "class TestSite < JekyllUnitTest\n  def test_password\n  end\nend\n",
		"test/plugin/test_in_x.rb":      "class XTest < Test::Unit::TestCase\n  data('ipv4' => 1)\n  def test_rpc_server(data)\n  end\n  test 'strings for rotate_age' do\n  end\nend\n",
		"spec/rubocop/cop/a_spec.rb":    "RSpec.describe RuboCop::Cop::A, :config do\n  it 'registers an offense' do\n  end\n  it 'allows \"#@@a\" with freeze' do\n  end\nend\n",
		"spec/rubocop/cop/b_spec.rb":    "RSpec.describe RuboCop::Cop::B do\n  describe '#call' do\n    it 'registers an offense' do\n    end\n  end\n  it_behaves_like 'shared thing'\nend\n",
		"spec/support/shared.rb":        "RSpec.shared_examples 'shared thing' do\n  it 'does the shared thing' do\n  end\nend\n",
		"spec/fpm/x_spec.rb":            "describe FPM::X do\n  ['a b', 'c'].each do |v|\n    it \"should reject '#{v}'\" do\n    end\n  end\nend\n",
		"fastlane/spec/zip_spec.rb":     "describe Fastlane do\nend\n",
		"features/post_data.feature":    "Feature: x\n",
		"spec/fixtures/ignored_spec.rb": "it 'registers an offense' do\nend\n",
		"test/helper.rb":                "class JekyllUnitTest < Minitest::Test\n  def test_dir(*subdirs)\n  end\nend\n",
		"test/test_url.rb":              "class TestURL < JekyllUnitTest\nend\n",
		"spec/rubocop/cop/c_spec.rb":    "RSpec.describe C do\n  it 'registers an offense when using ' \\\n     'two lines' do\n  end\nend\n",
		"test/document_test.rb":         "context 'Document' do\n  test ':backend option should override' do\n  end\nend\n",
	})
	names := []string{
		"Minitest::Test::TestAPI::TestConvert#test_should_convert_file_to_x", // method in two files, the class picks one
		"test_should_convert_file_to_x",                                      // ambiguous
		"Minitest::Test::TestBlocks#test_should_handle_listing_blocks",       // interpolated test name
		"TestFilters#test_: filters where_exp filter should filter objects. ",
		"test: filters where_exp filter should filter objects. ",
		"test_password",
		"test_rpc_server[ipv4]",
		"strings for rotate_age[weekly]",
		"registers an offense",
		"allows \"#@@a\" with freeze",
		"RuboCop::Cop::B#call registers an offense",
		"does the shared thing",
		"should reject 'hello (1234)'",
		"zips - ./fastlane/spec/zip_spec.rb[1:1]",
		"features/post_data.feature:30  Scenario: Use page.name variable",
		"nothing matches this",
		"TestURL#test_dir",
		"registers an offense when using two lines",
		"test_backend_option_should_override",
	}
	patch := map[string]bool{"spec/rubocop/cop/a_spec.rb": true}
	got := LocateRubyTests(mt, names, patch)
	want := []struct {
		files   []string
		via     string
		notUnit bool
	}{
		{[]string{"test/api_test.rb"}, "method+class", false},
		{[]string{"test/api_test.rb", "test/blocks_test.rb"}, "method", false},
		{[]string{"test/blocks_test.rb"}, "method-template+class", false},
		{[]string{"test/test_filters.rb"}, "composite+class", false},
		{[]string{"test/test_filters.rb"}, "composite", false},
		{[]string{"test/test_site.rb"}, "method", false},
		{[]string{"test/plugin/test_in_x.rb"}, "method+label", false},
		{[]string{"test/plugin/test_in_x.rb"}, "desc+label", false},
		{[]string{"spec/rubocop/cop/a_spec.rb", "spec/rubocop/cop/b_spec.rb"}, "desc", false},
		{[]string{"spec/rubocop/cop/a_spec.rb"}, "desc", false},
		{[]string{"spec/rubocop/cop/b_spec.rb"}, "composite", false},
		{[]string{"spec/rubocop/cop/b_spec.rb"}, "desc", false},
		{[]string{"spec/fpm/x_spec.rb"}, "template", false},
		{[]string{"fastlane/spec/zip_spec.rb"}, "path", false},
		{[]string{"features/post_data.feature"}, "path", false},
		{nil, "", false},
		{[]string{"test/test_url.rb"}, "class-inherited", false},
		{[]string{"spec/rubocop/cop/c_spec.rb"}, "desc", false},
		{[]string{"test/document_test.rb"}, "method", false},
	}
	for i, w := range want {
		g := got[i]
		if !reflect.DeepEqual(g.Files, w.files) || g.Via != w.via || g.NotUnit != w.notUnit {
			t.Errorf("%q: got files=%v via=%q notUnit=%v (form %s), want %v %q %v", g.Name, g.Files, g.Via, g.NotUnit, g.Form, w.files, w.via, w.notUnit)
		}
		if g.NotFound != (len(w.files) == 0) || g.Ambiguous != (len(w.files) > 1) {
			t.Errorf("%q: NotFound=%v Ambiguous=%v", g.Name, g.NotFound, g.Ambiguous)
		}
	}
	if !got[8].InPatch || !reflect.DeepEqual(got[8].PatchFiles, []string{"spec/rubocop/cop/a_spec.rb"}) || got[9].Helpers != nil {
		t.Errorf("patch files: %+v", got[8])
	}
	if !reflect.DeepEqual(got[11].Helpers, []string{"spec/support/shared.rb"}) {
		t.Errorf("helpers: %+v", got[11])
	}
}
