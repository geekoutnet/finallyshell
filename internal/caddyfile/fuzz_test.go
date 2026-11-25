package caddyfile

import (
	"os"
	"path/filepath"
	"testing"
)

// The Caddy panel's Sites section parses whatever Caddyfile the server
// has: no input may panic or keep the parser from returning.
func FuzzParseSites(f *testing.F) {
	for _, seed := range []string{
		"example.com {\n\treverse_proxy localhost:8080\n}\n",
		"{\n\temail a@b.c\n}\n\n(common) {\n\tencode zstd gzip\n}\n\nexample.com, www.example.com {\n\timport common\n\troot * /srv\n\tfile_server\n}\n",
		":80 {\n\troot * /usr/share/caddy\n\tfile_server\n}\n",
		"a.example {\n\t@api path /api/*\n\thandle @api {\n\t\treverse_proxy 127.0.0.1:3000\n\t}\n\thandle {\n\t\troot * /srv\n\t\ttry_files {path} /index.html\n\t\tfile_server\n\t}\n}\n",
		"b.example {\n\trespond <<HTML\n\t\t<p>hi</p>\n\t\tHTML 200\n}\n",
		"c.example {\n\tbasic_auth {\n\t\tbob $2a$14$abc\n\t}\n\theader {\n\t\t-Server\n\t\tStrict-Transport-Security \"max-age=31536000;\"\n\t}\n}\n",
		"d.example {\n\treverse_proxy {\n\t\tto a:1 b:2\n\t\tlb_policy round_robin\n\t\thealth_uri /health\n\t\ttransport http {\n\t\t\ttls_insecure_skip_verify\n\t\t}\n\t}\n}\n",
		"import sites/*\n",
		"e.example {\n\ttls {$EMAIL}\n\tphp_fastcgi unix//run/php/php-fpm.sock\n}\n",
		"f.example {\r\n\tredir https://g.example{uri} permanent\r\n}\r\n",
		"&(route) {\n\trespond ok\n}\nh.example {\n\tinvoke route\n}\n",
		"# only a comment\n",
	} {
		f.Add(seed)
	}
	if files, err := filepath.Glob("testdata/*"); err == nil {
		for _, name := range files {
			if data, err := os.ReadFile(name); err == nil {
				f.Add(string(data))
			}
		}
	}
	f.Fuzz(func(t *testing.T, source string) {
		doc, err := Parse(source)
		if err != nil {
			return
		}
		doc.Snippets()
		for _, node := range doc.Sites() {
			doc.HasComments(node)
			active := node
			if node.Kind == Disabled {
				copy := *node
				copy.Kind = Site
				active = &copy
			}
			if spec, err := ParseSite(active); err == nil {
				GenerateSite(spec)
			}
		}
	})
}
