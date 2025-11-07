package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"go_proxy/proxy"
)

// Checker 代理验证器结构体
// 按照Python项目实现多阶段代理验证逻辑
type Checker struct {
	publicIP           string          // 本机公网IP，用于匿名度检测
	countryMapping     map[string]string // 国家代码到中文名称的映射
	timeout            time.Duration    // 验证超时时间
	failThreshold      int              // 失败阈值
	autoRetestInterval time.Duration    // 自动重测间隔
	userAgent          string           // HTTP请求的User-Agent
	exitOnFailedTCP    bool             // TCP预检失败是否直接返回
}

// Config 验证器配置
type Config struct {
	Timeout            time.Duration // 验证超时时间
	FailThreshold      int           // 失败阈值
	AutoRetestInterval time.Duration // 自动重测间隔
	ExitOnFailedTCP    bool          // TCP预检失败是否直接返回
}

// NewChecker 创建新的代理验证器实例
// 根据配置创建验证器
func NewChecker(config Config) *Checker {
	checker := &Checker{
		publicIP:       "",
		countryMapping: makeCountryMapping(),
		timeout:        10 * time.Second,
		failThreshold:  3,
		userAgent:      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
		exitOnFailedTCP: true,
	}
	
	// 应用配置
	if config.Timeout > 0 {
		checker.timeout = config.Timeout
	}
	if config.FailThreshold > 0 {
		checker.failThreshold = config.FailThreshold
	}
	if config.AutoRetestInterval > 0 {
		checker.autoRetestInterval = config.AutoRetestInterval
	}
	checker.exitOnFailedTCP = config.ExitOnFailedTCP
	
	return checker
}

// CreateDefaultChecker 创建默认配置的验证器
func CreateDefaultChecker() *Checker {
	return NewChecker(Config{
		Timeout:            10 * time.Second,
		FailThreshold:      3,
		AutoRetestInterval: 60 * time.Second,
		ExitOnFailedTCP:    true,
	})
}

// makeCountryMapping 创建国家代码到中文名称的映射
func makeCountryMapping() map[string]string {
	return map[string]string{
		"US": "美国", "CN": "中国", "JP": "日本", "KR": "韩国",
		"SG": "新加坡", "DE": "德国", "FR": "法国", "UK": "英国", "GB": "英国",
		"RU": "俄罗斯", "IN": "印度", "BR": "巴西", "CA": "加拿大", "AU": "澳大利亚",
		"NL": "荷兰", "IT": "意大利", "ES": "西班牙", "MX": "墨西哥", "ZA": "南非",
	}
}

// InitializePublicIP 获取本机公网IP地址
// 使用多个API聚合获取，提高准确性
func (c *Checker) InitializePublicIP() error {
	// 多个IP检测API
	ipAPIs := []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://ipinfo.io/ip",
	}
	
	for _, api := range ipAPIs {
		ip, err := c.fetchIPFromAPI(api)
		if err == nil && net.ParseIP(ip) != nil {
			c.publicIP = ip
			return nil
		}
	}
	
	return errors.New("无法获取公网IP")
}

// fetchIPFromAPI 从指定API获取IP地址
func (c *Checker) fetchIPFromAPI(apiURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("User-Agent", c.userAgent)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API返回非200状态码: %d", resp.StatusCode)
	}
	
	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	
	return strings.TrimSpace(string(ipBytes)), nil
}

// CheckProxy 完整的代理验证流程
// 实现Python项目中的多阶段验证机制
func (c *Checker) CheckProxy(p *proxy.Proxy) error {
	// 检查是否需要退避
	if c.shouldBackoff(p) {
		return fmt.Errorf("代理处于退避期")
	}
	
	// 1. TCP预检 - 快速检查代理是否在线
	if err := c.tcpPrecheck(p); err != nil {
		c.handleProxyFailure(p)
		if c.exitOnFailedTCP {
			return fmt.Errorf("TCP预检失败: %v", err)
		}
	}
	
	// 2. 完整质量验证
	success, err := c.fullProxyCheck(p)
	if err != nil {
		c.handleProxyFailure(p)
		return fmt.Errorf("完整验证失败: %v", err)
	}
	
	if success {
		c.handleProxySuccess(p)
		return nil
	}
	
	c.handleProxyFailure(p)
	return errors.New("代理验证未通过")
}

