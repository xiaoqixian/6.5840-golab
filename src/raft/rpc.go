// Date:   Wed Apr 17 09:41:54 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

type AppendEntriesArgs struct {
	Id int
	Term int
	Entries []*LogEntry
}

type AppendEntriesReply struct {
	Id int
	Accepted bool
}

// example RequestVote RPC arguments structure.
// field names must start with capital letters!
type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term int
	CandidateID int
	LastLogIndex int
	LastLogTerm int
}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	Term int // for candidate to update itself
	VoterID int
	VoteStatus VoteStatus
}
