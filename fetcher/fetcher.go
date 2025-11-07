package fetcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go_proxy/proxy"

	"github.com/PuerkitoBio/goquery"
)

// ProxySource 代理源结构体
type ProxySource struct {
	URL      string
	Protocol string
	IsAPI    bool
	Parser   string // 解析器类型，对应不同网站的特定解析方法
}

// ProxyFetcher 代理获取器结构体
// 按照Python项目的fetcher.py实现
// 提供多协议代理源和专用解析方法
type ProxyFetcher struct {
	userAgent    string        // HTTP请求的User-Agent
	timeout      time.Duration // 请求超时时间
	retryCount   int           // 请求失败重试次数
	retryDelay   time.Duration // 重试间隔
	sourceLock   sync.RWMutex  // 代理源列表读写锁
	customSources []ProxySource // 用户自定义代理源
}

// Config 获取器配置
type Config struct {
	Timeout    time.Duration
	RetryCount int
	RetryDelay time.Duration
	UserAgent  string
}

// NewProxyFetcher 创建新的代理获取器实例
func NewProxyFetcher(config Config) *ProxyFetcher {
	fetcher := &ProxyFetcher{
		timeout:      15 * time.Second,
		retryCount:   3,
		retryDelay:   2 * time.Second,
		customSources: make([]ProxySource, 0),
		userAgent:    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	}
	
	// 应用配置
	if config.Timeout > 0 {
		fetcher.timeout = config.Timeout
	}
	if config.RetryCount > 0 {
		fetcher.retryCount = config.RetryCount
	}
	if config.RetryDelay > 0 {
		fetcher.retryDelay = config.RetryDelay
	}
	if config.UserAgent != "" {
		fetcher.userAgent = config.UserAgent
	}
	
	return fetcher
}

// CreateDefaultFetcher 创建默认配置的代理获取器
func CreateDefaultFetcher() *ProxyFetcher {
	return NewProxyFetcher(Config{
		Timeout:    15 * time.Second,
		RetryCount: 3,
		RetryDelay: 2 * time.Second,
	})
}

