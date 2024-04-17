// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "sync"

type LogEntry struct {
	term int
	content []byte
}
type Logs struct {
	entries []*LogEntry
	sync.RWMutex
}

func (logs *Logs) lastLogIndex() int {
	logs.RLock()
	defer logs.RUnlock()
	return len(logs.entries)-1
}

func (logs *Logs) lastLogTerm() int {
	logs.RLock()
	defer logs.RUnlock()
	return logs.entries[len(logs.entries)-1].term
}

func (logs *Logs) appendEntries(ets []*LogEntry) {
	logs.Lock()
	defer logs.Unlock()
	logs.entries = append(logs.entries, ets...)
}
