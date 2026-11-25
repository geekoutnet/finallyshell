package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui/editor"
)

type codeEditorReview struct {
	items       []*codeEditorDiff
	selected    int
	layout      editor.DiffLayout
	closeWindow bool
	closeTab    *codeEditorDoc
}

type codeEditorDiff struct {
	doc           *codeEditorDoc
	before, after monitor.CaddyFile
	diff          editor.DiffState
}

func (window *codeEditorWindow) beginReview(target *codeEditorDoc, closeWindow bool, closeTab *codeEditorDoc) {
	if window.cancel != nil || window.review != nil {
		return
	}
	if window.spec.Save != nil {
		target = nil
	}
	review := &codeEditorReview{closeWindow: closeWindow, closeTab: closeTab}
	for _, doc := range window.files {
		if (target == nil || target == doc) && doc.tab.modified() {
			current := doc.content()
			if err := codeTextLimit([]byte(current)); err != nil {
				window.setFailure(locales.Save(), doc.tab.path+": "+err.Error())
				return
			}
			before, after := string(doc.tab.raw), current
			language := doc.tab.code.Buffer.Language()
			old, next := editor.NewBuffer(decodeText(doc.tab.raw, encUTF8).text), editor.NewBuffer(doc.tab.code.Buffer.String())
			old.SetLanguage(language)
			next.SetLanguage(language)
			review.items = append(review.items, &codeEditorDiff{
				doc: doc, before: monitor.CaddyFile{Path: doc.tab.path, Content: before}, after: monitor.CaddyFile{Path: doc.tab.path, Content: after},
				diff: editor.DiffState{Old: old, New: next, Wrap: true},
			})
		}
	}
	if len(review.items) != 0 {
		window.review, window.override = review, nil
	}
}

type codeValidationInput struct {
	file monitor.CaddyFile
	read bool
}

func (window *codeEditorWindow) validationInput(changed map[string]string) ([]codeValidationInput, error) {
	files := make([]codeValidationInput, 0, len(window.files))
	for _, doc := range window.files {
		item := codeValidationInput{file: monitor.CaddyFile{Path: doc.tab.path}, read: true}
		switch content, editing := changed[doc.tab.path]; {
		case editing:
			item.file.Content, item.read = content, false
		case doc.limited || doc.loaded && doc.tab.code == nil:
			return nil, fmt.Errorf("%s: %s", doc.tab.path, locales.CeditCannotValidateFile())
		case doc.tab.code != nil:
			item.file.Content, item.read = string(doc.tab.raw), false
		}
		files = append(files, item)
	}
	return files, nil
}

type codeSaveResult struct {
	saved            int
	output           string
	err              error
	validationFailed bool
}

func (window *codeEditorWindow) saveReview(review *codeEditorReview, skipValidation bool) {
	if window.cancel != nil || review == nil {
		return
	}
	if !window.online() {
		window.review, window.override = nil, nil
		window.setFailure(locales.Save(), window.offlineMessage())
		return
	}
	window.review, window.override = nil, nil
	var writes, originals []monitor.CaddyFile
	expect, changed := map[string]string{}, map[string]string{}
	for _, item := range review.items {
		if !item.doc.tab.modified() || item.doc.content() != item.after.Content || string(item.doc.tab.raw) != item.before.Content {
			window.setFailure(locales.Save(), locales.CeditReviewStale())
			return
		}
		writes, originals = append(writes, item.after), append(originals, item.before)
		expect[item.before.Path], changed[item.after.Path] = item.before.Content, item.after.Content
	}
	validate, save := window.spec.Validate, window.spec.Save
	if skipValidation && (save != nil || !window.spec.AllowSaveAnyway) {
		return
	}
	var validation []codeValidationInput
	if validate != nil && !skipValidation {
		inputs, err := window.validationInput(changed)
		if err != nil {
			window.setFailure(locales.CeditValidate(), err.Error())
			return
		}
		validation = inputs
	}
	transport := codeTransport{}
	if save == nil || slices.ContainsFunc(validation, func(item codeValidationInput) bool { return item.read }) {
		transport = window.transport()
	}
	operation := locales.Save()
	if validation != nil {
		operation = locales.CeditCheckAndSave()
	}
	window.start(operation, func(ctx context.Context) func() {
		result := func() (result codeSaveResult) {
			closeTransport, err := transport.connect(ctx)
			defer closeTransport()
			if err != nil {
				result.err = err
				return
			}
			if validation != nil {
				result.output, result.err = runCodeValidation(ctx, transport, validation, validate)
				if result.err != nil {
					result.validationFailed = true
					return
				}
				codeEditorProgress(ctx)(result.output)
			}
			if result.err = ctx.Err(); result.err != nil {
				return
			}
			if save != nil {
				out, err := save(ctx, slices.Clone(writes), maps.Clone(expect))
				result.output, result.err = joinCodeOutput(result.output, out), err
				if err == nil {
					result.saved = len(writes)
				}
				return
			}
			for index, file := range writes {
				if result.err = ctx.Err(); result.err != nil {
					return
				}
				codeEditorProgress(ctx)(file.Path)
				backup, err := transport.write(ctx, originals[index], file)
				result.output = joinCodeOutput(result.output, backup)
				if err != nil {
					if ctx.Err() != nil {
						err = ctx.Err()
					}
					result.err = codePathError{file.Path, err}
					return
				}
				result.saved++
				if transport.root == nil {
					codeEditorProgress(ctx)(backup)
				}
			}
			return
		}()
		return func() { window.finishSave(review, result) }
	})
}

