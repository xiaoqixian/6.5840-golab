# Notes

#### Lab1 MapReduce

##### MapReduce 过程

1. 总共有 M 个 Map 任务和 R 个 Reduce 任务, 在本 Lab 中 M 对应输入文件的数量, 每个 Map 任务需要读取文件中的内容并交给第三方的 `Map` 函数处理之后得到一个「键值对」数组. 

   随后 Map 任务需要根据键值对中的键值进行哈希计算之后模 R 之后得到最终需要进行处理的 Reduce 任务, 写入文件 `mr-X-Y`  中, 其中 `X` 表示 Map 任务的 id (由 coordinator 在分配任务时给定), `Y` 表示计算得到的 Reducer 的 id. 

2. Reducer worker 从 coordinator 那里领取需要进行处理的文件列表, 可以保证的是, 每个要处理的 key 只会散落在这些文件中. Reducer 需要先读取所有的文件内容之后进行排序, 然后将所有具有相同 key 的 键值对 整合得到一个具有相同 key 的 value array, 然后交给第三方提供的 `reduce` 函数处理后得到一个 最终结果的 键值对. 最后 reducer 将该键值对写入 `mr-out-X` 文件中, 其中 `X` 表示该 reducer 的 id (由 reducer worker 向 coordinator 领取任务进行指配). 

3. worker 的数量通常代表实际执行任务的 CPU 资源数量, 例如在一台机器上运行时表示 线程/进程 的数量, 在一个集群中运行时表示 woker 集群的机器数量. 

4. 通常 Map 任务可以设置与 worker 数量一致, 然后通过给这些任务设置不同的文件列表来完成负载均衡.

5. Reducer 任务数量 R 的大小的设置比较重要, 通常 R 设置越大, 越内存友好, 因为 Reducer 在进行任务时, 需要将所有的目标文件全部读取之后才能开始, 而 R 越大, 产生的中间文件更多但更小, 在 Map 任务数量固定的情况下, 单个 Reducer 任务需要读取的总文件大小就越小, 因此越内存友好.

##### 实现过程

​	首先, 我们确定 RPC 的调用是单向的, 也就是说 coordinator 不必知道 worker 的存在, 也不会调任何 RPC. worker 通过调用 `GetTask` RPC 来向 coordinator 领取任务, 并通过返回结果的具体类型来判断要做的工作.

1. 最开始, 每个 worker 会调用 `RegisterWorker` RPC 来向 coordinator 声明自己的存在, coordinator 会向其回复一个结构体, 包含为 worker 分配的 uuid、心跳信息的间隔时间等必要信息;

2. 接着 worker 开始调用 `GetTask` RPC 来获取需要进行的任务, 若是:

   1. `MapTask`: 包含一个文件名, worker 需要读取该文件并进行 map 工作;
   2. `ReduceTask`: 包含一个 Reduce ID, worker 需要查找所有包含该ID的中间结果文件, 并整合所有相同的 key 的 value 数组, 交给 `reduce` 函数处理后得到最终结果并写入文件.
   3. `NoopTask`: 表示暂时没有要做的工作, worker 会等待一定时间之后继续获取任务.
   4. `NoMoreTask`: 表示所有的工作已经完成, worker 可以自行退出.

   在 coordinator 中会对每个已注册未过期的 worker 保存起有当前领取的任务 (为 `nil` 时表示未领取任务). 作用主要有两个:

   1. 在 worker 完成任务后标记该任务已完成;
   2. 在 worker 过期之后将该任务提取出来重新分配给其它 worker.

3. worker 在完成任务之后调用 `FinishTask` RPC 表示领取的任务已经完成, 由于 coordinator 已经保存有 worker 的任务信息, 所以 worker 只需要传入自己的 worker ID 即可.

##### 心跳信息

​	coordinator 和 worker 之间通过发送心跳信息来感知彼此的存在, 主要是让 coordinator 感知到 worker 宕机的情况, 若 coordinator 挂掉, 则整个程序将无法继续推进, 不会造成非一致性风险.

​	当 worker 向 coordinator 介绍自己的时候, coordinator 会发送给其一个心跳信息的间隔时间, 指示 worker 按照该时间定时发送心跳信息, coordinator 则会根据该 worker id 设置一个定时器检查 worker 是否已经宕机, coordinator 设置的等待时间会大于给 worker 指示的时间(通常是两倍).

​	若 worker 发生宕机, coordinator 的定时器超时之后会自动将其从 worker group 中删除, 同时需要根据其工作状态对相应的资源状态进行修改. 例如, 假设 worker 超时时正在对某个文件进行 map work, 则需要将该文件状态从 `PROCESSING` 修改为 `TODO`; reduce work 同理.

​	在这过程中会涉及到比较复杂的多线程同步问题, 需要进行非常仔细的考虑以避免死锁的问题出现. 

​	首先, 对于每个 worker, 其主要会受到两条线程的影响:

1. Coordinator: 当 coordinator 收到 `GetTask` 的 RPC 请求时, 其需要将 worker 关联到某个 file 或者 reduce id, 在这个过程中不允许其它线程对 worker 进行任何修改, 因此需要加锁. 修改完成后将锁释放.

2. timer: 每个 worker 对应一个 timer, 当 worker 超时未发送心跳信息时, 需要将对应的 worker 从 worker group 中删除, 为了防止将正在完成修改的 worker 删除, 需要先找到对应的 worker 上锁.

   同时, 为了防止不必要的麻烦, 我们选择保守的策略, 对整个 worker group 使用读写锁. 

   timer 在删除 worker 的同时还需要将 worker 占用的资源进行释放, 将其占用的 map file 或者 reduce ID 放入 TaskContainer, 以供其它 worker 领取.
