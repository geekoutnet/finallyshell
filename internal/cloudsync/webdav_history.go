package cloudsync

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func (w *WebDAV) historyFolder() (*WebDAV, error) {
	folder, err := w.folderURL()
	if err != nil {
		return nil, err
	}
	copy := *w
	copy.URL = folder.JoinPath(HistoryFolder + "/").String()
	return &copy, nil
}

func (w *WebDAV) historyURL(id string) (string, error) {
	if !ValidSnapshotID(strings.TrimSuffix(id, pinSuffix)) {
		return "", ErrNotFound
	}
	folder, err := w.historyFolder()
	if err != nil {
		return "", err
	}
	u, err := folder.folderURL()
	if err != nil {
		return "", err
	}
	return u.JoinPath(id).String(), nil
}

const pinSuffix = ".pin"

func (w *WebDAV) List(ctx context.Context) ([]Snapshot, error) { return w.listHistory(ctx, true) }

func (w *WebDAV) listHistory(ctx context.Context, metadata bool) ([]Snapshot, error) {
	folder, err := w.historyFolder()
	if err != nil {
		return nil, err
	}
	resp, err := w.do(ctx, "PROPFIND", folder.URL, []byte(`<propfind xmlns="DAV:"><prop><getcontentlength/><resourcetype/></prop></propfind>`), func(req *http.Request) {
		req.Header.Set("Depth", "1")
		req.Header.Set("Content-Type", "application/xml")
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusMultiStatus {
		return nil, w.error("PROPFIND", resp)
	}
	body, err := readBody(resp.Body)
	if err != nil {
		return nil, err
	}
	var listing struct {
		Responses []struct {
			Href  string `xml:"href"`
			Stats []struct {
				Status string `xml:"status"`
				Size   int64  `xml:"prop>getcontentlength"`
			} `xml:"propstat"`
		} `xml:"response"`
	}
	if err := xml.Unmarshal(body, &listing); err != nil {
		return nil, err
	}
	files := map[string]int64{}
	for _, item := range listing.Responses {
		u, err := url.Parse(item.Href)
		if err != nil || strings.HasSuffix(u.Path, "/") {
			continue
		}
		name := path.Base(u.Path)
		if !ValidSnapshotID(strings.TrimSuffix(name, pinSuffix)) {
			continue
		}
		for _, stat := range item.Stats {
			if strings.Contains(stat.Status, " 200 ") {
				files[name] = stat.Size
				break
			}
		}
	}
	versions := historyFiles(files)
	if metadata {
		return hydrateHistory(ctx, versions, func(ctx context.Context, id string) (Snapshot, error) {
			data, err := w.versionRead(ctx, id, true)
			return readSnapshotPrefix(data), err
		})
	}
	return versions, nil
}

func (w *WebDAV) GetVersion(ctx context.Context, id string) ([]byte, error) {
	if !ValidSnapshotID(id) {
		return nil, ErrNotFound
	}
	return w.versionRead(ctx, id, false)
}

func (w *WebDAV) versionRead(ctx context.Context, id string, prefix bool) ([]byte, error) {
	u, err := w.historyURL(id)
	if err != nil {
		return nil, err
	}
	resp, err := w.do(ctx, http.MethodGet, u, nil, snapshotRange(prefix))
	if err != nil {
		return nil, err
	}
	return historyBody(resp, prefix, w.error)
}

func (w *WebDAV) PutVersion(ctx context.Context, id string, data []byte) error {
	if !ValidSnapshotID(id) || len(data) > maxResponse {
		return ErrNotEnvelope
	}
	return w.putHistory(ctx, id, data, true)
}

func (w *WebDAV) putHistory(ctx context.Context, id string, data []byte, immutable bool) error {
	u, err := w.historyURL(id)
	if err != nil {
		return err
	}
	put := func() (int, error) {
		resp, err := w.do(ctx, http.MethodPut, u, data, func(req *http.Request) {
			req.Header.Set("Content-Type", "application/json")
			if immutable {
				req.Header.Set("If-None-Match", "*")
			}
		})
		if err != nil {
			return 0, err
		}
		defer drain(resp.Body)
		switch resp.StatusCode {
		case http.StatusCreated, http.StatusOK, http.StatusNoContent, http.StatusPreconditionFailed, http.StatusNotFound, http.StatusConflict:
			return resp.StatusCode, nil
		default:
			return resp.StatusCode, w.error(http.MethodPut, resp)
		}
	}
	if immutable {
		if existing, err := w.GetVersion(ctx, id); err == nil {
			if bytes.Equal(existing, data) {
				return nil
			}
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	status, err := put()
	if err == nil && (status == http.StatusConflict || status == http.StatusNotFound) {
		folder, folderErr := w.historyFolder()
		if folderErr != nil {
			return folderErr
		}
		if err := folder.mkcol(ctx); err != nil {
			return err
		}
		status, err = put()
	}
	if err != nil {
		return err
	}
	switch status {
	case http.StatusCreated, http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusPreconditionFailed:
		if existing, err := w.GetVersion(ctx, id); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		return ErrConflict
	default:
		return errFolderMissing
	}
}

func (w *WebDAV) DeleteVersion(ctx context.Context, id string) error {
	if !ValidSnapshotID(id) {
		return ErrNotFound
	}
	if _, err := w.versionRead(ctx, id+pinSuffix, true); err == nil {
		return ErrPinned
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return w.deleteHistory(ctx, id)
}

func (w *WebDAV) PinVersion(ctx context.Context, id string, pin bool) error {
	if !ValidSnapshotID(id) {
		return ErrNotFound
	}
	if pin {
		if _, err := w.versionRead(ctx, id, true); err != nil {
			return err
		}
		return w.putHistory(ctx, id+pinSuffix, []byte(`{}`), false)
	}
	err := w.deleteHistory(ctx, id+pinSuffix)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (w *WebDAV) deleteHistory(ctx context.Context, id string) error {
	u, err := w.historyURL(id)
	if err != nil {
		return err
	}
	resp, err := w.do(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	return historyDelete(resp, w.error)
}
