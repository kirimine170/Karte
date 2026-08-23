package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type appSiteBuildState struct {
	mu          sync.Mutex
	builder     *siteBuilder
	coordinator *siteBuildCoordinator
	run         siteBuildRun
	clock       siteBuildClock
	debounce    time.Duration
	maxDirty    int
}

func (a *App) siteBuildRoot() string {
	if a == nil {
		return ""
	}
	if a.dataDir != "" {
		return a.dataDir
	}
	return a.root
}

func (a *App) getSiteBuilder() *siteBuilder {
	a.siteBuild.mu.Lock()
	defer a.siteBuild.mu.Unlock()
	if a.siteBuild.builder == nil {
		a.siteBuild.builder = newSiteBuilder(siteBuildHooks{})
	}
	return a.siteBuild.builder
}

func (a *App) resetSiteBuildCoordinator() {
	if a == nil {
		return
	}
	a.siteBuild.mu.Lock()
	a.siteBuild.coordinator = nil
	a.siteBuild.mu.Unlock()
}

func (a *App) scheduleSiteBuild(paths ...string) bool {
	if a == nil || a.siteBuildRoot() == "" {
		return false
	}
	coordinator := a.getSiteBuildCoordinator()
	return coordinator != nil && coordinator.Schedule(paths...)
}

func (a *App) getSiteBuildCoordinator() *siteBuildCoordinator {
	a.siteBuild.mu.Lock()
	defer a.siteBuild.mu.Unlock()
	if a.siteBuild.coordinator != nil {
		return a.siteBuild.coordinator
	}

	builder := a.siteBuild.builder
	if builder == nil {
		builder = newSiteBuilder(siteBuildHooks{})
		a.siteBuild.builder = builder
	}
	execute := a.siteBuild.run
	if execute == nil {
		execute = func(ctx context.Context, _ siteBuildRequest) error {
			_, err := builder.BuildIncremental(ctx, a.siteBuildRoot())
			return err
		}
	}
	run := func(ctx context.Context, request siteBuildRequest) error {
		manager := a.getJobManager()
		if manager == nil {
			return errJobManagerClosed
		}
		return manager.SubmitAndWait(ctx, managedJob{
			Category: appJobCategorySite,
			Group:    appJobCategorySite,
			Key:      "incremental",
			Priority: jobPriorityNormal,
			Coalesce: jobKeepExisting,
			Run: func(jobContext context.Context) error {
				return execute(jobContext, request)
			},
		})
	}
	coordinator := newSiteBuildCoordinator(
		a.lifecycle.context(),
		run,
		func(err error) {
			a.logError(fmt.Sprintf("Incremental site build failed: %v", err))
		},
		a.siteBuild.clock,
		a.siteBuild.debounce,
		a.siteBuild.maxDirty,
	)
	if !a.lifecycle.goWorker(coordinator.Run) {
		return nil
	}
	a.siteBuild.coordinator = coordinator
	return coordinator
}
