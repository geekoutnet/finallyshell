package monitor

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const nginxSample = `# configuration file /etc/nginx/nginx.conf:
events {}
http {
    access_log /var/log/nginx/custom.log main;
    include /etc/nginx/sites-enabled/*;
    include conf.d/*.conf;
}
stream { server { listen 1234; proxy_pass mail_backend; } }

# configuration file /etc/nginx/sites-enabled/front:
# deliberately { ignored }
server {
    listen 127.0.0.1:80 default_server;
    listen [::]:443 ssl http2;
    server_name front.example.org "www.example.org";
    server_name extra.example.org;
    include snippets/tls.conf;
    root '/srv/with # and ;';
    location / { proxy_pass http://backend; }
    location ~ "\\.php$" { try_files ${uri} =404; }
}
# configuration file /etc/nginx/snippets/tls.conf:
ssl_certificate /etc/letsencrypt/live/front/fullchain.pem;
ssl_certificate_key /etc/letsencrypt/live/front/privkey.pem;
# configuration file /etc/nginx/conf.d/default.conf:
server { listen 8080; http2 on; server_name _; access_log off; }
`

func TestNginxInventoryAndSafeChanges(t *testing.T) {
	const inactive = "# configuration file /etc/nginx/sites-available/sandbox:\nserver { listen 8081; server_name sandbox.example.org; root /srv/sandbox; }\n"
	ctx := context.Background()
	read := func(_ context.Context, script string) (string, string, error) {
		switch {
		case script == nginxLF+NginxDumpCommand:
			return nginxSample, "", nil
		case script == nginxLayoutCommand:
			return "debian\nenabled\tfront\ndisabled\tsandbox\ndisabled\tsandbox.bak-20261125120000\nconfd\n", "", nil
		case strings.Contains(script, "'set' '--' '/etc/nginx/sites-available/sandbox'"):
			return inactive, "", nil
		default:
			t.Fatalf("unexpected command: %s", script)
			return "", "", nil
		}
	}
	inventory, err := ReadNginxInventory(ctx, read)
	if err != nil || !inventory.Debian || !inventory.ConfD || len(inventory.Inactive) != 1 || inventory.Inactive[0].Names[0] != "sandbox.example.org" {
		t.Fatalf("%+v: %v", inventory, err)
	}
	file, link, state := inventory.Target(inventory.Sites[0])
	if file != NginxAvailable+"front" || link != "front" || state != "enabled" {
		t.Fatalf("%q %q %q", file, link, state)
	}
	for _, enable := range []bool{true, false} {
		script, err := inventory.ToggleCommand([]string{"sandbox", "front"}, enable, true, true)
		if err != nil || !strings.Contains(script, "'set' '--' 'sandbox' 'front'\n") || !strings.HasSuffix(script, "'nginx' '-t'\ntrap - EXIT\n'systemctl' 'reload' 'nginx.service'") {
			t.Fatalf("%q: %v", script, err)
		}
	}
	if _, err := inventory.ToggleCommand([]string{"../other"}, true, true, true); err == nil {
		t.Fatal("accepted escaping symlink name")
	}
	stamp := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	original, replacement := "server { listen 80; }\n", "server { listen 81; }\n"
	change, err := NewNginxChange(file, original, replacement, stamp, inventory.NginxTree)
	if err != nil || change.Backup != file+".bak-20261125120000" {
		t.Fatalf("%+v: %v", change, err)
	}
	write, err := change.SaveCommand(original, inventory.NginxTree)
	if err != nil || strings.Contains(write, replacement) || !strings.Contains(write, "'cat' >\"$tmp\"") || strings.Index(write, "'ln' '-T'") >= strings.Index(write, "'mv' '-fT'") {
		t.Fatalf("%q: %v", write, err)
	}
	restore, err := change.RestoreCommand(inventory.NginxTree, stamp.Add(time.Second))
	if err != nil || !strings.Contains(restore, "'cat' '"+change.Backup+"' >\"$tmp\"") || !strings.Contains(restore, ".bak-20261125120001") {
		t.Fatalf("%q: %v", restore, err)
	}
	if inventory.AllowsBackup(NginxEnabled+"front.bak-20261125120000") || !inventory.AllowsBackup(change.Backup) {
		t.Fatal("incorrect backup include matching")
	}
	issue, backups, err := inventory.CertbotIssueWithBackups([]string{"front.example.org"}, "admin@example.org", stamp)
	if err != nil || !slices.Contains(backups, change.Backup) || !strings.HasSuffix(issue, CertbotIssueCommand([]string{"front.example.org"}, "admin@example.org")) {
		t.Fatalf("%q %v: %v", issue, backups, err)
	}
}

