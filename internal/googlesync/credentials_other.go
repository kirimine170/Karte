//go:build !windows && (!darwin || !cgo)

package googlesync

import "errors"

var errOSCredentialUnavailable = errors.New("OS credential storage is unavailable")

func (s osCredentialStore) load() ([]byte, error) { return nil, errOSCredentialUnavailable }
func (s osCredentialStore) save([]byte) error     { return errOSCredentialUnavailable }
func (s osCredentialStore) remove() error         { return errOSCredentialUnavailable }
