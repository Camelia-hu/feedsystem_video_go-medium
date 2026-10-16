package fault

import "sync/atomic"

// Injector 内存态故障注入开关（演示用）。
// 生产环境可改为 ConfigMap/外部配置驱动，或换成更细粒度的开关。
type Injector struct {
	slowSQLEnabled        atomic.Bool
	slowSQLSeconds        atomic.Int64
	cacheBreakdownEnabled atomic.Bool
	errorEnabled          atomic.Bool
	errorPercent          atomic.Int64
}

func NewInjector() *Injector {
	i := &Injector{}
	i.slowSQLSeconds.Store(5) // 默认慢 SQL 5 秒
	i.errorPercent.Store(100) // 默认错误注入 100%
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

// ErrorRate 故障：按比例直接返回 5xx，拉高接口错误率（用于验证错误率告警）。
// percent 超出 1~100 时按 100 处理。
func (i *Injector) EnableErrorRate(percent int) {
	if percent <= 0 || percent > 100 {
		percent = 100
	}
	i.errorPercent.Store(int64(percent))
	i.errorEnabled.Store(true)
}
func (i *Injector) DisableErrorRate() { i.errorEnabled.Store(false) }
func (i *Injector) ErrorRate() (bool, int) {
	return i.errorEnabled.Load(), int(i.errorPercent.Load())
}