// GetDefaultSources 返回内置代理源列表
// 按照Python项目的源配置组织
func GetDefaultSources() []ProxySource {
	return []ProxySource{
		// HTTP代理API
		{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=http", "http", true, "text"},
		{"https://openproxylist.xyz/http.txt", "http", true, "text"},
		{"https://www.proxy-list.download/api/v1/get?type=http", "http", true, "text"},
		{"https://proxylist.geonode.com/api/proxy-list?limit=500&page=1&sort_by=lastChecked&sort_type=desc&protocols=http", "http", true, "geonode"},
		{"https://www.proxyscan.io/api/proxy?type=http&format=txt", "http", true, "text"},
		{"https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/http.txt", "http", true, "text"},
		{"http://77.93.157.21:3030/fetch_all", "http", true, "text"},
		{"http://199.245.100.84:5000/fetch_all", "http", true, "text"},
		{"http://123.117.160.38:5000/fetch_all", "http", true, "text"},
		{"http://142.171.31.40:5010/fetch_all", "http", true, "text"},
		{"http://120.46.21.7:5000/fetch_all", "http", true, "text"},
		{"http://www.66ip.cn/nmtq.php?get_num=300&isp=0&anonym=0&type=2", "http", true, "66ip"},
		{"http://proxylist.fatezero.org/proxy.list", "http", true, "fatezero"},
		
		// HTTP代理网页
		{"https://free-proxy-list.net/", "http", false, "freeproxylist"},
		{"http://www.kxdaili.com/dailiip/1/1.html", "http", false, "kxdaili"},
		{"https://www.us-proxy.org/", "http", false, "freeproxylist"},
		{"https://www.socks-proxy.net/", "http", false, "freeproxylist"},
		// 新增国内代理源
		{"https://www.kuaidaili.com/free/inha/1/", "http", false, "kuaidaili"},
		{"http://www.ip3366.net/free/?stype=1&page=1", "http", false, "ip3366"},
		{"https://www.89ip.cn/index_1.html", "http", false, "89ip"},
		
		// HTTPS代理
		{"https://www.proxy-list.download/api/v1/get?type=https", "https", true, "text"},
		
		// SOCKS4代理
		{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=socks4", "socks4", true, "text"},
		{"https://openproxylist.xyz/socks4.txt", "socks4", true, "text"},
		{"https://www.proxy-list.download/api/v1/get?type=socks4", "socks4", true, "text"},
		
		// SOCKS5代理
		{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=socks5", "socks5", true, "text"},
		{"https://openproxylist.xyz/socks5.txt", "socks5", true, "text"},
		{"https://www.proxy-list.download/api/v1/get?type=socks5", "socks5", true, "text"},
		{"https://www.proxyscan.io/api/proxy?type=socks5&format=txt", "socks5", true, "text"},
	}
}

// AddCustomSource 添加自定义代理源
func (f *ProxyFetcher) AddCustomSource(source ProxySource) {
	f.sourceLock.Lock()
	defer f.sourceLock.Unlock()
	f.customSources = append(f.customSources, source)
}

// RemoveCustomSource 移除自定义代理源
func (f *ProxyFetcher) RemoveCustomSource(url string) bool {
	f.sourceLock.Lock()
	defer f.sourceLock.Unlock()
	
	for i, source := range f.customSources {
		if source.URL == url {
			// 删除该源
			f.customSources = append(f.customSources[:i], f.customSources[i+1:]...)
			return true
		}
	}
	return false
}

// GetCustomSources 获取所有自定义代理源
func (f *ProxyFetcher) GetCustomSources() []ProxySource {
	f.sourceLock.RLock()
	defer f.sourceLock.RUnlock()
	return append([]ProxySource(nil), f.customSources...) // 返回副本
}

// FetchAllProxies 从所有代理源获取代理列表
// 整合内置源和自定义源
func (f *ProxyFetcher) FetchAllProxies() ([]*proxy.Proxy, error) {
	// 获取所有源
	sources := GetDefaultSources()
	
	// 添加自定义源
	f.sourceLock.RLock()
	customSourcesCopy := append([]ProxySource(nil), f.customSources...)
	f.sourceLock.RUnlock()
	sources = append(sources, customSourcesCopy...)
	
	return f.fetchProxiesFromSources(sources)
}

// FetchProxiesByProtocol 按协议类型获取代理
func (f *ProxyFetcher) FetchProxiesByProtocol(protocol string) ([]*proxy.Proxy, error) {
	var filteredSources []ProxySource
	
	// 过滤内置源
	for _, source := range GetDefaultSources() {
		if strings.EqualFold(source.Protocol, protocol) {
			filteredSources = append(filteredSources, source)
		}
	}
	
	// 过滤自定义源
	f.sourceLock.RLock()
	for _, source := range f.customSources {
		if strings.EqualFold(source.Protocol, protocol) {
			filteredSources = append(filteredSources, source)
		}
	}
	f.sourceLock.RUnlock()
	
	return f.fetchProxiesFromSources(filteredSources)
}

// fetchProxiesFromSources 从指定的代理源列表获取代理
func (f *ProxyFetcher) fetchProxiesFromSources(sources []ProxySource) ([]*proxy.Proxy, error) {
	var wg sync.WaitGroup
	proxyChan := make(chan []*proxy.Proxy, len(sources))
	
	// 限制并发数
	maxConcurrency := 10
	sem := make(chan struct{}, maxConcurrency)
	
	for _, source := range sources {
		wg.Add(1)
		sem <- struct{}{}
		go func(s ProxySource) {
			defer wg.Done()
			defer func() { <-sem }()
			
			proxies, err := f.fetchFromSourceWithRetry(s)
			if err != nil {
				log.Printf("Error fetching from %s: %v", s.URL, err)
				proxyChan <- []*proxy.Proxy{}
				return
			}
			proxyChan <- proxies
		}(source)
	}
	
	go func() {
		wg.Wait()
		close(proxyChan)
	}()
	
	// 合并结果并去重
	allProxies := make([]*proxy.Proxy, 0)
	seen := make(map[string]bool)
	
	for p := range proxyChan {
		for _, proxyItem := range p {
			if !seen[proxyItem.Address] {
				seen[proxyItem.Address] = true
				allProxies = append(allProxies, proxyItem)
			}
		}
	}
	
	if len(allProxies) == 0 {
		return nil, fmt.Errorf("failed to fetch any proxies from all sources")
	}
	
	return allProxies, nil
}

// fetchFromSourceWithRetry 带重试的代理源获取
func (f *ProxyFetcher) fetchFromSourceWithRetry(source ProxySource) ([]*proxy.Proxy, error) {
	var proxies []*proxy.Proxy
	var err error
	
	for attempt := 0; attempt <= f.retryCount; attempt++ {
		if attempt > 0 {
			time.Sleep(f.retryDelay)
			log.Printf("Retrying fetch from %s (attempt %d/%d)", source.URL, attempt, f.retryCount)
		}
		
		proxies, err = f.fetchFromSource(source)
		if err == nil && len(proxies) > 0 {
			break
		}
	}
	
	return proxies, err
}

// fetchFromSource 从单个代理源获取代理
func (f *ProxyFetcher) fetchFromSource(source ProxySource) ([]*proxy.Proxy, error) {
	// 创建HTTP客户端
	client := &http.Client{Timeout: f.timeout}
	
	// 创建请求
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()
	
	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.8,en-US;q=0.5,en;q=0.3")
	
	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s from %s", resp.Status, source.URL)
	}
	
	// 复制响应体，以便多次读取
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	
	// 根据解析器类型选择相应的解析方法
	switch source.Parser {
	case "freeproxylist":
		return f.parseFreeProxyList(string(body), source.Protocol)
	case "kxdaili":
		return f.parseKXDaili(string(body), source.Protocol)
	case "66ip":
		return f.parse66IP(string(body), source.Protocol)
	case "fatezero":
		return f.parseFateZero(string(body))
	case "geonode":
		return f.parseGeoNode(string(body), source.Protocol)
	case "kuaidaili":
		return f.parseKuaiDaiLi(string(body), source.Protocol)
	case "ip3366":
		return f.parseIP3366(string(body), source.Protocol)
	case "89ip":
		return f.parse89IP(string(body), source.Protocol)
	case "text":
		return f.parseTextResponse(body, source.Protocol)
	default:
		// 使用通用解析器
		if source.IsAPI {
			return f.parseAPIResponse(body, source.Protocol)
		}
		return f.parseHTMLResponse(body, source.Protocol)
	}
}

// parseFreeProxyList 解析free-proxy-list.net类网站
func (f *ProxyFetcher) parseFreeProxyList(html string, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	
	var proxies []*proxy.Proxy
	
	// 解析表格行
	doc.Find("table tbody tr").Each(func(i int, s *goquery.Selection) {
		// 获取IP和端口
		ip := s.Find("td:nth-child(1)").Text()
		portText := s.Find("td:nth-child(2)").Text()
		port, err := strconv.Atoi(portText)
		if err != nil || ip == "" || port <= 0 || port > 65535 {
			return
		}
		
		// 检查匿名度
		anonymity := s.Find("td:nth-child(5)").Text()
		var anonymousLevel string
		switch strings.ToLower(anonymity) {
		case "elite proxy":
			anonymousLevel = "HighAnon"
		case "anonymous":
			anonymousLevel = "Anonymous"
		default:
			anonymousLevel = "Transparent"
		}
		
		// 创建代理对象
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", ip, port),
			Protocol:       protocol,
			AnonymousLevel: anonymousLevel,
			Location:       "", // 后续会通过checker填充
			Status:         "Fresh",
		}
		
		proxies = append(proxies, proxyItem)
	})
	
	return proxies, nil
}

// parseKXDaili 解析开心代理网站
func (f *ProxyFetcher) parseKXDaili(html string, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy

	// 解析表格行
	doc.Find("table tbody tr").Each(func(i int, s *goquery.Selection) {
		// 获取IP和端口
		ip := s.Find("td:nth-child(1)").Text()
		portText := s.Find("td:nth-child(2)").Text()
		port, err := strconv.Atoi(portText)
		if err != nil || ip == "" || port <= 0 || port > 65535 {
			return
		}

		// 创建代理对象
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", ip, port),
			Protocol:       protocol,
			AnonymousLevel: "", // 无法直接从页面获取
			Location:       "", // 后续会通过checker填充
			Status:         "Fresh",
		}

		proxies = append(proxies, proxyItem)
	})

	return proxies, nil
}

