package monitor

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// nginxBaotaSample is the shape of a BaoTa panel's nginx -T: the main file
// under /www/server/nginx, sites in the panel's vhost directory, and the
// Lua block its certificate checks include in every site.
const nginxBaotaSample = `# configuration file /www/server/nginx/conf/nginx.conf:
user www www;
events { worker_connections 1024; }
http {
    include proxy.conf;
    upstream api_pool {
        server 127.0.0.1:10161 weight=2;
        server unix:/run/api.sock;
    }
    include /www/server/panel/vhost/nginx/*.conf;
}
# configuration file /www/server/nginx/conf/proxy.conf:
proxy_temp_path /www/server/nginx/proxy_temp_dir;
# configuration file /www/server/panel/vhost/nginx/api.example.org.conf:
server
{
    listen 80;
    listen 443 ssl http2 ;
    server_name api.example.org;
    root /www/wwwroot/api.example.org;
    include /www/server/panel/vhost/nginx/well-known/api.example.org.conf;
    include enable-php-00.conf;
    if ($server_port !~ 443){
        rewrite ^(/.*)$ https://$host$1 permanent;
    }
    ssl_certificate    /www/server/panel/vhost/cert/api.example.org/fullchain.pem;
    location ^~ /api/ { proxy_pass http://api_pool/; }
    location ~ ^/a}b { return 404; }
    access_log  /www/wwwlogs/api.example.org.log;
}
# configuration file /www/server/panel/vhost/nginx/well-known/api.example.org.conf:

set $well_known '';
if ( $uri ~ "^/.well-known/" ) {
  set_by_lua_block $well_known {
    -- a comment with a quote ' and a brace }
    local m,err = ngx.re.match(ngx.var.uri,"/.well-known/(.*)","isjo")
    --[[ a long
    comment } ]]
    local text = [==[ } ]==]
    if m and not ngx.re.find(m[1],"\\./","isjo") then
      local t = { "}" }
      return #t > 0 and m[0] or ""
    end
    return ""
  }
}
if ( $well_known != "" ) {
  return 200 $well_known;
}
# configuration file /www/server/nginx/conf/enable-php-00.conf:

# configuration file /www/server/panel/vhost/nginx/plain.example.org.conf:
server { listen 8080; server_name plain.example.org; proxy_pass_header Server; location / { proxy_pass http://172.17.0.2:3000; } }
`

func TestNginxParseBaota(t *testing.T) {
	tree, err := ParseNginx(nginxBaotaSample)
	if err != nil || len(tree.Unparsed) != 0 || len(tree.Sites) != 2 || tree.Main != "/www/server/nginx/conf/nginx.conf" {
		t.Fatalf("%+v %v: %v", tree.Sites, tree.Unparsed, err)
	}
	api := tree.Sites[0]
	if !reflect.DeepEqual(api.Names, []string{"api.example.org"}) || !reflect.DeepEqual(api.Listen, []NginxListen{{80, false, false, false, ""}, {443, true, true, false, ""}}) ||
		!reflect.DeepEqual(api.Proxy, []string{"http://api_pool/"}) || api.AccessLog != "/www/wwwlogs/api.example.org.log" || api.Line != 1 {
		t.Fatalf("%+v", api)
	}
	if !reflect.DeepEqual(tree.Upstreams["api_pool"], []string{"127.0.0.1:10161", "unix:/run/api.sock"}) {
		t.Fatalf("%v", tree.Upstreams)
	}
	if plain := tree.Sites[1]; !reflect.DeepEqual(plain.Proxy, []string{"http://172.17.0.2:3000"}) || plain.Listen[0].Port != 8080 {
		t.Fatalf("%+v", plain)
	}
}

