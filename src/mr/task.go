// Date:   Thu Apr 11 17:53:04 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package mr

import (
	"log"
	"sync"
)

type TaskEnum uint8
const (
	MAP_TASK TaskEnum = iota
	REDUCE_TASK
)

type TaskStatus uint8
const (
	TASK_TODO TaskStatus = iota
	TASK_PROCESSING
	TASK_DONE
)

// Task type interface
// should be a superset of RPCReply
type TaskType interface {
	Type() TaskEnum
	Status() TaskStatus
	SetStatus(TaskStatus)
	Mutex() *sync.Mutex

	// every TaskType carryies a link to its 
	// container, so it can release itself to 
	// the container.
	Container() *TaskContainer
}

type MapTask struct {
	file string
	status TaskStatus
	lock sync.Mutex
	container *TaskContainer
}

type ReduceTask struct {
	reduceID ReduceID
	status TaskStatus
	lock sync.Mutex
	container *TaskContainer
}

func (*MapTask) Type() TaskEnum {
	return MAP_TASK
}
func (mt *MapTask) Status() TaskStatus {
	return mt.status
}
func (mt *MapTask) SetStatus(status TaskStatus) {
	mt.status = status
}
func (mt *MapTask) Mutex() *sync.Mutex {
	return &mt.lock
}
func (mt *MapTask) Container() *TaskContainer {
	return mt.container
}

func (*ReduceTask) Type() TaskEnum {
	return REDUCE_TASK
}
func (rt *ReduceTask) Status() TaskStatus {
	return rt.status
}
func (rt *ReduceTask) SetStatus(status TaskStatus) {
	rt.status = status
}
func (rt *ReduceTask) Mutex() *sync.Mutex {
	return &rt.lock
}
func (rt *ReduceTask) Container() *TaskContainer {
	return rt.container
}

func taskToRpc(task TaskType) TaskRpc {
	switch task.Type() {
	case MAP_TASK:
		return &MapTaskRpc {
			File: task.(*MapTask).file,
		}
	case REDUCE_TASK:
		return &ReduceTaskRpc {
			ReduceID: task.(*ReduceTask).reduceID,
		}

	default:
		return nil
	}
}

func releaseTask(task TaskType) {
	if task.Container() == nil {
		log.Fatal("This task does not link to any TaskContainer")
	}
	task.Container().AddTask(task)
}

// A thread safe task container
// task is indexed by WorkID
type TaskContainer struct {
	tasks []TaskType
	lock sync.RWMutex
}

func (tc *TaskContainer) AddTask(task TaskType) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	tc.tasks = append(tc.tasks, task)
}

func (tc *TaskContainer) TakeTask() TaskType {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	if len(tc.tasks) == 0 {
		return nil
	}
	res := tc.tasks[0]
	tc.tasks = tc.tasks[1:]
	return res
}

func (tc *TaskContainer) releaseMapTasks(files []string) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	for _, f := range files {
		tc.tasks = append(tc.tasks, &MapTask {
			file: f,
			status: TASK_TODO,
			container: tc,
		})
	}
}

func (tc *TaskContainer) releaseReduceTasks(nReduce uint16) {
	tc.lock.Lock()
	defer tc.lock.Unlock()

	for i := uint16(0); i < nReduce; i++ {
		tc.tasks = append(tc.tasks, &ReduceTask {
			reduceID: ReduceID(i),
			status: TASK_TODO,
			container: tc,
		})
	}
}
