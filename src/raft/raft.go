package raft

//
// this is an outline of the API that raft must expose to
// the service (or tester). see comments below for
// each of these functions for more details.
//
// rf = Make(...)
//   create a new Raft server.
// rf.Start(command interface{}) (index, term, isleader)
//   start agreement on a new log entry
// rf.GetState() (term, isLeader)
//   ask a Raft for its current term, and whether it thinks it is leader
// ApplyMsg
//   each time a new entry is committed to the log, each Raft peer
//   should send an ApplyMsg to the service (or tester)
//   in the same server.
//

import (
	//	"bytes"
	// "bytes"
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	//	"6.5840/labgob"
	// "6.5840/labgob"
	"6.5840/labrpc"
)

// as each Raft peer becomes aware that successive log entries are
// committed, the peer should send an ApplyMsg to the service (or
// tester) on the same server, via the applyCh passed to Make(). set
// CommandValid to true to indicate that the ApplyMsg contains a newly
// committed log entry.
var registerd = false
const logEnabled = false
//
// in part 3D you'll want to send other kinds of messages (e.g.,
// snapshots) on the applyCh, but set CommandValid to false for these
// other uses.
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 3D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}

type RoleEnum uint8
const (
	FOLLOWER RoleEnum = iota
	CANDIDATE
	LEADER
)

type Role interface {
	role() RoleEnum
	name() string

	closed() bool
	activate()
	process(Event)

	// return false if the role is already stopped.
	stop() bool
}

type Snapshot struct {
	Snapshot []byte
	LastIncludeIndex int
	LastIncludeTerm int
	LastIncludeCmdIndex int
	NoopCount int
}

// A Go object implementing a single Raft peer.
type Raft struct {
	peers     []*labrpc.ClientEnd // RPC end points of all peers
	persister *Persister          // Object to hold this peer's persisted state
	me        int                 // this peer's index into peers[]
	dead      atomic.Bool
	majorN    int

	term int
	voteFor int

	role Role

	logs *Logs

	applyCh chan ApplyMsg

	evCh chan Event
	// the chLock is only used by goroutines, cause they don't 
	// know if they could push events to the channel.
	// the chLock is write locked until the role is activated, 
	// and relocked when the role is stopped again.
	chLock sync.RWMutex

	saveLock sync.Mutex
}

func (rf *Raft) Term() int {
	return rf.term
}

func (rf *Raft) setTerm(term int) {
	// make sure the term increase monotonically.
	if term <= rf.term {
		_, file, line, ok := runtime.Caller(1)
		if ok {
			rf.fatal("[%s/%d] term %d <= rf.term %d", file, line, term, rf.term)
		} else {
			rf.fatal("term <= rf.term")
		}
	}
	rf.term = term
	// erase voteFor information everytime the term is modified.
	rf.voteFor = -1
	rf.persistState()
}

// return currentTerm and whether this server
// believes it is the leader.
func (rf *Raft) GetState() (int, bool) {
	if !rf.dead.Load() {
		ch := make(chan *NodeState, 1)
		var state *NodeState
		for ; state == nil; state = <- ch {
			// rf.chLock.RLock()
			rf.evCh <- &GetStateEvent {
				ch: ch,
			}
			// rf.chLock.RUnlock()
		}
		return state.term, state.isLeader
	}
	return -1, false
}


// restore previously persisted state.
func (rf *Raft) readPersist(data []byte) {
	if data == nil || len(data) < 1 { // bootstrap without any state?
		return
	}
	// Your code here (3C).
	// Example:
	// r := bytes.NewBuffer(data)
	// d := labgob.NewDecoder(r)
	// var xxx
	// var yyy
	// if d.Decode(&xxx) != nil ||
	//    d.Decode(&yyy) != nil {
	//   error...
	// } else {
	//   rf.xxx = xxx
	//   rf.yyy = yyy
	// }
}


// the service says it has created a snapshot that has
// all info up to and including index. this means the
// service no longer needs the log through (and including)
// that index. Raft should now trim its log as much as possible.
func (rf *Raft) Snapshot(index int, snapshot []byte) {
	rf.log("Take snapshot at command index %d", index)
	ch := make(chan bool, 1)
	for ok := false; !ok && !rf.dead.Load(); ok = <- ch {
		rf.chLock.RLock()
		rf.evCh <- &SnapshotEvent {
			index: index,
			snapshot: snapshot,
			ch: ch,
		}
		rf.chLock.RUnlock()
	}
}

