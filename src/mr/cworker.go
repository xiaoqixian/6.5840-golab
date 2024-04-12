// Date:   Thu Apr 11 14:42:49 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package mr

import (
	"sync"
	"time"
)

// Coordinatro Worker
// the lock enables only one thread can
// access the CWorker at a time, to prevent
// race between Coordinator and the timer.
type CWorker struct {
	lock sync.Mutex
	task TaskType
	id WorkerID
	timer *time.Timer
}

func (cw *CWorker) release() {
	if cw.task != nil {
		releaseTask(cw.task)
	}
	cw.task = nil
}

type WorkerGroup struct {
	workers map[WorkerID]*CWorker
	groupLock sync.RWMutex

	expireDuration time.Duration
}

func (wg *WorkerGroup) addWorker(id WorkerID) {
	wg.groupLock.Lock()
	defer wg.groupLock.Unlock()
	wg.workers[id] = &CWorker {
		id: id,
		timer: time.AfterFunc(wg.expireDuration, func() {
			wg.removeWorker(id)
		}),
	}
}

func (wg *WorkerGroup) removeWorker(id WorkerID) {
	wg.groupLock.Lock()
	defer wg.groupLock.Unlock()

	cw, ok := wg.workers[id]
	if !ok { return }
	
	cw.lock.Lock()
	cw.release() // important, release resources hold by the worker.
	delete(wg.workers, id)
}

func (wg *WorkerGroup) hasWorker(id WorkerID) bool {
	wg.groupLock.RLock()
	defer wg.groupLock.RUnlock()
	_, ok := wg.workers[id]
	return ok
}

func (wg *WorkerGroup) getLockedWorker(id WorkerID) *CWorker {
	wg.groupLock.RLock()
	defer wg.groupLock.RUnlock()

	cw, ok := wg.workers[id]
	if !ok { return nil }

	cw.lock.Lock()
	cw.timer.Reset(wg.expireDuration)
	return cw
}

func (wg *WorkerGroup) tickWorker(id WorkerID) bool {
	wg.groupLock.RLock()
	defer wg.groupLock.RUnlock()

	cw, ok := wg.workers[id]
	if !ok { return false }

	cw.lock.Lock()
	defer cw.lock.Unlock()
	cw.timer.Reset(wg.expireDuration)
	return true
}

func (wg *WorkerGroup) lockWorker(id WorkerID) bool {
	wg.groupLock.RLock()
	defer wg.groupLock.RUnlock()

	w, ok := wg.workers[id]
	if !ok { return false }
	
	w.lock.Lock()
	return true
}

func (wg *WorkerGroup) unlockWorker(id WorkerID) bool {
	wg.groupLock.RLock()
	defer wg.groupLock.RUnlock()

	w, ok := wg.workers[id]
	if !ok { return false }

	w.lock.Unlock()
	return true
}