// parseKuaiDaiLi 解析快代理网站的国内高匿代理
func (f *ProxyFetcher) parseKuaiDaiLi(content string, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy

	// 解析表格行
	doc.Find("table tbody tr").Each(func(i int, s *goquery.Selection) {
		// 获取IP和端口
		ip := s.Find("td:nth-child(1)").Text()
		portText := s.Find("td:nth-child(2)").Text()
		port, err := strconv.Atoi(strings.TrimSpace(portText))
		if err != nil || ip == "" || port <= 0 || port > 65535 {
			return
		}

		// 创建代理对象
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", ip, port),
			Protocol:       protocol,
			AnonymousLevel: "HighAnon", // 快代理声称提供高匿代理
			Location:       "",
			Status:         "Fresh",
		}

		proxies = append(proxies, proxyItem)
	})

	return proxies, nil
}

// parseIP3366 解析云代理(ip3366.net)的国内高匿代理
func (f *ProxyFetcher) parseIP3366(content string, protocol string) ([]*proxy.Proxy, error) {
	// 设置正确的编码处理
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy

	// 解析表格行
	table := doc.Find("table#list tbody")
	if table.Length() == 0 {
		// 尝试其他可能的选择器
		table = doc.Find("table tbody")
	}

	table.Find("tr").Each(func(i int, s *goquery.Selection) {
		// 获取IP和端口
		cols := s.Find("td")
		if cols.Length() < 2 {
			return
		}

		ip := strings.TrimSpace(cols.Eq(0).Text())
		portText := strings.TrimSpace(cols.Eq(1).Text())
		port, err := strconv.Atoi(portText)
		if err != nil || ip == "" || port <= 0 || port > 65535 {
			return
		}

		// 创建代理对象
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", ip, port),
			Protocol:       protocol,
			AnonymousLevel: "HighAnon", // 云代理声称提供高匿代理
			Location:       "",
			Status:         "Fresh",
		}

		proxies = append(proxies, proxyItem)
	})

	return proxies, nil
}