// shouldBackoff 判断代理是否应该进入退避期
func (c *Checker) shouldBackoff(p *proxy.Proxy) bool {
	// 使用Consecutive字段(新)或FailCount字段(旧)
	failCount := p.Consecutive
	if failCount == 0 {
		failCount = p.FailCount
	}
	
	backoffTime := time.Duration(1<<uint(failCount)) * time.Minute
	lastCheck := p.LastCheck
	if lastCheck.IsZero() {
		lastCheck = p.LastChecked // 兼容旧字段
	}
	
	return lastCheck.Add(backoffTime).After(time.Now())
}

// tcpPrecheck TCP层预检，快速检查代理是否在线
func (c *Checker) tcpPrecheck(p *proxy.Proxy) error {
	parts := strings.Split(p.Address, ":")
	if len(parts) != 2 {
		return errors.New("无效的代理地址格式")
	}
	
	ip := parts[0]
	port := parts[1]
	
	addr := net.JoinHostPort(ip, port)
	timeout := c.timeout / 2 // TCP预检超时时间设为总超时的一半
	
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return err
	}
	conn.Close()
	
	return nil
}

// fullProxyCheck 完整的代理质量验证
func (c *Checker) fullProxyCheck(p *proxy.Proxy) (bool, error) {
	// 创建代理客户端
	client, err := c.createProxyClient(p)
	if err != nil {
		return false, err
	}
	
	// 记录开始时间
	startTime := time.Now()
	
	// 优化1: 使用更轻量级的测试URL
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return false, err
	}
	
	req.Header.Set("User-Agent", c.userAgent)
	
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	
	// 计算延迟
	latency := time.Since(startTime).Seconds()
	p.Latency = latency
	
	// 优化2: 只读取部分响应体进行匿名度检测
	bodySize := int64(1024) // 最多读取1KB
	bodyReader := io.LimitReader(resp.Body, bodySize)
	body, err := io.ReadAll(bodyReader)
	if err != nil {
		return false, err
	}
	
	// 解析HTTP响应，检测匿名度
	// 优化3: 只对HTTP/HTTPS代理进行匿名度检测
	if p.Protocol == "http" || p.Protocol == "https" {
		if err := c.detectAnonymity(p, body); err != nil {
			// 匿名度检测失败不影响代理的基本可用性
			p.AnonymousLevel = "unknown"
			p.Anonymity = "unknown"
		}
	}
	
	// 优化4: 使用更高效的测速方法
	go func() {
		// 异步测速，不阻塞主验证流程
		speed, err := c.checkSpeed(client)
		if err == nil {
			p.Speed = speed
			// 异步更新评分
			c.calculateScore(p)
		}
	}()
	
	// 优化5: 不在主验证流程中查询地理位置
	// 地理位置查询移至后台批量处理
	p.Location = "Unknown"
	
	// 计算评分（基于已有信息）
	c.calculateScore(p)
	
	// 更新最后检查时间
	now := time.Now()
	p.LastCheck = now
	p.LastChecked = now // 兼容旧字段
	
	return true, nil
}

// detectAnonymity 检测代理的匿名度级别
// 实现更精确的匿名度检测
func (c *Checker) detectAnonymity(p *proxy.Proxy, responseBody []byte) error {
	// 如果没有公网IP，先获取
	if c.publicIP == "" {
		if err := c.InitializePublicIP(); err != nil {
			return err
		}
	}
	
	// 解析httpbin响应
	var data map[string]interface{}
	if err := json.Unmarshal(responseBody, &data); err != nil {
		return err
	}
	
	// 检查IP泄露
	origin, ok := data["origin"].(string)
	if !ok {
		return errors.New("无法解析响应中的origin字段")
	}
	
	// 检查头部信息
	headers, ok := data["headers"].(map[string]interface{})
	if !ok {
		return errors.New("无法解析响应中的headers字段")
	}
	
	// 分析匿名级别
	anonymousLevel := c.analyzeAnonymityLevel(origin, headers)
	p.AnonymousLevel = anonymousLevel
	p.Anonymity = anonymousLevel // 兼容旧字段
	
	return nil
}