func TestNginxParseTree(t *testing.T) {
	tree, err := ParseNginx(nginxSample)
	if err != nil || len(tree.Sites) != 2 {
		t.Fatalf("%+v: %v", tree.Sites, err)
	}
	front := tree.Sites[0]
	if front.File != "/etc/nginx/sites-enabled/front" || front.Line != 2 || !reflect.DeepEqual(front.Names, []string{"front.example.org", "www.example.org", "extra.example.org"}) ||
		!reflect.DeepEqual(front.Listen, []NginxListen{{80, false, false, true}, {443, true, true, false}}) ||
		front.Root != "/srv/with # and ;" || front.Proxy[0] != "http://backend" || front.Certificates[0] != "/etc/letsencrypt/live/front/fullchain.pem" || front.AccessLog != "/var/log/nginx/custom.log" {
		t.Fatalf("%+v", front)
	}
	if tree.Sites[1].Listen[0] != (NginxListen{8080, false, true, false}) || tree.Sites[1].AccessLog != "off" || strings.Contains(strings.Join(tree.Sites[0].Certificates, " "), "privkey") {
		t.Fatalf("%+v", tree.Sites)
	}
	if _, err := ParseNginx(nginxSample + "server {"); err == nil {
		t.Fatal("accepted a truncated dump")
	}
}

func TestNginxCommandsAndTemplate(t *testing.T) {
	for _, item := range []struct{ action, want string }{
		{"test", "'nginx' '-t'"},
		{"reload", "'nginx' '-t'\n'systemctl' 'reload' 'nginx.service'"},
		{"restart", "'nginx' '-t'\n'systemctl' 'restart' 'nginx.service'"},
		{"start;id", ""},
	} {
		command, _ := NginxActionCommand(item.action, true, "/etc/nginx/nginx.conf")
		if strings.TrimPrefix(command, "set -e\n") != item.want {
			t.Errorf("%s: %q", item.action, command)
		}
	}
	if got := CertificateRemoteCommand("www.example.org"); !strings.Contains(got, "'openssl' 's_client' '-connect' 'www.example.org:443' '-servername' 'www.example.org'") || CertificateRemoteCommand("example.org;id") != "" {
		t.Errorf("unexpected remote TLS command: %q", got)
	}
	if got := CertbotIssueCommand([]string{"example.org", "www.example.org"}, "admin@example.org"); got != "'certbot' '--nginx' '--non-interactive' '--agree-tos' '--no-redirect' '--email' 'admin@example.org' '-d' 'example.org' '-d' 'www.example.org'" {
		t.Errorf("unexpected issue command: %q", got)
	}
	form := NginxTemplate{Names: []string{"example.org", "www.example.org"}, Port: 3030, Certificate: "/etc/tls/site.pem", Key: "/etc/tls/site.key"}
	text, err := form.Render()
	if err != nil || !strings.Contains(text, "server_name example.org www.example.org;") || !strings.Contains(text, "proxy_pass http://127.0.0.1:3030;") || !strings.Contains(text, "listen 443 ssl;") {
		t.Fatalf("%q: %v", text, err)
	}
	form.Names[0] = "example.org;include"
	if _, err := form.Render(); err == nil {
		t.Fatal("accepted injected directive")
	}
	for _, file := range []string{"/etc/nginx/../shadow", "/etc/nginx/sites-enabled/foo", "/etc/nginx/conf.d/foo.conf\nbar", "/etc/ssl/private/foo.key"} {
		if _, err := NginxReadCommand(file); err == nil {
			t.Errorf("allowed editing %q", file)
		}
	}
}

func TestNginxConfigRefusesKnownCredentials(t *testing.T) {
	for _, text := range []string{
		`proxy_set_header Authorization "Basic confidential";`,
		`proxy_pass https://user:synthetic-value@upstream.example.org;`,
		`proxy_pass https://upstream.example.org/?token=confidential;`,
		"-----BEGIN " + "PRIVATE KEY-----",
	} {
		if NginxConfigSafe(text) || NginxOutputSafe(text) {
			t.Error("accepted a credential-bearing configuration")
		}
	}
	for _, text := range []string{
		`proxy_set_header Authorization $http_authorization;`,
		`ssl_certificate_key /etc/letsencrypt/live/example.org/privkey.pem;`,
		`auth_basic_user_file /etc/nginx/.htpasswd;`,
	} {
		if !NginxConfigSafe(text) {
			t.Errorf("rejected a runtime variable or path: %s", text)
		}
	}
	for _, name := range []string{"site.key", "privkey.pem", "id_ed25519", "site.bak-20261125120000"} {
		if NginxEditable(NginxConfD + name) {
			t.Errorf("editable non-configuration file: %s", name)
		}
	}
}
