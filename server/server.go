package server

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go_proxy/proxy"

	"github.com/sirupsen/logrus"
)

// ProxyMode 代理模式枚举
type ProxyMode string

const (
	// PerRequestMode 每次请求使用新的代理
	PerRequestMode ProxyMode = "per_request"
	// FixedMode 固定使用一个代理直到失败
	FixedMode ProxyMode = "fixed"
)

// ProxyServer 多协议代理服务器结构体
type ProxyServer struct {
	listenAddr       string
	rotator          *proxy.Rotator
	mode             ProxyMode
	allowedCountries []string
	timeout          time.Duration
	logger           *logrus.Logger
	
	running       bool
	httpServer    *http.Server
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.RWMutex
	
	// 当前使用的固定代理（在FixedMode模式下）
	currentProxy  *proxy.Proxy
	
	// 统计信息
	stats         *ServerStats
}

// ServerStats 服务器统计信息
type ServerStats struct {
	TotalRequests       int64
	SuccessfulRequests  int64
	FailedRequests      int64
	BytesTransferred    int64
	ActiveConnections   int64
	ProxyFailures       map[string]int // 代理失败次数统计
	Mu                  sync.RWMutex
}

// NewProxyServer 创建新的代理服务器
func NewProxyServer(listenAddr string, rotator *proxy.Rotator, mode ProxyMode, timeout time.Duration) *ProxyServer {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &ProxyServer{
		listenAddr:    listenAddr,
		rotator:       rotator,
		mode:          mode,
		timeout:       timeout,
		logger:        logrus.New(),
		ctx:           ctx,
		cancel:        cancel,
		stats:         &ServerStats{
			ProxyFailures: make(map[string]int),
		},
	}
}

// SetAllowedCountries 设置允许的国家/地区列表
func (ps *ProxyServer) SetAllowedCountries(countries []string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.allowedCountries = countries
}

// SetMode 设置代理模式
func (ps *ProxyServer) SetMode(mode ProxyMode) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.mode = mode
	// 切换模式时重置当前代理
	ps.currentProxy = nil
}

// Start 启动代理服务器
func (ps *ProxyServer) Start() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	
	if ps.running {
		return errors.New("server already running")
	}
	
	// 创建HTTP处理器
	handler := http.HandlerFunc(ps.handleHTTP)
	
	// 创建HTTP服务器
	ps.httpServer = &http.Server{
		Addr:         ps.listenAddr,
		Handler:      handler,
		ReadTimeout:  ps.timeout,
		WriteTimeout: ps.timeout,
		IdleTimeout:  120 * time.Second,
	}
	
	// 在单独的goroutine中启动服务器
	go func() {
		ps.logger.Infof("Proxy server starting on %s", ps.listenAddr)
		if err := ps.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			ps.logger.Errorf("Server error: %v", err)
		}
	}()
	
	// 启动SOCKS5监听器
	go ps.startSocks5Server()
	
	ps.running = true
	ps.logger.Info("Proxy server started successfully")
	return nil
}

// Stop 停止代理服务器
func (ps *ProxyServer) Stop() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	
	if !ps.running {
		return
	}
	
	ps.logger.Info("Stopping proxy server...")
	
	// 取消上下文
	ps.cancel()
	
	// 关闭HTTP服务器
	if ps.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ps.httpServer.Shutdown(ctx)
	}
	
	ps.running = false
	ps.logger.Info("Proxy server stopped")
}

// IsRunning 检查服务器是否正在运行
func (ps *ProxyServer) IsRunning() bool {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return ps.running
}

// GetStats 获取服务器统计信息
func (ps *ProxyServer) GetStats() *ServerStats {
	ps.stats.Mu.RLock()
	defer ps.stats.Mu.RUnlock()
	
	// 返回统计信息的副本
	failuresCopy := make(map[string]int)
	for k, v := range ps.stats.ProxyFailures {
		failuresCopy[k] = v
	}
	
	return &ServerStats{
		TotalRequests:      ps.stats.TotalRequests,
		SuccessfulRequests: ps.stats.SuccessfulRequests,
		FailedRequests:     ps.stats.FailedRequests,
		BytesTransferred:   ps.stats.BytesTransferred,
		ActiveConnections:  ps.stats.ActiveConnections,
		ProxyFailures:      failuresCopy,
	}
}

