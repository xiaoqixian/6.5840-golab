// Date:   Sat Apr 20 16:24:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "sync"

// A thread-safe bit vec
type BitVec struct {
	cap int
	count int
	bits []uint8
	sync.RWMutex
}

func newBitVec(cap int) *BitVec {
	off := 0
	if cap % 8 != 0 { off = 1 }
	vecSize := cap/8 + off
	return &BitVec {
		cap: cap,
		count: 0,
		bits: make([]uint8, vecSize),
	}
}

// Set true at idx, return true if it is not set before.
func (bv *BitVec) Set(idx int) bool {
	numIdx, off := idx/8, idx%8
	bv.Lock()
	defer bv.Unlock()
	num, offNum := bv.bits[numIdx], uint8(1) << off
	
	if num & offNum != 0 { return false }
	
	bv.bits[numIdx] |= offNum
	bv.count++
	return true
}

// Set false at idx, return true if it is set true before.
func (bv *BitVec) Unset(idx int) bool {
	numIdx, off := idx/8, idx%8
	bv.Lock()
	defer bv.Unlock()
	num, offNum := bv.bits[numIdx], uint8(1) << off
	
	if num & offNum == 0 { return false }
	
	bv.bits[numIdx] ^= offNum
	bv.count--
	return true
}

func (bv *BitVec) At(idx int) bool {
	bv.RLock()
	defer bv.RUnlock()
	return (bv.bits[idx/8] & (1 << (idx%8))) != 0
}

func (bv *BitVec) Count() int {
	bv.RLock()
	defer bv.RUnlock()
	return bv.count
}
