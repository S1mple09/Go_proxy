package checker

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
// 参数 p 是要检查的代理对象
// 返回值：
//   float64: 延迟时间（秒）
//   string: 匿名级别（"Elite", "Anonymous" 或 "Transparent"）
//   error: 如果检查失败返回错误信息
func (c *Checker) CheckConnectivityAndSpeed(p *proxy.Proxy) (float64, string, error) {
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
// 使用 ipinfo.io 的批量API提高查询效率
// 参数 proxies 是需要查询的代理列表
// 返回错误如果API调用失败
func (c *Checker) BatchLookupLocations(proxies []*proxy.Proxy) error {
	if len(proxies) == 0 {
		return nil
	}

	ipMap := make(map[string][]*proxy.Proxy)
	var ips []string
	for _, p := range proxies {
		ip := strings.Split(p.Address, ":")[0]
		if _, exists := ipMap[ip]; !exists {
			ips = append(ips, ip)
		}
		ipMap[ip] = append(ipMap[ip], p)
	}

	for i := 0; i < len(ips); i += 100 {
		end := i + 100
		if end > len(ips) {
			end = len(ips)
		}
		batch := ips[i:end]

		jsonData, err := json.Marshal(batch)
		if err != nil {
			return err
		}

		req, err := http.NewRequest("POST", "https://ipinfo.io/batch", bytes.NewBuffer(jsonData))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		var results map[string]struct {
			Country string `json:"country"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
			return err
		}

		for ip, data := range results {
			if relatedProxies, ok := ipMap[ip]; ok {
				for _, p := range relatedProxies {
					p.Location = data.Country
				}
			}
		}
	}
	return nil
}

// checkSpeed 测试代理的下载速度
// 通过下载100KB测试文件计算速度（Mbps）
// 参数 client 是配置好代理的HTTP客户端
// 返回速度（Mbps）和可能的错误
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

	sizeMB := float64(len(data)) / (1024 * 1024)
	speedMbps := (sizeMB * 8) / duration

	return speedMbps, nil
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
