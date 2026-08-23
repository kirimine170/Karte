//go:build windows && !cgo

package asr

import "errors"

var errWindowsASRRuntimeUnavailable = errors.New("ASR requires the Windows sherpa-onnx runtime")

func unsupportedOfflinePlatformError(string) error {
	return errWindowsASRRuntimeUnavailable
}

func unsupportedRealtimePlatformError() error {
	return errWindowsASRRuntimeUnavailable
}
