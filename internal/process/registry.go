package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
)

const (
	MaxConcurrentTasks       = 64
	FinishedTTL              = 30 * time.Minute
	defaultBackgroundTimeout = 120 * time.Second
)

var DefaultBackgroundTimeout = defaultBackgroundTimeout

func SetDefaultBackgroundTimeout(d time.Duration) {
	DefaultBackgroundTimeout = d
}

type TaskType string

const (
	TaskTypeBash  TaskType = "bash"
	TaskTypeAgent TaskType = "agent"
)

type ProcessSession struct {
	ID          string
	Type        TaskType
	Command     string
	Description string
	Prompt      string
	PID         int
	Status      string
	ExitCode    int
	Result      string
	Output      *RollingBuffer
	CreatedAt   time.Time
	FinishedAt  time.Time
	OnChunk     func(string)
	OnComplete  func(*ProcessSession)

	mu       sync.Mutex
	cmd      *exec.Cmd
	ptmx     *os.File
	done     chan struct{}
	bgSignal chan struct{}
	bgTimer  *time.Timer
	cancel   context.CancelFunc
}

type SpawnOption func(*ProcessSession)

func WithAutoBackground(d time.Duration) SpawnOption {
	return func(ps *ProcessSession) {
		if d > 0 {
			ps.bgTimer = time.NewTimer(d)
		}
	}
}

func WithOnComplete(fn func(*ProcessSession)) SpawnOption {
	return func(ps *ProcessSession) {
		ps.OnComplete = fn
	}
}

type Registry struct {
	running      map[string]*ProcessSession
	backgrounded map[string]*ProcessSession
	finished     map[string]*ProcessSession
	mu           sync.RWMutex
}

var DefaultRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{
		running:      make(map[string]*ProcessSession),
		backgrounded: make(map[string]*ProcessSession),
		finished:     make(map[string]*ProcessSession),
	}
}

func (r *Registry) Spawn(command string, onChunk func(string), opts ...SpawnOption) (*ProcessSession, error) {
	r.mu.Lock()
	total := len(r.running) + len(r.backgrounded)
	r.mu.Unlock()

	if total >= MaxConcurrentTasks {
		return nil, fmt.Errorf("max concurrent tasks (%d) reached", MaxConcurrentTasks)
	}

	env := os.Environ()
	found := false
	for i, e := range env {
		if len(e) > 5 && e[:5] == "TERM=" {
			env[i] = "TERM=xterm-256color"
			found = true
			break
		}
	}
	if !found {
		env = append(env, "TERM=xterm-256color")
	}

	cmd := exec.Command("bash", "-c", "set +m; "+command)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty.Start: %w", err)
	}

	ps := &ProcessSession{
		ID:        uuid.New().String(),
		Type:      TaskTypeBash,
		Command:   command,
		PID:       cmd.Process.Pid,
		Status:    "running",
		Output:    NewRollingBuffer(0),
		CreatedAt: time.Now(),
		OnChunk:   onChunk,
		cmd:       cmd,
		ptmx:      ptmx,
		done:      make(chan struct{}),
		bgSignal:  make(chan struct{}),
	}

	for _, opt := range opts {
		opt(ps)
	}

	r.mu.Lock()
	r.running[ps.ID] = ps
	r.mu.Unlock()

	if ps.bgTimer != nil {
		go r.autoBackground(ps)
	}

	go r.readOutput(ps)

	return ps, nil
}

func (r *Registry) SpawnAgent(description, prompt string, runFunc func(ctx context.Context) (string, error), opts ...SpawnOption) (*ProcessSession, error) {
	r.mu.Lock()
	total := len(r.running) + len(r.backgrounded)
	r.mu.Unlock()

	if total >= MaxConcurrentTasks {
		return nil, fmt.Errorf("max concurrent tasks (%d) reached", MaxConcurrentTasks)
	}

	ctx, cancel := context.WithCancel(context.Background())

	ps := &ProcessSession{
		ID:          uuid.New().String(),
		Type:        TaskTypeAgent,
		Description: description,
		Prompt:      prompt,
		Status:      "running",
		Output:      NewRollingBuffer(0),
		CreatedAt:   time.Now(),
		done:        make(chan struct{}),
		bgSignal:    make(chan struct{}),
		cancel:      cancel,
	}

	for _, opt := range opts {
		opt(ps)
	}

	r.mu.Lock()
	r.running[ps.ID] = ps
	r.mu.Unlock()

	if ps.bgTimer != nil {
		go r.autoBackground(ps)
	}

	go r.runAgent(ps, ctx, runFunc)

	return ps, nil
}

func (r *Registry) runAgent(ps *ProcessSession, ctx context.Context, runFunc func(ctx context.Context) (string, error)) {
	result, err := runFunc(ctx)

	ps.mu.Lock()
	if err != nil && ctx.Err() == nil {
		ps.Status = "failed"
		ps.Result = fmt.Sprintf("error: %v", err)
	} else if ctx.Err() != nil {
		ps.Status = "killed"
		ps.Result = result
	} else {
		ps.Status = "completed"
		ps.Result = result
	}
	ps.FinishedAt = time.Now()
	ps.mu.Unlock()

	r.finalize(ps)
}

func (r *Registry) autoBackground(ps *ProcessSession) {
	<-ps.bgTimer.C

	ps.mu.Lock()
	if ps.Status != "running" {
		ps.mu.Unlock()
		return
	}
	ps.Status = "backgrounded"
	ps.mu.Unlock()

	r.mu.Lock()
	if _, ok := r.running[ps.ID]; ok {
		delete(r.running, ps.ID)
		r.backgrounded[ps.ID] = ps
	}
	r.mu.Unlock()

	close(ps.bgSignal)
}

