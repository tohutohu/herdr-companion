// Package translation owns the optional, local-only PLaMo MLX process.
package translation

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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
	"syscall"
	"time"
	"unicode"

	"github.com/tohutohu/herdr-android-client/gateway/internal/model"
)

//go:embed worker.py
var workerSource []byte

type Options struct {
	Input             bool   `json:"input"`
	InputEverEnabled  bool   `json:"inputEverEnabled,omitempty"`
	Model             string `json:"model"`
	Output            bool   `json:"output"`
	AutoStart         bool   `json:"autoStart"`
	OutputEverEnabled bool   `json:"outputEverEnabled,omitempty"`
}
type Status struct {
	Options
	Supported bool   `json:"supported"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	Error     string `json:"error,omitempty"`
}
type worker struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}
type sourcePrompt struct {
	Source     string
	Translated string
	Created    time.Time
}

type cached struct {
	Text  string `json:"text,omitempty"`
	Used  uint64 `json:"used,omitempty"`
	retry time.Time
}
type job struct{ key, text string }
type Service struct {
	dir        string
	ctx        context.Context
	mu         sync.Mutex
	timingMu   sync.Mutex
	life       sync.Mutex
	status     Status
	cancel     context.CancelFunc
	done       chan struct{}
	worker     *worker
	gate       chan struct{}
	cache      map[string]cached
	cacheBytes int
	cacheClock uint64
	pending    map[string]bool
	queue      chan job
	priority   chan job
}

func New(ctx context.Context, dir string) *Service {
	s := &Service{dir: dir, ctx: ctx, gate: make(chan struct{}, 1), cache: map[string]cached{}, pending: map[string]bool{}, queue: make(chan job, 16), priority: make(chan job, 64)}
	s.status = Status{Options: Options{Model: "0.8b"}, Supported: runtime.GOOS == "darwin" && runtime.GOARCH == "arm64", State: "stopped", Detail: "停止中"}
	if data, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(data, &s.status.Options)
	}
	s.status.OutputEverEnabled = s.status.OutputEverEnabled || s.status.Output
	s.status.InputEverEnabled = s.status.InputEverEnabled || s.status.Input
	// Prompt originals are durable history, including sessions that opted in
	// without ever enabling the global input setting.
	if info, err := os.Stat(filepath.Join(dir, "prompts")); err == nil && info.IsDir() {
		s.status.InputEverEnabled = true
	}
	if revisions[s.status.Model] == "" {
		s.status.Model = "0.8b"
	}
	s.loadCache()
	go s.runQueue()
	if s.status.AutoStart && s.status.Supported {
		_ = s.Start(s.status.Model)
	}
	return s
}
func (s *Service) Status() Status { s.mu.Lock(); defer s.mu.Unlock(); return s.status }
func (s *Service) saveLocked() error {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(s.status.Options)
	p := filepath.Join(s.dir, "settings.json")
	if err := os.WriteFile(p+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}
func (s *Service) Configure(input, output bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.status.Options
	s.status.Input, s.status.Output = input, output
	s.status.InputEverEnabled = s.status.InputEverEnabled || input
	s.status.OutputEverEnabled = s.status.OutputEverEnabled || output
	if err := s.saveLocked(); err != nil {
		s.status.Options = old
		return err
	}
	return nil
}
func (s *Service) stage(detail string) { s.mu.Lock(); s.status.Detail = detail; s.mu.Unlock() }
func (s *Service) Start(size string) error {
	if revisions[size] == "" {
		return errors.New("choose 0.8b, 2b or 8b")
	}
	if !s.Status().Supported {
		return errors.New("MLX翻訳にはApple silicon Macが必要です")
	}
	s.life.Lock()
	defer s.life.Unlock()
	return s.startLocked(size)
}

// Caller holds life so concurrent recovery requests never restart each other.
func (s *Service) startLocked(size string) error {
	s.stop(false)
	s.mu.Lock()
	s.status.Model = size
	s.status.AutoStart = true
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.status.State, s.status.Detail, s.status.Error = "starting", "準備中…", ""
	s.mu.Unlock()
	go func() {
		defer close(done)
		started := time.Now()
		err := s.install(ctx, size)
		var w *worker
		if err == nil {
			s.stage("モデルを読み込み中…")
			w, err = s.launch(ctx, size)
		}
		s.mu.Lock()
		if err != nil {
			if ctx.Err() == nil {
				s.status.State, s.status.Error = "failed", err.Error()
			}
			s.mu.Unlock()
			s.recordTiming(timing{Event: "worker_start", Model: size, ElapsedMS: milliseconds(time.Since(started)), Outcome: timingOutcome(err)})
			return
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return
		}
		s.worker = w
		s.status.State, s.status.Detail = "ready", "翻訳可能"
		s.mu.Unlock()
		s.recordTiming(timing{Event: "worker_start", Model: size, ElapsedMS: milliseconds(time.Since(started)), Outcome: "ok"})
		readyAt := time.Now()
		err = w.cmd.Wait()
		outcome := "error"
		if ctx.Err() != nil {
			outcome = "stopped"
		}
		s.recordTiming(timing{Event: "worker_exit", Model: size, ElapsedMS: milliseconds(time.Since(readyAt)), Outcome: outcome, Exit: w.cmd.ProcessState.String()})
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.worker == w {
			s.worker = nil
		}
		if ctx.Err() == nil {
			s.status.State, s.status.Detail = "failed", "翻訳プロセスが終了しました"
			s.status.Error = fmt.Sprintf("翻訳プロセスが終了しました: %v", err)
		}
	}()
	return nil
}

// Stop keeps mode settings so an interrupted translation cannot silently send the original.
func (s *Service) Stop() error {
	s.life.Lock()
	defer s.life.Unlock()
	s.stop(true)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}
func (s *Service) Close() { s.life.Lock(); defer s.life.Unlock(); s.stop(false) }
func (s *Service) stop(manual bool) {
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel = nil
	s.done = nil
	if manual {
		s.status.AutoStart = false
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.mu.Lock()
	s.worker = nil
	s.status.State, s.status.Detail = "stopped", "停止中"
	s.mu.Unlock()
}
func (s *Service) environment() []string {
	env := []string{}
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(key, "HF_") || strings.HasPrefix(key, "UV_") || strings.HasPrefix(key, "PYTHON") || key == "VIRTUAL_ENV" {
			continue
		}
		env = append(env, e)
	}
	return append(env, "HF_HUB_DISABLE_TELEMETRY=1", "TOKENIZERS_PARALLELISM=false", "UV_PYTHON_INSTALL_DIR="+filepath.Join(s.dir, "python"), "UV_CACHE_DIR="+filepath.Join(s.dir, "uv-cache"), "PYTHONUNBUFFERED=1", "HF_HUB_OFFLINE=1")
}
func processContext(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 3 * time.Second
	return cmd
}
func (s *Service) launch(ctx context.Context, size string) (*worker, error) {
	log, err := os.OpenFile(filepath.Join(s.dir, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	cmd := processContext(ctx, filepath.Join(s.dir, "venv", "bin", "python"), filepath.Join(s.dir, "worker.py"), "serve", s.modelDir(size))
	cmd.Env = s.environment()
	cmd.Stderr = log
	in, err := cmd.StdinPipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	log.Close()
	w := &worker{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 64<<10)}
	result := make(chan error, 1)
	go func() {
		var reply struct{ Ready bool }
		line, e := w.out.ReadBytes('\n')
		if e == nil {
			e = json.Unmarshal(line, &reply)
			if e == nil && !reply.Ready {
				e = errors.New("worker not ready")
			}
		}
		result <- e
	}()
	select {
	case err = <-result:
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(3 * time.Minute):
		err = errors.New("model load timed out")
	}
	if err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		return nil, fmt.Errorf("モデルの起動に失敗: %w（translation/worker.log）", err)
	}
	return w, nil
}

func hasJapanese(text string) bool {
	for _, r := range text {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}
func (s *Service) Input(ctx context.Context, sessionID, text string, mode ...*bool) (string, error) {
	if !modeEnabled(s.Status().Input, mode) || !hasJapanese(text) || strings.HasPrefix(strings.TrimSpace(text), "/") {
		return text, nil
	}
	translated, err := s.Translate(ctx, text, "en")
	if err != nil {
		return "", err
	}
	if translated == text {
		return text, nil
	}
	if err := s.RememberPrompt(sessionID, text, translated); err != nil {
		return "", err
	}
	return translated, nil
}

// RememberPrompt also records the exact form providers put in their transcript
// (trimmed whitespace and appended attachment paths), without translating paths.
func (s *Service) RememberPrompt(sessionID, source, translated string) error {
	// Source prompts are private durable user data, separate from agent transcripts.
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := filepath.Join(s.dir, "prompts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, cacheKey("input:"+sessionID, translated)+".json")
	var saved []sourcePrompt
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &saved); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	saved = append(saved, sourcePrompt{Source: source, Translated: translated, Created: time.Now().UTC()})
	data, _ := json.Marshal(saved)
	if err := os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	s.status.InputEverEnabled = true
	return nil
}

// readyWorker recovers unexpected exits only when automatic startup is enabled.
// An explicit Stop remains stopped; a request during startup waits for readiness.
func (s *Service) readyWorker(ctx context.Context) (*worker, string, error) {
	s.life.Lock()
	st := s.Status()
	if st.Supported && st.AutoStart && (st.State == "failed" || st.State == "stopped") {
		if err := s.startLocked(st.Model); err != nil {
			s.life.Unlock()
			return nil, "", err
		}
	}
	s.life.Unlock()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.mu.Lock()
		w, st := s.worker, s.status
		s.mu.Unlock()
		if st.State == "ready" && w != nil {
			return w, st.Model, nil
		}
		if st.State != "starting" {
			if st.Error != "" && st.State == "failed" {
				return nil, "", errors.New(st.Error)
			}
			return nil, "", errors.New("PLaMo翻訳が停止中です。Gatewayメニューから起動するか、プロンプト翻訳を無効にしてください")
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) Translate(ctx context.Context, text, target string) (translated string, err error) {
	started := time.Now()
	metric := timing{Event: "request", Model: s.Status().Model, Target: target, InputBytes: len(text)}
	defer func() {
		metric.ElapsedMS = milliseconds(time.Since(started))
		metric.OutputBytes, metric.Outcome = len(translated), timingOutcome(err)
		s.recordTiming(metric)
	}()
	if len(text) > 128<<10 {
		return "", errors.New("翻訳対象は128 KiB以下に分割してください")
	}
	if target != "en" && target != "ja" {
		return "", errors.New("unsupported target")
	}
	// Cache hits do not need a loaded model, even after a Gateway restart.
	s.mu.Lock()
	value, ok := s.cacheGetLocked(translationKey(s.status.Model, target, text))
	s.mu.Unlock()
	if ok {
		metric.CacheHit = true
		return value, nil
	}
	waitStarted := time.Now()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		metric.QueueMS = milliseconds(time.Since(waitStarted))
		return "", ctx.Err()
	}
	metric.QueueMS = milliseconds(time.Since(waitStarted))
	// A duplicate may have completed while this request waited for the worker.
	s.mu.Lock()
	value, ok = s.cacheGetLocked(translationKey(s.status.Model, target, text))
	s.mu.Unlock()
	if ok {
		metric.CacheHit = true
		<-s.gate
		return value, nil
	}
	if err := ctx.Err(); err != nil {
		<-s.gate
		return "", err
	}
	type result struct {
		text string
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer func() { <-s.gate }()
		// Finish and cache work after a phone disconnects. Only our own inference
		// deadline or explicit Stop may kill the worker and interrupt its protocol.
		workCtx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
		defer cancel()
		workStarted := time.Now()
		workMetric := timing{Event: "inference", Model: s.Status().Model, Target: target, InputBytes: len(text)}
		w, size, err := s.readyWorker(workCtx)
		workMetric.StartupMS = milliseconds(time.Since(workStarted))
		if err != nil {
			workMetric.ElapsedMS = milliseconds(time.Since(workStarted))
			workMetric.Outcome = timingOutcome(err)
			s.recordTiming(workMetric)
			resultCh <- result{err: err}
			return
		}
		workMetric.Model = size
		inferenceStarted := time.Now()
		replyCh := make(chan result, 1)
		go func() {
			req, _ := json.Marshal(map[string]string{"text": text, "target": target})
			_, err := w.in.Write(append(req, '\n'))
			var reply struct{ Text, Error string }
			if err == nil {
				var line []byte
				line, err = w.out.ReadBytes('\n')
				if err == nil {
					err = json.Unmarshal(line, &reply)
				}
			}
			if reply.Error != "" {
				err = errors.New(reply.Error)
			}
			if err == nil && reply.Text == "" {
				err = errors.New("empty translation")
			}
			replyCh <- result{reply.Text, err}
		}()
		select {
		case r := <-replyCh:
			workMetric.InferenceMS = milliseconds(time.Since(inferenceStarted))
			workMetric.ElapsedMS = milliseconds(time.Since(workStarted))
			workMetric.OutputBytes, workMetric.Outcome = len(r.text), timingOutcome(r.err)
			s.recordTiming(workMetric)
			if r.err == nil {
				s.mu.Lock()
				s.cachePutLocked(translationKey(size, target, text), r.text)
				s.mu.Unlock()
			}
			resultCh <- r
		case <-workCtx.Done():
			_ = w.cmd.Cancel()
			// Drain the old reader before letting another call use the protocol.
			<-replyCh
			workMetric.InferenceMS = milliseconds(time.Since(inferenceStarted))
			workMetric.ElapsedMS = milliseconds(time.Since(workStarted))
			workMetric.Outcome = timingOutcome(workCtx.Err())
			s.recordTiming(workMetric)
			resultCh <- result{err: workCtx.Err()}
		}
	}()
	select {
	case r := <-resultCh:
		return r.text, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func cacheKey(scope, text string) string {
	sum := sha256.Sum256([]byte(scope + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

// Messages never mutates provider caches. Translation runs off the polling path.
// Full snapshots are served while enabled so older blocks receive finished translations too.
func (s *Service) Messages(sessionID string, msgs []model.Message, complete bool, mode ...*bool) []model.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]model.Message(nil), msgs...)
	if s.status.InputEverEnabled || modeEnabled(false, mode) {
		for i := range out {
			if out[i].Role != model.RoleUser {
				continue
			}
			out[i].Blocks = append([]model.Block(nil), out[i].Blocks...)
			for b := range out[i].Blocks {
				block := &out[i].Blocks[b]
				if block.Type != model.BlockText {
					continue
				}
				var chosen sourcePrompt
				for _, scope := range []string{"", sessionID} {
					var saved []sourcePrompt
					data, err := os.ReadFile(filepath.Join(s.dir, "prompts", cacheKey("input:"+scope, block.Text)+".json"))
					if err != nil || json.Unmarshal(data, &saved) != nil {
						continue
					}
					for _, candidate := range saved {
						if candidate.Translated == block.Text &&
							promptPrecedesMessage(candidate.Created, out[i].Timestamp) &&
							candidate.Created.After(chosen.Created) {
							chosen = candidate
						}
					}
				}
				if chosen.Source != "" {
					block.OriginalText, block.Text = block.Text, chosen.Source
				}
			}
		}
	}

	if !modeEnabled(s.status.Output, mode) {
		return out
	}
	for i := range out {
		out[i].Blocks = append([]model.Block(nil), out[i].Blocks...)
		for b := range out[i].Blocks {
			if ia := out[i].Blocks[b].Interaction; ia != nil {
				out[i].Blocks[b].Interaction = s.dialogLocked(ia)
			}
		}
	}
	count := 0
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != model.RoleAssistant {
			continue
		}
		out[i].Blocks = append([]model.Block(nil), out[i].Blocks...)
		for b := range out[i].Blocks {
			block := &out[i].Blocks[b]
			if block.Type != model.BlockText || !block.Report || !hasLatin(block.Text) {
				continue
			}
			count++
			if count > 128 {
				continue
			}
			key := translationKey(s.status.Model, "ja", block.Text)
			v, ok := s.cache[key]
			if translated, hit := s.cacheGetLocked(key); hit {
				if translated != block.Text {
					block.OriginalText, block.Text = block.Text, translated
				}
				continue
			}
			if !s.canTranslateLocked() || !complete || s.pending[key] || (ok && time.Now().Before(v.retry)) {
				continue
			}
			select {
			case s.queue <- job{key, block.Text}:
				s.pending[key] = true
			default:
			}
		}
	}
	return out
}

// Codex turn timestamps are truncated to seconds; Claude's are typically
// milliseconds. Compare at the provider's precision, not the save clock's.
func promptPrecedesMessage(created, message time.Time) bool {
	if message.IsZero() {
		return true
	}
	precision := time.Millisecond
	if message.Nanosecond() == 0 {
		precision = time.Second
	}
	return created.Before(message.Add(precision))
}
func (s *Service) runQueue() {
	for {
		var j job
		select {
		case <-s.ctx.Done():
			return
		case j = <-s.priority:
		default:
			select {
			case <-s.ctx.Done():
				return
			case j = <-s.priority:
			case j = <-s.queue:
			}
		}
		{
			s.mu.Lock()
			valid := s.canTranslateLocked() && j.key == translationKey(s.status.Model, "ja", j.text)
			if !valid {
				delete(s.pending, j.key)
			}
			s.mu.Unlock()
			if !valid {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
			_, err := s.Translate(ctx, j.text, "ja")
			cancel()
			s.mu.Lock()
			delete(s.pending, j.key)
			if err != nil {
				// Do not replace a success that finished after the caller timed out.
				if _, hit := s.cacheGetLocked(j.key); !hit {
					s.cache[j.key] = cached{retry: time.Now().Add(30 * time.Second)}
					s.trimCacheLocked()
				}
				if s.status.State == "ready" {
					s.status.Error = "回答の翻訳に失敗: " + err.Error()
				}
			}
			s.mu.Unlock()
		}
	}
}

// A session choice overrides the global default without changing other sessions.
func modeEnabled(fallback bool, mode []*bool) bool {
	if len(mode) > 0 && mode[0] != nil {
		return *mode[0]
	}
	return fallback
}