// parse89IP 解析89免费代理(89ip.cn)的代理
func (f *ProxyFetcher) parse89IP(content string, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy

	// 解析表格行
	table := doc.Find("table.layui-table tbody")
	if table.Length() == 0 {
		// 尝试其他可能的选择器
		table = doc.Find("table tbody")
	}

	table.Find("tr").Each(func(i int, s *goquery.Selection) {
		// 获取IP和端口
		cols := s.Find("td")
		if cols.Length() < 2 {
			return
		}

		ip := strings.TrimSpace(cols.Eq(0).Text())
		portText := strings.TrimSpace(cols.Eq(1).Text())
		port, err := strconv.Atoi(portText)
		if err != nil || ip == "" || port <= 0 || port > 65535 {
			return
		}

		// 创建代理对象
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", ip, port),
			Protocol:       protocol,
			AnonymousLevel: "",
			Location:       "",
			Status:         "Fresh",
		}

		proxies = append(proxies, proxyItem)
	})

	return proxies, nil
}

// parse66IP 解析66ip.cn网站
func (f *ProxyFetcher) parse66IP(content string, protocol string) ([]*proxy.Proxy, error) {
	// 66ip返回的是JavaScript代码，需要提取IP:端口列表
	regex := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)
	matches := regex.FindAllString(content, -1)
	
	var proxies []*proxy.Proxy
	for _, match := range matches {
		proxyItem := &proxy.Proxy{
			Address:        match,
			Protocol:       protocol,
			AnonymousLevel: "",
			Location:       "",
			Status:         "Fresh",
		}
		proxies = append(proxies, proxyItem)
	}
	
	return proxies, nil
}

