//go:build !windows

package audio

import (
	"errors"
	"os"
	"path/filepath"
)

type recordingDirectory interface {
	Sync() error
	Close() error
}

type recordingPublishHooks struct {
	link          func(string, string) error
	remove        func(string) error
	openDirectory func(string) (recordingDirectory, error)
}

func atomicPublishRecordingFile(sourcePath, destinationPath string) error {
	return publishRecordingFile(sourcePath, destinationPath, recordingPublishHooks{
		link:   os.Link,
		remove: os.Remove,
		openDirectory: func(path string) (recordingDirectory, error) {
			return os.Open(path)
		},
	})
}

func publishRecordingFile(sourcePath, destinationPath string, hooks recordingPublishHooks) error {
	// A same-directory hard-link installation is atomic and fails when the
	// destination already exists，so a timestamp collision never replaces user
	// data．Opening the directory first ensures install durability can be checked．
	directory, err := hooks.openDirectory(filepath.Dir(destinationPath))
	if err != nil {
		return err
	}
	if err := hooks.link(sourcePath, destinationPath); err != nil {
		_ = directory.Close()
		return err
	}
	if syncErr := directory.Sync(); syncErr != nil {
		rollbackErr := hooks.remove(destinationPath)
		var rollbackSyncErr error
		if rollbackErr == nil {
			rollbackSyncErr = directory.Sync()
		}
		closeErr := directory.Close()
		return errors.Join(syncErr, rollbackErr, rollbackSyncErr, closeErr)
	}
	// Once the install itself has been synced，a directory Close error cannot be
	// rolled back without risking loss of the only published name．Treat the
	// durable destination as success and continue best-effort temp cleanup．
	_ = directory.Close()
	if err := hooks.remove(sourcePath); err != nil {
		// The published path is already complete and durable．Leaving the private
		// temp link is safer than removing the successfully published recording．
		return nil
	}
	return nil
}
