package mr

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"net/rpc"
	"os"
	"regexp"
	"sort"
	"sync/atomic"
	"time"
)

//
// Map functions return a slice of KeyValue.
//
type KeyValue struct {
	Key   string
	Value string
}
type ByKey []KeyValue

// for sorting by key.
func (a ByKey) Len() int           { return len(a) }
func (a ByKey) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByKey) Less(i, j int) bool { return a[i].Key < a[j].Key }

type FileEncoder struct {
	file *os.File
	encoder *json.Encoder
}

type MapFunc func(string, string) []KeyValue
type ReduceFunc func(string, []string) string

type WorkerInfo struct {
	WorkerID WorkerID
	R uint16
	TaskWaitingDuration time.Duration
	HeartBeatDuration time.Duration
}

type MRWorker struct {
	workerInfo *WorkerInfo
	mapf MapFunc
	reducef ReduceFunc
	hearBeatTimer *RepeatTimer
}

type WorkerResult uint8
const (
	WORKER_RESTAET WorkerResult = iota
	WORKER_DONE
)

type RepeatTimer struct {
	ticker *time.Ticker
	f func()
	stopped atomic.Bool
}

func (rt *RepeatTimer) stop() {
	rt.ticker.Stop()
	rt.stopped.Store(true)
}
func (rt *RepeatTimer) reset(d time.Duration) {
	rt.ticker.Reset(d)
	rt.stopped.Store(false)
}

func newRepeatTimer(d time.Duration, f func()) *RepeatTimer {
	repeatTimer := &RepeatTimer {
		ticker: time.NewTicker(d),
		f: f,
	}

	go func() {
		for !repeatTimer.stopped.Load() {
			<- repeatTimer.ticker.C
			repeatTimer.f()
		}
	}()
	return repeatTimer
}

//
// use ihash(key) % NReduce to choose the reduce
// task number for each KeyValue emitted by Map.
//
func ihash(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}

// func bufReadFile(file *os.File, buf []byte) (int, error) {
// 	buf_io := bufio.NewReader(file)
// }

func createOrOpenFile(filename string) *os.File {
	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			file, err = os.Create(filename)
		}

		if err != nil {
			log.Fatalf("createOrOpenFile: %s", err.Error())
		}
	}
	return file
}

func createOrRemoveOld(filename string) *os.File {
	file, err := os.Create(filename)
	if err != nil {
		if os.IsExist(err) {
			err = os.Remove(filename)
		}

		if err != nil {
			panic(fmt.Sprintf("createOrRemoveOld %s failed", filename))
		} else {
			file, err = os.Create(filename)
			if err != nil {
				panic(fmt.Sprintf("createOrRemoveOld %s failed", filename))
			}
		}
	}
	return file
}

func (worker *MRWorker) finishedMapping(mapTask *MapTaskRpc) {
	log.Printf("Worker %d finished mapping on file %s\n", worker.workerInfo.WorkerID, mapTask.File)

	arg := FinishTaskArg {
		WorkerID: worker.workerInfo.WorkerID,
	}
	var reply RpcReply
	ok := call("Coordinator.FinishTask", arg, &reply)
	if !ok {
		log.Fatal("call FinishedMapping failed")
	}
	log.Printf("FinishedMapping RPC reply type: %T\n", reply)
}

func (worker *MRWorker) finishedReducing(reduceTask *ReduceTaskRpc) {
	log.Printf("Worker %d finished reducing on ID %d\n", worker.workerInfo.WorkerID, reduceTask.ReduceID)

	arg := FinishTaskArg {
		WorkerID: worker.workerInfo.WorkerID,
	}

	var reply RpcReply
	ok := call("Coordinator.FinishTask", arg, &reply)
	if !ok {
		log.Fatal("call FinishedReducing failed")
	}
	log.Printf("FinishedReducing RPC reply type: %T\n", reply)
}