// parseFateZero 解析fatezero.org的JSON格式代理列表
func (f *ProxyFetcher) parseFateZero(content string) ([]*proxy.Proxy, error) {
	var proxies []*proxy.Proxy
	
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		
		var proxyInfo struct {
			IP      string `json:"host"`
			Port    int    `json:"port"`
			Type    string `json:"type"`
			Anon    string `json:"anonymity"`
		}
		
		if err := json.Unmarshal([]byte(line), &proxyInfo); err != nil {
			continue
		}
		
		// 确定协议
		protocol := "http"
		if strings.Contains(proxyInfo.Type, "socks5") {
			protocol = "socks5"
		} else if strings.Contains(proxyInfo.Type, "socks4") {
			protocol = "socks4"
		}
		
		// 确定匿名级别
		anonymousLevel := "Transparent"
		switch proxyInfo.Anon {
		case "elite":
			anonymousLevel = "HighAnon"
		case "anonymous":
			anonymousLevel = "Anonymous"
		}
		
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", proxyInfo.IP, proxyInfo.Port),
			Protocol:       protocol,
			AnonymousLevel: anonymousLevel,
			Location:       "",
			Status:         "Fresh",
		}
		
		proxies = append(proxies, proxyItem)
	}
	
	return proxies, nil
}

// parseGeoNode 解析GeoNode API的JSON响应
func (f *ProxyFetcher) parseGeoNode(content string, protocol string) ([]*proxy.Proxy, error) {
	var response struct {
		Data []struct {
			IP    string `json:"ip"`
			Port  int    `json:"port"`
			Country string `json:"country"`
			AnonymityLevel string `json:"anonymityLevel"`
		} `json:"data"`
	}
	
	if err := json.Unmarshal([]byte(content), &response); err != nil {
		return nil, err
	}
	
	var proxies []*proxy.Proxy
	for _, item := range response.Data {
		// 转换匿名级别
		anonymousLevel := "Transparent"
		switch strings.ToLower(item.AnonymityLevel) {
		case "elite", "high_anon":
			anonymousLevel = "HighAnon"
		case "anonymous":
			anonymousLevel = "Anonymous"
		}
		
		proxyItem := &proxy.Proxy{
			Address:        fmt.Sprintf("%s:%d", item.IP, item.Port),
			Protocol:       protocol,
			AnonymousLevel: anonymousLevel,
			Location:       item.Country,
			Status:         "Fresh",
		}
		
		proxies = append(proxies, proxyItem)
	}
	
	return proxies, nil
}

// parseTextResponse 解析纯文本格式的代理列表
func (f *ProxyFetcher) parseTextResponse(content []byte, protocol string) ([]*proxy.Proxy, error) {
	lines := strings.Split(string(content), "\n")
	proxyRegex := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)
	
	var proxies []*proxy.Proxy
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if proxyRegex.MatchString(line) {
			proxyItem := &proxy.Proxy{
				Address:        line,
				Protocol:       protocol,
				AnonymousLevel: "",
				Location:       "",
				Status:         "Fresh",
			}
			proxies = append(proxies, proxyItem)
		}
	}
	
	return proxies, nil
}

