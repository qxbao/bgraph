package rules

import "testing"

func TestRubyAutoloadDeclaration(t *testing.T) {
	valid := `[[ruby_autoload]]
paths = ["app/services/**/*.rb"]
reason = "Services load by constant name"`
	f, err := Parse("bgraph.toml", []byte(valid))
	if err != nil || len(f.RubyAutoload) != 1 || !f.RubyAutoload[0].Paths[0].Match("app/services/email.rb") {
		t.Fatalf("declaration not loaded: %+v, %v", f, err)
	}
	for _, invalid := range []string{
		`paths = ["app/**/*.rb"]`,
		`paths = []
reason = "services"`,
		`paths = [" "]
reason = "services"`,
		`paths = ["[{bad"]
reason = "services"`,
	} {
		if _, err := Parse("bgraph.toml", []byte("[[ruby_autoload]]\n"+invalid)); err == nil {
			t.Errorf("accepted invalid declaration %q", invalid)
		}
	}
}