func (worker *MRWorker) doMapWork(mapTask *MapTaskRpc) {
	log.Printf("Worker %d do map work on file %s\n", worker.workerInfo.WorkerID, mapTask.File)

	filesToClose := []*os.File {}
	defer func() {
		for _, f := range filesToClose {
			if f != nil {
				f.Close()
			}
		}
	}()

	inputFile, err := os.Open(mapTask.File)
	if err != nil {
		log.Fatalf("cannot open %v", mapTask.File)
	}
	filesToClose = append(filesToClose, inputFile)

	content, err := io.ReadAll(inputFile)
	if err != nil {
		log.Fatalf("cannot read %v", mapTask.File)
	}
	kva := worker.mapf(mapTask.File, string(content))

	reduceList := make([][]KeyValue, worker.workerInfo.R)
	
	for _, kv := range kva {
		reduceID := uint16(ihash(kv.Key) % int(worker.workerInfo.R))
		reduceList[reduceID] = append(reduceList[reduceID], kv)
	}

	for rid := uint16(0); rid < worker.workerInfo.R; rid++ {
		midFileName := fmt.Sprintf("mr-%d-%d", worker.workerInfo.WorkerID, rid)
		// midFile := createOrOpenFile(midFileName)
		midFile, err := os.OpenFile(midFileName, os.O_APPEND | os.O_WRONLY, os.ModeAppend)
		if err != nil { panic(err.Error()) }
		enc := json.NewEncoder(midFile)

		for _, kv := range reduceList[rid] {
			err = enc.Encode(kv)
			if err != nil { panic(err.Error()) }
		}
	}

	worker.finishedMapping(mapTask)
}

func (worker *MRWorker) doReduceTask(reduceTask *ReduceTaskRpc) {
	log.Printf("Worker %d do reduce work on ReduceID %d\n", worker.workerInfo.WorkerID, reduceTask.ReduceID)

	validReduceFile := regexp.MustCompile(fmt.Sprintf(`^mr-\d+-%d$`, reduceTask.ReduceID))

	var filesToClose []*os.File

	defer func() {
		for _, f := range filesToClose {
			if f != nil {
				f.Close()
			}
		}
	}()

	curDir, err := os.Open(".")
	if err != nil {
		log.Fatalf("open . failed")
	}
	filesToClose = append(filesToClose, curDir)

	fileList, err := curDir.ReadDir(0)
	if err != nil {
		log.Fatalf("List curDir failed")
	}

	var intermediate []KeyValue
	for _, fileInfo := range fileList {
		if fileInfo.IsDir() || !validReduceFile.MatchString(fileInfo.Name()) {
			continue
		}

		log.Printf("Reduce intermediate file %s\n", fileInfo.Name())
		file, err := os.Open(fileInfo.Name())
		if err != nil {
			log.Fatalf("open %s failed", fileInfo.Name())
		}
		filesToClose = append(filesToClose, file)

		var kv KeyValue
		dec := json.NewDecoder(file)
		
		for dec.More() {
			err = dec.Decode(&kv)
			if err != nil {
				log.Fatalf("Parse %s failed", fileInfo.Name())
			}
			intermediate = append(intermediate, kv)
		}
	}

	sort.Sort(ByKey(intermediate))

	reduceOutFileName := fmt.Sprintf("mr-out-%d", reduceTask.ReduceID)
	reduceOutFile, err := os.Create(reduceOutFileName)
	if err != nil {
		log.Fatalf("create %s failed: %s", reduceOutFileName, err.Error())
	}

	filesToClose = append(filesToClose, reduceOutFile)

	for i := 0; i < len(intermediate); {
		reduceKey := intermediate[i].Key
		j := i+1
		for j < len(intermediate) && reduceKey == intermediate[j].Key {
			j++
		}

		values := []string {}
		for k := i; k < j; k++ {
			values = append(values, intermediate[k].Value)
		}

		reduceOut := worker.reducef(reduceKey, values)

		fmt.Fprintf(reduceOutFile, "%v %v\n", reduceKey, reduceOut)

		i = j
	}

	worker.finishedReducing(reduceTask)
}

