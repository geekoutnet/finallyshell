package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ImageRow struct {
	ID, Ref, Platform  string
	Size               uint64
	Created            time.Time
	Untagged, Dangling bool
}

func ImageIDKey(id string) string { return strings.TrimPrefix(id, "sha256:") }

type imageListEntry struct {
	ID, Repository, Tag, OS, Architecture string
	Names, RepoTags                       []string
	Size                                  imageBytes
	Created, CreatedAt                    imageTime
}

type imageBytes uint64

func (size *imageBytes) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return err
		}
		*size = imageBytes(ParseSize(text))
		return nil
	}
	return json.Unmarshal(raw, (*uint64)(size))
}

type imageTime struct{ time.Time }

func (created *imageTime) UnmarshalJSON(raw []byte) error {
	if seconds, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
		if seconds > 0 {
			created.Time = time.Unix(seconds, 0)
		}
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return err
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05 -0700 MST", "2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"} {
		if instant, err := time.Parse(layout, text); err == nil {
			created.Time = instant
			break
		}
	}
	return nil
}

func imageJSONRows[Entry any](output string) ([]Entry, error) {
	var entries []Entry
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "WARNING:") || strings.HasPrefix(line, "time=") && strings.Contains(line, "level=warning") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if err := json.Unmarshal([]byte(strings.Join(lines[index:], "\n")), &entries); err != nil {
				return nil, err
			}
			return entries, nil
		}
		var entry Entry
		if !strings.HasPrefix(line, "{") {
			return nil, fmt.Errorf("expected image JSON at line %d", index+1)
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func ParseImageList(output string) ([]ImageRow, error) {
	entries, err := imageJSONRows[imageListEntry](output)
	if err != nil {
		return nil, err
	}
	var result []ImageRow
	seen := map[[2]string]bool{}
	taggedIDs := map[string]bool{}
	for _, entry := range entries {
		if !ValidImageID(entry.ID) {
			return nil, errors.New("invalid image ID in image list")
		}
		refs := entry.RepoTags
		if len(refs) == 0 {
			refs = entry.Names
		}
		if len(refs) == 0 && entry.Repository != "" && entry.Repository != "<none>" && entry.Tag != "" && entry.Tag != "<none>" {
			refs = []string{entry.Repository + ":" + entry.Tag}
		}
		row := ImageRow{ID: entry.ID, Size: uint64(entry.Size), Created: entry.CreatedAt.Time}
		if entry.OS != "" && entry.Architecture != "" {
			row.Platform = entry.OS + "/" + entry.Architecture
		}
		if row.Created.IsZero() {
			row.Created = entry.Created.Time
		}
		var tagged bool
		for _, ref := range refs {
			if ValidImageTag(ref) {
				tagged = true
				taggedIDs[ImageIDKey(row.ID)] = true
				row.Ref = ref
				key := [2]string{ImageIDKey(row.ID), ref}
				if !seen[key] {
					result = append(result, row)
					seen[key] = true
				}
			}
		}
		if !tagged {
			key := [2]string{ImageIDKey(row.ID), ""}
			if !seen[key] {
				row.Ref, row.Untagged = row.ID, true
				result = append(result, row)
				seen[key] = true
			}
		}
	}
	return slices.DeleteFunc(result, func(row ImageRow) bool { return row.Untagged && taggedIDs[ImageIDKey(row.ID)] }), nil
}

type ImageInspect struct {
	ID                        string `json:"Id"`
	RepoTags, RepoDigests     []string
	OS, Architecture, Variant string
	Author                    string
	Size                      uint64
	Created                   time.Time
	RootFS                    struct{ Layers []string }
	Config                    struct {
		Env, Cmd, Entrypoint  strList
		ExposedPorts, Volumes map[string]json.RawMessage
		Labels                map[string]string
		WorkingDir, User      string
	}
	Raw json.RawMessage `json:"-"`
}

func (image ImageInspect) Platform() string {
	if image.OS == "" || image.Architecture == "" {
		return ""
	}
	if image.Variant != "" {
		return image.OS + "/" + image.Architecture + "/" + image.Variant
	}
	return image.OS + "/" + image.Architecture
}

func ParseImageInspect(output string) ([]ImageInspect, error) {
	entries, err := imageJSONRows[json.RawMessage](output)
	if err != nil {
		return nil, err
	}
	result := make([]ImageInspect, 0, len(entries))
	for _, raw := range entries {
		var image ImageInspect
		if err := json.Unmarshal(raw, &image); err != nil {
			return nil, err
		}
		if !ValidImageID(image.ID) {
			return nil, errors.New("invalid image ID in image details")
		}
		image.Raw = raw
		result = append(result, image)
	}
	return result, nil
}

type ImageLayer struct {
	ID, CreatedBy, Comment string
	Size                   uint64
	Created                time.Time
}

func ParseImageHistory(output string) ([]ImageLayer, error) {
	entries, err := imageJSONRows[struct {
		ID, CreatedBy, Comment string
		Size                   imageBytes
		CreatedAt, Created     imageTime
	}](output)
	if err != nil {
		return nil, err
	}
	result := make([]ImageLayer, 0, len(entries))
	for _, entry := range entries {
		created := entry.CreatedAt.Time
		if created.IsZero() {
			created = entry.Created.Time
		}
		result = append(result, ImageLayer{entry.ID, entry.CreatedBy, entry.Comment, uint64(entry.Size), created})
	}
	return result, nil
}

func ParseImageSearch(output string) []string {
	entries, err := imageJSONRows[struct{ Name string }](output)
	if err != nil {
		return nil
	}
	var result []string
	for _, entry := range entries {
		if ValidImageReference(entry.Name) {
			result = append(result, entry.Name)
		}
	}
	return result
}
