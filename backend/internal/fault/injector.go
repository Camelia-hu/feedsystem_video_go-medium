package fault

import "sync/atomic"

// Injector 内存态故障注入开关（演示用）。
// 生产环境可改为 ConfigMap/外部配置驱动，或换成更细粒度的开关。
type Injector struct {
	slowSQLEnabled        atomic.Bool
	slowSQLSeconds        atomic.Int64
	cacheBreakdownEnabled atomic.Bool
}

func NewInjector() *Injector {
	i := &Injector{}
	i.slowSQLSeconds.Store(5) // 默认慢 SQL 5 秒
	return i
}

// SlowSQL 故障：在请求前执行 SELECT SLEEP(n)
func (i *Injector) EnableSlowSQL(seconds int) {
	if seconds <= 0 {
		seconds = 5
	}
	i.slowSQLSeconds.Store(int64(seconds))
	i.slowSQLEnabled.Store(true)
}
func (i *Injector) DisableSlowSQL() { i.slowSQLEnabled.Store(false) }
func (i *Injector) SlowSQL() (bool, int) {
	return i.slowSQLEnabled.Load(), int(i.slowSQLSeconds.Load())
}

// CacheBreakdown 故障：请求前删除 feed 缓存 key，制造缓存击穿
func (i *Injector) EnableCacheBreakdown()  { i.cacheBreakdownEnabled.Store(true) }
func (i *Injector) DisableCacheBreakdown() { i.cacheBreakdownEnabled.Store(false) }
func (i *Injector) CacheBreakdown() bool   { return i.cacheBreakdownEnabled.Load() }
