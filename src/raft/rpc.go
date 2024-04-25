// Date:   Wed Apr 17 09:41:54 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

type AppendEntriesArgs struct {
	Id int
	Term int

	PrevLogIndex int
	PrevLogTerm int

	LeaderCommit int

	Entries *LogEntry
}

type AppendEntriesReply struct {
	Success bool
	Term int // for leader to update itself.
	Received bool // indicate that the follower confirm this RPC.
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

type NoopEntry struct {}

// example RequestVote RPC reply structure.
// field names must start with capital letters!
type RequestVoteReply struct {
	Term int // for candidate to update itself
	VoterID int
	VoteStatus VoteStatus
}
