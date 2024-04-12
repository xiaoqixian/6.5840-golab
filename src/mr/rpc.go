package mr

//
// RPC definitions.
//
// remember to capitalize all names.
//

import "os"
import "strconv"

type RpcType uint8
const (
	REGISTER_WORKER RpcType = iota
	GET_TASK
	FINISH_MAPPING
	FINISH_REDUCING
)

func (r RpcType) String() string {
	switch r {
	case REGISTER_WORKER:
		return "REGISTER_WORKER"
	case GET_TASK:
		return "GET_TASK"
	case FINISH_MAPPING:
		return "FINISH_MAPPING"
	case FINISH_REDUCING:
		return "FINISH_REDUCING"
	default:
		return "UNKNOWN"
	}
}

// RPC argument interface
type RpcArg interface {
	RpcType() RpcType
}

type RegisterWorkerArg struct {}

type GetTaskArg struct {
	WorkerID WorkerID
}

type FinishTaskArg struct {
	WorkerID WorkerID
}

type HeartBeatArg struct {
	WorkerID WorkerID
}

// RPC reply interface
type RpcReply interface {}

// inform the worker that it is invalid, 
// because it do not register or heartbeat
// expired.
// The worker should re-register itself after
// receiving this reply.
type InvalidWorker struct {}

// inform the worker that the argument type is invalid
type InvalidArgType struct {
	Hint string
}

type TaskRpc interface {}
type MapTaskRpc struct {
	File string
}
type ReduceTaskRpc struct {
	ReduceID ReduceID
}
// inform the worker there is no task 
// temporarily, just sleep for a while and 
// ask again later.
type NoopTaskRpc struct {}

type NoMoreTaskRpc struct {}

// inform the worker the RPC is well accepted
type OkReply struct {}


// Cook up a unique-ish UNIX-domain socket name
// in /var/tmp, for the coordinator.
// Can't use the current directory since
// Athena AFS doesn't support UNIX-domain sockets.
func coordinatorSock() string {
	s := "/var/tmp/5840-mr-"
	s += strconv.Itoa(os.Getuid())
	return s
}