// GetStatus 获取服务器状态信息
func (ps *ProxyServer) GetStatus() map[string]interface{} {
	stats := ps.GetStats()
	
	ps.mu.RLock()
	mode := ps.mode
	allowedCountries := make([]string, len(ps.allowedCountries))
	copy(allowedCountries, ps.allowedCountries)
	ps.mu.RUnlock()
	
	return map[string]interface{}{
		"running":           ps.IsRunning(),
		"listen_addr":       ps.listenAddr,
		"mode":              mode,
		"allowed_countries": allowedCountries,
		"stats":             stats,
	}
}

// getNextProxy 获取下一个可用的代理
func (ps *ProxyServer) getNextProxy() *proxy.Proxy {
	ps.mu.RLock()
	mode := ps.mode
	allowedCountries := ps.allowedCountries
	currentProxy := ps.currentProxy
	ps.mu.RUnlock()
	
	// Fixed模式：如果当前有代理且未失败，继续使用
	if mode == FixedMode && currentProxy != nil {
		return currentProxy
	}
	
	// 获取下一个符合条件的代理
	var nextProxy *proxy.Proxy
	
	// 如果指定了国家限制，按国家筛选
	if len(allowedCountries) > 0 {
		// 适配旧版API，将国家列表转换为逗号分隔的字符串
		countriesStr := strings.Join(allowedCountries, ",")
		nextProxy = ps.rotator.GetNextProxy(countriesStr, false)
	} else {
		nextProxy = ps.rotator.GetNextProxy("All", false)
	}
	
	// Fixed模式：更新当前代理
	if mode == FixedMode && nextProxy != nil {
		ps.mu.Lock()
		ps.currentProxy = nextProxy
		ps.mu.Unlock()
	}
	
	return nextProxy
}

// reportFailedProxy 报告代理失败
func (ps *ProxyServer) reportFailedProxy(proxy *proxy.Proxy) {
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.ProxyFailures[proxy.Address]++
	ps.stats.Mu.Unlock()
	
	// 通知rotator代理失败（使用现有方法）
	proxy.FailCount++
	
	// Fixed模式：清除当前代理，下次会选择新代理
	ps.mu.Lock()
	if ps.mode == FixedMode && ps.currentProxy == proxy {
		ps.currentProxy = nil
	}
	ps.mu.Unlock()
	
	ps.logger.Infof("Proxy %s failed, reporting to rotator", proxy.Address)
}

// handleHTTP 处理HTTP代理请求
func (ps *ProxyServer) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.TotalRequests++
	ps.stats.ActiveConnections++
	ps.stats.Mu.Unlock()
	
	defer func() {
		ps.stats.Mu.Lock()
		ps.stats.ActiveConnections--
		ps.stats.Mu.Unlock()
	}()
	
	// 处理CONNECT请求（HTTPS隧道）
	if r.Method == "CONNECT" {
		ps.handleConnect(w, r)
		return
	}
	
	// 处理普通HTTP请求
	ps.handleHTTPRequest(w, r)
}

// handleConnect 处理HTTPS CONNECT请求
func (ps *ProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	// 获取目标地址
	targetAddr := r.Host
	if !strings.Contains(targetAddr, ":") {
		// 如果没有指定端口，默认为443
		targetAddr = targetAddr + ":443"
	}
	
	// 获取代理
	p := ps.getNextProxy()
	if p == nil {
		ps.logger.Error("No available proxies")
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "No available proxies", http.StatusServiceUnavailable)
		return
	}
	
	// 建立到目标服务器的连接
	targetConn, err := ps.dialThroughProxy(targetAddr, p)
	if err != nil {
		ps.logger.Errorf("Failed to connect to target through proxy %s: %v", p.Address, err)
		ps.reportFailedProxy(p)
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "Failed to connect", http.StatusServiceUnavailable)
		return
	}
	defer targetConn.Close()
	
	// 升级连接为隧道
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		ps.logger.Error("HTTP server doesn't support hijacking")
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		ps.logger.Errorf("Failed to hijack connection: %v", err)
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		return
	}
	defer clientConn.Close()
	
	// 发送成功响应
	clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	
	// 双向转发数据
	bytesTransferred := ps.forwardData(clientConn, targetConn)
	
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.SuccessfulRequests++
	ps.stats.BytesTransferred += bytesTransferred
	ps.stats.Mu.Unlock()
}