func (rf *Raft) RequestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf.log("RequestVote RPC from [%d/%d], last log info = [%d/%d]", args.CandidateID, args.Term, args.LastLogInfo.Index, args.LastLogInfo.Term)

	if !rf.dead.Load() {
		ch := make(chan bool, 1)
		rf.chLock.RLock()
		rf.evCh <- &RequestVoteEvent {
			args: args,
			reply: reply,
			ch: ch,
		}
		rf.log("RequestVote RPC from [%d/%d] received in channel", args.CandidateID, args.Term)
		rf.chLock.RUnlock()
		reply.Responsed = <- ch
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if args.Snapshot != nil {
		rf.log("InstallSnapshot RPC from [%d/%d], LastInclude = [%d/%d]", args.Id, args.Term, args.Snapshot.LastIncludeIndex, args.Snapshot.LastIncludeTerm)
	} else {
		var indices []int
		if args.SendEntries != nil && len(args.SendEntries.Entries) > 0 {
			indices = []int{args.SendEntries.PrevLogInfo.Index+1, args.SendEntries.PrevLogInfo.Index+len(args.SendEntries.Entries)}
		}
		rf.log("AppendEntries RPC from [%d/%d], PrevLogInfo = [%d/%d], LeaderCommit = %d, Entries = %v", args.Id, args.Term, args.SendEntries.PrevLogInfo.Index, args.SendEntries.PrevLogInfo.Term, args.LeaderCommit, indices)
	}

	if !rf.dead.Load() {
		ch := make(chan bool, 1)
		rf.chLock.RLock()
		rf.evCh <- &AppendEntriesEvent {
			args: args,
			reply: reply,
			ch: ch,
		}
		rf.log("AppendEntries from [%d/%d] received in channel", args.Id, args.Term)
		rf.chLock.RUnlock()
		reply.Responsed = <- ch
	}
}

func (rf *Raft) processor() {
	for !rf.dead.Load() {
		ev := <- rf.evCh
		switch ev := ev.(type) {
		case *TransEvent:
			rf.role.activate()

		case *SnapshotEvent:
			if rf.role.closed() {
				ev.ch <- false
			} else {
				rf.logs.takeSnapshot(ev.index, ev.snapshot)
				ev.ch <- true
			}

		default:
			if !rf.role.closed() {
				rf.role.process(ev)
			} else {
				rf.log("Abandoned %s", typeName(ev))
				abandonEv(ev)
			}
		}
	}
}

func (rf *Raft) transRole(f func(Role) Role) {
	if rf.role.stop() {
		rf.role = f(rf.role)
		rf.evCh <- &TransEvent{}
	}
}

// example code to send a RequestVote RPC to a server.
// server is the index of the target server in rf.peers[].
// expects RPC arguments in args.
// fills in *reply with RPC reply, so caller should
// pass &reply.
// the types of the args and reply passed to Call() must be
// the same as the types of the arguments declared in the
// handler function (including whether they are pointers).
//
// The labrpc package simulates a lossy network, in which servers
// may be unreachable, and in which requests and replies may be lost.
// Call() sends a request and waits for a reply. If a reply arrives
// within a timeout interval, Call() returns true; otherwise
// Call() returns false. Thus Call() may not return for a while.
// A false return can be caused by a dead server, a live server that
// can't be reached, a lost request, or a lost reply.
//
// Call() is guaranteed to return (perhaps after a delay) *except* if the
// handler function on the server side does not return.  Thus there
// is no need to implement your own timeouts around Call().
//
// look at the comments in ../labrpc/labrpc.go for more details.
//
// if you're having trouble getting RPC to work, check that you've
// capitalized all field names in structs passed over RPC, and
// that the caller passes the address of the reply struct with &, not
// the struct itself.