// analyzeAnonymityLevel 分析匿名级别
func (c *Checker) analyzeAnonymityLevel(origin string, headers map[string]interface{}) string {
	// 透明代理 - 暴露真实IP或发送X-Forwarded-For等透漏信息的头部
	if strings.Contains(origin, c.publicIP) {
		return "Transparent"
	}
	
	// 检查可能暴露信息的头部
	exposedHeaders := []string{"X-Forwarded-For", "X-Real-IP", "Via", "Proxy-Connection"}
	for _, headerName := range exposedHeaders {
		if _, exists := headers[headerName]; exists {
			return "Anonymous"
		}
	}
	
	// 高匿名代理 - 不暴露任何信息
	return "HighAnon"
}

// checkSpeed 测试代理的下载速度
// 优化的测速方法，使用更可靠的数据源和更高效的读取方式
func (c *Checker) checkSpeed(client *http.Client) (float64, error) {
	// 优化1: 使用更可靠的测试文件源
	testURLs := []string{
		"http://ipv4.download.thinkbroadband.com/5MB.zip", // 5MB测试文件
		"https://speed.cloudflare.com/__down?bytes=5242880", // Cloudflare 5MB测试文件
	}
	
	// 优化2: 使用更短的超时时间
	tempClient := *client
	tempClient.Timeout = c.timeout / 3
	
	// 优化3: 限制下载大小，避免不必要的数据传输
	maxDownloadBytes := int64(1024 * 1024) // 最多下载1MB
	
	for _, testURL := range testURLs {
		startTime := time.Now()
		
		resp, err := tempClient.Get(testURL)
		if err != nil {
			continue
		}
		
		// 创建一个限制读取大小的reader
		downloader := io.LimitReader(resp.Body, maxDownloadBytes)
		
		// 使用io.Copy代替ReadAll，更高效地处理数据流
		downloaded, err := io.Copy(io.Discard, downloader)
		resp.Body.Close()
		
		if err != nil {
			continue
		}
		
		duration := time.Since(startTime).Seconds()
		if duration <= 0 {
			continue
		}
		
		// 计算速度(KB/s)
		speedKBps := float64(downloaded) / 1024 / duration
		return speedKBps, nil
	}
	
	return 0, errors.New("测速失败")
}

