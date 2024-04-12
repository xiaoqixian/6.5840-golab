package mr

import (
	"encoding/gob"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type StageEnum uint16
type FileStatus uint8
type ReduceIDStatus uint8
type WorkerStatus uint8

type WorkerID uint16
type ReduceID uint16

const (
	MAPPING StageEnum = 1 << iota
	MAP_WAITING
	REDUCING
	REDUCE_WAITING
	DONE
)

const (
	FILE_TODO FileStatus = iota
	FILE_PROCESSING
	FILE_DONE
)
const (
	REDUCE_TODO ReduceIDStatus = iota
	REDUCE_REDUCING
	REDUCE_DONE
)
const (
	WORKER_UNKNOWN WorkerStatus = iota
	WORKER_WORKING
	WORKER_EXPIRED
)

type MapStatus struct {
	lock sync.Mutex
}
type ReduceStatus struct {
	reduceIDStatus ReduceIDStatus 
	lock sync.Mutex
}

type Coordinator struct {
	// Your definitions here.
	files []string

	unfinishedMappingCount uint16
	fileMappingStatus map[string]MapStatus

	workingStage StageEnum
	stageLock sync.Mutex

	taskContainer *TaskContainer

	nReduce uint16
	reducingStatus map[ReduceID]ReduceStatus
	unfinishedReducingCount uint16

	// worker information
	workerIDAcc atomic.Uint32
	taskWaitingDuration time.Duration
	heartBeatDuration time.Duration

	workers *WorkerGroup
}

func (c *Coordinator) RegisterWorker(arg RegisterWorkerArg, reply *RpcReply) error {
	newWorkerID := WorkerID(c.workerIDAcc.Add(1))
	log.Printf("Get a RegisterWorker request, newWorkerID: %d", newWorkerID)

	*reply = &WorkerInfo {
		WorkerID: newWorkerID,
		R: c.nReduce,
		TaskWaitingDuration: c.taskWaitingDuration,
		HeartBeatDuration: c.heartBeatDuration,
	}

	c.workers.addWorker(newWorkerID)
	return nil
}

func (c *Coordinator) GetTask(arg GetTaskArg, reply *RpcReply) error {
	log.Printf("Get a GetTask request from worker %d\n", arg.WorkerID)

	c.stageLock.Lock()
	if c.workingStage == DONE {
		c.stageLock.Unlock()
		*reply = &NoMoreTaskRpc{}
		return nil
	}
	c.stageLock.Unlock()

	cwoker := c.workers.getLockedWorker(arg.WorkerID)
	if cwoker == nil {
		log.Printf("Worker %d is invalid\n", arg.WorkerID)
		*reply = &InvalidWorker {}
		return nil
	}
	defer cwoker.lock.Unlock()
	
	if cwoker.task != nil {
		panic(fmt.Sprintf("Worker %d request GetTask while carring a task", arg.WorkerID))
	}

	newTask := c.taskContainer.TakeTask()
	if newTask == nil {
		*reply = &NoopTaskRpc {}
		return nil
	}

	cwoker.task = newTask
	*reply = taskToRpc(newTask)
	return nil
}

func (c *Coordinator) FinishTask(arg FinishTaskArg, reply *RpcReply) error {
	log.Printf("Get a FinishTask request from worker %d\n", arg.WorkerID)

	cworker := c.workers.getLockedWorker(arg.WorkerID)
	if cworker == nil {
		log.Printf("Worker %d is invalid\n", arg.WorkerID)
		*reply = &InvalidWorker{}
		return nil
	}
	defer cworker.lock.Unlock()

	if cworker.task == nil {
		panic(fmt.Sprintf("Received a FinishTask RPC from a worker with no task attached, workerID: %d", cworker.id))
	}

	c.stageLock.Lock()
	switch cworker.task.Type() {
	case MAP_TASK:
		if c.workingStage & (MAPPING | MAP_WAITING) == 0 {
			panic(fmt.Sprintf("Receive FinishMapTask on a mismatched working stage: %d", c.workingStage))
		}

		c.unfinishedMappingCount--
		if c.unfinishedMappingCount == 0 {
			c.taskContainer.releaseReduceTasks(c.nReduce)
			c.workingStage = REDUCING
		}

	case REDUCE_TASK:
		if c.workingStage & (REDUCING | REDUCE_WAITING) == 0 {
			panic(fmt.Sprintf("Receive FinishReduceTask on a mismatched working stage: %d", c.workingStage))
		}

		c.unfinishedReducingCount--
		if c.unfinishedReducingCount == 0 {
			c.workingStage = DONE
		}
	}
	c.stageLock.Unlock()

	// set cworker.task to nil to represent the worker is 
	// not processing any task now.
	cworker.task = nil

	*reply = &OkReply{}
	return nil
}

func (c *Coordinator) HeartBeat(arg HeartBeatArg, reply *RpcReply) error {
	if c.workers.tickWorker(arg.WorkerID) {
		*reply = &OkReply{}
	} else {
		*reply = &InvalidWorker{}
	}
	return nil
}

// As all tasks are represented in TaskType, 
// their actual types need to be registered, 
// as golang rpc package required.
func RegisterTasks() {
	// Replies
	gob.Register(&WorkerInfo{})
	gob.Register(&MapTaskRpc{})
	gob.Register(&ReduceTaskRpc{})
	gob.Register(&NoMoreTaskRpc{})
	gob.Register(&NoopTaskRpc{})
	gob.Register(&OkReply{})
	gob.Register(&InvalidArgType{})
	gob.Register(&InvalidWorker{})

	// Arguments
	gob.Register(RegisterWorkerArg{})
	gob.Register(GetTaskArg{})
	gob.Register(FinishTaskArg{})
	gob.Register(HeartBeatArg{})

}

//
// start a thread that listens for RPCs from worker.go
//
func (c *Coordinator) serve() {
	// register publishes the receiver's methods.
	rpc.Register(c)
	// register a HTTP handler for rpc messages.
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := coordinatorSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go http.Serve(l, nil)
}

//
// main/mrcoordinator.go calls Done() periodically to find out
// if the entire job has finished.
//
func (c *Coordinator) Done() bool {
	c.stageLock.Lock()
	defer c.stageLock.Unlock()
	return c.workingStage == DONE
}

//
// create a Coordinator.
// main/mrcoordinator.go calls this function.
// nReduce is the number of reduce tasks to use.
//
func MakeCoordinator(files []string, nReduce int) *Coordinator {
	RegisterTasks()

	taskWaitingDuration := time.Second * 2
	heartBeatDuration := time.Second

	c := Coordinator{
		files: files,
		unfinishedMappingCount: uint16(len(files)),
		workingStage: MAPPING,
		nReduce: uint16(nReduce),
		unfinishedReducingCount: uint16(nReduce),
		taskWaitingDuration: taskWaitingDuration,
		heartBeatDuration: heartBeatDuration,

		workers: &WorkerGroup {
			workers: make(map[WorkerID]*CWorker),
			expireDuration: heartBeatDuration * 2,
		},

		taskContainer: &TaskContainer {},
	}

	SetupLogger("/Users/lunar/pros/golab/src/main/logs/log-coordinator")
	
	c.taskContainer.releaseMapTasks(c.files)
	c.serve()
	log.Println("Coordinator started")
	return &c
}
