package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	appJobCategoryASRHeavy = "asr-heavy"
	appJobCategorySite     = "site-build"
	appJobCategoryWebClip  = "web-clip"
	appJobCategoryUILatest = "ui-latest"

	appJobGroupAudioImport = "audio-import"
	appJobGroupWebClip     = "web-clip"
	appJobGroupInputLevel  = "recording-input-level"
)

type appJobState struct {
	mu      sync.Mutex
	manager *jobManager
	closing bool
	config  *jobManagerConfig
}

func defaultAppJobManagerConfig(app *App) jobManagerConfig {
	return jobManagerConfig{
		Workers:    defaultJobManagerWorkers,
		MaxPending: defaultJobManagerMaxPending,
		CategoryLimits: map[string]int{
			appJobCategoryASRHeavy: 1,
			appJobCategorySite:     1,
			appJobCategoryWebClip:  1,
			appJobCategoryUILatest: 1,
		},
		OnPanic: func(job managedJob, recovered any) {
			if app != nil {
				app.logError(fmt.Sprintf("Background job panicked: category=%s key=%s panic=%v", job.Category, job.Key, recovered))
			}
		},
	}
}

func (app *App) getJobManager() *jobManager {
	if app == nil {
		return nil
	}
	app.jobs.mu.Lock()
	defer app.jobs.mu.Unlock()
	if app.jobs.closing {
		return nil
	}
	if app.jobs.manager != nil {
		return app.jobs.manager
	}
	config := defaultAppJobManagerConfig(app)
	if app.jobs.config != nil {
		config = *app.jobs.config
		if config.OnPanic == nil {
			config.OnPanic = defaultAppJobManagerConfig(app).OnPanic
		}
	}
	manager := newJobManager(config)
	if !manager.Start(app.lifecycle.context(), app.lifecycle.goWorker) {
		return nil
	}
	app.jobs.manager = manager
	return manager
}

func (app *App) resetJobManager() {
	if app == nil {
		return
	}
	app.jobs.mu.Lock()
	previous := app.jobs.manager
	app.jobs.manager = nil
	app.jobs.closing = false
	app.jobs.mu.Unlock()
	if previous != nil {
		waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
		previous.Shutdown(waitContext)
		cancel()
	}
}

// closeJobAdmission closes the manager creation race before lifecycle
// cancellation．No submission can create a replacement manager after this
// boundary．
func (app *App) closeJobAdmission() *jobManager {
	manager := app.sealJobAdmission()
	if manager != nil {
		manager.Close()
	}
	return manager
}

// sealJobAdmission prevents public call sites from creating or admitting new
// application jobs while leaving an existing manager alive long enough for a
// recording processor to submit its already-accepted final segment．
func (app *App) sealJobAdmission() *jobManager {
	if app == nil {
		return nil
	}
	app.jobs.mu.Lock()
	app.jobs.closing = true
	manager := app.jobs.manager
	app.jobs.mu.Unlock()
	return manager
}

func (app *App) recordingJobManager() *jobManager {
	if app == nil {
		return nil
	}
	app.jobs.mu.Lock()
	defer app.jobs.mu.Unlock()
	return app.jobs.manager
}

func cancelNonRecordingJobsForShutdown(manager *jobManager) int {
	if manager == nil {
		return 0
	}
	canceled := manager.CancelGroup(appJobGroupAudioImport)
	canceled += manager.CancelCategory(appJobCategoryWebClip)
	canceled += manager.CancelCategory(appJobCategorySite)
	canceled += manager.CancelCategory(appJobCategoryUILatest)
	return canceled
}

func (app *App) enqueueRecordingInputLevel(level float32) {
	manager := app.getJobManager()
	if manager == nil {
		return
	}
	// This telemetry is intentionally lossy．A fixed key plus replacement keeps
	// at most one pending value and always delivers the newest observed level．
	manager.Submit(managedJob{
		Category: appJobCategoryUILatest,
		Group:    appJobGroupInputLevel,
		Key:      appJobGroupInputLevel,
		Priority: jobPriorityLow,
		Coalesce: jobReplacePending,
		Run: func(ctx context.Context) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			app.emitEvent("recording-input-level", map[string]interface{}{
				"level": level,
			})
			return nil
		},
	})
}
