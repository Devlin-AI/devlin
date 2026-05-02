package process

import (
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
	MaxConcurrentProcesses = 64
	FinishedTTL            = 30 * time.Minute
)

type ProcessSession struct {
	ID         string
	Command    string
	PID        int
	Status     string
	ExitCode   int
	Output     *RollingBuffer
	CreatedAt  time.Time
	FinishedAt time.Time
	OnChunk    func(string)
	mu         sync.Mutex
	cmd        *exec.Cmd
	ptmx       *os.File
	done       chan struct{}
}

type Registry struct {
	running  map[string]*ProcessSession
	finished map[string]*ProcessSession
	mu       sync.RWMutex
}

var DefaultRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{
		running:  make(map[string]*ProcessSession),
		finished: make(map[string]*ProcessSession),
	}
}

func (r *Registry) Spawn(command string, onChunk func(string)) (*ProcessSession, error) {
	r.mu.Lock()
	if len(r.running) >= MaxConcurrentProcesses {
		r.mu.Unlock()
		return nil, fmt.Errorf("max concurrent processes (%d) reached", MaxConcurrentProcesses)
	}
	r.mu.Unlock()

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
		Command:   command,
		PID:       cmd.Process.Pid,
		Status:    "running",
		Output:    NewRollingBuffer(0),
		CreatedAt: time.Now(),
		OnChunk:   onChunk,
		cmd:       cmd,
		ptmx:      ptmx,
		done:      make(chan struct{}),
	}

	r.mu.Lock()
	r.running[ps.ID] = ps
	r.mu.Unlock()

	go r.readOutput(ps)

	return ps, nil
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
	ps.Status = "exited"
	ps.FinishedAt = time.Now()
	ps.mu.Unlock()

	r.mu.Lock()
	if _, ok := r.running[ps.ID]; ok {
		delete(r.running, ps.ID)
		r.finished[ps.ID] = ps
	}
	r.mu.Unlock()

	ps.ptmx.Close()
	close(ps.done)
}

func (r *Registry) Write(id, data string) error {
	r.mu.RLock()
	ps, ok := r.running[id]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process not found or not running: %s", id)
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	_, err := ps.ptmx.WriteString(data)
	return err
}

func (r *Registry) Read(id string) (string, error) {
	r.mu.RLock()
	ps, ok := r.running[id]
	r.mu.RUnlock()

	if !ok {
		r.mu.RLock()
		ps, ok = r.finished[id]
		r.mu.RUnlock()
		if !ok {
			return "", fmt.Errorf("process not found: %s", id)
		}
	}

	return ps.Output.Read(), nil
}

func (r *Registry) Wait(id string, timeout time.Duration) (*ProcessSession, error) {
	r.mu.RLock()
	ps, ok := r.running[id]
	r.mu.RUnlock()

	if !ok {
		r.mu.RLock()
		ps, ok = r.finished[id]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("process not found: %s", id)
		}
		return ps, nil
	}

	if timeout > 0 {
		select {
		case <-time.After(timeout):
			return nil, fmt.Errorf("wait timeout")
		case <-ps.done:
			return ps, nil
		}
	}

	<-ps.done
	return ps, nil
}

func (r *Registry) Kill(id string) error {
	r.mu.RLock()
	ps, ok := r.running[id]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("process not found or not running: %s", id)
	}

	ps.cmd.Process.Signal(syscall.SIGTERM)

	time.Sleep(200 * time.Millisecond)

	if ps.cmd.ProcessState == nil {
		ps.cmd.Process.Kill()
	}

	r.mu.Lock()
	if _, ok := r.running[id]; ok {
		delete(r.running, id)
		ps.Status = "killed"
		ps.FinishedAt = time.Now()
		r.finished[id] = ps
	}
	r.mu.Unlock()

	return nil
}

func (r *Registry) List() []*ProcessSession {
	r.mu.Lock()
	defer r.mu.Unlock()

	result := make([]*ProcessSession, 0, len(r.running)+len(r.finished))
	for _, ps := range r.running {
		result = append(result, ps)
	}
	for _, ps := range r.finished {
		result = append(result, ps)
	}
	return result
}

func (r *Registry) Poll(id string) (*ProcessSession, error) {
	r.mu.RLock()
	ps, ok := r.running[id]
	r.mu.RUnlock()

	if !ok {
		r.mu.RLock()
		ps, ok = r.finished[id]
		r.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("process not found: %s", id)
		}
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
		ps.cmd.Process.Kill()
		ps.ptmx.Close()
		ps.Status = "killed"
	}
	r.running = make(map[string]*ProcessSession)
}
