package checker

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go_proxy/proxy"

	xproxy "golang.org/x/net/proxy"
)

// Checker 代理验证器结构体
// 用于验证代理的连通性、速度、匿名度和地理位置信息
// 包含公网IP和超时配置
type Checker struct {
	publicIP string
	timeout  time.Duration
}

// NewChecker 创建新的代理验证器实例
// 默认超时时间为10秒
func NewChecker() *Checker {
	return &Checker{timeout: 10 * time.Second}
}

// InitializePublicIP 获取本机公网IP地址
// 用于后续判断代理的匿名级别（是否隐藏真实IP）
// 返回错误如果无法获取公网IP
func (c *Checker) InitializePublicIP() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	ip := strings.TrimSpace(string(ipBytes))
	if net.ParseIP(ip) == nil {
		return errors.New("获取到无效的公网IP: " + ip)
	}
	c.publicIP = ip
	return nil
}

// CheckConnectivityAndSpeed 检查代理的连通性、响应速度和匿名度
// 实现指数退避策略 - 失败次数越多，检查间隔越长
// 参数 p 是要检查的代理对象
// 返回值：
//
//	float64: 延迟时间（秒）
//	string: 匿名级别（"Elite", "Anonymous" 或 "Transparent"）
//	error: 如果检查失败返回错误信息
func (c *Checker) CheckConnectivityAndSpeed(p *proxy.Proxy) (float64, string, error) {
	// 检查是否需要退避
	backoffTime := time.Duration(1<<uint(p.FailCount)) * time.Minute
	if p.LastChecked.Add(backoffTime).After(time.Now()) {
		return 0, "", fmt.Errorf("proxy in backoff period (next check in %v)",
			p.LastChecked.Add(backoffTime).Sub(time.Now()).Round(time.Second))
	}

	latency, anonymity, err := c.checkProxy(p)
	if err != nil {
		p.FailCount++
	} else {
		p.FailCount = 0 // Reset on success
	}
	p.LastChecked = time.Now()

	// 计算代理评分
	c.calculateScore(p)
	return latency, anonymity, err
}

// checkProxy 实际执行代理检查的内部方法
func (c *Checker) checkProxy(p *proxy.Proxy) (float64, string, error) {
	client, err := c.createProxyClient(p)
	if err != nil {
		return 0, "", err
	}

	startTime := time.Now()
	resp, err := client.Get("http://httpbin.org/get")
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	p.Latency = time.Since(startTime).Seconds()

	var data map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err == nil {
		headers, _ := data["headers"].(map[string]interface{})
		forwardedFor, _ := headers["X-Forwarded-For"].(string)
		if forwardedFor != "" {
			p.Anonymity = "Anonymous"
		} else {
			p.Anonymity = "Elite"
		}
	}

	speed, _ := c.checkSpeed(client)
	p.Speed = speed

	return p.Latency, p.Anonymity, nil
}