// parseAPIResponse 解析通用API响应
func (f *ProxyFetcher) parseAPIResponse(content []byte, protocol string) ([]*proxy.Proxy, error) {
	// 尝试JSON格式
	var jsonResp struct {
		Data []struct {
			Ip   string `json:"ip"`
			Port int    `json:"port"`
		} `json:"data"`
	}
	
	if err := json.Unmarshal(content, &jsonResp); err == nil && len(jsonResp.Data) > 0 {
		proxies := make([]*proxy.Proxy, len(jsonResp.Data))
		for i, item := range jsonResp.Data {
			proxies[i] = &proxy.Proxy{
				Address:        fmt.Sprintf("%s:%d", item.Ip, item.Port),
				Protocol:       protocol,
				AnonymousLevel: "",
				Location:       "",
				Status:         "Fresh",
			}
		}
		return proxies, nil
	}
	
	// 尝试数组格式
	var jsonArray []struct {
		Ip   string `json:"ip"`
		Port int    `json:"port"`
	}
	
	if err := json.Unmarshal(content, &jsonArray); err == nil && len(jsonArray) > 0 {
		proxies := make([]*proxy.Proxy, len(jsonArray))
		for i, item := range jsonArray {
			proxies[i] = &proxy.Proxy{
				Address:        fmt.Sprintf("%s:%d", item.Ip, item.Port),
				Protocol:       protocol,
				AnonymousLevel: "",
				Location:       "",
				Status:         "Fresh",
			}
		}
		return proxies, nil
	}
	
	// 回退到纯文本解析
	return f.parseTextResponse(content, protocol)
}

// parseHTMLResponse 解析通用HTML响应
func (f *ProxyFetcher) parseHTMLResponse(content []byte, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(content)))
	if err != nil {
		// 如果解析HTML失败，回退到正则匹配
		return f.parseTextResponse(content, protocol)
	}
	
	var proxies []*proxy.Proxy
	proxyRegex := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)
	
	// 尝试从表格中提取
	doc.Find("table").Each(func(i int, table *goquery.Selection) {
		table.Find("tr").Each(func(j int, row *goquery.Selection) {
			text := row.Text()
			matches := proxyRegex.FindAllString(text, -1)
			for _, match := range matches {
				proxyItem := &proxy.Proxy{
					Address:        match,
					Protocol:       protocol,
					AnonymousLevel: "",
					Location:       "",
					Status:         "Fresh",
				}
				proxies = append(proxies, proxyItem)
			}
		})
	})
	
	// 如果没有找到，尝试从整个页面提取
	if len(proxies) == 0 {
		text := doc.Text()
		matches := proxyRegex.FindAllString(text, -1)
		for _, match := range matches {
			proxyItem := &proxy.Proxy{
				Address:        match,
				Protocol:       protocol,
				AnonymousLevel: "",
				Location:       "",
				Status:         "Fresh",
			}
			proxies = append(proxies, proxyItem)
		}
	}
	
	return proxies, nil
}

// ValidateProxySource 验证代理源是否有效
func (f *ProxyFetcher) ValidateProxySource(source ProxySource) (bool, error) {
	proxies, err := f.fetchFromSource(source)
	return len(proxies) > 0, err
}

// SetTimeout 设置请求超时时间
func (f *ProxyFetcher) SetTimeout(timeout time.Duration) {
	f.timeout = timeout
}

// SetRetryCount 设置重试次数
func (f *ProxyFetcher) SetRetryCount(count int) {
	f.retryCount = count
}

// SetRetryDelay 设置重试间隔
func (f *ProxyFetcher) SetRetryDelay(delay time.Duration) {
	f.retryDelay = delay
}

// SetUserAgent 设置User-Agent
func (f *ProxyFetcher) SetUserAgent(userAgent string) {
	f.userAgent = userAgent
}

