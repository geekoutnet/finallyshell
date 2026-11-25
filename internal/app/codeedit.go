package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/pkg/sftp"
)

type codeEditorWindow struct {
	app                      *App
	kind                     WindowKind
	spec                     CodeEditorSpec
	host                     *config.Host
	files                    []*codeEditorDoc
	tabs                     []*codeEditorDoc
	active                   *codeEditorDoc
	tree                     *codeEditorDir
	folded                   map[string]bool
	wrap, refocus, showFiles bool
	closed, askClose         bool
	askFile                  *codeEditorDoc
	cancel                   context.CancelFunc
	review                   *codeEditorReview
	override                 *codeEditorReview
	output, operation        string
	operationID              uint64
	failed                   bool
}

type codeEditorDoc struct {
	tab             editTab
	loaded, limited bool
}

func init() {
	openCodeEditorImpl = newCodeEditorWindow
}

func (a *App) codeEditorMenuItem(menu *ui.Menu, session *Session, file string, root bool) {
	label := locales.CeditOpen()
	if root {
		label = locales.CeditOpenRoot()
	}
	item := menu.Item(label)
	switch {
	case !validCodePath(file):
		item.Disabled(true).Subtitle(locales.CeditPathInvalid())
	case session.status != statusConnected || session.client == nil && (!root || session.overview.memory.privilegeRunner == nil):
		item.Disabled(true).Subtitle(locales.EditorNotConnected())
	}
	if item.Chosen() {
		a.openCodeEditor(CodeEditorSpec{Session: session, Root: root, Files: []CodeEditorFile{{Path: file}}})
	}
}

func newCodeEditorWindow(a *App, spec CodeEditorSpec) {
	window := &codeEditorWindow{
		app: a, spec: spec, kind: WindowKind("code-editor-" + rand.Text()), folded: map[string]bool{},
	}
	if spec.Session != nil && spec.Session.Host != nil {
		host := *spec.Session.Host
		window.host = &host
	} else if spec.Session == nil && spec.Client != nil && spec.Root {
		host, port, _ := net.SplitHostPort(spec.Client.RemoteAddr().String())
		portNumber, _ := strconv.Atoi(port)
		window.spec.Session = newSession(&config.Host{ID: config.NewHostID(), Addr: host, Port: portNumber, User: spec.Client.User()})
		window.spec.Session.client = spec.Client
		window.spec.Session.status = statusConnected
	}
	seen := map[string]bool{}
	for _, file := range spec.Files {
		if seen[file.Path] {
			continue
		}
		seen[file.Path] = true
		doc := &codeEditorDoc{tab: editTab{path: file.Path}}
		window.files = append(window.files, doc)
		if !validCodePath(file.Path) {
			doc.fail(errCodePath)
		} else if file.Loaded {
			doc.fill(file.Content, spec.Language)
		}
	}
	window.spec.Files = nil
	if len(window.files) == 0 {
		return
	}
	window.tree = makeCodeEditorTree(window.files)
	if a.codeEditors == nil {
		a.codeEditors = map[WindowKind]*codeEditorWindow{}
	}
	a.codeEditors[window.kind] = window
	window.activate(window.files[0])
	a.openWindow(WindowSpec{
		Kind: window.kind, Title: window.title, Width: 1150, Height: 740, MinWidth: 460, MinHeight: 300, OwnKeys: true,
		CloseRequested: window.closeRequested,
		Closed: func() {
			window.closed = true
			if window.cancel != nil {
				window.cancel()
			}
			delete(a.codeEditors, window.kind)
		},
	}, window.view)
}

func (window *codeEditorWindow) title() string {
	if window.spec.Title != "" {
		return window.spec.Title
	}
	if window.host != nil {
		return locales.CeditTitle() + " · " + window.host.Title()
	}
	return locales.CeditTitle()
}

