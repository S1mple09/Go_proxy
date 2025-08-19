package proxy

import (
	"math/rand"
	"sort"
	"sync"
	"time"
)

// Proxy 代理服务器信息结构体
// 包含代理的基本信息和性能指标
// Address: 代理地址(格式: host:port)
// Protocol: 代理协议类型(SOCKS5/HTTP等)
// Latency: 连接延迟(秒)
// Speed: 传输速度(KB/s)
// Anonymity: 匿名级别(透明/普通/高匿)
// Location: 地理位置信息
type Proxy struct {
	Address     string
	Protocol    string
	Latency     float64
	Speed       float64
	Anonymity   string
	Location    string
	Country     string
	Province    string
	City        string
	Score       float64 // 0-100 score based on performance metrics
	LastChecked time.Time
	Region      string
	IsPremium   bool
	FailCount   int
}

// Rotator 代理池管理器
// 负责代理的存储、验证状态跟踪和轮换策略实现
// rawProxies: 原始代理列表(未验证的代理)
// validProxies: 有效代理列表(已验证可使用的代理)
// indices: 轮换索引，跟踪不同类别代理的当前位置
// mutex: 读写锁，确保线程安全操作
type Rotator struct {
	rawProxies   []*Proxy
	validProxies []*Proxy
	indices      map[string]int
	mutex        sync.RWMutex
}

// NewRotator 创建新的代理轮换器实例
// 初始化代理存储结构和轮换索引
// 返回初始化后的Rotator实例
func NewRotator() *Rotator {
	return &Rotator{
		indices: make(map[string]int),
	}
}

// SetRawProxies 替换原始代理列表
// 完全覆盖现有原始代理数据
// 参数 proxies: 新的原始代理列表
func (r *Rotator) SetRawProxies(proxies []*Proxy) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.rawProxies = proxies
}

// AddRawProxies 批量添加原始代理(去重)
// 仅添加地址不在现有列表中的代理
// 参数 proxies: 待添加的原始代理列表
func (r *Rotator) AddRawProxies(proxies []*Proxy) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	seen := make(map[string]bool)
	for _, p := range r.rawProxies {
		seen[p.Address] = true
	}
	for _, p := range proxies {
		if !seen[p.Address] {
			r.rawProxies = append(r.rawProxies, p)
			seen[p.Address] = true
		}
	}
}

// GetRawProxies 获取所有原始代理的副本
// 返回原始代理列表的深拷贝，防止外部修改内部数据
func (r *Rotator) GetRawProxies() ([]*Proxy, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	proxiesCopy := make([]*Proxy, len(r.rawProxies))
	copy(proxiesCopy, r.rawProxies)
	return proxiesCopy, nil
}

// SetValidProxies 替换有效代理列表
// 完全覆盖现有有效代理数据
// 参数 proxies: 新的有效代理列表
func (r *Rotator) SetValidProxies(proxies []*Proxy) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.validProxies = proxies
	return nil
}

// AddValidProxies 线程安全地添加有效代理
// 追加到现有有效代理列表，不检查重复
// 参数 proxies: 待添加的有效代理列表
func (r *Rotator) AddValidProxies(proxies []*Proxy) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.validProxies = append(r.validProxies, proxies...)
	return nil
}

// GetValidProxies 获取所有有效代理的副本
// 返回有效代理列表的深拷贝，防止外部修改内部数据
func (r *Rotator) GetValidProxies() ([]*Proxy, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	proxiesCopy := make([]*Proxy, len(r.validProxies))
	copy(proxiesCopy, r.validProxies)
	return proxiesCopy, nil
}

// GetValidProxyCount 返回有效代理的数量
// 线程安全地获取当前有效代理总数
func (r *Rotator) GetValidProxyCount() int {
	r.mutex.RLock()
	defer r.mutex.RUnlock()
	return len(r.validProxies)
}

// CleanupProxies 清理失效代理
// 移除超过最大失败次数或长时间未检查的代理
func (r *Rotator) CleanupProxies(maxAge time.Duration) {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	var valid []*Proxy
	for _, p := range r.validProxies {
		if p.FailCount < 5 && // maxFailCount hardcoded as 5 for now
			time.Since(p.LastChecked) <= maxAge {
			valid = append(valid, p)
		}
	}
	r.validProxies = valid
}

// GetFilteredAndSortedProxies 获取经过筛选和排序的有效代理
// 根据延迟和速度筛选代理，并按延迟升序排序
// 参数 maxLatency: 最大允许延迟(-1表示不限制)
// 参数 minSpeed: 最小允许速度(-1表示不限制)
// 返回符合条件的代理列表和可能的错误
func (r *Rotator) GetFilteredAndSortedProxies(maxLatency, minSpeed float64) ([]*Proxy, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	var filtered []*Proxy
	for _, p := range r.validProxies {
		if (maxLatency < 0 || p.Latency <= maxLatency) && (minSpeed < 0 || p.Speed >= minSpeed) {
			filtered = append(filtered, p)
		}
	}

	// 按延迟升序排序
	sort.SliceStable(filtered, func(i, j int) bool {
		return filtered[i].Latency < filtered[j].Latency
	})

	return filtered, nil
}

// GetNextProxy 按轮换策略获取下一个可用代理
// 实现加权随机选择策略，基于代理性能指标
// 参数 region: 区域筛选(当前未实现)
// 参数 premiumOnly: 是否只返回高级代理(当前未实现)
// 返回下一个代理实例或nil(如果没有有效代理)
func (r *Rotator) GetNextProxy(region string, premiumOnly bool) *Proxy {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if len(r.validProxies) == 0 {
		return nil
	}

	// 计算总权重
	totalScore := 0.0
	for _, p := range r.validProxies {
		totalScore += 1/(p.Latency+0.1) + p.Speed*0.1
	}

	// 随机选择
	rand.Seed(time.Now().UnixNano())
	randScore := rand.Float64() * totalScore
	runningScore := 0.0
	for _, p := range r.validProxies {
		runningScore += 1/(p.Latency+0.1) + p.Speed*0.1
		if runningScore >= randScore {
			return p
		}
	}

	// 如果由于浮点精度问题未选择，返回最后一个代理
	return r.validProxies[len(r.validProxies)-1]
}
