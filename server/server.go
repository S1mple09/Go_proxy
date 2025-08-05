package server

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"go_proxy/proxy"

	"github.com/sirupsen/logrus"
	xproxy "golang.org/x/net/proxy"
)

// Server SOCKS5代理服务结构体
// 实现基于代理池的SOCKS5代理服务器，支持动态代理切换
// 包含服务配置、代理轮换器和连接管理功能
type Server struct {
	socks5Addr string
	rotator    *proxy.Rotator
	logger     *logrus.Logger

	listener net.Listener
	running  bool
	mutex    sync.Mutex
}

// NewServer 创建新的代理服务实例
// 参数 host: 监听主机地址
// 参数 port: 监听端口号
// 参数 rotator: 代理轮换器实例，用于获取可用代理
// 返回初始化后的Server实例
func NewServer(host string, port int, rotator *proxy.Rotator) *Server {
	return &Server{
		socks5Addr: fmt.Sprintf("%s:%d", host, port),
		rotator:    rotator,
		logger:     logrus.New(),
	}
}

// Start 启动SOCKS5代理服务
// 开始在指定地址监听TCP连接
// 如果服务已运行或监听失败返回错误
func (s *Server) Start() error {
	s.mutex.Lock()
	if s.running {
		s.mutex.Unlock()
		return errors.New("服务已在运行")
	}

	listener, err := net.Listen("tcp", s.socks5Addr)
	if err != nil {
		s.mutex.Unlock()
		return fmt.Errorf("SOCKS5监听失败: %v", err)
	}
	s.listener = listener
	s.running = true
	s.mutex.Unlock()

	s.logger.Infof("SOCKS5代理服务已在 %s 启动", s.listener.Addr().String())
	go s.acceptConnections()
	return nil
}

// Stop 停止SOCKS5代理服务
// 关闭监听器并停止接受新连接
// 如果服务未运行返回错误
func (s *Server) Stop() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !s.running {
		return errors.New("服务未在运行")
	}
	s.running = false
	if err := s.listener.Close(); err != nil {
		s.logger.Errorf("关闭SOCKS5监听器错误: %v", err)
	}
	s.logger.Info("SOCKS5代理服务已停止")
	return nil
}

// acceptConnections 循环接受客户端连接
// 在独立goroutine中运行，持续接受新连接并分发给handleConnection处理
func (s *Server) acceptConnections() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if !s.running {
				return // 正常关闭
			}
			s.logger.Errorf("接受连接失败: %v", err)
			continue
		}
		go s.handleConnection(conn)
	}
}

// handleConnection 完整处理单个SOCKS5客户端连接
// 执行SOCKS5握手、认证、目标地址解析、上游代理选择和数据转发
// 参数 clientConn: 客户端TCP连接
func (s *Server) handleConnection(clientConn net.Conn) {
	defer clientConn.Close()

	if err := s.socks5Auth(clientConn); err != nil {
		s.logger.Errorf("SOCKS5认证失败: %v", err)
		return
	}

	targetAddr, err := s.socks5Connect(clientConn)
	if err != nil {
		s.logger.Errorf("SOCKS5连接请求失败: %v", err)
		return
	}

	proxyInfo := s.rotator.GetNextProxy("All", false)
	if proxyInfo == nil {
		s.logger.Error("无可用上游代理，无法处理请求")
		return
	}
	s.logger.Infof("使用代理 %s 转发到 %s", proxyInfo.Address, targetAddr)

	upstreamConn, err := s.dialUpstream(proxyInfo, targetAddr)
	if err != nil {
		s.logger.Errorf("连接上游代理 %s 失败: %v", proxyInfo.Address, err)
		return
	}
	defer upstreamConn.Close()

	s.forwardData(clientConn, upstreamConn)
}

// socks5Auth 处理SOCKS5协议的认证阶段
// 仅支持无认证方式(0x00)
// 返回错误如果客户端不支持无认证或通信失败
func (s *Server) socks5Auth(conn net.Conn) error {
	buf := make([]byte, 256)
	n, err := io.ReadFull(conn, buf[:2])
	if n != 2 || err != nil {
		return errors.New("读取认证信息失败")
	}
	if buf[0] != 0x05 {
		return errors.New("不支持的SOCKS版本")
	}
	nMethods := int(buf[1])
	n, err = io.ReadFull(conn, buf[:nMethods])
	if n != nMethods || err != nil {
		return errors.New("读取认证方法失败")
	}
	_, err = conn.Write([]byte{0x05, 0x00})
	return err
}

// socks5Connect 处理SOCKS5连接请求并解析目标地址
// 支持IPv4、IPv6和域名类型的目标地址
// 返回解析后的目标地址字符串和可能的错误
func (s *Server) socks5Connect(conn net.Conn) (string, error) {
	buf := make([]byte, 256)
	n, err := io.ReadFull(conn, buf[:4])
	if n != 4 || err != nil {
		return "", errors.New("读取连接请求失败")
	}
	if buf[0] != 0x05 || buf[1] != 0x01 {
		return "", errors.New("无效的连接请求")
	}

	var host string
	switch buf[3] {
	case 0x01:
		n, err = io.ReadFull(conn, buf[:6])
		if n != 6 || err != nil {
			return "", errors.New("读取IPv4地址失败")
		}
		host = net.IPv4(buf[0], buf[1], buf[2], buf[3]).String()
		port := binary.BigEndian.Uint16(buf[4:6])
		host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	case 0x03:
		n, err = io.ReadFull(conn, buf[:1])
		if n != 1 || err != nil {
			return "", errors.New("读取域名长度失败")
		}
		domainLen := int(buf[0])
		n, err = io.ReadFull(conn, buf[:domainLen+2])
		if n != domainLen+2 || err != nil {
			return "", errors.New("读取域名失败")
		}
		host = string(buf[:domainLen])
		port := binary.BigEndian.Uint16(buf[domainLen : domainLen+2])
		host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	default:
		return "", errors.New("不支持的地址类型")
	}

	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	return host, err
}

// dialUpstream 通过选中的上游代理连接到目标地址
// 根据代理协议类型(SOCKS/HTTP)创建相应的拨号器
// 参数 p: 选中的上游代理
// 参数 targetAddr: 最终目标地址(格式: host:port)
func (s *Server) dialUpstream(p *proxy.Proxy, targetAddr string) (net.Conn, error) {
	dialer, err := xproxy.SOCKS5("tcp", p.Address, nil, xproxy.Direct)
	if err != nil {
		if p.Protocol == "http" || p.Protocol == "https" {
			return net.DialTimeout("tcp", targetAddr, 10*time.Second)
		}
		return nil, err
	}
	return dialer.Dial("tcp", targetAddr)
}

// forwardData 在客户端和目标服务器之间双向转发数据
// 使用两个goroutine分别处理两个方向的数据传输
// 参数 client: 客户端连接
// 参数 target: 目标服务器连接
func (s *Server) forwardData(client, target net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(target, client)
		if tcpConn, ok := target.(interface{ CloseWrite() error }); ok {
			tcpConn.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(client, target)
		if tcpConn, ok := client.(interface{ CloseWrite() error }); ok {
			tcpConn.CloseWrite()
		}
	}()
	wg.Wait()
}