func (window *codeEditorWindow) session() *Session {
	session := window.spec.Session
	if session == nil {
		return nil
	}
	if window.host == nil {
		return session
	}
	if slices.Contains(window.app.sessions, session) && hostMatchesPrivilege(session.Host, window.host) && session.status == statusConnected {
		return session
	}
	if session.Host == nil || !hostMatchesPrivilege(session.Host, window.host) {
		return nil
	}
	for _, candidate := range window.app.sessions {
		if candidate.status == statusConnected && hostMatchesPrivilege(candidate.Host, window.host) {
			return candidate
		}
	}
	return nil
}

func (window *codeEditorWindow) online() bool {
	if session := window.session(); session != nil {
		return session.status == statusConnected && (window.spec.Save == nil && window.spec.Validate == nil && window.spec.Format == nil || session == window.spec.Session)
	}
	return window.spec.Session == nil
}

func (window *codeEditorWindow) offlineMessage() string {
	if window.spec.Session != nil && (window.spec.Validate != nil || window.spec.Format != nil || window.spec.Save != nil) {
		return locales.CeditReconnectOriginal()
	}
	return locales.EditorDisconnected(window.hostLabel())
}

type codeTransport struct {
	root monitor.CaddyRunner
	sftp *sftp.Client
	open func() (*sftp.Client, error)
}

func (window *codeEditorWindow) transport() codeTransport {
	if session := window.session(); session != nil {
		if window.spec.Root && (session.client != nil || session.overview.memory.privilegeRunner != nil) {
			priv := window.app.sessionPrivilege(session).runner
			return codeTransport{root: func(ctx context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
				return priv.Run(ctx, script, monitor.PrivilegeOptions{Stdin: input, ScriptStdin: true, Interrupt: progress != nil, Progress: progress})
			}}
		}
		if !window.spec.Root && session.client != nil {
			client := session.client
			return codeTransport{open: func() (*sftp.Client, error) { return sftp.NewClient(client) }}
		}
	}
	if window.spec.Session == nil && window.spec.Client != nil {
		client := window.spec.Client
		return codeTransport{open: func() (*sftp.Client, error) { return sftp.NewClient(client) }}
	}
	return codeTransport{}
}

func (transport *codeTransport) connect(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return func() {}, err
	}
	if transport.open != nil {
		client, err := transport.open()
		if err != nil {
			return func() {}, err
		}
		transport.sftp = client
		stop := context.AfterFunc(ctx, func() { _ = client.Close() })
		return func() {
			if stop() {
				_ = client.Close()
			}
		}, nil
	}
	return func() {}, nil
}

func (transport codeTransport) read(ctx context.Context, file string) ([]byte, error) {
	if transport.root != nil {
		return readRootCode(ctx, transport.root, file)
	}
	if transport.sftp != nil {
		return readSFTPCode(ctx, transport.sftp, file)
	}
	return nil, errNotConnected
}

func (transport codeTransport) write(ctx context.Context, before, after monitor.CaddyFile) (string, error) {
	if transport.root != nil {
		return writeRootCode(ctx, transport.root, after.Path, before.Content, after.Content, codeEditorProgress(ctx))
	}
	if transport.sftp != nil {
		return writeSFTPCode(ctx, transport.sftp, after.Path, before.Content, after.Content)
	}
	return "", errNotConnected
}

func (doc *codeEditorDoc) fail(err error) {
	doc.tab.state, doc.tab.err = tabFailed, err
}

func (doc *codeEditorDoc) fill(raw, language string) {
	doc.loaded = true
	doc.limited = len(raw) > codeFileLimit
	if doc.limited {
		end := 256 << 10
		for end > 0 && end > (256<<10)-utf8.UTFMax && !utf8.RuneStart(raw[end]) {
			end--
		}
		raw = raw[:end]
	}
	if strings.ContainsRune(raw, 0) || !utf8.ValidString(raw) {
		doc.tab.state = tabBinary
		return
	}
	doc.tab.raw = []byte(raw)
	doc.reset(language)
}

