package proxy

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Proxy 代表一个代理，按照Python项目的结构定义
type Proxy struct {
	Address        string    `json:"proxy"`       // 代理地址
	Protocol       string    `json:"protocol"`    // 协议类型
	Location       string    `json:"location"`    // 地理位置
	Score          float64   `json:"score"`       // 评分
	Latency        float64   `json:"latency"`     // 延迟(秒)
	Status         string    `json:"status"`      // 状态: "Working", "Unavailable"
	Consecutive    int       `json:"consecutive_failures"` // 连续失败次数
	AnonymousLevel string    `json:"anonymous_level"` // 匿名度级别: "Transparent", "Anonymous", "HighAnon"
	LastCheck      time.Time `json:"last_check"`  // 最后检查时间
	
	// 兼容旧字段
	Speed          float64   // 传输速度(KB/s)
	Anonymity      string    // 匿名级别
	LastChecked    time.Time // 最后检查时间
	FailCount      int       // 失败次数
	SuccessCount   int       // 成功次数
}

// Rotator 管理代理池并提供代理选择功能
type Rotator struct {
	allProxies       []*Proxy           // 所有代理
	proxiesByCountry map[string][]*Proxy // 按国家/地区分组的代理
	currentProxy     *Proxy             // 当前使用的代理
	indices          map[string]int     // 不同筛选条件下的索引
	lock             sync.RWMutex       // 并发安全锁
	
	// 当前激活的过滤器状态
	currentFilterRegion       string  // 区域筛选
	currentFilterQualityMs    *float64 // 延迟筛选(毫秒)
	
	// 兼容旧字段
	rawProxies   []*Proxy
	validProxies []*Proxy
}

// NewRotator 创建一个新的代理轮换器
func NewRotator() *Rotator {
	return &Rotator{
		allProxies:       make([]*Proxy, 0),
		proxiesByCountry: make(map[string][]*Proxy),
		indices:          make(map[string]int),
		currentProxy:     nil,
		currentFilterRegion: "All",
		currentFilterQualityMs: nil,
		rawProxies: make([]*Proxy, 0),
		validProxies: make([]*Proxy, 0),
	}
}

// Clear 清空所有代理并重置状态
func (r *Rotator) Clear() {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	r.allProxies = []*Proxy{}
	r.proxiesByCountry = make(map[string][]*Proxy)
	r.indices = make(map[string]int)
	r.currentProxy = nil
	
	// 同时清空兼容字段
	r.rawProxies = []*Proxy{}
	r.validProxies = []*Proxy{}
}

// SetFilters 设置筛选条件
func (r *Rotator) SetFilters(region string, qualityMs *float64) {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	r.currentFilterRegion = region
	r.currentFilterQualityMs = qualityMs
}

// AddProxy 添加一个新代理，如果已存在则忽略
func (r *Rotator) AddProxy(proxy *Proxy) {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	// 检查代理是否已存在
	for _, p := range r.allProxies {
		if p.Address == proxy.Address {
			return
		}
	}
	
	// 初始化一些默认值
	if proxy.Status == "" {
		proxy.Status = "Working"
	}
	if proxy.Consecutive == 0 {
		proxy.Consecutive = 0
	}
	
	// 添加到总列表
	r.allProxies = append(r.allProxies, proxy)
	
	// 添加到按国家分组的映射
	country := proxy.Location
	if country == "" {
		country = "Unknown"
	}
	r.proxiesByCountry[country] = append(r.proxiesByCountry[country], proxy)
	
	// 更新兼容字段
	if proxy.Status == "Working" {
		r.validProxies = append(r.validProxies, proxy)
	} else {
		r.rawProxies = append(r.rawProxies, proxy)
	}
}

// RemoveProxy 根据地址移除代理
func (r *Rotator) RemoveProxy(address string) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	// 查找要移除的代理
	var proxyToRemove *Proxy
	for _, p := range r.allProxies {
		if p.Address == address {
			proxyToRemove = p
			break
		}
	}
	
	if proxyToRemove == nil {
		return false
	}
	
	// 从总列表移除
	var newAllProxies []*Proxy
	for _, p := range r.allProxies {
		if p.Address != address {
			newAllProxies = append(newAllProxies, p)
		}
	}
	r.allProxies = newAllProxies
	
	// 从按国家分组的映射中移除
	country := proxyToRemove.Location
	if country == "" {
		country = "Unknown"
	}
	
	if countryProxies, exists := r.proxiesByCountry[country]; exists {
		var newCountryProxies []*Proxy
		for _, p := range countryProxies {
			if p.Address != address {
				newCountryProxies = append(newCountryProxies, p)
			}
		}
		if len(newCountryProxies) > 0 {
			r.proxiesByCountry[country] = newCountryProxies
		} else {
			delete(r.proxiesByCountry, country)
		}
	}
	
	// 从兼容字段中移除
	var newValidProxies []*Proxy
	for _, p := range r.validProxies {
		if p.Address != address {
			newValidProxies = append(newValidProxies, p)
		}
	}
	r.validProxies = newValidProxies
	
	var newRawProxies []*Proxy
	for _, p := range r.rawProxies {
		if p.Address != address {
			newRawProxies = append(newRawProxies, p)
		}
	}
	r.rawProxies = newRawProxies
	
	// 如果移除的是当前代理，重置当前代理
	if r.currentProxy != nil && r.currentProxy.Address == address {
		r.currentProxy = nil
	}
	
	return true
}

