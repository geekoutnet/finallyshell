package cloudsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (s *S3) historyPrefix() string {
	key := s.objectKey()
	if key == FileName {
		return HistoryFolder + "/"
	}
	digest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s/%x/", HistoryFolder, digest[:12])
}

func (s *S3) List(ctx context.Context) ([]Snapshot, error) { return s.listHistory(ctx, true) }

func (s *S3) listHistory(ctx context.Context, metadata bool) ([]Snapshot, error) {
	u, err := s.bucketURL()
	if err != nil {
		return nil, err
	}
	prefix := s.historyPrefix()
	query := url.Values{"list-type": {"2"}, "prefix": {prefix}}
	files := map[string]int64{}
	for {
		u.RawQuery = query.Encode()
		resp, err := s.doURL(ctx, http.MethodGet, u, nil, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			err := s.error(http.MethodGet, resp, nil)
			drain(resp.Body)
			return nil, err
		}
		body, err := readBody(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var listing struct {
			Truncated bool   `xml:"IsTruncated"`
			Token     string `xml:"NextContinuationToken"`
			Contents  []struct {
				Key  string `xml:"Key"`
				Size int64  `xml:"Size"`
			} `xml:"Contents"`
		}
		if err := xml.Unmarshal(body, &listing); err != nil {
			return nil, err
		}
		for _, object := range listing.Contents {
			if id, ok := strings.CutPrefix(object.Key, prefix); ok {
				files[id] = object.Size
			}
		}
		if !listing.Truncated {
			break
		}
		if listing.Token == "" || listing.Token == query.Get("continuation-token") {
			return nil, errors.New("cloudsync: S3 did not advance the history listing")
		}
		query.Set("continuation-token", listing.Token)
	}
	versions := historyFiles(files)
	if metadata {
		return hydrateHistory(ctx, versions, func(ctx context.Context, id string) (Snapshot, error) {
			data, err := s.versionRead(ctx, id, true)
			return readSnapshotPrefix(data), err
		})
	}
	return versions, nil
}

func (s *S3) versionRemote(id string) (*S3, error) {
	if !ValidSnapshotID(strings.TrimSuffix(id, pinSuffix)) {
		return nil, ErrNotFound
	}
	copy := *s
	copy.Key = s.historyPrefix() + id
	return &copy, nil
}

func (s *S3) GetVersion(ctx context.Context, id string) ([]byte, error) {
	if !ValidSnapshotID(id) {
		return nil, ErrNotFound
	}
	return s.versionRead(ctx, id, false)
}

func (s *S3) versionRead(ctx context.Context, id string, prefix bool) ([]byte, error) {
	object, err := s.versionRemote(id)
	if err != nil {
		return nil, err
	}
	var headers http.Header
	if prefix {
		headers = http.Header{"Range": {"bytes=0-4095"}}
	}
	resp, err := object.do(ctx, http.MethodGet, nil, headers)
	if err != nil {
		return nil, err
	}
	return historyBody(resp, prefix, func(method string, resp *http.Response) error { return s.error(method, resp, nil) })
}

func (s *S3) PutVersion(ctx context.Context, id string, data []byte) error {
	if !ValidSnapshotID(id) || len(data) > maxResponse {
		return ErrNotEnvelope
	}
	object, _ := s.versionRemote(id)
	resp, err := object.do(ctx, http.MethodPut, data, http.Header{"Content-Type": {"application/json"}, "If-None-Match": {"*"}})
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
		return nil
	case http.StatusPreconditionFailed, http.StatusConflict:
		if existing, err := s.GetVersion(ctx, id); err == nil && bytes.Equal(existing, data) {
			return nil
		}
		return ErrConflict
	default:
		return s.error(http.MethodPut, resp, nil)
	}
}

func (s *S3) DeleteVersion(ctx context.Context, id string) error {
	if !ValidSnapshotID(id) {
		return ErrNotFound
	}
	if _, err := s.versionRead(ctx, id+pinSuffix, true); err == nil {
		return ErrPinned
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	return s.deleteHistory(ctx, id)
}

func (s *S3) PinVersion(ctx context.Context, id string, pin bool) error {
	if !ValidSnapshotID(id) {
		return ErrNotFound
	}
	if pin {
		if _, err := s.versionRead(ctx, id, true); err != nil {
			return err
		}
		object, _ := s.versionRemote(id + pinSuffix)
		_, err := object.Put(ctx, []byte(`{}`), nil)
		return err
	}
	err := s.deleteHistory(ctx, id+pinSuffix)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

func (s *S3) deleteHistory(ctx context.Context, id string) error {
	object, err := s.versionRemote(id)
	if err != nil {
		return err
	}
	resp, err := object.do(ctx, http.MethodDelete, nil, nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	return historyDelete(resp, func(method string, resp *http.Response) error { return s.error(method, resp, nil) })
}