func (doc *codeEditorDoc) reset(language string) {
	tab := &doc.tab
	decoded := decodeText(tab.raw, encUTF8)
	buf := editor.NewBuffer(decoded.text)
	buf.SetLineEnding(decoded.ending)
	buf.SetLanguage(codeLanguage(tab.path, decoded.text, language))
	tab.bom, tab.savedEnding, tab.enc, tab.savedEnc, tab.state = decoded.bom, decoded.ending, encUTF8, encUTF8, tabReady
	tab.modKnown = false
	tab.code = &editor.State{Buffer: buf, Font: codeFont, Label: tab.name(), ReadOnly: doc.limited}
	tab.code.OnCompletion = doc.complete
}

func (doc *codeEditorDoc) content() string {
	if !doc.tab.modified() {
		return string(doc.tab.raw)
	}
	buffer := doc.tab.code.Buffer
	data, _ := encodeText(buffer.String(), encUTF8, buffer.LineEnding(), doc.tab.bom)
	return string(data)
}

func (doc *codeEditorDoc) complete(request editor.CompletionRequest) {
	state := doc.tab.code
	if state.Buffer.Language() != caddyCodeLanguage || request.Version != state.Buffer.Version() {
		return
	}
	start := request.Offset - len(request.Prefix)
	position := state.Buffer.Pos(start)
	line := state.Buffer.Line(position.Line)[:position.Col]
	trimmed := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(trimmed)]
	matcher := strings.HasPrefix(trimmed, "@") && !strings.ContainsAny(trimmed, "{}#;")
	if trimmed != "" && !matcher || request.Prefix == "" && !request.Explicit {
		return
	}
	if request.Prefix == "" && matcher && !strings.HasSuffix(trimmed, " ") && !strings.HasSuffix(trimmed, "\t") {
		return
	}
	state.ShowCompletions(start, caddyCompletions(indent, codeIndentUnit(state), matcher))
}

func (window *codeEditorWindow) activate(doc *codeEditorDoc) {
	if !slices.Contains(window.tabs, doc) {
		window.tabs = append(window.tabs, doc)
	}
	window.active, window.refocus = doc, true
	if !doc.loaded && doc.tab.err == nil && window.cancel == nil {
		window.load(doc)
	}
}

func (window *codeEditorWindow) load(doc *codeEditorDoc) {
	if !validCodePath(doc.tab.path) || window.cancel != nil {
		return
	}
	file, transport, language := doc.tab.path, window.transport(), window.spec.Language
	doc.tab.state, doc.tab.err = tabLoading, nil
	window.start(locales.CeditReading(file), func(ctx context.Context) func() {
		closeTransport, err := transport.connect(ctx)
		defer closeTransport()
		var data []byte
		if err == nil {
			data, err = transport.read(ctx, file)
			if ctx.Err() != nil {
				err = ctx.Err()
			}
		}
		return func() {
			if err != nil {
				doc.fail(err)
				window.operation = ""
			} else {
				doc.fill(string(data), language)
				window.operation = ""
				window.refocus = true
			}
		}
	})
}

func (window *codeEditorWindow) start(operation string, run func(context.Context) func()) {
	ctx, cancel := context.WithCancel(context.Background())
	window.cancel, window.operation, window.output, window.failed = cancel, operation, "", false
	window.operationID++
	operationID := window.operationID
	progress := func(text string) {
		if text != "" && ctx.Err() == nil {
			window.app.post(func() {
				if !window.closed && window.cancel != nil && operationID == window.operationID {
					window.output = joinCodeOutput(window.output, text)
				}
			})
		}
	}
	go func() {
		finish := run(context.WithValue(ctx, codeEditorProgressKey{}, progress))
		cancel()
		window.app.post(func() {
			if window.closed {
				return
			}
			window.cancel = nil
			finish()
			if doc := window.active; !window.closed && doc != nil && !doc.loaded && doc.tab.err == nil && doc.tab.state == tabLoading && window.cancel == nil {
				window.load(doc)
			}
		})
	}()
}