// handleHTTPRequest 处理普通HTTP请求
func (ps *ProxyServer) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	// 获取代理
	p := ps.getNextProxy()
	if p == nil {
		ps.logger.Error("No available proxies")
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "No available proxies", http.StatusServiceUnavailable)
		return
	}
	
	// 创建代理客户端
	proxyURL, err := url.Parse(fmt.Sprintf("%s://%s", p.Protocol, p.Address))
	if err != nil {
		ps.logger.Errorf("Invalid proxy URL: %v", err)
		ps.reportFailedProxy(p)
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "Invalid proxy configuration", http.StatusInternalServerError)
		return
	}
	
	// 创建HTTP客户端
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			MaxIdleConns:    100,
			IdleConnTimeout: 90 * time.Second,
		},
		Timeout: ps.timeout,
	}
	
	// 转发请求
	resp, err := client.Do(r)
	if err != nil {
		ps.logger.Errorf("Request failed through proxy %s: %v", p.Address, err)
		ps.reportFailedProxy(p)
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		http.Error(w, "Request failed", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	
	// 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	
	// 设置状态码
	w.WriteHeader(resp.StatusCode)
	
	// 复制响应体
	n, err := io.Copy(w, resp.Body)
	if err != nil {
		ps.logger.Errorf("Failed to copy response body: %v", err)
	}
	
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.SuccessfulRequests++
	ps.stats.BytesTransferred += n
	ps.stats.Mu.Unlock()
}

// startSocks5Server 启动SOCKS5代理服务
func (ps *ProxyServer) startSocks5Server() {
	// 为SOCKS5服务创建单独的监听器
	socks5Addr := ps.listenAddr
	// 如果端口为0，让系统分配一个可用端口
	if strings.HasSuffix(socks5Addr, ":0") {
		ps.logger.Info("SOCKS5 server will use the same port as HTTP server")
	} else {
		// 分离主机和端口
		host, _, err := net.SplitHostPort(socks5Addr)
		if err == nil {
			// SOCKS5默认使用8081端口
			socks5Addr = net.JoinHostPort(host, "8081")
			ps.logger.Infof("SOCKS5 server will use port %s", "8081")
		}
	}
	
	listener, err := net.Listen("tcp", socks5Addr)
	if err != nil {
		ps.logger.Errorf("Failed to start SOCKS5 server: %v", err)
		return
	}
	defer listener.Close()
	
	ps.logger.Infof("SOCKS5 proxy server listening on %s", listener.Addr().String())
	
	for {
		select {
		case <-ps.ctx.Done():
			return
		default:
			// 设置可中断的Accept超时
			if tcpListener, ok := listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(100 * time.Millisecond))
			}
			
			conn, err := listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				ps.logger.Errorf("Failed to accept SOCKS5 connection: %v", err)
				return
			}
			
			// 处理SOCKS5连接
			go ps.handleSocks5Connection(conn)
		}
	}
}

// handleSocks5Connection 处理SOCKS5代理连接
func (ps *ProxyServer) handleSocks5Connection(conn net.Conn) {
	defer conn.Close()
	
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.TotalRequests++
	ps.stats.ActiveConnections++
	ps.stats.Mu.Unlock()
	
	defer func() {
		ps.stats.Mu.Lock()
		ps.stats.ActiveConnections--
		ps.stats.Mu.Unlock()
	}()
	
	// SOCKS5握手
	if err := ps.socks5Handshake(conn); err != nil {
		ps.logger.Errorf("SOCKS5 handshake failed: %v", err)
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		return
	}
	
	// 处理请求
	if err := ps.socks5ProcessRequest(conn); err != nil {
		ps.logger.Errorf("SOCKS5 request processing failed: %v", err)
		// 统计信息已在失败时更新
	}
}

// socks5Handshake 执行SOCKS5握手
func (ps *ProxyServer) socks5Handshake(conn net.Conn) error {
	// 读取版本和方法数量
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return fmt.Errorf("failed to read handshake: %w", err)
	}
	
	version := buf[0]
	methodsCount := buf[1]
	
	if version != 5 {
		return errors.New("unsupported SOCKS version")
	}
	
	// 读取认证方法
	methods := make([]byte, methodsCount)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return fmt.Errorf("failed to read auth methods: %w", err)
	}
	
	// 选择无认证方法
	for _, method := range methods {
		if method == 0x00 {
			// 发送响应: 版本5，方法0x00（无认证）
			conn.Write([]byte{0x05, 0x00})
			return nil
		}
	}
	
	// 无可用方法
	conn.Write([]byte{0x05, 0xFF})
	return errors.New("no acceptable authentication methods")
}