// ReportFailure 报告代理连接失败，立即设为不可用
func (r *Rotator) ReportFailure(address string) {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range r.allProxies {
		if p.Address == address {
			p.Status = "Unavailable"
			p.Consecutive++
			p.FailCount++ // 更新兼容字段
			
			// 如果失败的是当前代理，重置当前代理
			if r.currentProxy != nil && r.currentProxy.Address == address {
				r.currentProxy = nil
			}
			break
		}
	}
	
	// 更新兼容字段：将代理从validProxies移动到rawProxies
	var newValidProxies []*Proxy
	for _, p := range r.validProxies {
		if p.Address != address {
			newValidProxies = append(newValidProxies, p)
		}
	}
	r.validProxies = newValidProxies
}

// GetProxyByAddress 根据地址获取代理详情
func (r *Rotator) GetProxyByAddress(address string) *Proxy {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	for _, p := range r.allProxies {
		if p.Address == address {
			return p
		}
	}
	return nil
}

// UpdateProxy 更新代理信息
func (r *Rotator) UpdateProxy(address string, updateData map[string]interface{}) bool {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range r.allProxies {
		if p.Address == address {
			// 更新代理信息
			if latency, ok := updateData["latency"].(float64); ok {
				p.Latency = latency
			}
			if status, ok := updateData["status"].(string); ok {
				oldStatus := p.Status
				p.Status = status
				
				// 更新兼容字段
				if oldStatus != status {
					if status == "Working" {
						// 从rawProxies移到validProxies
						var newRawProxies []*Proxy
						for _, vp := range r.rawProxies {
							if vp.Address != address {
								newRawProxies = append(newRawProxies, vp)
							}
						}
						r.rawProxies = newRawProxies
						r.validProxies = append(r.validProxies, p)
						p.SuccessCount++
					} else {
						// 从validProxies移到rawProxies
						var newValidProxies []*Proxy
						for _, vp := range r.validProxies {
							if vp.Address != address {
								newValidProxies = append(newValidProxies, vp)
							}
						}
						r.validProxies = newValidProxies
						r.rawProxies = append(r.rawProxies, p)
					}
				}
			}
			if location, ok := updateData["location"].(string); ok {
				// 更新地理位置时需要同时更新分组映射
				oldLocation := p.Location
				if oldLocation == "" {
					oldLocation = "Unknown"
				}
				p.Location = location
				
				// 从旧位置移除
				if oldCountryProxies, exists := r.proxiesByCountry[oldLocation]; exists {
					var newCountryProxies []*Proxy
					for _, cp := range oldCountryProxies {
						if cp.Address != address {
							newCountryProxies = append(newCountryProxies, cp)
						}
					}
					if len(newCountryProxies) > 0 {
						r.proxiesByCountry[oldLocation] = newCountryProxies
					} else {
						delete(r.proxiesByCountry, oldLocation)
					}
				}
				
				// 添加到新位置
				newLocation := location
				if newLocation == "" {
					newLocation = "Unknown"
				}
				r.proxiesByCountry[newLocation] = append(r.proxiesByCountry[newLocation], p)
			}
			if score, ok := updateData["score"].(float64); ok {
				p.Score = score
			} else if intScore, ok := updateData["score"].(int); ok {
				p.Score = float64(intScore)
			}
			if anonymousLevel, ok := updateData["anonymous_level"].(string); ok {
				p.AnonymousLevel = anonymousLevel
				p.Anonymity = anonymousLevel // 更新兼容字段
			}
			if lastCheck, ok := updateData["last_check"].(time.Time); ok {
				p.LastCheck = lastCheck
				p.LastChecked = lastCheck // 更新兼容字段
			}
			return true
		}
	}
	return false
}

// GetAllProxiesForRevalidation 获取所有代理的副本用于重新验证
func (r *Rotator) GetAllProxiesForRevalidation() []*Proxy {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	// 返回副本
	dst := make([]*Proxy, len(r.allProxies))
	copy(dst, r.allProxies)
	return dst
}