type codePathError struct {
	file string
	err  error
}

func (err codePathError) Error() string { return err.file + ": " + err.err.Error() }
func (err codePathError) Unwrap() error { return err.err }

func (window *codeEditorWindow) finishSave(review *codeEditorReview, result codeSaveResult) {
	if result.output != "" {
		window.output = result.output
	}
	for _, item := range review.items[:result.saved] {
		tab := &item.doc.tab
		tab.raw = []byte(item.after.Content)
		tab.savedEnding = tab.code.Buffer.LineEnding()
		tab.markSaved()
	}
	if result.err == nil && result.saved > 0 && window.spec.Saved != nil {
		window.spec.Saved()
	}
	if result.err != nil {
		if result.validationFailed {
			window.operation = locales.CeditValidate()
			if window.spec.Save == nil && window.spec.AllowSaveAnyway && !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, context.DeadlineExceeded) {
				window.override = review
			}
		}
		window.failed = true
		window.output = joinCodeOutput(window.output, codeFormatError(result.err, window.hostLabel()))
		return
	}
	window.operation = locales.EditorSaved()
	if review.closeWindow && window.unsaved() == 0 {
		window.app.closeWindow(window.kind)
	} else if review.closeTab != nil {
		window.closeTab(review.closeTab, false)
	}
}

func joinCodeOutput(first, second string) string {
	second = strings.TrimSpace(second)
	if second == "" {
		return first
	}
	if first == "" {
		return second
	}
	return first + "\n" + second
}

func runCodeValidation(ctx context.Context, transport codeTransport, inputs []codeValidationInput, validate func(context.Context, []monitor.CaddyFile) (string, error)) (string, error) {
	files := make([]monitor.CaddyFile, 0, len(inputs))
	for _, item := range inputs {
		if item.read {
			raw, err := transport.read(ctx, item.file.Path)
			if err != nil {
				return "", codePathError{item.file.Path, err}
			}
			if err := codeTextLimit(raw); err != nil {
				return "", codePathError{item.file.Path, err}
			}
			item.file.Content = string(raw)
		}
		files = append(files, item.file)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return validate(ctx, files)
}

func (window *codeEditorWindow) validate() {
	if window.cancel != nil || window.spec.Validate == nil || !window.online() {
		return
	}
	changed := map[string]string{}
	for _, doc := range window.files {
		if doc.tab.modified() {
			changed[doc.tab.path] = doc.content()
			if err := codeTextLimit([]byte(changed[doc.tab.path])); err != nil {
				window.setFailure(locales.CeditValidate(), doc.tab.path+": "+err.Error())
				return
			}
		}
	}
	validation, err := window.validationInput(changed)
	if err != nil {
		window.setFailure(locales.CeditValidate(), err.Error())
		return
	}
	validate, transport := window.spec.Validate, window.transport()
	window.override = nil
	window.start(locales.CeditValidate(), func(ctx context.Context) func() {
		closeTransport, err := transport.connect(ctx)
		defer closeTransport()
		var result string
		if err == nil {
			result, err = runCodeValidation(ctx, transport, validation, validate)
		}
		return func() {
			if result != "" {
				window.output = result
			}
			if err != nil {
				window.failed = true
				window.output = joinCodeOutput(window.output, codeFormatError(err, window.hostLabel()))
			} else {
				window.operation = locales.CeditValid()
			}
		}
	})
}

func (window *codeEditorWindow) format(doc *codeEditorDoc) {
	if window.cancel != nil || doc == nil || doc.tab.code == nil || doc.limited || window.spec.Format == nil || !window.online() {
		return
	}
	file, content, format := doc.tab.path, doc.content(), window.spec.Format
	window.override = nil
	window.start(locales.CeditFormat(), func(ctx context.Context) func() {
		result, err := format(ctx, file, content)
		return func() {
			if err == nil {
				err = codeTextLimit([]byte(result))
			}
			if err != nil {
				window.setFailure(locales.CeditFormat(), codeFormatError(err, window.hostLabel()))
				return
			}
			doc.tab.code.Buffer.Set(decodeText([]byte(result), encUTF8).text)
			window.operation, window.refocus = locales.CeditFormatted(), true
		}
	})
}

func (window *codeEditorWindow) setFailure(operation, output string) {
	window.operation, window.output, window.failed = operation, output, true
}
