// Date:   Fri May 03 12:48:36 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"bytes"
	"encoding/gob"
	"log"
)

type RolePersist interface {}

type PersistentLogs struct {
	Entries []LogEntry
	Lci int32
	Lli int32
	Lai int32
	NoopCount int
	Offset int
	
	LastIncludeIndex int
	LastIncludeTerm int
	SnapshotNoopCount int
}

type PersistentState struct {
	Term int
	Logs PersistentLogs
}

// this function should be called when 
// the Logs is locked.
func (rf *Raft) encodeState() []byte {
	logs := rf.logs
	state := PersistentState { 
		Term: rf.term,
		Logs: PersistentLogs {
			Entries: logs.entries,
			Lci: logs.lci.Load(),
			Lli: logs.lli.Load(),
			Lai: logs.lai.Load(),
			NoopCount: logs.noopCount,
			Offset: logs.offset,
		},
	}

	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(state)
	if err != nil {
		log.Fatal(err.Error())
	}
	return buf.Bytes()
}

// this function should be called when 
// the Logs is locked.
func (rf *Raft) encodeSnapshot() []byte {
	// the Logs persist snapshot everytime it 
	// update its snapshot.
	// The persistence should be done before the 
	// write lock is released. 
	// So we don't need to acquire Logs.Lock() here

	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(rf.logs.snapshot)
	if err != nil {
		rf.fatal("Encode logs snapshot failed: %s", err.Error())
	}
	
	return buf.Bytes()
}

func (rf *Raft) persistState() {
	// if !rf.saveLock.TryLock() { return }
	rf.saveLock.Lock()
	defer rf.saveLock.Unlock()

	rf.persister.SaveState(rf.encodeState())
}

func (rf *Raft) persistSnapshot() {
	rf.saveLock.Lock()
	defer rf.saveLock.Unlock()

	rf.persister.SaveSnapshot(rf.encodeSnapshot())
}

func (rf *Raft) persist() {
	rf.saveLock.Lock()
	defer rf.saveLock.Unlock()

	rf.persister.Save(rf.encodeState(), rf.encodeSnapshot())
}
