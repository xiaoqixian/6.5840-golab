// Date:   Sat Apr 27 19:57:54 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

type Event interface{
	// abandon the event, the sender should be 
	// prepared for this.
}

func abandonEv(ev Event) {
	switch ev := ev.(type) {
	case *RequestVoteEvent:
		ev.ch <- false
		
	case *AppendEntriesEvent:
		ev.ch <- false

	case *StartCommandEvent:
		ev.ch <- nil

	case *GetStateEvent:
		ev.ch <- nil
	}
}

// == PUBLIC EVNETS ==

// After a role has transformed, the event processor 
// should abandon all events, until a TransEvent occurs. 
// After received TransEvent, the processor should activate 
// the role, so it starts to process events.
type TransEvent struct {}

type RequestVoteEvent struct {
	args *RequestVoteArgs
	reply *RequestVoteReply
	ch chan bool
}

type AppendEntriesEvent struct {
	args *AppendEntriesArgs
	reply *AppendEntriesReply
	ch chan bool
}

type StartCommandReply struct {
	ok bool
	term int
	index int
}
type StartCommandEvent struct {
	command interface {}
	ch chan *StartCommandReply
}

type NodeState struct {
	term int
	isLeader bool
}
type GetStateEvent struct {
	ch chan *NodeState
}

// == FOLLOWER EVENT ==

type HeartBeatTimeoutEvent struct {}

// == CANDIDATE EVNET ==

type ElectionTimeoutEvent struct {}

type VoteGrantEvent struct {
	term int
	voter int
}

type SendHeartBeatEvent struct {}

type StaleCandidateEvent struct {
	newTerm int
}

// == LEADER EVNET ==

type StaleLeaderEvent struct {
	newTerm int
}

type ReplConfirmEvent struct {
	id int
	index int
}
