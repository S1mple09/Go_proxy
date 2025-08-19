package storage

import (
	"encoding/json"
	"go_proxy/proxy"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
)

const (
	rawProxiesFile   = "raw_proxies.json"
	validProxiesFile = "valid_proxies.json"
)

type DiskStorage struct {
	basePath string
	mu       sync.RWMutex
}

func NewDiskStorage(basePath string) *DiskStorage {
	os.MkdirAll(basePath, 0755)
	return &DiskStorage{basePath: basePath}
}

func (s *DiskStorage) SaveRawProxies(proxies []*proxy.Proxy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveProxies(filepath.Join(s.basePath, rawProxiesFile), proxies)
}

func (s *DiskStorage) LoadRawProxies() ([]*proxy.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadProxies(filepath.Join(s.basePath, rawProxiesFile))
}

func (s *DiskStorage) SaveValidProxies(proxies []*proxy.Proxy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveProxies(filepath.Join(s.basePath, validProxiesFile), proxies)
}

func (s *DiskStorage) LoadValidProxies() ([]*proxy.Proxy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadProxies(filepath.Join(s.basePath, validProxiesFile))
}

func (s *DiskStorage) saveProxies(path string, proxies []*proxy.Proxy) error {
	data, err := json.Marshal(proxies)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

func (s *DiskStorage) loadProxies(path string) ([]*proxy.Proxy, error) {
	data, err := ioutil.ReadFile(path)
	if os.IsNotExist(err) {
		return []*proxy.Proxy{}, nil
	}
	if err != nil {
		return nil, err
	}

	var proxies []*proxy.Proxy
	err = json.Unmarshal(data, &proxies)
	return proxies, err
}
