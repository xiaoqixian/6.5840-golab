// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"sync"
	"sync/atomic"
)

type LogEntry struct {
	Term int
	Content interface{}
}
type Logs struct {
	entries []*LogEntry
	lastCommitedIndex atomic.Int32
	lastAppliedIndex atomic.Int32
	applyCh chan ApplyMsg
	applyCount int
	entryCount int
	sync.RWMutex

	rf *Raft

	applyLock sync.Mutex
}

func newLogs(rf *Raft, applyCh chan ApplyMsg) *Logs {
	logs := &Logs {
		applyCh: applyCh,
		rf: rf,
	}
	logs.lastCommitedIndex.Store(-1)
	logs.lastAppliedIndex.Store(-1)
	return logs
}

func (logs *Logs) lastLogIndex() int {
	logs.RLock()
	defer logs.RUnlock()
	return len(logs.entries)-1
}

func (logs *Logs) indexLogTerm(idx int) int {
	logs.RLock()
	defer logs.RUnlock()

	if idx < 0 || idx >= len(logs.entries) {
		return -1
	} else {
		return logs.entries[idx].Term
	}
}

func (logs *Logs) indexLogEntry(idx int) *LogEntry {
	logs.RLock()
	defer logs.RUnlock()
	return logs.entries[idx]
}

func (logs *Logs) lastCommitedLogIndex() int {
	return int(logs.lastCommitedIndex.Load())
}

func (logs *Logs) lastCommitedLogTerm() int {
	logs.RLock()
	defer logs.RUnlock()
	idx := logs.lastLogIndex()
	return logs.indexLogTerm(idx)
}

// return the index, term of the last log.
func (logs *Logs) lastCommitedLogInfo() (int, int) {
	logs.RLock()
	defer logs.RUnlock()
	idx := int(logs.lastCommitedIndex.Load())
	return idx, logs.indexLogTerm(idx)
}

// return the index of this command and the index of this log.
func (logs *Logs) appendEntry(et *LogEntry) (int, int) {
	logs.Lock()
	defer logs.Unlock()
	logs.entries = append(logs.entries, et)
	logIndex := len(logs.entries)-1

	switch et.Content.(type) {
	case NoopEntry:
		return -1, logIndex
	default:
		logs.entryCount++
		return logs.entryCount, logIndex
	}
}

func (logs *Logs) removeUncommitedTail() {
	logs.Lock()
	defer logs.Unlock()
	lci := int(logs.lastCommitedIndex.Load())
	assert(lci < len(logs.entries))
	logs.entries = logs.entries[:lci+1]
}
func (logs *Logs) updateCommitIndex(leaderCommit int) {
	logs.lastCommitedIndex.Store(int32(minInt(
		logs.lastLogIndex(), leaderCommit)))
}

func (rf *Raft) applyLogs() int {
	// only allow one goroutine applying logs at a time.
	logs := rf.logs
	if !logs.applyLock.TryLock() {
		return 0
	}
	defer logs.applyLock.Unlock()

	ai, ci := logs.lastAppliedIndex.Load(), logs.lastCommitedIndex.Load()
	rf.log("ai = %d, ci = %d", ai, ci)
	assert(ai <= ci)
	
	logs.RLock()
	defer logs.RUnlock()
	for i := ai+1; i <= ci; i++ {
		entry := logs.entries[i]
		switch entry.Content.(type) {
		case NoopEntry:
			logs.rf.log("Found a NoopEntry with index %d", i)
		default:
			logs.applyCount++
			logs.applyCh <- ApplyMsg {
				CommandValid: true,
				Command: entry.Content,
				CommandIndex: logs.applyCount,
			}
			logs.rf.log("Applied ApplyMsg with index %d", logs.applyCount)
		}
	}

	logs.lastAppliedIndex.Store(ci)
	
	return int(ci - ai)
}
