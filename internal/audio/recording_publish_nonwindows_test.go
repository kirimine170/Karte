//go:build !windows

package audio

import (
	"errors"
	"reflect"
	"testing"
)

type recordingPublishTestDirectory struct {
	operations *[]string
	syncErr    error
	syncErrors []error
	closeErr   error
}

func (directory *recordingPublishTestDirectory) Sync() error {
	*directory.operations = append(*directory.operations, "sync-directory")
	if len(directory.syncErrors) > 0 {
		err := directory.syncErrors[0]
		directory.syncErrors = directory.syncErrors[1:]
		return err
	}
	return directory.syncErr
}

func (directory *recordingPublishTestDirectory) Close() error {
	*directory.operations = append(*directory.operations, "close-directory")
	return directory.closeErr
}

func TestPublishRecordingFileSyncsDirectoryBeforeRemovingTemp(t *testing.T) {
	var operations []string
	hooks := recordingPublishHooks{
		link: func(source, destination string) error {
			operations = append(operations, "link")
			return nil
		},
		remove: func(path string) error {
			operations = append(operations, "remove:"+path)
			return nil
		},
		openDirectory: func(string) (recordingDirectory, error) {
			operations = append(operations, "open-directory")
			return &recordingPublishTestDirectory{operations: &operations}, nil
		},
	}
	if err := publishRecordingFile("temp.wav", "/recordings/final.wav", hooks); err != nil {
		t.Fatal(err)
	}
	want := []string{"open-directory", "link", "sync-directory", "close-directory", "remove:temp.wav"}
	if !reflect.DeepEqual(operations, want) {
		t.Fatalf("publish operations = %v，want %v", operations, want)
	}
}

func TestPublishRecordingFileDirectorySyncFailureRollsBackDestination(t *testing.T) {
	wantErr := errors.New("directory sync failed")
	var removed []string
	var operations []string
	hooks := recordingPublishHooks{
		link: func(string, string) error { return nil },
		remove: func(path string) error {
			removed = append(removed, path)
			return nil
		},
		openDirectory: func(string) (recordingDirectory, error) {
			return &recordingPublishTestDirectory{operations: &operations, syncErrors: []error{wantErr, nil}}, nil
		},
	}
	if err := publishRecordingFile("temp.wav", "/recordings/final.wav", hooks); !errors.Is(err, wantErr) {
		t.Fatalf("publish error = %v，want sync failure", err)
	}
	if !reflect.DeepEqual(removed, []string{"/recordings/final.wav"}) {
		t.Fatalf("rollback paths = %v", removed)
	}
	if !reflect.DeepEqual(operations, []string{"sync-directory", "sync-directory", "close-directory"}) {
		t.Fatalf("rollback directory operations = %v", operations)
	}
}

func TestPublishRecordingFileCloseFailureKeepsDurableDestination(t *testing.T) {
	wantErr := errors.New("directory close failed")
	var removed []string
	var operations []string
	hooks := recordingPublishHooks{
		link: func(string, string) error { return nil },
		remove: func(path string) error {
			removed = append(removed, path)
			return nil
		},
		openDirectory: func(string) (recordingDirectory, error) {
			return &recordingPublishTestDirectory{operations: &operations, closeErr: wantErr}, nil
		},
	}
	if err := publishRecordingFile("temp.wav", "/recordings/final.wav", hooks); err != nil {
		t.Fatalf("durable publication was reported as failed: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"temp.wav"}) {
		t.Fatalf("close failure removed the destination or leaked cleanup order: %v", removed)
	}
}
