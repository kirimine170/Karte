package ocr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"karte/internal/accounting/receipt/domain"
)

type Progress struct {
	EvidenceID, Stage, Message string
	Progress                   *float64
}

type WorkerManager struct {
	runtimeDir string
	dataDir    string
	timeout    time.Duration
	onProgress func(Progress)
	mu         sync.Mutex
	healthMu   sync.RWMutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	scanner    *bufio.Scanner
	health     domain.OCRHealth
}

func NewWorkerManager(runtimeDir, dataDir string, timeout time.Duration, onProgress func(Progress)) *WorkerManager {
	return &WorkerManager{runtimeDir: runtimeDir, dataDir: dataDir, timeout: timeout, onProgress: onProgress, health: domain.OCRHealth{Status: "unknown", Engine: "yomitoku", Version: "0.13.1", Message: "OCRワーカーは未起動です"}}
}

func (m *WorkerManager) Health(context.Context) domain.OCRHealth {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()
	return m.health
}

func (m *WorkerManager) setHealth(health domain.OCRHealth) {
	m.healthMu.Lock()
	m.health = health
	m.healthMu.Unlock()
}

func (m *WorkerManager) setUnavailable(message string) {
	m.setHealth(domain.OCRHealth{Status: "unavailable", Engine: "yomitoku", Version: "0.13.1", Message: message})
}

func (m *WorkerManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startLocked(ctx)
}

func (m *WorkerManager) startLocked(ctx context.Context) error {
	if m.cmd != nil && m.cmd.Process != nil {
		return nil
	}
	python, bundled := resolvePython(m.runtimeDir)
	if python == "" {
		err := fmt.Errorf("Python 3.12が見つかりません．python/.venvを作成するかRECEIPT_OCR_PYTHONを設定してください (%s)", runtime.GOOS)
		m.setUnavailable(err.Error())
		return err
	}
	script := workerScript(m.runtimeDir)
	start := time.Now()
	cmd := exec.CommandContext(context.Background(), python, "-u", script)
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1")
	cmd.Env = append(cmd.Env,
		"HF_HUB_DISABLE_XET=1",
		"XDG_CACHE_HOME="+filepath.Join(m.dataDir, "runtime-cache"),
		"TORCH_HOME="+filepath.Join(m.dataDir, "runtime-cache", "torch"),
	)
	if bundled {
		pythonHome := filepath.Join(m.runtimeDir, "python")
		modelHome := filepath.Join(m.runtimeDir, "models")
		cmd.Env = append(cmd.Env,
			"PYTHONHOME="+pythonHome,
			"HF_HOME="+modelHome,
			"HF_HUB_OFFLINE=1",
			"TRANSFORMERS_OFFLINE=1",
			// The bundled models are offline．Avoid optional SOCKS dependencies from host proxy settings．
			"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "http_proxy=", "https_proxy=", "all_proxy=",
		)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		m.setUnavailable(err.Error())
		return err
	}
	m.cmd = cmd
	m.stdin = stdin
	m.scanner = bufio.NewScanner(stdout)
	m.scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	readyCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	line, err := m.readLine(readyCtx)
	if err != nil {
		m.stopLocked()
		message := "OCRワーカー起動失敗: " + err.Error()
		m.setUnavailable(message)
		return errors.New(message)
	}
	event, err := decodeEvent(line)
	if err != nil {
		m.stopLocked()
		return fmt.Errorf("ワーカーハンドシェイク不正: %w", err)
	}
	if event.Type == "fatal_error" {
		m.stopLocked()
		message := event.Message
		if message == "" {
			message = event.Code
		}
		m.setUnavailable(message)
		return errors.New(message)
	}
	if event.Type != "ready" || event.ProtocolVersion != "1" {
		m.stopLocked()
		return fmt.Errorf("予期しないハンドシェイク: %s", event.Type)
	}
	m.setHealth(domain.OCRHealth{Status: "ready", Engine: event.Engine.Name, Version: event.Engine.Version, Device: event.Device, Mode: event.Mode, Message: "OCRワーカーは利用可能です", InitializationMS: time.Since(start).Milliseconds()})
	return nil
}

func (m *WorkerManager) Recognize(ctx context.Context, e domain.Evidence) (*domain.OCRDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recognizeLocked(ctx, e, requestParams{Path: e.StoredPath, Visualize: true, DocumentType: "receipt"}, "ocr")
}