// GetConfig 获取当前配置
func (f *ProxyFetcher) GetConfig() Config {
	return Config{
		Timeout:    f.timeout,
		RetryCount: f.retryCount,
		RetryDelay: f.retryDelay,
		UserAgent:  f.userAgent,
	}
}

// 兼容旧接口
// FetchAllProxies 从所有代理源并发获取代理列表
// 保持与原有代码兼容
func FetchAllProxies(sources []ProxySource) ([]*proxy.Proxy, error) {
	fetcher := CreateDefaultFetcher()
	return fetcher.fetchProxiesFromSources(sources)
}

// SourceManager 代理源管理器
// 实现对代理源的添加、删除、获取和测试功能
type SourceManager struct {
	defaultSources []ProxySource
	customSources  []ProxySource
	mutex          sync.RWMutex
	fetcher        *ProxyFetcher
}

// NewSourceManager 创建新的代理源管理器实例
func NewSourceManager() *SourceManager {
	return &SourceManager{
		defaultSources: GetDefaultSources(),
		customSources:  make([]ProxySource, 0),
		fetcher:        CreateDefaultFetcher(),
	}
}

// GetSources 获取所有代理源（包括默认和自定义）
func (sm *SourceManager) GetSources() []ProxySource {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	
	// 合并默认源和自定义源
	allSources := make([]ProxySource, len(sm.defaultSources)+len(sm.customSources))
	copy(allSources, sm.defaultSources)
	copy(allSources[len(sm.defaultSources):], sm.customSources)
	
	return allSources
}

// GetDefaultSources 获取默认代理源
func (sm *SourceManager) GetDefaultSources() []ProxySource {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return append([]ProxySource{}, sm.defaultSources...)
}

// GetCustomSources 获取自定义代理源
func (sm *SourceManager) GetCustomSources() []ProxySource {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return append([]ProxySource{}, sm.customSources...)
}

// AddSource 添加自定义代理源
func (sm *SourceManager) AddSource(source ProxySource) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// 添加到自定义源列表
	sm.customSources = append(sm.customSources, source)
	
	// 同时添加到fetcher的自定义源中
	sm.fetcher.AddCustomSource(source)
}

// RemoveSource 移除自定义代理源
func (sm *SourceManager) RemoveSource(index int) bool {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// 确保索引有效
	if index < 0 || index >= len(sm.customSources) {
		return false
	}
	
	// 获取要删除的源的URL，以便从fetcher中移除
	sourceToRemove := sm.customSources[index]
	
	// 从自定义源列表中删除
	sm.customSources = append(sm.customSources[:index], sm.customSources[index+1:]...)
	
	// 同时从fetcher中移除
	sm.fetcher.RemoveCustomSource(sourceToRemove.URL)
	
	return true
}

// TestSource 测试代理源是否有效
func (sm *SourceManager) TestSource(source ProxySource) (string, error) {
	// 使用fetcher验证源
	valid, err := sm.fetcher.ValidateProxySource(source)
	if err != nil {
		return "", fmt.Errorf("测试失败: %v", err)
	}
	
	if valid {
		return "代理源有效", nil
	}
	
	return "", fmt.Errorf("无法从该源获取有效的代理")
}

// SetSources 设置所有代理源（用于从配置加载）
func (sm *SourceManager) SetSources(sources []ProxySource) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()
	
	// 清空现有自定义源
	sm.customSources = make([]ProxySource, 0)
	
	// 重新添加所有源
	for _, source := range sources {
		// 检查是否是默认源
		isDefault := false
		for _, defaultSource := range sm.defaultSources {
			if source.URL == defaultSource.URL && source.Protocol == defaultSource.Protocol {
				isDefault = true
				break
			}
		}
		
		// 非默认源添加到自定义源列表
		if !isDefault {
			sm.customSources = append(sm.customSources, source)
			sm.fetcher.AddCustomSource(source)
		}
	}
}
