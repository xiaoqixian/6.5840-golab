# MIT 6.5840 Lab

​	本仓库是 mit 6.5840 课程 Lab 的 Go 实现. 

### [Lab1: MapReduce](src/mr)

​	基于 MapReduce 论文实现的一套简易 MapReduce 框架. 分别实现一个 coordinator 和 worker, 分别负责工作的分发和完成.

### [Lab2: Key/Value Server](src/kvsrv)

​	实现一个单机 Key/Value 服务器, 需要支持并发访问, 并且满足一致性: 当一个调用的开始后于另一个调用的结束时, 后开始的调用应该能够看到前一个调用的结果. 

​	服务器需要提供三个接口:

- `Put(key, value)`: 放入一个 Key/Value pair;
- `Append(key, value)`: 将 `value` 添加到 `key` 原来的 `value` 之后, 并返回旧值;
- `Get(key)`: 获取 `key` 对应的 `value`.

### [Lab3: Raft](src/raft)

​	实现 Raft 算法, 实现 `GetState`, `Start(cmd)` 等接口. tester 会检查集群节点的一致性, 例如是否出现两个有同一个 term 的 leader. 是否出现节点 Apply log 的不一致性等. 

