package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

func TestHistoryWebDAV(t *testing.T) {
	dav := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(dav)
	defer srv.Close()
	remote := &WebDAV{URL: srv.URL + "/missing/sync"}
	checkHistory(t, remote)
}

func TestHistoryS3(t *testing.T) {
	var mu sync.Mutex
	files := map[string][]byte{}
	listRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(req.URL.Path, "/bucket/")
		if req.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if req.URL.Query().Get("list-type") == "2" {
			listRequests++
			if req.URL.Query().Get("continuation-token") == "" {
				fmt.Fprint(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>second+page</NextContinuationToken></ListBucketResult>`)
				return
			}
			if req.URL.Query().Get("continuation-token") != "second+page" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated>`)
			for id, data := range files {
				if strings.HasPrefix(id, req.URL.Query().Get("prefix")) {
					fmt.Fprint(w, `<Contents><Key>`)
					_ = xml.EscapeText(w, []byte(id))
					fmt.Fprintf(w, `</Key><Size>%d</Size></Contents>`, len(data))
				}
			}
			fmt.Fprint(w, `</ListBucketResult>`)
			return
		}
		switch req.Method {
		case http.MethodPut:
			if _, ok := files[key]; ok && req.Header.Get("If-None-Match") == "*" {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			files[key], _ = io.ReadAll(req.Body)
		case http.MethodGet:
			if data, ok := files[key]; ok {
				_, _ = w.Write(data)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodDelete:
			delete(files, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()
	remote := &S3{Endpoint: srv.URL, Bucket: "bucket", Key: "team a/sync.json", AccessKeyID: "fake", SecretAccessKey: "fake-secret"}
	checkHistory(t, remote)
	mu.Lock()
	defer mu.Unlock()
	if listRequests < 4 {
		t.Errorf("did not follow continuation tokens: %d", listRequests)
	}
}

func checkHistory(t *testing.T, remote Remote) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 11, 24, 8, 30, 0, 0, time.UTC)
	payload := hosts(host("h1", "private-host"))
	payload.Secrets = map[string]string{"key": "not-plaintext"}
	plain, _ := json.Marshal(payload)
	envelope, err := Seal("sync", plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err = attachSnapshot(envelope, Snapshot{At: at, Device: "Office PC", Reason: ReasonManual, Counts: payload.Counts(), Counted: true})
	if err != nil {
		t.Fatal(err)
	}
	id := snapshotID(at, "Office PC", envelope)
	if versions, err := remote.List(ctx); err != nil || len(versions) != 0 {
		t.Fatalf("empty history: %v, %v", versions, err)
	}
	for range 2 {
		if err := remote.PutVersion(ctx, id, envelope); err != nil {
			t.Fatal(err)
		}
	}
	if err := remote.PutVersion(ctx, id, []byte(`{"different":true}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("overwrite: %v", err)
	}
	if err := remote.PinVersion(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	versions, err := remote.List(ctx)
	if err != nil || len(versions) != 1 || versions[0].ID != id || !versions[0].Pinned || versions[0].Counts.Live != 1 || versions[0].Device != "Office PC" || versions[0].Size != int64(len(envelope)) {
		t.Fatalf("history listing: %+v, %v", versions, err)
	}
	if err := remote.DeleteVersion(ctx, id); !errors.Is(err, ErrPinned) {
		t.Fatalf("pinned delete: %v", err)
	}
	data, err := remote.GetVersion(ctx, id)
	if err != nil || !bytes.Equal(data, envelope) || bytes.Contains(data, []byte("private-host")) || bytes.Contains(data, []byte("not-plaintext")) {
		t.Fatalf("history contents: %v", err)
	}
	if _, err := OpenSnapshot("wrong", data); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := remote.PinVersion(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if err := remote.DeleteVersion(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := remote.GetVersion(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted read: %v", err)
	}
}

func TestHistoryGistRevisions(t *testing.T) {
	const first = "0123456789abcdef0123456789abcdef01234567"
	const second = "abcdef0123456789abcdef0123456789abcdef01"
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.URL.Path {
		case "/gists/fake/commits":
			if req.URL.Query().Get("page") == "2" {
				fmt.Fprintf(w, `[{"version":%q,"committed_at":"2026-11-23T08:03:00Z"}]`, second)
			} else {
				w.Header().Set("Link", `<`+base+`/gists/fake/commits?page=2>; rel="next"`)
				fmt.Fprintf(w, `[{"version":%q,"committed_at":"2026-11-24T08:03:00Z","user":{"login":"office"}}]`, first)
			}
		case "/gists/fake/" + second:
			fmt.Fprintf(w, `{"files":{%q:{"truncated":true,"raw_url":%q}}}`, FileName, base+"/raw")
		case "/raw":
			fmt.Fprint(w, `revision contents`)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()
	base = srv.URL
	remote := &Gist{ID: "fake", Token: "fake", BaseURL: base}
	ctx := context.Background()
	versions, err := remote.List(ctx)
	if err != nil || len(versions) != 2 || versions[0].ID != first || versions[0].Device != "office" {
		t.Fatalf("revisions: %+v, %v", versions, err)
	}
	if data, err := remote.GetVersion(ctx, second); err != nil || string(data) != "revision contents" {
		t.Fatalf("revision: %q, %v", data, err)
	}
	if err := remote.DeleteVersion(ctx, second); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatal(err)
	}
	if err := remote.PutVersion(ctx, second, nil); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatal(err)
	}
	if err := remote.PinVersion(ctx, second, true); !errors.Is(err, ErrHistoryUnsupported) {
		t.Fatal(err)
	}
}
