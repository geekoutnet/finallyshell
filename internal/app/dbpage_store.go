package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/dbclient"
)

type dbProfiles map[string][]dbclient.Profile

func dbProfilesPath() string { return filepath.Join(config.Dir(), "db.json") }

func loadDBProfiles(path string) (dbProfiles, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(dbProfiles), nil
	}
	if err != nil {
		return nil, err
	}
	profiles := make(dbProfiles)
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}
	if profiles == nil {
		profiles = make(dbProfiles)
	}
	for _, list := range profiles {
		seen := make(map[string]bool, len(list))
		for _, profile := range list {
			if profile.ID == "" || seen[profile.ID] || profile.Validate() != nil {
				return nil, dbclient.ErrProfile
			}
			seen[profile.ID] = true
		}
	}
	return profiles, nil
}

func saveDBProfiles(path string, profiles dbProfiles) error {
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return dbReplaceFile(path, append(data, '\n'))
}

func dbReplaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".database-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot back up %s", filepath.Base(path))
		}
		old, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, err := old.Stat()
		if err != nil || !os.SameFile(info, opened) {
			old.Close()
			return fmt.Errorf("file changed before backup: %s", filepath.Base(path))
		}
		backupErr := dbBackup(path, old)
		if err := errors.Join(backupErr, old.Close()); err != nil {
			return err
		}
		current, err := os.Lstat(path)
		if err != nil || !os.SameFile(current, info) || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
			return fmt.Errorf("file changed during backup: %s", filepath.Base(path))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func dbBackup(path string, data io.Reader) error {
	stamp := time.Now().UTC()
	for attempt := 0; attempt < 60; attempt++ {
		backup := path + ".bak-" + stamp.Add(time.Duration(attempt)*time.Second).Format("20060102150405")
		file, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, writeErr := io.Copy(file, data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			os.Remove(backup)
			return err
		}
		return nil
	}
	return fmt.Errorf("cannot create a unique backup for %s", filepath.Base(path))
}

func dbWriteNew(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

func dbSecretKey(hostID, id string) string { return "database/" + hostID + "/" + id + "/password" }