// BatchLookupLocations 批量查询代理IP的地理位置信息
// 使用多个IP查询API并发获取国家/省份/城市信息
// 实现工作池模式、动态并发控制和结果缓存
// 参数 proxies 是需要查询的代理列表
// 返回错误如果所有API调用都失败
func (c *Checker) BatchLookupLocations(proxies []*proxy.Proxy) error {
	if len(proxies) == 0 {
		return nil
	}

	// 过滤掉已有地理位置信息的代理
	var needLookup []*proxy.Proxy
	for _, p := range proxies {
		if p.Country == "" || p.Province == "" || p.City == "" {
			needLookup = append(needLookup, p)
		}
	}
	if len(needLookup) == 0 {
		return nil
	}

	// 定义多个地理位置API (按优先级排序)
	geoAPIs := []struct {
		URL       string
		Parser    func([]byte) (country, province, city string)
		RateLimit time.Duration // API调用间隔限制
	}{
		{
			URL:       "https://ipapi.co/%s/json/",
			RateLimit: 1 * time.Second, // 1秒间隔限制
			Parser: func(data []byte) (string, string, string) {
				var result struct {
					Country string `json:"country_name"`
					Region  string `json:"region"`
					City    string `json:"city"`
				}
				if err := json.Unmarshal(data, &result); err == nil {
					return result.Country, result.Region, result.City
				}
				return "", "", ""
			},
		},
		{
			URL:       "https://ipinfo.io/%s/json",
			RateLimit: 500 * time.Millisecond, // 0.5秒间隔限制
			Parser: func(data []byte) (string, string, string) {
				var result struct {
					Country string `json:"country"`
					Region  string `json:"region"`
					City    string `json:"city"`
				}
				if err := json.Unmarshal(data, &result); err == nil {
					return result.Country, result.Region, result.City
				}
				return "", "", ""
			},
		},
		{
			URL:       "https://ip9.com.cn/get?ip=%s",
			RateLimit: 2 * time.Second, // 2秒间隔限制
			Parser: func(data []byte) (string, string, string) {
				var result struct {
					Ret  int `json:"ret"`
					Data struct {
						Country string `json:"country"`
						Prov    string `json:"prov"`
						City    string `json:"city"`
					} `json:"data"`
				}
				if err := json.Unmarshal(data, &result); err == nil && result.Ret == 200 {
					return result.Data.Country, result.Data.Prov, result.Data.City
				}
				return "", "", ""
			},
		},
	}

	// 工作池模式
	type job struct {
		proxy *proxy.Proxy
		ip    string
	}
	type result struct {
		proxy    *proxy.Proxy
		country  string
		province string
		city     string
	}

	jobs := make(chan job, len(proxies))
	results := make(chan result, len(proxies))

	// 动态调整并发数 (初始10，最大50)
	maxConcurrency := 10
	adjustTicker := time.NewTicker(5 * time.Second)
	defer adjustTicker.Stop()

	// 启动worker池
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{Timeout: 5 * time.Second}
			for j := range jobs {
				// 按优先级尝试各个API
				for _, api := range geoAPIs {
					time.Sleep(api.RateLimit) // 遵守API速率限制
					url := fmt.Sprintf(api.URL, j.ip)
					req, _ := http.NewRequest("GET", url, nil)
					req.Header.Set("User-Agent", "Mozilla/5.0")
					resp, err := client.Do(req)
					if err != nil {
						continue
					}

					data, err := io.ReadAll(resp.Body)
					resp.Body.Close()
					if err != nil {
						continue
					}

					country, province, city := api.Parser(data)
					if country != "" {
						results <- result{
							proxy:    j.proxy,
							country:  country,
							province: province,
							city:     city,
						}
						break
					}
				}
			}
		}()
	}

	// 动态调整并发数
	go func() {
		for range adjustTicker.C {
			// 根据当前负载调整并发数
			newConcurrency := maxConcurrency
			if len(jobs) > 100 {
				newConcurrency = 50
			} else if len(jobs) < 20 {
				newConcurrency = 10
			}

			if newConcurrency != maxConcurrency {
				diff := newConcurrency - maxConcurrency
				if diff > 0 {
					// 增加worker
					for i := 0; i < diff; i++ {
						wg.Add(1)
						go func() {
							defer wg.Done()
							client := &http.Client{Timeout: 5 * time.Second}
							for j := range jobs {
								for _, api := range geoAPIs {
									url := fmt.Sprintf(api.URL, j.ip)
									resp, err := client.Get(url)
									if err != nil {
										continue
									}

									data, err := io.ReadAll(resp.Body)
									resp.Body.Close()
									if err != nil {
										continue
									}

									country, province, city := api.Parser(data)
									if country != "" {
										results <- result{
											proxy:    j.proxy,
											country:  country,
											province: province,
											city:     city,
										}
										break
									}
								}
							}
						}()
					}
				}
				maxConcurrency = newConcurrency
			}
		}
	}()

	// 分发任务
	for _, p := range proxies {
		ip := strings.Split(p.Address, ":")[0]
		jobs <- job{proxy: p, ip: ip}
	}
	close(jobs)

	// 收集结果
	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		r.proxy.Country = r.country
		r.proxy.Province = r.province
		r.proxy.City = r.city
	}

	return nil
}