func (r *Registry) readOutput(ps *ProcessSession) {
	buf := make([]byte, 4096)
	for {
		n, err := ps.ptmx.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			ps.Output.Write([]byte(chunk))
			if ps.OnChunk != nil {
				ps.OnChunk(chunk)
			}
		}
		if err != nil {
			break
		}
	}

	ps.cmd.Wait()

	ps.mu.Lock()
	if ws, ok := ps.cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		ps.ExitCode = ws.ExitStatus()
	}
	if ps.Status != "killed" {
		ps.Status = "completed"
	}
	ps.FinishedAt = time.Now()
	ps.mu.Unlock()

	r.finalize(ps)
}

func (r *Registry) finalize(ps *ProcessSession) {
	r.mu.Lock()
	delete(r.running, ps.ID)
	delete(r.backgrounded, ps.ID)
	r.finished[ps.ID] = ps
	r.mu.Unlock()

	if ps.ptmx != nil {
		ps.ptmx.Close()
	}

	if ps.bgTimer != nil {
		ps.bgTimer.Stop()
	}

	close(ps.done)

	if ps.OnComplete != nil {
		go ps.OnComplete(ps)
	}
}

func (r *Registry) Write(id, data string) error {
	r.mu.RLock()
	ps, ok := r.running[id]
	if !ok {
		ps, ok = r.backgrounded[id]
	}
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process not found or not running: %s", id)
	}

	if ps.Type != TaskTypeBash {
		return fmt.Errorf("write only supported for bash tasks")
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	_, err := ps.ptmx.WriteString(data)
	return err
}

func (r *Registry) Read(id string) (string, error) {
	ps := r.lookup(id)
	if ps == nil {
		return "", fmt.Errorf("task not found: %s", id)
	}

	if ps.Type == TaskTypeAgent && ps.Result != "" {
		return ps.Result, nil
	}

	return ps.Output.Read(), nil
}

func (r *Registry) Wait(id string, timeout time.Duration) (*ProcessSession, error) {
	ps := r.lookup(id)
	if ps == nil {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	if isFinished(ps) {
		return ps, nil
	}

	if timeout > 0 {
		select {
		case <-time.After(timeout):
			return nil, fmt.Errorf("wait timeout")
		case <-ps.done:
			return ps, nil
		case <-ps.bgSignal:
			return ps, nil
		}
	}

	select {
	case <-ps.done:
		return ps, nil
	case <-ps.bgSignal:
		return ps, nil
	}
}

func (r *Registry) Kill(id string) error {
	r.mu.RLock()
	ps := r.lookupLive(id)
	r.mu.RUnlock()

	if ps == nil {
		return fmt.Errorf("task not found or not running: %s", id)
	}

	if ps.Type == TaskTypeBash && ps.cmd != nil && ps.cmd.Process != nil {
		ps.cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(200 * time.Millisecond)
		if ps.cmd.ProcessState == nil {
			ps.cmd.Process.Kill()
		}
	}

	if ps.cancel != nil {
		ps.cancel()
	}

	ps.mu.Lock()
	ps.Status = "killed"
	ps.FinishedAt = time.Now()
	ps.mu.Unlock()

	r.mu.Lock()
	delete(r.running, id)
	delete(r.backgrounded, id)
	r.finished[id] = ps
	r.mu.Unlock()

	if ps.ptmx != nil {
		ps.ptmx.Close()
	}
	if ps.bgTimer != nil {
		ps.bgTimer.Stop()
	}

	return nil
}

func (r *Registry) List() []*ProcessSession {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ProcessSession, 0, len(r.running)+len(r.backgrounded)+len(r.finished))
	for _, ps := range r.running {
		result = append(result, ps)
	}
	for _, ps := range r.backgrounded {
		result = append(result, ps)
	}
	for _, ps := range r.finished {
		result = append(result, ps)
	}
	return result
}

func (r *Registry) Poll(id string) (*ProcessSession, error) {
	ps := r.lookup(id)
	if ps == nil {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return ps, nil
}

func (r *Registry) Cleanup() {
	r.mu.Lock()
	defer r.mu.Unlock()

	cutoff := time.Now().Add(-FinishedTTL)
	for id, ps := range r.finished {
		if ps.FinishedAt.Before(cutoff) {
			delete(r.finished, id)
		}
	}
}

func KillAll() {
	r := DefaultRegistry
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, ps := range r.running {
		if ps.Type == TaskTypeBash && ps.cmd != nil && ps.cmd.Process != nil {
			ps.cmd.Process.Kill()
		}
		if ps.cancel != nil {
			ps.cancel()
		}
		if ps.ptmx != nil {
			ps.ptmx.Close()
		}
		ps.Status = "killed"
	}
	for _, ps := range r.backgrounded {
		if ps.Type == TaskTypeBash && ps.cmd != nil && ps.cmd.Process != nil {
			ps.cmd.Process.Kill()
		}
		if ps.cancel != nil {
			ps.cancel()
		}
		if ps.ptmx != nil {
			ps.ptmx.Close()
		}
		ps.Status = "killed"
	}
	r.running = make(map[string]*ProcessSession)
	r.backgrounded = make(map[string]*ProcessSession)
}

func (r *Registry) lookup(id string) *ProcessSession {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if ps, ok := r.running[id]; ok {
		return ps
	}
	if ps, ok := r.backgrounded[id]; ok {
		return ps
	}
	if ps, ok := r.finished[id]; ok {
		return ps
	}
	return nil
}

func (r *Registry) lookupLive(id string) *ProcessSession {
	if ps, ok := r.running[id]; ok {
		return ps
	}
	if ps, ok := r.backgrounded[id]; ok {
		return ps
	}
	return nil
}

func isFinished(ps *ProcessSession) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.Status == "completed" || ps.Status == "failed" || ps.Status == "killed"
}