func TestNginxTokensLikeNginx(t *testing.T) {
	words := func(text string) []string {
		tokens, err := nginxTokens(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		var result []string
		for _, token := range tokens {
			result = append(result, cmpOr(token.word, string(token.mark)))
		}
		return result
	}
	for _, item := range []struct {
		text string
		want []string
	}{
		{`a b}c d"e;`, []string{"a", "b}c", `d"e`, ";"}},
		{`if ($a ~ "x y") {}`, []string{"if", "($a", "~", "x y", ")", "{", "}"}},
		{`return 200 ${host}x#y; # note`, []string{"return", "200", "${host}x#y", ";"}},
		{`a "q\"t" 'b\\c' d\;e;`, []string{"a", `q"t`, `b\c`, `d\;e`, ";"}},
		{"content_by_lua_block { ngx.say('}') -- }\n }\nb c;", []string{"content_by_lua_block", "{", "}", "b", "c", ";"}},
	} {
		if got := words(item.text); !slices.Equal(got, item.want) {
			t.Errorf("%q: %q, want %q", item.text, got, item.want)
		}
	}
	for _, bad := range []string{`a "b"c;`, `a "b;`, `a b`, "x_by_lua_block { [[ }", `a \`} {
		if tokens, err := nginxTokens(bad); err == nil {
			if _, err := nginxNodes(&tokens, 0); err == nil {
				t.Errorf("accepted %q", bad)
			}
		}
	}
}

func TestNginxBaotaEditsAndMaster(t *testing.T) {
	tree, err := ParseNginx(nginxBaotaSample)
	if err != nil {
		t.Fatal(err)
	}
	site := "/www/server/panel/vhost/nginx/api.example.org.conf"
	if !tree.Editable(site) || !tree.Editable(tree.Main) || tree.Editable("/www/server/panel/vhost/nginx/other.conf") || tree.Editable("/www/server/panel/vhost/cert/api.example.org/fullchain.pem") {
		t.Fatal("wrong editable files")
	}
	if (NginxTree{Main: NginxMain, Files: []string{"/opt/site.conf"}}).Editable("/opt/site.conf") {
		t.Fatal("standard layout edits files outside /etc/nginx")
	}
	read, err := NginxReadCommand(site, tree)
	if err != nil || !strings.Contains(read, "'head' '-c'") {
		t.Fatalf("%q: %v", read, err)
	}
	stamp := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	change, err := NewNginxChange(site, "server {}\n", "server { }\n", stamp, tree)
	if err != nil {
		t.Fatal(err)
	}
	save, err := change.SaveCommand("server {}\n", tree)
	if err != nil || !strings.Contains(save, "snapshot=$('nginx' '-c' '/www/server/nginx/conf/nginx.conf' '-T' | 'sha256sum')") {
		t.Fatalf("%q: %v", save, err)
	}

	var status NginxStatus
	ParseNginxMaster(&status, "1382\nconf\t/www/server/nginx/conf/nginx.conf\n")
	if status.PID != 1382 || !status.MasterVerified || status.Custom || status.Conf != "/www/server/nginx/conf/nginx.conf" || status.State != "active" {
		t.Fatalf("%+v", status)
	}
	for _, text := range []string{"12\ncustom\n", "12\nconf\tnginx.conf\n"} {
		status = NginxStatus{Unit: true, State: "inactive"}
		if ParseNginxMaster(&status, text); !status.Custom || status.State != "inactive" {
			t.Fatalf("%q: %+v", text, status)
		}
	}
	status = NginxStatus{}
	if ParseNginxMaster(&status, ""); status.MasterVerified {
		t.Fatal("verified no master")
	}
	if !strings.Contains(NginxMasterCommand(0), "'/www/server/nginx/logs/nginx.pid'") {
		t.Fatal("BaoTa PID file not searched")
	}

	var scripts []string
	run := func(_ context.Context, script string) (string, string, error) {
		scripts = append(scripts, script)
		if strings.HasSuffix(script, "'-T'") {
			return nginxBaotaSample, "", nil
		}
		return "", "", nil
	}
	inventory, err := ReadNginxInventoryFrom(context.Background(), run, "/www/server/nginx/conf/nginx.conf")
	if err != nil || len(inventory.Sites) != 2 || scripts[0] != nginxLF+"'nginx' '-c' '/www/server/nginx/conf/nginx.conf' '-T'" {
		t.Fatalf("%q: %v", scripts, err)
	}
	if _, err := ReadNginxInventoryFrom(context.Background(), run, "conf;id"); err == nil {
		t.Fatal("dumped an invalid file")
	}
}

func TestNginxVisits(t *testing.T) {
	tree, err := ParseNginx(nginxBaotaSample)
	if err != nil {
		t.Fatal(err)
	}
	got := tree.Visits(tree.Sites[0])
	want := []NginxVisit{
		{URL: "http://api.example.org/", Host: "api.example.org", Addr: "127.0.0.1"},
		{URL: "https://api.example.org/", Host: "api.example.org", Addr: "127.0.0.1"},
		{URL: "http://127.0.0.1:10161/", Backend: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if got := tree.Visits(tree.Sites[1]); !reflect.DeepEqual(got, []NginxVisit{
		{URL: "http://plain.example.org:8080/", Host: "plain.example.org", Addr: "127.0.0.1"},
		{URL: "http://172.17.0.2:3000", Backend: true},
	}) {
		t.Fatalf("%+v", got)
	}
	odd := NginxSite{
		Names:  []string{"_", "*.example.org", "~^www[.](.+)$", "10.0.0.9"},
		Listen: []NginxListen{{Port: 8443, TLS: true, Addr: "10.0.0.9"}, {Port: 0}, {Port: 81, Addr: "::1"}},
		Proxy:  []string{"http://$backend", "http://unix:/run/x.sock", "https://user@host.example.org/"},
	}
	if got := tree.Visits(odd); !reflect.DeepEqual(got, []NginxVisit{{URL: "https://10.0.0.9:8443/"}, {URL: "http://[::1]:81/"}}) {
		t.Fatalf("%+v", got)
	}
	if got := tree.Visits(NginxSite{Listen: []NginxListen{{Port: 81, Addr: "::1"}}}); !reflect.DeepEqual(got, []NginxVisit{{URL: "http://[::1]:81/"}}) {
		t.Fatalf("%+v", got)
	}
}
