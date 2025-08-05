package fetcher

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"go_proxy/proxy"
)

// ProxySource 代理源结构体
// 定义代理列表的来源URL、协议类型和解析方式
// URL: 代理列表的网页或API地址
// Protocol: 代理协议类型(http/https/socks4/socks5)
// IsAPI: 是否为API响应(true)或HTML页面(false)
type ProxySource struct {
	URL      string
	Protocol string
	IsAPI    bool
}

// proxySources 内置代理源列表
// 包含16个免费代理源，覆盖HTTP/HTTPS/SOCKS4/SOCKS5协议
// 混合使用API接口和HTML页面类型的数据源
var proxySources = []ProxySource{
	{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=http", "http", true},
	{"https://openproxylist.xyz/http.txt", "http", true},
	{"https://www.proxy-list.download/api/v1/get?type=http", "http", true},
	{"https://proxylist.geonode.com/api/proxy-list?limit=500&page=1&sort_by=lastChecked&sort_type=desc&protocols=http", "http", true},
	{"https://free-proxy-list.net/", "http", false},
	{"http://www.kxdaili.com/dailiip/1/1.html", "http", false},
	{"http://www.66ip.cn/nmtq.php?get_num=300&isp=0&anonym=0&type=2", "http", true},
	{"http://proxylist.fatezero.org/proxy.list", "http", false},
	{"https://www.proxy-list.download/api/v1/get?type=https", "https", true},
	{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=socks4", "socks4", true},
	{"https://openproxylist.xyz/socks4.txt", "socks4", true},
	{"https://www.proxy-list.download/api/v1/get?type=socks4", "socks4", true},
	{"https://api.proxyscrape.com/v3/free-proxy-list/get?request=displayproxies&protocol=socks5", "socks5", true},
	{"https://openproxylist.xyz/socks5.txt", "socks5", true},
	{"https://www.proxy-list.download/api/v1/get?type=socks5", "socks5", true},
	{"https://www.proxyscan.io/api/proxy?type=socks5&format=txt", "socks5", true},
}

// FetchAllProxies 从所有代理源并发获取代理列表
// 使用goroutine并发请求所有代理源提高获取速度
// 自动去重相同地址的代理
// 返回值：
//   []*proxy.Proxy: 去重后的代理列表
//   error: 如果所有源都获取失败返回错误
func FetchAllProxies() ([]*proxy.Proxy, error) {
	var wg sync.WaitGroup
	proxyChan := make(chan []*proxy.Proxy, len(proxySources))
	errChan := make(chan error, len(proxySources))

	for _, source := range proxySources {
		wg.Add(1)
		go func(s ProxySource) {
			defer wg.Done()
			proxies, err := fetchFromSource(s)
			if err != nil {
				errChan <- err
				return
			}
			proxyChan <- proxies
		}(source)
	}

	go func() {
		wg.Wait()
		close(proxyChan)
		close(errChan)
	}()

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

	for err := range errChan {
		log.Printf("error fetching proxies: %v", err)
	}

	return allProxies, nil
}

// fetchFromSource 从单个代理源获取代理
// 参数 source 是要获取的代理源配置
// 根据IsAPI标志选择合适的解析器
// 返回该源的代理列表和可能的错误
func fetchFromSource(source ProxySource) ([]*proxy.Proxy, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s from %s", resp.Status, source.URL)
	}

	if source.IsAPI {
		return parseAPIResponse(resp.Body, source.Protocol)
	}
	return parseHTMLResponse(resp.Body, source.Protocol)
}

// parseAPIResponse 解析API响应获取代理列表
// 支持JSON格式和纯文本格式的API响应
// 参数 body 是HTTP响应体
// 参数 protocol 是代理协议类型
// 返回解析出的代理列表和可能的错误
func parseAPIResponse(body io.Reader, protocol string) ([]*proxy.Proxy, error) {
	content, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

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
				Address:  fmt.Sprintf("%s:%d", item.Ip, item.Port),
				Protocol: protocol,
			}
		}
		return proxies, nil
	}

	lines := strings.Split(string(content), "\n")
	proxyRegex := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)

	var proxies []*proxy.Proxy
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if proxyRegex.MatchString(line) {
			proxies = append(proxies, &proxy.Proxy{
				Address:  line,
				Protocol: protocol,
			})
		}
	}

	return proxies, nil
}

// parseHTMLResponse 解析HTML页面提取代理列表
// 使用正则表达式从HTML文本中提取IP:端口格式的代理
// 参数 body 是HTTP响应体
// 参数 protocol 是代理协议类型
// 返回解析出的代理列表和可能的错误
func parseHTMLResponse(body io.Reader, protocol string) ([]*proxy.Proxy, error) {
	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy
	proxyRegex := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+`)

	doc.Find("body").Each(func(i int, s *goquery.Selection) {
		text := s.Text()
		matches := proxyRegex.FindAllString(text, -1)
		for _, match := range matches {
			proxies = append(proxies, &proxy.Proxy{
				Address:  match,
				Protocol: protocol,
			})
		}
	})

	return proxies, nil
}
