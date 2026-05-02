package process

import (
	"bytes"
	"sync"
)

const (
	DefaultMaxSize = 200 * 1024
	HeadRatio      = 0.4
)

type RollingBuffer struct {
	mu       sync.Mutex
	data     bytes.Buffer
	maxSize  int
	headSize int
}

func NewRollingBuffer(maxSize int) *RollingBuffer {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	headSize := int(float64(maxSize) * HeadRatio)
	return &RollingBuffer{
		maxSize:  maxSize,
		headSize: headSize,
	}
}

func (r *RollingBuffer) Write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.data.Write(p)

	if r.data.Len() > r.maxSize {
		r.truncate()
	}
}

func (r *RollingBuffer) truncate() {
	content := r.data.Bytes()
	totalLen := len(content)

	headLen := r.headSize
	tailLen := totalLen - headLen
	if tailLen < 0 {
		tailLen = 0
		headLen = totalLen
	}

	var buf bytes.Buffer
	buf.Grow(headLen + 45 + tailLen)

	buf.Write(content[:headLen])
	buf.WriteString("\n... [output truncated] ...\n")
	buf.Write(content[headLen:])

	r.data.Reset()
	r.data.Write(buf.Bytes())
}

func (r *RollingBuffer) Read() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.String()
}

func (r *RollingBuffer) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Len()
}
