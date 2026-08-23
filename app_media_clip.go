package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (a *App) convertImageFileToWebP(sourceAbs, webpAbs, sourceExt string) error {
	return a.convertImageFileToWebPContext(context.Background(), sourceAbs, webpAbs, sourceExt)
}

func (a *App) convertImageFileToWebPContext(ctx context.Context, sourceAbs, webpAbs, sourceExt string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	config, hooks := a.mediaImportSettings()
	spec, err := mediaImportSpecForKind(mediaImportKindImage, config)
	if err != nil {
		return err
	}
	sourceExt = strings.ToLower(sourceExt)
	if sourceExt != strings.ToLower(filepath.Ext(sourceAbs)) {
		return errors.New("Web Clip image extension does not match its source path")
	}
	if _, ok := spec.extensions[sourceExt]; !ok {
		return fmt.Errorf("unsupported Web Clip image format: %s", sourceExt)
	}
	if filepath.Clean(filepath.Dir(sourceAbs)) != filepath.Clean(filepath.Dir(webpAbs)) || strings.ToLower(filepath.Ext(webpAbs)) != ".webp" {
		return errors.New("Web Clip WebP destination must be in the source directory")
	}
	sourceRelative, err := filepath.Rel(a.dataDir, sourceAbs)
	if err != nil || sourceRelative == ".." || strings.HasPrefix(sourceRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(sourceRelative) {
		return errors.New("Web Clip source image is outside the data directory")
	}
	destinationDirectoryRelative, err := filepath.Rel(a.dataDir, filepath.Dir(webpAbs))
	if err != nil || destinationDirectoryRelative == ".." || strings.HasPrefix(destinationDirectoryRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(destinationDirectoryRelative) {
		return errors.New("Web Clip destination is outside the data directory")
	}

	source, sourceInfo, err := openConfinedMediaFile(a.dataDir, filepath.ToSlash(sourceRelative))
	if err != nil {
		return fmt.Errorf("open Web Clip source image: %w", err)
	}
	defer source.Close()
	sourceName := filepath.Base(sourceAbs)
	if sourceInfo.Size() <= 0 || sourceInfo.Size() > spec.maxBytes {
		return fmt.Errorf("%w: image limit is %d bytes", errMediaImportTooLarge, spec.maxBytes)
	}
	_, format, decoded, err := decodeBoundedMediaImage(ctx, source, sourceName, hooks)
	if err != nil {
		return err
	}
	if err := mediaImportContextError(ctx); err != nil {
		return err
	}
	decoded = resizeImportedImage(decoded)
	if err := mediaImportContextError(ctx); err != nil {
		return err
	}

	destinationRoot, err := openStableMediaDirectory(a.dataDir, filepath.ToSlash(destinationDirectoryRelative), false, hooks)
	if err != nil {
		return fmt.Errorf("open Web Clip image directory: %w", err)
	}
	stage := &mediaImportStage{
		root:         destinationRoot,
		spec:         spec,
		originalName: sourceName,
		hooks:        hooks,
	}
	defer stage.abort()
	derived, err := createMediaStageFile(destinationRoot, hooks)
	if err != nil {
		return err
	}
	stage.derived = derived
	writer := &limitedMediaStageWriter{stage: stage, file: derived, limit: spec.maxBytes, ctx: ctx}
	lossless := format == "png" || format == "gif"
	if err := hooks.encodeWebP(writer, decoded, lossless); err != nil {
		return fmt.Errorf("encode Web Clip image as WebP: %w", err)
	}
	if err := mediaImportContextError(ctx); err != nil {
		return err
	}
	if err := stage.syncAndClose(derived); err != nil {
		return err
	}
	if err := mediaImportContextError(ctx); err != nil {
		return err
	}

	finalName := filepath.Base(webpAbs)
	if err := hooks.link(destinationRoot, derived.name, finalName); err != nil {
		return fmt.Errorf("publish Web Clip WebP without replacement: %w", err)
	}
	if err := hooks.syncRoot(destinationRoot); err != nil {
		removeErr := hooks.remove(destinationRoot, finalName)
		rollbackSyncErr := hooks.syncRoot(destinationRoot)
		return errors.Join(
			fmt.Errorf("sync published Web Clip WebP: %w", err),
			wrapMediaImportError("remove published Web Clip WebP", removeErr),
			wrapMediaImportError("sync Web Clip WebP rollback", rollbackSyncErr),
		)
	}
	stage.published = true
	if cleanupErr := stage.cleanup(); cleanupErr != nil {
		a.logError(fmt.Sprintf("Web Clip WebP temp cleanup failed after publish: %v", cleanupErr))
	}
	return nil
}