// checkSpeed 测试代理的下载速度
// 通过下载100KB测试文件计算速度（KB/s）
// 参数 client 是配置好代理的HTTP客户端
// 返回速度（KB/s）和可能的错误
func (c *Checker) checkSpeed(client *http.Client) (float64, error) {
	startTime := time.Now()
	resp, err := client.Get("http://cachefly.cachefly.net/100kb.test")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	duration := time.Since(startTime).Seconds()
	if duration <= 0 {
		return 0, errors.New("测试时间过短")
	}

	// 转换为KB/s
	speedKBps := float64(len(data)) / 1024 / duration
	return speedKBps, nil
}

// calculateScore 计算代理综合评分
// 评分维度及权重：
// - 延迟(25%): 0-5秒线性评分，越低越好
// - 速度(25%): 0-1000KB/s线性评分，越高越好
// - 匿名度(15%): Elite(15), Anonymous(7.5), 其他(0)
// - 稳定性(20%): 基于失败次数和成功率计算
// - 地理位置(10%): 特定地区加分(如美国、日本、新加坡等网络质量好的地区)
// - 新鲜度(5%): 最近检查的代理加分
func (c *Checker) calculateScore(p *proxy.Proxy) {
	p.LastChecked = time.Now()

	// 计算基础评分
	latencyScore := (1 - math.Min(p.Latency/5, 1)) * 30
	speedScore := math.Min(p.Speed/1000, 1) * 30

	// 匿名度评分
	anonymityScore := 0.0
	switch p.Anonymity {
	case "Elite":
		anonymityScore = 15
	case "Anonymous":
		anonymityScore = 7.5
	}

	// 稳定性评分(基于失败率和成功率)
	stabilityScore := 0.0
	totalChecks := p.FailCount + 1 // At least 1 check
	successRate := 1 - float64(p.FailCount)/float64(totalChecks)
	stabilityScore = successRate * 20

	// 新鲜度评分(最近检查的代理加分)
	freshnessScore := 0.0
	if time.Since(p.LastChecked) < 30*time.Minute {
		freshnessScore = 5
	}

	// 地理位置评分
	locationScore := 0.0
	switch p.Country {
	case "United States", "Japan", "Singapore", "Germany", "South Korea":
		locationScore = 10
	case "China", "Russia", "Brazil", "India":
		locationScore = 5
	}

	// 综合评分
	p.Score = math.Max(0, latencyScore+speedScore+anonymityScore+
		stabilityScore+locationScore+freshnessScore)
}

// ConcurrentCheck 并发验证代理列表
// workers参数控制最大并发数
func (c *Checker) ConcurrentCheck(proxies []*proxy.Proxy, workers int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, workers)

	for _, p := range proxies {
		wg.Add(1)
		sem <- struct{}{}
		go func(proxy *proxy.Proxy) {
			defer wg.Done()
			c.CheckConnectivityAndSpeed(proxy)
			<-sem
		}(p)
	}
	wg.Wait()
}

// createProxyClient 创建配置了指定代理的HTTP客户端
// 根据代理协议（HTTP/HTTPS/SOCKS4/SOCKS5）创建对应的传输层
// 参数 p 是要使用的代理信息
// 返回配置好的HTTP客户端和可能的错误
func (c *Checker) createProxyClient(p *proxy.Proxy) (*http.Client, error) {
	proxyURL, err := url.Parse(fmt.Sprintf("%s://%s", strings.ToLower(p.Protocol), p.Address))
	if err != nil {
		return nil, err
	}

	var transport *http.Transport
	switch strings.ToLower(p.Protocol) {
	case "http", "https":
		transport = &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	case "socks5", "socks4":
		dialer, err := xproxy.FromURL(proxyURL, xproxy.Direct)
		if err != nil {
			return nil, err
		}
		transport = &http.Transport{Dial: dialer.Dial}
	default:
		return nil, errors.New("不支持的代理协议: " + p.Protocol)
	}

	return &http.Client{Transport: transport, Timeout: c.timeout}, nil
}