type codeEditorProgressKey struct{}

func codeEditorProgress(ctx context.Context) func(string) {
	if progress, ok := ctx.Value(codeEditorProgressKey{}).(func(string)); ok {
		return progress
	}
	return func(string) {}
}

func (window *codeEditorWindow) hostLabel() string {
	if window.host != nil {
		return window.host.Title()
	}
	return window.title()
}

func (window *codeEditorWindow) unsaved() int {
	count := 0
	for _, doc := range window.files {
		if doc.tab.modified() {
			count++
		}
	}
	return count
}

func (a *App) unsavedCodeFiles() int {
	count := 0
	for _, window := range a.codeEditors {
		count += window.unsaved()
	}
	return count
}

func (window *codeEditorWindow) closeRequested() bool {
	if window.cancel != nil {
		return false
	}
	if window.unsaved() == 0 {
		return true
	}
	window.askClose, window.askFile, window.review, window.override = true, nil, nil, nil
	window.app.redraw()
	return false
}

func (window *codeEditorWindow) closeTab(doc *codeEditorDoc, discard bool) {
	if window.cancel != nil {
		return
	}
	if doc.tab.modified() {
		if !discard {
			window.askFile = doc
			return
		}
		doc.reset(window.spec.Language)
	}
	index := slices.Index(window.tabs, doc)
	if index < 0 {
		return
	}
	window.tabs = slices.Delete(window.tabs, index, index+1)
	if len(window.tabs) == 0 {
		if len(window.files) == 1 && window.closeRequested() {
			window.app.closeWindow(window.kind)
		}
		window.active = nil
	} else if window.active == doc {
		window.activate(window.tabs[min(index, len(window.tabs)-1)])
	}
}

func codeFormatError(err error, host string) string {
	var withPath codePathError
	if errors.As(err, &withPath) {
		return withPath.file + ": " + codeFormatError(withPath.err, host)
	}
	switch {
	case errors.Is(err, monitor.ErrCaddyChanged):
		return locales.CeditChanged()
	case errors.Is(err, errCodePath):
		return locales.CeditPathInvalid()
	case errors.Is(err, errCodeRegular):
		return locales.CeditRegularOnly()
	case errors.Is(err, errCodeAtomic):
		return locales.CeditAtomicUnavailable()
	case errors.Is(err, errCodeBackup):
		return locales.CeditBackupExists()
	case errors.Is(err, errCodeRead):
		return locales.CeditIncompleteRead()
	case errors.Is(err, errCodeResult):
		return locales.CeditMissingBackup()
	case errors.Is(err, context.Canceled), errors.Is(err, monitor.ErrPrivilegeCancelled):
		return locales.CeditInterrupted()
	case errors.Is(err, monitor.ErrPrivilegeDenied):
		return locales.PrivNeedsRoot()
	}
	return fileErrText(err, host)
}

func codeTextLimit(data []byte) error {
	if len(data) > codeFileLimit {
		return fmt.Errorf("%s", locales.CeditTooLarge())
	}
	if bytes.ContainsRune(data, 0) || !utf8.Valid(data) {
		return fmt.Errorf("%s", locales.CeditUTF8Only())
	}
	return nil
}

func codeIndentUnit(state *editor.State) string {
	if state.Indent == editor.IndentTabs || state.Indent == editor.IndentAuto && state.Buffer.Language() != nil && state.Buffer.Language().HardTabs {
		return "\t"
	}
	return strings.Repeat(" ", codeIndentSize(state))
}

func codeIndentSize(state *editor.State) int {
	if state.TabSize > 0 {
		return state.TabSize
	}
	if lang := state.Buffer.Language(); lang != nil && lang.TabSize > 0 {
		return lang.TabSize
	}
	return 4
}
