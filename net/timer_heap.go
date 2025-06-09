package net

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/godyy/gutils/container/heap"
)

// timerOfTimerHeap TimerHeap 定时器.
type timerOfTimerHeap struct {
	id        TimerId       // 定时器ID.
	heapIndex int           // 堆索引.
	delay     time.Duration // 延迟时间.
	periodic  bool          // 是否周期性定时器.
	args      any           // 参数.
	cb        TimerFunc     // 回调函数.
	expireAt  int64         // 到期时间.
}

func (t *timerOfTimerHeap) HeapLess(other *timerOfTimerHeap) bool {
	if n := t.expireAt - other.expireAt; n == 0 {
		return t.id < other.id
	} else {
		return n < 0
	}
}

func (t *timerOfTimerHeap) HeapIndex() int {
	return t.heapIndex
}

func (t *timerOfTimerHeap) SetHeapIndex(index int) {
	t.heapIndex = index
}

// TimerHeap 最小堆定时器系统.
type TimerHeap struct {
	mtx        sync.Mutex                    // 互斥锁.
	sysTimer   *time.Timer                   // 系统定时器.
	timerIdGen uint64                        // 定时器ID生成自增键.
	timerHeap  *heap.Heap[*timerOfTimerHeap] // 定时器最小堆.
	timerMap   map[TimerId]*timerOfTimerHeap // 定时器映射.
	stopped    bool                          // 是否已停止.
	cStopped   chan struct{}                 // 已停止信号.
}

// NewTimerHeap 构造 TimerHeap.
func NewTimerHeap() *TimerHeap {
	th := &TimerHeap{
		sysTimer:  time.NewTimer(0),
		timerHeap: heap.NewHeap[*timerOfTimerHeap](),
		timerMap:  make(map[TimerId]*timerOfTimerHeap),
		stopped:   false,
		cStopped:  make(chan struct{}),
	}

	go th.loop()

	return th
}

// genTimerId 生成定时器ID.
func (th *TimerHeap) genTimerId() TimerId {
	timerId := atomic.AddUint64(&th.timerIdGen, 1)
	if timerId == TimerIdNone {
		timerId = atomic.AddUint64(&th.timerIdGen, 1)
	}
	return timerId
}

// addTimer 添加定时器.
func (th *TimerHeap) addTimer(t *timerOfTimerHeap) {
	th.timerHeap.Push(t)
	th.timerMap[t.id] = t
}

// remTimer 移除定时器.
func (th *TimerHeap) remTimer(t *timerOfTimerHeap) {
	th.timerHeap.Remove(t.heapIndex)
	delete(th.timerMap, t.id)
}

// resetSysTimer 重置系统定时器.
func (th *TimerHeap) resetSysTimer(expireAt int64) {
	th.stopSysTimer()
	th.sysTimer.Reset(time.Duration(expireAt - time.Now().UnixNano()))
}

// stopSysTimer 停止系统定时器.
func (th *TimerHeap) stopSysTimer() {
	if !th.sysTimer.Stop() {
		select {
		case <-th.sysTimer.C:
		default:
		}
	}
}

// TimerHeap 停止 TimerHeap.
func (th *TimerHeap) Stop() {
	th.mtx.Lock()
	defer th.mtx.Unlock()

	if th.stopped {
		return
	}

	th.stopSysTimer()
	th.timerHeap = nil
	th.timerMap = nil
	close(th.cStopped)
	th.stopped = true
}

// StartTimer 启动定时器.
func (th *TimerHeap) StartTimer(delay time.Duration, periodic bool, args any, cb TimerFunc) TimerId {
	if delay <= 0 {
		panic("delay must > 0")
	}

	if cb == nil {
		panic("callback func is nil")
	}

	// 创建定时器.
	t := &timerOfTimerHeap{
		id:        th.genTimerId(),
		heapIndex: -1,
		delay:     delay,
		periodic:  periodic,
		args:      args,
		cb:        cb,
		expireAt:  time.Now().Add(delay).UnixNano(),
	}

	th.mtx.Lock()
	defer th.mtx.Unlock()

	// 检查是否已停止.
	if th.stopped {
		return TimerIdNone
	}

	// 添加定时器.
	th.addTimer(t)
	if t == th.timerHeap.Top() {
		th.resetSysTimer(t.expireAt)
	}

	return t.id
}

// StopTimer 停止定时器.
func (th *TimerHeap) StopTimer(tid TimerId) {
	th.mtx.Lock()
	defer th.mtx.Unlock()

	// 检查是否已停止.
	if th.stopped {
		return
	}

	// 获取定时器.
	t, exists := th.timerMap[tid]
	if !exists {
		return
	}

	// 检查是否为堆顶定时器.
	top := t == th.timerHeap.Top()

	// 移除定时器.
	th.remTimer(t)

	// 更新系统定时器.
	if top {
		if th.timerHeap.Len() == 0 {
			th.stopSysTimer()
		} else {
			th.resetSysTimer(th.timerHeap.Top().expireAt)
		}
	}
}

// update 更新定时器.
func (th *TimerHeap) update() {
	var (
		t    *timerOfTimerHeap
		cb   TimerFunc
		args TimerArgs
	)
	for {
		now := time.Now().UnixNano()

		// 获取并更新堆顶定时器.
		th.mtx.Lock()
		if th.timerHeap.Len() == 0 {
			th.mtx.Unlock()
			return
		}
		t = th.timerHeap.Top()
		if t.expireAt > now {
			th.resetSysTimer(t.expireAt)
			th.mtx.Unlock()
			return
		}
		cb = t.cb
		args.TID = t.id
		args.Args = t.args
		if t.periodic {
			t.expireAt += int64(t.delay)
			th.timerHeap.Fix(t.heapIndex)
		} else {
			th.remTimer(t)
		}
		th.mtx.Unlock()

		// 调用回调函数.
		th.invokeCallback(cb, &args)
	}
}

// invokeCallback 调用回调函数.
func (th *TimerHeap) invokeCallback(cb TimerFunc, args *TimerArgs) {
	cb(args)
}

// loop 主循环逻辑.
func (th *TimerHeap) loop() {
	for {
		select {
		case <-th.sysTimer.C:
			th.update()
		case <-th.cStopped:
			return
		}
	}
}
