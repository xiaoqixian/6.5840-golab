// Date:   Wed Apr 17 09:41:54 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

type SendEntries struct {
	Entries []LogEntry
	PrevLogInfo LogInfo
}

type AppendEntriesArgs struct {
	Id int
	Term int

	LeaderCommit int

	EntryType EntryType

	// If Snapshot != nil, this is a snapshot AppendEntries RPC.
	Snapshot *Snapshot
	SendEntries *SendEntries
}

type AppendEntriesReply struct {
	EntryStatus EntryStatus
	// for leader to update itself.
	Term int
	// indicate that the RPC request is explicitly 
	// processed by the remote.
	Responsed bool
}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term int
	CandidateID int
	LastLogInfo LogInfo
}

type NoopEntry struct {}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	Term int // for candidate to update itself
	VoterID int
	VoteStatus VoteStatus
	Responsed bool
}

type InstallSnapshotArgs struct {
	LastIncludeIndex int
	Snapshot []byte
}

type InstallSnapshotReply struct {
	Responsed bool
	Hold bool
}