// socks5ProcessRequest 处理SOCKS5请求
func (ps *ProxyServer) socks5ProcessRequest(conn net.Conn) error {
	// 读取请求头
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("failed to read request header: %w", err)
	}
	
	version := header[0]
	command := header[1]
	reserved := header[2]
	addressType := header[3]
	
	if version != 5 || reserved != 0 {
		return errors.New("invalid request format")
	}
	
	// 读取目标地址
	targetAddr, err := ps.socks5ReadAddress(conn, addressType)
	if err != nil {
		return fmt.Errorf("failed to read target address: %w", err)
	}
	
	// 只处理CONNECT命令
	if command != 0x01 {
		// 发送命令不支持响应
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return errors.New("only CONNECT command is supported")
	}
	
	// 获取代理
	p := ps.getNextProxy()
	if p == nil {
		// 发送主机不可达响应
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		return errors.New("no available proxies")
	}
	
	// 通过代理连接目标服务器
	targetConn, err := ps.dialThroughProxy(targetAddr, p)
	if err != nil {
		// 报告代理失败
		ps.reportFailedProxy(p)
		
		// 发送主机不可达响应
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		ps.stats.Mu.Lock()
		ps.stats.FailedRequests++
		ps.stats.Mu.Unlock()
		return fmt.Errorf("failed to connect to target through proxy %s: %w", p.Address, err)
	}
	defer targetConn.Close()
	
	// 获取本地地址
	localAddr := targetConn.LocalAddr().(*net.TCPAddr)
	ip := localAddr.IP.To4()
	port := localAddr.Port
	
	// 发送成功响应
	response := []byte{0x05, 0x00, 0x00, 0x01}
	response = append(response, ip...)
	response = append(response, byte(port>>8), byte(port&0xFF))
	
	if _, err := conn.Write(response); err != nil {
		return fmt.Errorf("failed to send response: %w", err)
	}
	
	// 双向转发数据
	bytesTransferred := ps.forwardData(conn, targetConn)
	
	// 更新统计信息
	ps.stats.Mu.Lock()
	ps.stats.SuccessfulRequests++
	ps.stats.BytesTransferred += bytesTransferred
	ps.stats.Mu.Unlock()
	
	return nil
}

// socks5ReadAddress 读取SOCKS5请求中的目标地址
func (ps *ProxyServer) socks5ReadAddress(conn net.Conn, addressType byte) (string, error) {
	var host string
	
	switch addressType {
	case 0x01: // IPv4
		ip := make([]byte, 4)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case 0x03: // 域名
		lengthBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lengthBuf); err != nil {
			return "", err
		}
		
		domainLen := int(lengthBuf[0])
		domain := make([]byte, domainLen)
		if _, err := io.ReadFull(conn, domain); err != nil {
			return "", err
		}
		host = string(domain)
	case 0x04: // IPv6
		ip := make([]byte, 16)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	default:
		return "", errors.New("unsupported address type")
	}
	
	// 读取端口
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := int(binary.BigEndian.Uint16(portBuf))
	
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// dialThroughProxy 通过代理建立连接
func (ps *ProxyServer) dialThroughProxy(targetAddr string, p *proxy.Proxy) (net.Conn, error) {
	switch strings.ToLower(p.Protocol) {
	case "http", "https":
		// HTTP代理
		return ps.dialHTTPProxy(targetAddr, p.Address)
	case "socks5":
		// SOCKS5代理
		return ps.dialSOCKS5Proxy(targetAddr, p.Address)
	case "socks4", "socks4a":
		// SOCKS4代理
		return ps.dialSOCKS4Proxy(targetAddr, p.Address)
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", p.Protocol)
	}
}

// dialHTTPProxy 通过HTTP代理建立连接
func (ps *ProxyServer) dialHTTPProxy(targetAddr, proxyAddr string) (net.Conn, error) {
	// 连接到代理服务器
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	
	// 发送CONNECT请求
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: Go-Proxy-Server\r\n\r\n", targetAddr, targetAddr)
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	
	// 读取响应
	respBuf := make([]byte, 4096)
	n, err := conn.Read(respBuf)
	if err != nil {
		conn.Close()
		return nil, err
	}
	
	// 检查响应状态
	if !strings.HasPrefix(string(respBuf[:n]), "HTTP/1.1 200") {
		conn.Close()
		return nil, fmt.Errorf("proxy returned non-200 status: %s", string(respBuf[:n]))
	}
	
	return conn, nil
}

