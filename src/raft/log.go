// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "sync"

type LogEntry struct {
	Term int
	Content []byte
}
type Logs struct {
	entries []*LogEntry
	sync.RWMutex
}

func (logs *Logs) lastLogIndex() int {
	logs.RLock()
	defer logs.RUnlock()
	return len(logs.entries)
}

func (logs *Logs) lastLogTerm() int {
	logs.RLock()
	defer logs.RUnlock()
	if len(logs.entries) == 0 {
		return 0
	} else {
		return logs.entries[len(logs.entries)-1].Term
	}
}

func (logs *Logs) appendEntries(ets []*LogEntry) {
	logs.Lock()
	defer logs.Unlock()
	logs.entries = append(logs.entries, ets...)
}
