package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

var gistRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (*Gist) NativeRevisions() bool { return true }

func (g *Gist) List(ctx context.Context) ([]Snapshot, error) {
	if g.ID == "" {
		id, err := g.find(ctx)
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		g.ID = id
	}
	base, err := url.Parse(g.base())
	if err != nil {
		return nil, err
	}
	next := g.base() + "/gists/" + url.PathEscape(g.ID) + "/commits?per_page=100"
	var versions []Snapshot
	pages := map[string]bool{}
	for next != "" && !pages[next] {
		pages[next] = true
		resp, err := g.do(ctx, http.MethodGet, next, nil, true)
		if err != nil {
			return versions, err
		}
		if resp.StatusCode != http.StatusOK {
			err := g.error(http.MethodGet, resp)
			drain(resp.Body)
			if errors.Is(err, ErrNotFound) && len(versions) == 0 {
				return nil, nil
			}
			return versions, err
		}
		body, err := readBody(resp.Body)
		resp.Body.Close()
		if err != nil {
			return versions, err
		}
		var commits []struct {
			ID   string    `json:"version"`
			At   time.Time `json:"committed_at"`
			User struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(body, &commits); err != nil {
			return versions, err
		}
		for _, commit := range commits {
			if gistRevision.MatchString(commit.ID) {
				versions = append(versions, Snapshot{ID: commit.ID, At: commit.At, Device: commit.User.Login})
			}
		}
		next = nextLink(resp.Header.Get("Link"))
		if target, err := url.Parse(next); err != nil || target.Host != base.Host || target.Scheme != base.Scheme {
			next = ""
		}
	}
	SortSnapshots(versions)
	return hydrateHistory(ctx, versions, func(ctx context.Context, id string) (Snapshot, error) {
		data, err := g.GetVersion(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return Snapshot{}, nil
		}
		meta := readSnapshotPrefix(data)
		meta.Size = int64(len(data))
		return meta, err
	})
}

func (g *Gist) GetVersion(ctx context.Context, id string) ([]byte, error) {
	if !gistRevision.MatchString(id) {
		return nil, ErrNotFound
	}
	if g.ID == "" {
		found, err := g.find(ctx)
		if err != nil {
			return nil, err
		}
		g.ID = found
	}
	doc, err := g.send(ctx, http.MethodGet, g.base()+"/gists/"+url.PathEscape(g.ID)+"/"+id, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	file := doc.Files[g.file()]
	if file == nil {
		return nil, ErrNotFound
	}
	if file.Truncated {
		return g.raw(ctx, file.RawURL)
	}
	return []byte(file.Content), nil
}

func (*Gist) PutVersion(context.Context, string, []byte) error { return ErrHistoryUnsupported }
func (*Gist) DeleteVersion(context.Context, string) error      { return ErrHistoryUnsupported }
func (*Gist) PinVersion(context.Context, string, bool) error   { return ErrHistoryUnsupported }
