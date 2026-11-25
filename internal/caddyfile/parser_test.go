package caddyfile

import (
	"strings"
	"testing"
)

func TestLosslessCaddyfile(t *testing.T) {
	source := strings.ReplaceAll(`# leading comment
{
	email alice@example.test # keep
	servers {
		protocols h1 h2 h3
	}
}

(common) {
	encode zstd gzip
}
&(named) {
	respond "# not a comment"
}
import sites/*.caddyfile

example.test, https://www.example.test, \
 :8443 { # beside the address
	@one path /api/*
	@two {
		header X-Name "a \"b\" { # }"
	}
	route @one {
		handle_path /api/* {
			reverse_proxy {env.UPSTREAM} {$OTHER:localhost:8080}
		}
	}
	respond @two <<MARK
        { # this line belongs to the body }
        MARK
	respond `+"`a # literal { }\nnext`"+`
} # keep this too

# separate comment
next.example.test {
	import common
}
`, "\n", "\r\n")
	doc, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Nodes) != 6 || doc.GlobalBlock() != doc.Nodes[0] || len(doc.Snippets()) != 1 || doc.Nodes[2].Kind != NamedRoute || doc.Nodes[3].Kind != Import {
		t.Fatalf("unexpected top-level nodes: %#v", doc.Nodes)
	}
	sites := doc.Sites()
	first := sites[0]
	if len(sites) != 2 || strings.Join(first.Args, "|") != "example.test|https://www.example.test|:8443" {
		t.Fatalf("addresses: %#v", first.Args)
	}
	children := first.Children
	if len(children) != 5 || children[0].Kind != Matcher || children[1].Kind != Matcher || children[2].Children[0].Children[0].Args[1] != "{$OTHER:localhost:8080}" || children[3].Args[1] != "{ # this line belongs to the body }" || children[4].Args[0] != "a # literal { }\r\nnext" {
		t.Fatalf("unexpected site tree: %#v", children)
	}
	if len(doc.Comments) != 5 {
		t.Fatalf("comments: %#v", doc.Comments)
	}
	updated, err := doc.RenameSite(first, []string{"new.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if want := source[:first.Start] + "new.example.test" + source[first.HeadEnd:]; updated != want {
		t.Fatalf("rename changed unrelated bytes:\n%q\nwant\n%q", updated, want)
	}
	updated, err = doc.SetGlobal("email", "email bob@example.test")
	if err != nil || updated != strings.Replace(source, "alice@example.test", "bob@example.test", 1) {
		t.Fatalf("global edit: %v %q", err, updated)
	}
	disabled, err := doc.DisableSite(first)
	if err != nil {
		t.Fatal(err)
	}
	disabledDoc, err := Parse(disabled)
	if err != nil || disabledDoc.Sites()[0].Kind != Disabled {
		t.Fatalf("disable: %v", err)
	}
	enabled, err := disabledDoc.EnableSite(disabledDoc.Sites()[0])
	if err != nil || enabled != source {
		t.Fatalf("enable changed bytes: %v\n%q", err, enabled)
	}
}

func TestCaddyfileEdits(t *testing.T) {
	source := "\ufeff# start\r\nexample.test {\r\n\trespond ok\r\n}\r\n# end"
	doc, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	withGlobal, err := doc.SetGlobal("debug", "debug")
	if err != nil || withGlobal != "\ufeff# start\r\n{\r\n\tdebug\r\n}\r\n\r\n"+strings.TrimPrefix(source, "\ufeff# start\r\n") {
		t.Fatalf("insert global: %v %q", err, withGlobal)
	}
	newDoc, err := Parse(withGlobal)
	if err != nil {
		t.Fatal(err)
	}
	without, err := newDoc.SetGlobal("debug", "")
	if err != nil || !strings.Contains(without, "example.test {\r\n\trespond ok\r\n}\r\n# end") || strings.Contains(without, "debug") {
		t.Fatalf("remove global: %v %q", err, without)
	}
	if _, err := doc.SetGlobal("debug", "debug\nadmin off"); err == nil {
		t.Fatal("accepted unrelated option")
	}
	if _, err := doc.Replace(newDoc.Sites()[0], ""); err == nil {
		t.Fatal("accepted foreign node")
	}
	implicit, err := Parse("example.test\nroot * /www\nfile_server\n")
	if err != nil || len(implicit.Sites()) != 1 || implicit.Sites()[0].Block || len(implicit.Sites()[0].Children) != 2 {
		t.Fatalf("implicit site: %v %#v", err, implicit)
	}
	appended, err := implicit.Append("new.test {\n\trespond ok\n}")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Parse(appended); err != nil || len(got.Sites()) != 2 {
		t.Fatalf("append to implicit site: %v %q", err, appended)
	}
}

func TestCaddyfileMalformed(t *testing.T) {
	for _, source := range []string{"a.test {\n", "a.test {\nrespond \"unclosed", "a.test {\nrespond <<EOF\nabc\n}", "a.test {\n}\n}", "a.test {}\n" + "a.test {\n\x00\n}"} {
		if _, err := Parse(source); err == nil {
			t.Errorf("accepted malformed source %q", source)
		}
	}
}

func TestCaddyfileLexicalBoundaries(t *testing.T) {
	args, err := SplitArgs("root C:\\web\\\n")
	if err == nil || args != nil {
		t.Fatal("attached backslash should not consume the newline")
	}
	args, err = SplitArgs("arg \\\r\n \"one\\\ntwo\" \\<<literal")
	if err != nil || strings.Join(args, "|") != "arg|one\\\ntwo|<<literal" {
		t.Fatalf("tokens: %q %v", args, err)
	}
}

func TestCaddyfileDisabledMarkerDoesNotExposeText(t *testing.T) {
	source := "# finallyshell: disabled site\n#| example.test {\n#| \trespond ok\n#| }\n# finallyshell: end disabled site import other.caddy\n"
	doc, err := Parse(source)
	if err != nil || len(doc.Sites()) != 0 {
		t.Fatalf("recognized a forged disabled marker: %v %v", doc, err)
	}
}

func TestCaddyfileReplaceSiteKeepsLineStyleAndQuotedText(t *testing.T) {
	source := "# keep\r\nexample.test {\r\n\trespond old\r\n} # keep as well\r\n"
	doc, err := Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := doc.ReplaceSite(doc.Sites()[0], "example.test {\n\trespond \"first\nsecond\"\n}\n")
	if want := "# keep\r\nexample.test {\r\n\trespond \"first\nsecond\"\r\n} # keep as well\r\n"; err != nil || updated != want {
		t.Fatalf("replace site: %q %v", updated, err)
	}
}