// the service using Raft (e.g. a k/v server) wants to start
// agreement on the next command to be appended to Raft's log. if this
// server isn't the leader, returns false. otherwise start the
// agreement and return immediately. there is no guarantee that this
// command will ever be committed to the Raft log, since the leader
// may fail or lose an election. even if the Raft instance has been killed,
// this function should return gracefully.
//
// the first return value is the index that the command will appear at
// if it's ever committed. the second return value is the current
// term. the third return value is true if this server believes it is
// the leader.
func (rf *Raft) Start(command interface{}) (int, int, bool) {
	if !rf.dead.Load() {
		ch := make(chan *StartCommandReply, 1)

		var reply *StartCommandReply
		for ; reply == nil; reply = <- ch {
			rf.chLock.RLock()
			rf.evCh <- &StartCommandEvent {
				command: command,
				ch: ch,
			}
			rf.chLock.RUnlock()
		}
		return reply.index, reply.term, reply.ok
	}
	return -1, -1, false
}

// the tester doesn't halt goroutines created by Raft after each test,
// but it does call the Kill() method. your code can use killed() to
// check whether Kill() has been called. the use of atomic avoids the
// need for a lock.
//
// the issue is that long-running goroutines use memory and may chew
// up CPU time, perhaps causing later tests to fail and generating
// confusing debug output. any goroutine with a long-running loop
// should call killed() to check whether it should stop.
func (rf *Raft) Kill() {
	rf.role.stop()
	rf.dead.Store(true)
	rf.log("Now I'm dead")
	<- rf.logs.applierDone
	rf.log("applier done")
	// Your code here, if desired.
}

func (rf *Raft) killed() bool {
	return rf.dead.Load()
}

func (rf *Raft) ticker() {
	for !rf.killed() {

		// Your code here (3A)
		// Check if a leader election should be started.


		// pause for a random amount of time between 50 and 350
		// milliseconds.
		ms := 50 + (rand.Int63() % 300)
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}

// the service or tester wants to create a Raft server. the ports
// of all the Raft servers (including this one) are in peers[]. this
// server's port is peers[me]. all the servers' peers[] arrays
// have the same order. persister is a place for this server to
// save its persistent state, and also initially holds the most
// recent saved state, if any. applyCh is a channel on which the
// tester or service expects Raft to send ApplyMsg messages.
// Make() must return quickly, so it should start goroutines
// for any long-running work.
func Make(peers []*labrpc.ClientEnd, me int,
	persister *Persister, applyCh chan ApplyMsg) *Raft {
	setupLogger()

	rf := &Raft {
		peers: peers,
		persister: persister,
		me: me,
		evCh: make(chan Event, 1000),
		majorN: len(peers)/2+1,
		applyCh: applyCh,
		term: 0,
		voteFor: -1,
	}

	// read snapshot
	snapshot := rf.persister.ReadSnapshot()

	// read persistent data and init logs
	var logs *Logs
	var state PersistentState
	reader := bytes.NewReader(persister.ReadRaftState())
	dec := gob.NewDecoder(reader)

	switch err := dec.Decode(&state); err {
	case nil:
		logs = newLogs(rf, &state.Logs, snapshot)
		rf.term = state.Term
		rf.voteFor = state.VoteFor

	case io.EOF:
		logs = newLogs(rf, nil, snapshot)

	default:
		log.Fatal(err.Error())
	}

	logs.rf = rf
	rf.logs = logs
	rf.chLock.Lock()
	
	flw := &Follower {
		rf: rf,
	}
	rf.role = flw

	go rf.processor()
	time.Sleep(100 * time.Millisecond)
	rf.evCh <- &TransEvent{}

	return rf
}


func (rf *Raft) tryPutEv(ev Event, role Role) bool {
	if !rf.chLock.TryRLock() {
		return false
	}
	defer rf.chLock.RUnlock()

	if !role.closed() {
		rf.evCh <- ev
		rf.log("Put ev %s", typeName(ev))
		return true
	} 
	return false
}

func (rf *Raft) _log(fn func(string, ...interface{}), format string, args ...interface{}) {
	if logEnabled {
		fn("[%s %d/%d/%d/%d/%d] %s", rf.role.name(), rf.me, rf.term, rf.logs.LLI(), rf.logs.LCI(), len(rf.logs.entries), fmt.Sprintf(format, args...))
	}
}

func (rf *Raft) log(format string, args ...interface{}) {
	rf._log(log.Printf, format, args...)
}
func (rf *Raft) fatal(format string, args ...interface{}) {
	rf._log(log.Fatalf, format, args...)
}

func setupLogger() {
	log.SetFlags(0)
}
