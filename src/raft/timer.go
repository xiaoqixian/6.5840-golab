// Date:   Tue Apr 16 11:31:45 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"time" 
	"sync/atomic")

type RepeatTimer struct {
	ticker *time.Ticker
	f func()
	killed atomic.Bool
	stopped atomic.Bool
	runner func(*RepeatTimer)
}

func (rt *RepeatTimer) kill() {
	rt.killed.Store(true)
	rt.ticker.Stop()
}
func (rt *RepeatTimer) reset(d time.Duration) {
	rt.killed.Store(false)
	rt.ticker.Reset(d)
	if rt.stopped.Load() {
		rt.stopped.Store(false)
		go rt.runner(rt)
	}
}

func newRepeatTimer(d time.Duration, f func()) *RepeatTimer {
	rt := &RepeatTimer {
		ticker: time.NewTicker(d),
		f: f,
		runner: func(rt *RepeatTimer) {
			for !rt.killed.Load() {
				rt.f()
				<- rt.ticker.C
			}
			rt.stopped.Store(true)
		},
	}

	go rt.runner(rt)
	return rt
}

