//go:build (darwin && universal) || linux || (windows && !cgo)

package asr

func offlinePlatformError(operation string) error {
	return unsupportedOfflinePlatformError(operation)
}

func newOfflineRecognizerAdapter(*Config) (offlineRecognizerAdapter, error) {
	return nil, offlinePlatformError("new")
}

func realtimePlatformError() error {
	return unsupportedRealtimePlatformError()
}

func newOnlineRecognizerAdapter(onlineRecognizerSpec) (onlineRecognizerAdapter, error) {
	return nil, realtimePlatformError()
}