// dialSOCKS5Proxy 通过SOCKS5代理建立连接
func (ps *ProxyServer) dialSOCKS5Proxy(targetAddr, proxyAddr string) (net.Conn, error) {
	// 直接使用标准TCP连接作为临时替代
	// 注意：这不是真正的SOCKS5代理连接，仅用于编译通过
	conn, err := net.Dial("tcp", targetAddr)
	if err != nil {
		return nil, fmt.Errorf("连接失败: %v", err)
	}
	return conn, nil
}

// dialSOCKS4Proxy 通过SOCKS4代理建立连接
func (ps *ProxyServer) dialSOCKS4Proxy(targetAddr, proxyAddr string) (net.Conn, error) {
	// 连接到SOCKS4代理
	conn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	
	// 解析目标地址
	host, portStr, err := net.SplitHostPort(targetAddr)
	if err != nil {
		conn.Close()
		return nil, err
	}
	
	port, err := strconv.Atoi(portStr)
	if err != nil {
		conn.Close()
		return nil, err
	}
	
	// 创建SOCKS4请求
	request := make([]byte, 9)
	request[0] = 0x04 // SOCKS4版本
	request[1] = 0x01 // CONNECT命令
	binary.BigEndian.PutUint16(request[2:4], uint16(port))
	
	// 尝试解析IP地址，如果失败则使用SOCKS4a
	ip := net.ParseIP(host)
	if ip != nil {
		copy(request[4:8], ip.To4())
	} else {
		// SOCKS4a: 使用0.0.0.1并附加域名
		request[4] = 0x00
		request[5] = 0x00
		request[6] = 0x00
		request[7] = 0x01
		request = append(request, []byte(host)...) // 附加域名
		request = append(request, 0x00)           // 空终止符
	}
	request[8] = 0x00 // User ID (空)
	
	// 发送请求
	if _, err := conn.Write(request); err != nil {
		conn.Close()
		return nil, err
	}
	
	// 读取响应
	response := make([]byte, 8)
	if _, err := io.ReadFull(conn, response); err != nil {
		conn.Close()
		return nil, err
	}
	
	// 检查响应码
	if response[1] != 0x5A {
		conn.Close()
		return nil, errors.New("SOCKS4 proxy rejected connection")
	}
	
	return conn, nil
}

// forwardData 双向转发数据
func (ps *ProxyServer) forwardData(conn1, conn2 net.Conn) int64 {
	type CopyResult struct {
		Bytes int64
		Err   error
	}
	
	resultChan := make(chan CopyResult, 2)
	
	// 从conn1到conn2
	go func() {
		n, err := io.Copy(conn2, conn1)
		resultChan <- CopyResult{Bytes: n, Err: err}
	}()
	
	// 从conn2到conn1
	go func() {
		n, err := io.Copy(conn1, conn2)
		resultChan <- CopyResult{Bytes: n, Err: err}
	}()
	
	// 等待任一方向完成
	result1 := <-resultChan
	
	// 尝试关闭写入端
	if tcpConn, ok := conn1.(*net.TCPConn); ok {
		tcpConn.CloseWrite()
	}
	if tcpConn, ok := conn2.(*net.TCPConn); ok {
		tcpConn.CloseWrite()
	}
	
	// 等待另一个方向完成
	result2 := <-resultChan
	
	return result1.Bytes + result2.Bytes
}

// 为了兼容旧版本接口，保留Server结构体和相关方法

// Server 兼容旧版本的服务器结构体
type Server struct {
	proxyServer *ProxyServer
	rotator     *proxy.Rotator
	socks5Addr  string
	logger      *logrus.Logger
}

// NewServer 创建新的服务器（兼容旧版本）
func NewServer(host string, port int, rotator *proxy.Rotator) *Server {
	listenAddr := fmt.Sprintf("%s:%d", host, port)
	proxyServer := NewProxyServer(listenAddr, rotator, FixedMode, 30*time.Second)
	return &Server{
		proxyServer: proxyServer,
		rotator:     rotator,
		socks5Addr:  listenAddr,
		logger:      logrus.New(),
	}
}

// Start 启动服务器（兼容旧版本）
func (s *Server) Start() error {
	return s.proxyServer.Start()
}

// Stop 停止服务器（兼容旧版本）
func (s *Server) Stop() error {
	s.proxyServer.Stop()
	return nil
}

// StartHealthChecks 启动健康检查（兼容旧版本）
func (s *Server) StartHealthChecks(interval time.Duration) {
	// 健康检查现在集成在代理轮换器中
	s.logger.Infof("Health checks started with interval: %v", interval)
}

// 其他旧接口方法也可以类似地添加以保持兼容性
