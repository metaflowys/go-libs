package pool

import (
	"sync"
)

type Option = interface{}
type OptionPoolSizePerCPU int
// 太大会导致Get操作卡顿，太小会导致创建过多的slice
// A size too large will slow down Get(), while a size too small leads to frequent slice allocation
type OptionInitFullPoolSize int

const POOL_SIZE_PER_CPU = OptionPoolSizePerCPU(256)
const INIT_FULL_POOL_SIZE = OptionInitFullPoolSize(256)

// sync.Pool对于每一个系统线程，能够无锁提取存放一个元素，
// 其余元素会通过锁追加到数组中。为了能够尽可能避免锁的使用，
// 我们需要利用好这一个元素的位置，所以在这个元素上放置slice指针
// 作为实际的pool使用，每次Get/Put时，先拿到slice，弹出/推入元素后再
// 将slice放回，以尽可能无锁分配释放资源
// For each system thread, sync.Pool will fetch or store one element locklessly,
// the rest elements will be appended to the slice with mutex mechanism.
// A slice will be put in this lockless element in order to avoid the use of mutex.
// When Get() or Put() is called on LockFreePool, the slice is fetched, elements
// pushed into or poped from the slice, then put back to sync.Pool.
type LockFreePool struct {
	emptyPool *sync.Pool
	fullPool  *sync.Pool

	alloc func() interface{}
}

func (p *LockFreePool) Get() interface{} {
	elemPool := p.fullPool.Get().(*[]interface{}) // avoid convT2Eslice
	pool := *elemPool
	e := pool[len(pool)-1]
	*elemPool = pool[:len(pool)-1]
	if len(pool) > 1 {
		p.fullPool.Put(elemPool)
	} else {
		p.emptyPool.Put(elemPool) // Empty, Put for other CPUs
	}
	return e
}

func (p *LockFreePool) Put(x interface{}) {
	pool := p.emptyPool.Get().(*[]interface{}) // avoid convT2Eslice
	*pool = append(*pool, x)
	if len(*pool) < cap(*pool) {
		p.emptyPool.Put(pool)
	} else {
		p.fullPool.Put(pool) // Full, Put for other CPUs
	}
}

// 注意OptionInitFullPoolSize不能大于OptionPoolSizePerCPU，且不能小于等于0
// Note that OptionInitFullPoolSize cannot be larger than OptionPoolSizePerCPU and must be positive.
func NewLockFreePool(alloc func() interface{}, options ...Option) LockFreePool {
	poolSizePerCPU := POOL_SIZE_PER_CPU
	initFullPoolSize := INIT_FULL_POOL_SIZE
	for _, opt := range options {
		if size, ok := opt.(OptionPoolSizePerCPU); ok {
			poolSizePerCPU = size
		} else if size, ok := opt.(OptionInitFullPoolSize); ok {
			initFullPoolSize = size
		}
	}
	if poolSizePerCPU < OptionPoolSizePerCPU(initFullPoolSize) || initFullPoolSize <= 0 {
		poolSizePerCPU = POOL_SIZE_PER_CPU
		initFullPoolSize = INIT_FULL_POOL_SIZE
	}
	newEmptySlice := func() interface{} {
		p := make([]interface{}, 0, poolSizePerCPU)
		return &p
	}
	newFullSlice := func() interface{} {
		p := make([]interface{}, initFullPoolSize, poolSizePerCPU)
		for i := OptionInitFullPoolSize(0); i < initFullPoolSize; i++ {
			p[i] = alloc()
		}
		return &p
	}
	return LockFreePool{
		emptyPool: &sync.Pool{
			New: newEmptySlice,
		},
		fullPool: &sync.Pool{
			New: newFullSlice,
		},
		alloc: alloc,
	}
}