// sendHeartBeat may fail as the worker may already be expired,
// but sendHeartBeat does nothing about it, as the worker will 
// try to call other RPC, and it will find it is expired as well.
func (worker *MRWorker) sendHeartBeat() {
	arg := HeartBeatArg {
		WorkerID: worker.workerInfo.WorkerID,
	}
	var reply RpcReply
	ok := call("Coordinator.HeartBeat", arg, &reply)
	if !ok {
		log.Fatal("call HeartBeat rpc failed")
	}
}

func (worker *MRWorker) start() WorkerResult {
	var reply RpcReply
	ok := call("Coordinator.RegisterWorker", RegisterWorkerArg {}, &reply)
	if !ok {
		log.Fatal("call RegisterWorker failed")
	}

	switch r := reply.(type) {
	case *WorkerInfo:
		worker.workerInfo = r

	default:
		log.Fatalf("Invalid RegisterWorker reply type: %T", reply)
	}

	if worker.hearBeatTimer == nil {
		worker.hearBeatTimer = newRepeatTimer(
			worker.workerInfo.HeartBeatDuration,
			worker.sendHeartBeat,
		)
	} else {
		worker.hearBeatTimer.reset(worker.workerInfo.HeartBeatDuration)
	}

	SetupLogger(fmt.Sprintf("/Users/lunar/pros/golab/src/main/logs/log-worker-%d", worker.workerInfo.WorkerID))

	log.Printf("Registered as worker %d\n", worker.workerInfo.WorkerID)

	// create intermediate files in advance
	for i := uint16(0); i < worker.workerInfo.R; i++ {
		f := createOrRemoveOld(fmt.Sprintf("mr-%d-%d", worker.workerInfo.WorkerID, i))
		f.Close()
	}

	getTaskArg := GetTaskArg {
		WorkerID: worker.workerInfo.WorkerID,
	}

	getTask: for {
		ok = call("Coordinator.GetTask", getTaskArg, &reply)
		if !ok {
			log.Fatal("Coordinator.GetTask failed")
		}

		log.Printf("Got a task of type %T\n", reply)

		switch reply := reply.(type) {
		case *InvalidWorker:
			log.Printf("Worker expired\n")
			return WORKER_RESTAET

		case *InvalidArgType:
			panic(reply.Hint)

		case *MapTaskRpc:
			worker.doMapWork(reply)

		case *ReduceTaskRpc:
			worker.doReduceTask(reply)

		case *OkReply:
			continue getTask

		case *NoopTaskRpc:
			time.Sleep(worker.workerInfo.TaskWaitingDuration)
			continue getTask

		case *NoMoreTaskRpc:
			return WORKER_DONE
		}
	}
}

//
// main/mrworker.go calls this function.
//
func Worker(mapf MapFunc, reducef ReduceFunc) {
	// First introduce yourself to the coordinator.
	RegisterTasks()
	worker := &MRWorker {
		mapf: mapf,
		reducef: reducef,
	}

	workerRes := WORKER_RESTAET
	for workerRes != WORKER_DONE {
		workerRes = worker.start()
	}
	log.Printf("Worker %d quit in %d status\n", worker.workerInfo.WorkerID, workerRes)
}

//
// send an RPC request to the coordinator, wait for the response.
// usually returns true.
// returns false if something goes wrong.
//
func call(rpcname string, args interface{}, reply interface{}) bool {
	// c, err := rpc.DialHTTP("tcp", "127.0.0.1"+":1234")
	sockname := coordinatorSock()
	c, err := rpc.DialHTTP("unix", sockname)
	if err != nil {
		log.Fatal("dialing:", err)
	}
	defer c.Close()

	err = c.Call(rpcname, args, reply)
	if err == nil {
		return true
	}

	fmt.Println("RPC call error: ", err)
	return false
}

func SetupLogger(fileName string) {
	logFile, err := os.Create(fileName)
	if err != nil {
		if os.IsExist(err) {
			err = os.Remove(fileName)
		}

		if err != nil {
			panic(fmt.Sprintf("setupLogger failed: %s", err.Error()))
		} else {
			logFile, _ = os.Create(fileName)
		}
	}

	defaultLogger := log.Default()
	defaultLogger.SetOutput(logFile)
}