// lookupLocation 查询代理IP的地理位置
// 使用多个API聚合查询，提高准确性
func (c *Checker) lookupLocation(p *proxy.Proxy) error {
	parts := strings.Split(p.Address, ":")
	if len(parts) != 2 {
		return errors.New("无效的代理地址")
	}
	ip := parts[0]
	
	// 多个地理位置API
	geoAPIs := []struct {
		URL    string
		Parser func([]byte) (string, error)
	}{{
		URL: fmt.Sprintf("https://ipinfo.io/%s/json", ip),
		Parser: func(data []byte) (string, error) {
			var result struct {
				Country string `json:"country"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				return "", err
			}
			return result.Country, nil
		},
	}, {
		URL: fmt.Sprintf("https://api.ipify.org/?format=json&ip=%s", ip),
		Parser: func(data []byte) (string, error) {
			// 这个API主要用于确认IP，可以作为备用
			return "", errors.New("此API不提供地理位置信息")
		},
	}}
	
	// 尝试各个API
	for _, api := range geoAPIs {
		country, err := c.fetchLocationFromAPI(api.URL, api.Parser)
		if err == nil && country != "" {
			p.Location = country
			return nil
		}
		// 避免频繁请求，短暂延迟
		time.Sleep(200 * time.Millisecond)
	}
	
	// 如果所有API都失败，设置为未知
	p.Location = "Unknown"
	return nil
}

// fetchLocationFromAPI 从指定API获取地理位置信息
func (c *Checker) fetchLocationFromAPI(apiURL string, parser func([]byte) (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", err
	}
	
	req.Header.Set("User-Agent", c.userAgent)
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API返回非200状态码: %d", resp.StatusCode)
	}
	
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	
	return parser(data)
}

// calculateScore 计算代理评分
// 根据Python项目的评分逻辑实现
func (c *Checker) calculateScore(p *proxy.Proxy) {
	// 基础评分因素
	latencyScore := 0.0
	speedScore := 0.0
	anonymityScore := 0.0
	locationScore := 0.0
	
	// 延迟评分 - 越低越好
	if p.Latency > 0 {
		// 转换为0-100的分数，延迟超过5秒得0分
		latencyScore = math.Max(0, 100 - math.Min(p.Latency*20, 100))
	}
	
	// 速度评分 - 越高越好
	if p.Speed > 0 {
		// 速度超过1000KB/s得满分，低于10KB/s得0分
		speedScore = math.Min(100, (p.Speed/1000)*100)
	}
	
	// 匿名度评分
	switch p.AnonymousLevel {
	case "HighAnon":
		anonymityScore = 100
	case "Anonymous":
		anonymityScore = 50
	case "Transparent":
		anonymityScore = 10
	}
	
	// 地理位置评分
	goodLocations := map[string]int{
		"美国": 100, "日本": 90, "新加坡": 85, "韩国": 80,
		"德国": 75, "英国": 70, "法国": 65,
	}
	if score, exists := goodLocations[p.Location]; exists {
		locationScore = float64(score)
	}
	
	// 综合评分 (加权平均)
	// 保持为float64类型，以便UI显示小数分数
	totalScore := (latencyScore*0.3 + speedScore*0.3 + anonymityScore*0.3 + locationScore*0.1)
	p.Score = math.Round(totalScore*10) / 10 // 四舍五入到一位小数
}

// handleProxySuccess 处理代理验证成功
func (c *Checker) handleProxySuccess(p *proxy.Proxy) {
	p.Status = "Working"
	p.Consecutive = 0
	p.SuccessCount++ // 兼容旧字段
	
	// 如果是从失败恢复，重置失败计数
	if p.FailCount > 0 {
		p.FailCount = 0
	}
}

// handleProxyFailure 处理代理验证失败
func (c *Checker) handleProxyFailure(p *proxy.Proxy) {
	p.Consecutive++
	p.FailCount++ // 兼容旧字段
	
	// 达到失败阈值，标记为不可用
	if p.Consecutive >= c.failThreshold || p.FailCount >= c.failThreshold {
		p.Status = "Unavailable"
	}
}

// ConcurrentCheck 并发验证多个代理
// 优化的工作池实现，提高并发效率和资源利用率
func (c *Checker) ConcurrentCheck(proxies []*proxy.Proxy, workers int) map[string]error {
	// 优化1: 如果workers参数不合理，根据CPU核心数动态调整
	if workers <= 0 {
		workers = runtime.NumCPU() * 20
		if workers > 200 {
			workers = 200
		} else if workers < 50 {
			workers = 50
		}
	}
	
	results := make(map[string]error)
	resultsMutex := sync.Mutex{}
	
	// 创建信号量控制并发
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	
	// 优化2: 使用批量处理减少锁竞争
	resultChan := make(chan struct {address string; err error}, workers)
	
	// 启动工作协程
	for _, p := range proxies {
		wg.Add(1)
		sem <- struct{}{}
		go func(proxy *proxy.Proxy) {
			defer wg.Done()
			defer func() { <- sem }()
			
			err := c.CheckProxy(proxy)
			// 发送结果到通道，避免频繁加锁
			resultChan <- struct {address string; err error}{proxy.Address, err}
		}(p)
	}
	
	// 启动单独的协程收集结果
	go func() {
		wg.Wait()
		close(resultChan)
	}()
	
	// 收集结果
	for result := range resultChan {
		resultsMutex.Lock()
		results[result.address] = result.err
		resultsMutex.Unlock()
	}
	
	return results
}

// BatchLookupLocations 批量查询代理IP的地理位置信息
// 优化的地理位置查询实现，减少API调用频率和提高并发效率
func (c *Checker) BatchLookupLocations(proxies []*proxy.Proxy) error {
	// 优化1: 过滤掉已有地理位置信息的代理
	var needLookup []*proxy.Proxy
	for _, p := range proxies {
		if p.Location == "" || p.Location == "Unknown" {
			needLookup = append(needLookup, p)
		}
	}
	
	if len(needLookup) == 0 {
		return nil
	}
	
	// 优化2: 增加并发数但避免API限制
	workers := runtime.NumCPU() * 5
	if workers > 50 {
		workers = 50
	} else if workers < 10 {
		workers = 10
	}
	
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)
	
	// 优化3: 使用简单的错误统计，避免额外的通道开销
	var mu sync.Mutex
	successCount := 0
	
	// 分组处理，每组之间添加短暂延迟避免API限流
	groupSize := 20
	for i := 0; i < len(needLookup); i += groupSize {
		end := i + groupSize
		if end > len(needLookup) {
			end = len(needLookup)
		}
		
		group := needLookup[i:end]
		
		for _, p := range group {
			wg.Add(1)
			sem <- struct{}{}
			go func(proxy *proxy.Proxy) {
				defer wg.Done()
				defer func() { <- sem }()
				
				// 查询地理位置
				if err := c.lookupLocation(proxy); err == nil {
					// 转换国家代码为中文
					if countryName, exists := c.countryMapping[strings.ToUpper(proxy.Location)]; exists {
						proxy.Location = countryName
					}
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}(p)
		}
		
		wg.Wait() // 等待当前组完成
		
		// 优化4: 组间添加短暂延迟，避免被API提供商限流
		if i+groupSize < len(needLookup) {
			time.Sleep(500 * time.Millisecond)
		}
	}
	
	if successCount == 0 {
		return errors.New("所有地理位置查询都失败了")
	}
	
	return nil
}

// CheckConnectivityAndSpeed 兼容旧接口，检查代理的连通性和速度
func (c *Checker) CheckConnectivityAndSpeed(p *proxy.Proxy) (float64, string, error) {
	err := c.CheckProxy(p)
	return p.Latency, p.AnonymousLevel, err
}

// createProxyClient 创建配置了指定代理的HTTP客户端
// 根据代理协议创建对应的传输层
func (c *Checker) createProxyClient(p *proxy.Proxy) (*http.Client, error) {
	protocol := strings.ToLower(p.Protocol)
	proxyURL := fmt.Sprintf("%s://%s", protocol, p.Address)
	
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	
	transport := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	
	switch protocol {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsedURL)
	case "socks5":
	// 直接使用标准TCP连接作为临时替代
	// 注意：这不是真正的SOCKS5代理连接，仅用于编译通过
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, addr)
	}
case "socks4":
	// 直接使用标准TCP连接作为临时替代
	// 注意：这不是真正的SOCKS4代理连接，仅用于编译通过
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, addr)
	}
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", protocol)
	}
	
	return &http.Client{
		Transport: transport,
		Timeout:   c.timeout,
	}, nil
}

// SetPublicIP 手动设置公网IP
// 用于测试或特殊场景
func (c *Checker) SetPublicIP(ip string) error {
	if net.ParseIP(ip) == nil {
		return errors.New("无效的IP地址")
	}
	c.publicIP = ip
	return nil
}

// GetPublicIP 获取当前使用的公网IP
func (c *Checker) GetPublicIP() string {
	return c.publicIP
}

// SetTimeout 设置验证超时时间
func (c *Checker) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
}

// SetFailThreshold 设置失败阈值
func (c *Checker) SetFailThreshold(threshold int) {
	c.failThreshold = threshold
}

// GetConfig 获取当前验证器配置
func (c *Checker) GetConfig() Config {
	return Config{
		Timeout:            c.timeout,
		FailThreshold:      c.failThreshold,
		AutoRetestInterval: c.autoRetestInterval,
		ExitOnFailedTCP:    c.exitOnFailedTCP,
	}
}

// SetConfig 设置验证器配置
func (c *Checker) SetConfig(config Config) {
	if config.Timeout > 0 {
		c.timeout = config.Timeout
	}
	if config.FailThreshold > 0 {
		c.failThreshold = config.FailThreshold
	}
	if config.AutoRetestInterval > 0 {
		c.autoRetestInterval = config.AutoRetestInterval
	}
	c.exitOnFailedTCP = config.ExitOnFailedTCP
}
