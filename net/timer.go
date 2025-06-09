package net

import "time"

// TimerSystem 定时器系统.
type TimerSystem interface {
	// StartTimer 启动定时器.
	StartTimer(delay time.Duration, periodic bool, args any, f TimerFunc) TimerId

	// StopTimer 停止定时器.
	StopTimer(tid TimerId)
}

// TimerId 定时器ID.
type TimerId = uint64

// TimerIdNone 定时器ID为0.
const TimerIdNone = 0

// TimerArgs 定时器参数.
type TimerArgs struct {
	TID  TimerId // 定时器ID.
	Args any     // 参数.
}

// TimerFunc 定时器回调函数.
type TimerFunc func(*TimerArgs)