// GetActiveProxiesCount 获取状态为Working的代理数量
func (r *Rotator) GetActiveProxiesCount() int {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	count := 0
	for _, p := range r.allProxies {
		if p.Status == "Working" {
			count++
		}
	}
	return count
}

// GetAvailableRegionsWithCounts 获取各地区的代理数量统计
func (r *Rotator) GetAvailableRegionsWithCounts(qualityMs *float64) map[string]int {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	counts := make(map[string]int)
	for _, p := range r.allProxies {
		if p.Status != "Working" {
			continue
		}
		
		// 按延迟筛选
		if qualityMs != nil {
			latencyMs := p.Latency * 1000
			if latencyMs > *qualityMs {
				continue
			}
		}
		
		region := p.Location
		if region == "" {
			region = "Unknown"
		}
		counts[region]++
	}
	return counts
}

// GetNextProxy 根据内部存储的筛选条件，轮换获取下一个可用代理
func (r *Rotator) GetNextProxy(region string, premiumOnly bool) *Proxy {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	// 使用内部存储的过滤器，同时兼容旧参数
	effectiveRegion := r.currentFilterRegion
	if region != "" {
		effectiveRegion = region
	}
	effectiveQualityMs := r.currentFilterQualityMs
	
	// 筛选候选代理
	var candidateProxies []*Proxy
	for _, p := range r.allProxies {
		if p.Status != "Working" {
			continue
		}
		
		// 区域匹配
		regionMatch := (effectiveRegion == "All" || p.Location == effectiveRegion)
		
		// 质量匹配
		qualityMatch := true
		if effectiveQualityMs != nil {
			latencyMs := p.Latency * 1000
			qualityMatch = (latencyMs <= *effectiveQualityMs)
		}
		
		if regionMatch && qualityMatch {
			candidateProxies = append(candidateProxies, p)
		}
	}
	
	// 如果没有找到代理，尝试放宽条件
	if len(candidateProxies) == 0 {
		if effectiveRegion != "All" || effectiveQualityMs != nil {
			// 保存原始过滤器
			originalRegion := effectiveRegion
			originalQualityMs := effectiveQualityMs
			
			// 放宽条件：不限区域和延迟
			effectiveRegion = "All"
			effectiveQualityMs = nil
			
			// 重新筛选
			candidateProxies = []*Proxy{}
			for _, p := range r.allProxies {
				if p.Status == "Working" {
					candidateProxies = append(candidateProxies, p)
				}
			}
			
			// 恢复原始过滤器
			r.currentFilterRegion = originalRegion
			r.currentFilterQualityMs = originalQualityMs
		}
		
		// 如果仍然没有代理，返回nil
		if len(candidateProxies) == 0 {
			r.currentProxy = nil
			return nil
		}
	}
	
	// 按分数排序
	sort.Slice(candidateProxies, func(i, j int) bool {
		return candidateProxies[i].Score > candidateProxies[j].Score
	})
	
	// 生成索引键
	qualityKey := "any"
	if effectiveQualityMs != nil {
		qualityKey = "lt" + fmt.Sprintf("%.0f", *effectiveQualityMs)
	}
	indexKey := fmt.Sprintf("%s_%s", effectiveRegion, qualityKey)
	
	// 获取当前索引并更新
	currentIdx := r.indices[indexKey]
	if currentIdx < 0 {
		currentIdx = -1
	}
	nextIdx := (currentIdx + 1) % len(candidateProxies)
	r.indices[indexKey] = nextIdx
	
	// 设置并返回当前代理
	r.currentProxy = candidateProxies[nextIdx]
	return r.currentProxy
}

// GetCurrentProxy 获取当前正在使用的代理
func (r *Rotator) GetCurrentProxy() *Proxy {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	// 检查当前代理是否仍然有效
	if r.currentProxy != nil && r.currentProxy.Status != "Working" {
		// 需要在写锁下重置
		r.lock.RUnlock()
		r.lock.Lock()
		defer r.lock.Unlock()
		defer r.lock.RLock()
		
		if r.currentProxy != nil && r.currentProxy.Status != "Working" {
			r.currentProxy = nil
		}
	}
	
	return r.currentProxy
}

// SetCurrentProxyByAddress 根据地址手动设置当前代理
func (r *Rotator) SetCurrentProxyByAddress(address string) *Proxy {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range r.allProxies {
		if p.Address == address && p.Status == "Working" {
			r.currentProxy = p
			return p
		}
	}
	return nil
}

// GetAllProxies 获取所有代理
func (r *Rotator) GetAllProxies() []*Proxy {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	// 返回副本
	dst := make([]*Proxy, len(r.allProxies))
	copy(dst, r.allProxies)
	return dst
}

// 以下是兼容旧接口的方法

