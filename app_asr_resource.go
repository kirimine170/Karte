package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
)

type appASRServiceFactory func(*asr.Config) (appASRService, error)

type appRealtimeASRService interface {
	Close()
	ProcessAudio([]float32) (string, string, bool)
	Flush() string
	Reset()
}

type appAudioRecorder interface {
	Start(func([]float32)) error
	Stop() error
	Close() error
}

type serializedRealtimeASRService struct {
	mu      sync.Mutex
	service appRealtimeASRService
}

func newSerializedRealtimeASRService(service appRealtimeASRService) appRealtimeASRService {
	if service == nil {
		return nil
	}
	return &serializedRealtimeASRService{service: service}
}

func (service *serializedRealtimeASRService) Close() {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.service == nil {
		return
	}
	service.service.Close()
	service.service = nil
}

func (service *serializedRealtimeASRService) ProcessAudio(samples []float32) (string, string, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.service == nil {
		return "", "", false
	}
	return service.service.ProcessAudio(samples)
}

func (service *serializedRealtimeASRService) Flush() string {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.service == nil {
		return ""
	}
	return service.service.Flush()
}

func (service *serializedRealtimeASRService) Reset() {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.service != nil {
		service.service.Reset()
	}
}

type appASRResourceState struct {
	mu sync.Mutex

	manager    *asrResourceManager
	closing    bool
	loader     asrResourceLoader
	clock      asrIdleClock
	newService appASRServiceFactory
}

func (app *App) getASRResourceManager() *asrResourceManager {
	if app == nil {
		return nil
	}
	app.asrResource.mu.Lock()
	defer app.asrResource.mu.Unlock()
	if app.asrResource.closing {
		return nil
	}
	if app.asrResource.manager != nil {
		return app.asrResource.manager
	}
	loader := app.asrResource.loader
	if loader == nil {
		loader = app.loadASRResource
	}
	manager := newASRResourceManager(app.lifecycle.context(), loader, app.asrResource.clock)
	app.asrResource.manager = manager
	return manager
}

func (app *App) currentASRResourceManager() *asrResourceManager {
	if app == nil {
		return nil
	}
	app.asrResource.mu.Lock()
	defer app.asrResource.mu.Unlock()
	return app.asrResource.manager
}

func (app *App) acquireASRResource(ctx context.Context) (*asrResourceLease, error) {
	manager := app.getASRResourceManager()
	if manager == nil {
		return nil, errASRResourceClosed
	}
	return manager.Acquire(ctx)
}

func (app *App) loadASRResource(ctx context.Context) (asrResourceLoadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return asrResourceLoadResult{}, err
	}
	cfgPath := filepath.Join(app.dataDir, "data", "asr", "config.json")
	cfg, err := asr.LoadConfigFromFile(cfgPath)
	if err != nil {
		return asrResourceLoadResult{}, err
	}
	if cfg == nil || !cfg.Enabled {
		return asrResourceLoadResult{}, errASRResourceDisabled
	}
	if err := cfg.Validate(); err != nil {
		return asrResourceLoadResult{}, fmt.Errorf("validate ASR config: %w", err)
	}
	cfg.EnsureModelPathsAbsolute(app.dataDir)

	app.asrResource.mu.Lock()
	newService := app.asrResource.newService
	app.asrResource.mu.Unlock()
	if newService == nil {
		newService = func(config *asr.Config) (appASRService, error) {
			return asr.NewService(config)
		}
	}
	service, err := newService(cfg)
	if err != nil {
		if service != nil {
			service.Close()
		}
		return asrResourceLoadResult{}, err
	}
	if service == nil {
		return asrResourceLoadResult{}, errors.New("ASR service factory returned nil")
	}
	if err := ctx.Err(); err != nil {
		service.Close()
		return asrResourceLoadResult{}, err
	}
	return asrResourceLoadResult{
		Service:     service,
		Config:      cfg,
		IdleTimeout: time.Duration(cfg.Runtime.IdleTimeoutSeconds) * time.Second,
	}, nil
}

func (app *App) resetASRResourceManager() {
	if app == nil {
		return
	}
	app.asrResource.mu.Lock()
	previous := app.asrResource.manager
	app.asrResource.manager = nil
	app.asrResource.closing = false
	app.asrResource.mu.Unlock()
	if previous != nil {
		waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
		previous.Shutdown(waitContext)
		cancel()
	}
}

func (app *App) closeASRAdmission() *asrResourceManager {
	if app == nil {
		return nil
	}
	app.asrResource.mu.Lock()
	app.asrResource.closing = true
	manager := app.asrResource.manager
	app.asrResource.mu.Unlock()
	if manager != nil {
		manager.CloseAdmission()
	}
	return manager
}

func (app *App) shutdownASRResource(manager *asrResourceManager) bool {
	if manager == nil {
		return true
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return manager.Shutdown(waitContext)
}

func (app *App) takeRecordingASRResourcesLocked() (appRealtimeASRService, *asrResourceLease) {
	realtimeService := app.realtimeService
	lease := app.recordingASRLease
	app.realtimeService = nil
	app.recordingASRLease = nil
	return realtimeService, lease
}

// closeRecordingASRResources preserves native ownership order: the realtime
// recognizer is closed before releasing the offline session lease．The manager
// may unload the shared offline recognizer immediately after Release．
func closeRecordingASRResources(realtimeService appRealtimeASRService, lease *asrResourceLease) {
	if realtimeService != nil {
		realtimeService.Close()
	}
	if lease != nil {
		lease.Release()
	}
}

func (app *App) newRecordingRealtimeService(config *asr.Config, logger asr.LogFunc) (appRealtimeASRService, error) {
	var service appRealtimeASRService
	var err error
	if app.recordingNewRealtime != nil {
		service, err = app.recordingNewRealtime(config, logger)
	} else {
		service, err = asr.NewRealtimeServiceWithLogger(config, logger)
	}
	return newSerializedRealtimeASRService(service), err
}

func (app *App) newRecordingRecorderInstance() (appAudioRecorder, error) {
	if app.recordingNewRecorder != nil {
		return app.recordingNewRecorder()
	}
	return audio.NewRecorder()
}

func (app *App) newRecordingVADInstance() *audio.SimpleVAD {
	if app.recordingNewVAD != nil {
		return app.recordingNewVAD()
	}
	return audio.DefaultSimpleVAD()
}
