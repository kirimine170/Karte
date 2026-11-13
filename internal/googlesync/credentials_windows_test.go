package googlesync

import (
	"bytes"
	"errors"
	"testing"
)

func TestWindowsCredentialManagerRoundTrip(t *testing.T) {
	id, err := randomString()
	if err != nil {
		t.Fatal(err)
	}
	store := osCredentialStore{key: "Karte.Test." + id}
	t.Cleanup(func() { _ = store.remove() })
	want := []byte(`{"access_token":"test-access","refresh_token":"test-refresh"}`)
	if err := store.save(want); err != nil {
		t.Fatal(err)
	}
	read, err := store.load()
	if err != nil || !bytes.Equal(read, want) {
		t.Fatalf("credential did not round-trip: %v", err)
	}
	if err := store.remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.load(); !errors.Is(err, errMissingCredential) {
		t.Fatal("credential remained after deletion")
	}
}
