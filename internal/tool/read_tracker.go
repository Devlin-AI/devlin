package tool

import (
	"container/list"
	"os"
	"sync"
)

type readEntry struct {
	path  string
	mtime int64
}

type readTracker struct {
	mu    sync.Mutex
	cap   int
	order *list.List
	items map[string]*list.Element
}

var tracker = &readTracker{
	cap:   100,
	order: list.New(),
	items: make(map[string]*list.Element),
}

func (t *readTracker) Store(fp string, mtime int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if elem, ok := t.items[fp]; ok {
		t.order.MoveToFront(elem)
		elem.Value.(*readEntry).mtime = mtime
		return
	}

	for len(t.items) >= t.cap {
		oldest := t.order.Back()
		if oldest == nil {
			break
		}
		t.order.Remove(oldest)
		delete(t.items, oldest.Value.(*readEntry).path)
	}

	entry := &readEntry{path: fp, mtime: mtime}
	elem := t.order.PushFront(entry)
	t.items[fp] = elem
}

func (t *readTracker) Check(fp string) (read bool, stale bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	elem, ok := t.items[fp]
	if !ok {
		return false, false
	}

	t.order.MoveToFront(elem)
	entry := elem.Value.(*readEntry)

	stat, err := os.Stat(fp)
	if err != nil {
		return true, false
	}

	currentMtime := stat.ModTime().Unix()
	if currentMtime != entry.mtime {
		return true, true
	}

	return true, false
}

func (t *readTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.order.Init()
	t.items = make(map[string]*list.Element)
}