func (m *WorkerManager) RecognizeRegion(ctx context.Context, e domain.Evidence, region domain.BoundingBox, variant string) (*domain.OCRDocument, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recognizeLocked(ctx, e, requestParams{Path: e.StoredPath, Visualize: false, DocumentType: "receipt", Region: &region, PreprocessVariant: variant}, "ocr-refine")
}

func (m *WorkerManager) recognizeLocked(ctx context.Context, e domain.Evidence, params requestParams, prefix string) (*domain.OCRDocument, error) {
	if err := m.startLocked(ctx); err != nil {
		return nil, err
	}
	runID := domain.NewID(prefix)
	outDir := filepath.Join(m.dataDir, "derived", e.ID, runID)
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	params.OutputDir = outDir
	req := request{RequestID: runID, Method: "recognize", Params: params}
	b, _ := json.Marshal(req)
	if _, err := m.stdin.Write(append(b, '\n')); err != nil {
		m.stopLocked()
		return nil, fmt.Errorf("OCR要求送信失敗: %w", err)
	}
	started := time.Now().UTC()
	deadline := m.timeout
	if deadline <= 0 {
		deadline = 120 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		line, err := m.readLine(reqCtx)
		if err != nil {
			m.stopLocked()
			return nil, fmt.Errorf("OCRワーカー応答失敗: %w", err)
		}
		event, err := decodeEvent(line)
		if err != nil {
			m.stopLocked()
			return nil, fmt.Errorf("PROTOCOL_ERROR: %w", err)
		}
		if event.RequestID != "" && event.RequestID != runID {
			continue
		}
		switch event.Type {
		case "progress":
			if m.onProgress != nil {
				m.onProgress(Progress{EvidenceID: e.ID, Stage: event.Stage, Message: event.Message, Progress: event.Progress})
			}
		case "error":
			if event.Error == nil {
				return nil, errors.New("OCRワーカーエラー")
			}
			return nil, fmt.Errorf("%s: %s", event.Error.Code, event.Error.Message)
		case "result":
			if event.Result == nil {
				return nil, errors.New("OCR結果が空です")
			}
			pages, err := AdaptYomiToku(event.Result.RawResultPath)
			if err != nil {
				return nil, err
			}
			completed := time.Now().UTC()
			return &domain.OCRDocument{ID: runID, EvidenceID: e.ID, Engine: event.Result.Engine, EngineVersion: event.Result.EngineVersion, Device: event.Result.Device, Mode: event.Result.Mode, StartedAt: started, CompletedAt: completed, DurationMS: event.Result.DurationMS, Pages: pages, RawResultPath: event.Result.RawResultPath, VisualizationPath: event.Result.VisualizationPath}, nil
		}
	}
}

func (m *WorkerManager) readLine(ctx context.Context) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	scanner := m.scanner
	go func() {
		if scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			ch <- result{b: line}
		} else {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- result{err: err}
		}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *WorkerManager) stopLocked() {
	if m.stdin != nil {
		_ = m.stdin.Close()
	}
	if m.cmd != nil && m.cmd.Process != nil {
		_ = m.cmd.Process.Kill()
		_ = m.cmd.Wait()
	}
	m.cmd = nil
	m.stdin = nil
	m.scanner = nil
}
func (m *WorkerManager) Close() error { m.mu.Lock(); defer m.mu.Unlock(); m.stopLocked(); return nil }

func resolvePython(runtimeDir string) (string, bool) {
	candidates := []string{}
	if p := os.Getenv("RECEIPT_OCR_PYTHON"); p != "" {
		candidates = append(candidates, p)
	}
	bundled := filepath.Join(runtimeDir, "python", "bin", "python3.12")
	candidates = append(candidates, bundled, filepath.Join(runtimeDir, "python", ".venv", "bin", "python"), filepath.Join(runtimeDir, ".venv", "bin", "python"), "python3.12")
	for _, c := range candidates {
		if strings.ContainsRune(c, filepath.Separator) {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				return c, c == bundled
			}
		} else if p, err := exec.LookPath(c); err == nil {
			return p, false
		}
	}
	return "", false
}

func workerScript(runtimeDir string) string {
	bundled := filepath.Join(runtimeDir, "worker", "main.py")
	if _, err := os.Stat(bundled); err == nil {
		return bundled
	}
	return filepath.Join(runtimeDir, "python", "worker", "main.py")
}