// SetRawProxies 设置原始代理列表
func (r *Rotator) SetRawProxies(proxies []*Proxy) {
	r.Clear()
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range proxies {
		// 初始化字段
		if p.Status == "" {
			p.Status = ""
		}
		p.Consecutive = 0
		p.LastCheck = time.Time{}
		p.LastChecked = time.Time{} // 兼容字段
		
		r.allProxies = append(r.allProxies, p)
		r.rawProxies = append(r.rawProxies, p)
	}
}

// GetValidProxies 获取所有有效代理
func (r *Rotator) GetValidProxies() ([]*Proxy, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	var validProxies []*Proxy
	for _, p := range r.allProxies {
		if p.Status == "Working" {
			validProxies = append(validProxies, p)
		}
	}
	return validProxies, nil
}

// SetValidProxies 设置有效代理列表
func (r *Rotator) SetValidProxies(proxies []*Proxy) error {
	r.Clear()
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range proxies {
		p.Status = "Working"
		r.allProxies = append(r.allProxies, p)
		r.validProxies = append(r.validProxies, p)
	}
	return nil
}

// AddValidProxies 添加有效代理到列表
func (r *Rotator) AddValidProxies(proxies []*Proxy) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range proxies {
		p.Status = "Working"
		
		// 检查是否已存在
		exists := false
		for _, existing := range r.allProxies {
			if existing.Address == p.Address {
				exists = true
				existing.Status = "Working"
				// 更新兼容字段
				existing.SuccessCount++
				break
			}
		}
		
		if !exists {
			r.allProxies = append(r.allProxies, p)
			r.validProxies = append(r.validProxies, p)
		}
	}
	return nil
}

// GetRawProxies 获取所有原始代理
func (r *Rotator) GetRawProxies() ([]*Proxy, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	// 返回原始代理和无效代理的合并列表
	var rawProxies []*Proxy
	for _, p := range r.allProxies {
		if p.Status != "Working" {
			rawProxies = append(rawProxies, p)
		}
	}
	return rawProxies, nil
}

// AddRawProxies 添加原始代理到列表
func (r *Rotator) AddRawProxies(proxies []*Proxy) {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	for _, p := range proxies {
		// 检查是否已存在
		exists := false
		for _, existing := range r.allProxies {
			if existing.Address == p.Address {
				exists = true
				break
			}
		}
		
		if !exists {
			p.Status = ""
			p.Consecutive = 0
			p.LastCheck = time.Time{}
			p.LastChecked = time.Time{} // 兼容字段
			r.allProxies = append(r.allProxies, p)
			r.rawProxies = append(r.rawProxies, p)
		}
	}
}

// GetFilteredAndSortedProxies 获取经过筛选和排序的代理列表
func (r *Rotator) GetFilteredAndSortedProxies(maxLatency, minSpeed float64) ([]*Proxy, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	
	var filtered []*Proxy
	for _, p := range r.allProxies {
		if p.Status != "Working" {
			continue
		}
		
		// 延迟筛选
		if maxLatency > 0 && p.Latency > maxLatency {
			continue
		}
		
		// 速度筛选
		if minSpeed > 0 && p.Speed < minSpeed {
			continue
		}
		
		filtered = append(filtered, p)
	}
	
	// 排序：先按分数降序，然后按延迟升序
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Score != filtered[j].Score {
			return filtered[i].Score > filtered[j].Score
		}
		return filtered[i].Latency < filtered[j].Latency
	})
	
	return filtered, nil
}

// GetValidProxyCount 获取有效代理数量
func (r *Rotator) GetValidProxyCount() int {
	return r.GetActiveProxiesCount()
}

// CleanupProxies 清理失效代理（兼容旧接口）
func (r *Rotator) CleanupProxies(maxAge time.Duration) {
	r.lock.Lock()
	defer r.lock.Unlock()
	
	var valid []*Proxy
	var invalid []*Proxy
	
	for _, p := range r.allProxies {
		// 计算指数退避时间
		backoffTime := time.Duration(1<<uint(p.Consecutive)) * time.Minute
		nextCheckTime := p.LastCheck.Add(backoffTime)
		
		// 保留代理条件
		if p.Consecutive < 5 &&
			time.Since(p.LastCheck) <= maxAge &&
			(p.Consecutive == 0 || time.Now().After(nextCheckTime)) {
			valid = append(valid, p)
		} else {
			invalid = append(invalid, p)
		}
	}
	
	// 更新代理列表
	r.allProxies = valid
	
	// 更新兼容字段
	r.validProxies = []*Proxy{}
	r.rawProxies = []*Proxy{}
	for _, p := range valid {
		if p.Status == "Working" {
			r.validProxies = append(r.validProxies, p)
		} else {
			r.rawProxies = append(r.rawProxies, p)
		}
	}
	for _, p := range invalid {
		r.rawProxies = append(r.rawProxies, p)
	}
}
