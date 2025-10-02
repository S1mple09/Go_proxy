package main

import (
	"bufio"
	"fmt"
	"go_proxy/checker"
	"go_proxy/config"
	"go_proxy/fetcher"
	"go_proxy/proxy"
	"go_proxy/server"
	"go_proxy/theme"
	"go_proxy/ui"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	fynetheme "fyne.io/fyne/v2/theme" // Import fynetheme
)

// App 用于统一管理应用的状态和组件
type App struct {
	fyneApp fyne.App
	win     fyne.Window

	rotator *proxy.Rotator
	checker *checker.Checker
	server  *server.Server

	// UI 组件的数据绑定
	proxyList       binding.UntypedList
	progressText    binding.String
	serverRunning   binding.Bool
	rotationStatus  binding.Bool
	currentProxy    binding.String
	rotationTicker  *time.Ticker
	rotationStop    chan struct{}
	rotationSeconds int

	// 数据
	proxySources []fetcher.ProxySource
	mutex        sync.RWMutex // Add mutex for thread safety
	config       *config.AppConfig

	// 筛选条件
	maxLatency float64
	minSpeed   float64
}

// SourceManager 定义代理源管理接口
type SourceManager interface {
	GetProxySourcesData() []fetcher.ProxySource
	AddProxySource(url, protocol string, isAPI bool)
	RemoveProxySource(index int)
	TestProxySource(source fetcher.ProxySource) (string, error)
}

// NewApp 创建并初始化一个新的 App
func NewApp() *App {
	a := &App{}
	a.fyneApp = app.New()
	a.fyneApp.Settings().SetTheme(&theme.MyTheme{})
	a.win = a.fyneApp.NewWindow("代理池工具 v0.2.0")

	a.rotator = proxy.NewRotator()
	a.checker = checker.NewChecker()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		a.Log(fmt.Sprintf("加载配置失败，使用默认配置: %v", err))
		cfg = config.NewDefaultConfig()
	}
	a.config = cfg

	a.proxySources = cfg.ProxySources // 从配置加载代理源

	a.proxyList = binding.NewUntypedList()
	a.progressText = binding.NewString()
	a.serverRunning = binding.NewBool()
	a.serverRunning.Set(false)
	a.rotationStatus = binding.NewBool()
	a.rotationStatus.Set(false)
	a.currentProxy = binding.NewString()
	a.currentProxy.Set("无")
	a.rotationSeconds = cfg.RotationInterval // 从配置加载轮换间隔

	a.rotationStop = make(chan struct{})

	// 从配置加载筛选条件
	a.maxLatency = cfg.MaxLatency
	a.minSpeed = cfg.MinSpeed

	return a
}

// Log 向控制台输出一条带时间戳的日志
func (a *App) Log(message string) {
	log.Println(message)
}

// FetchProxies 获取代理但不显示，仅存入原始列表
func (a *App) FetchProxies() {
	go func() {
		a.progressText.Set("获取代理: 正在进行中...")
		a.Log("开始从所有源获取在线代理...")

		proxies, err := fetcher.FetchAllProxies(a.proxySources) // 使用App内部的代理源
		if err != nil {
			a.Log(fmt.Sprintf("获取代理时发生错误: %v", err))
			a.progressText.Set("获取代理: 失败")
			return
		}
		if len(proxies) == 0 {
			a.Log("未能获取到任何代理。")
			a.progressText.Set("获取代理: 未找到")
			return
		}

		a.rotator.SetRawProxies(proxies)
		a.progressText.Set("获取代理: 完成")
		a.Log(fmt.Sprintf("获取完成，发现 %d 个代理地址。现在开始自动测试...", len(proxies)))
		a.TestAllProxies()
	}()
}

// TestAllProxies 高并发测试所有原始代理，并将有效代理存入列表
func (a *App) TestAllProxies() {
	go func() {
		rawProxies, err := a.rotator.GetRawProxies()
		if err != nil {
			a.Log(fmt.Sprintf("获取原始代理失败: %v", err))
			a.progressText.Set("测试代理: 失败")
			return
		}
		if len(rawProxies) == 0 {
			a.Log("没有可测试的代理，请先获取代理。")
			a.progressText.Set("测试代理: 无代理")
			return
		}
		a.Log(fmt.Sprintf("开始并发测试 %d 个代理...", len(rawProxies)))
		a.progressText.Set("测试代理: 正在进行中...")
		if err := a.rotator.SetValidProxies([]*proxy.Proxy{}); err != nil { // 开始测试前清空有效列表
			a.Log(fmt.Sprintf("清空有效代理失败: %v", err))
			return
		}
		a.ApplyFiltersAndRefresh()

		var wg sync.WaitGroup
		var testedCount int
		var testedMutex sync.Mutex

		concurrencyLimit := 200
		sem := make(chan struct{}, concurrencyLimit)

		for _, p := range rawProxies {
			wg.Add(1)
			sem <- struct{}{}
			go func(pr *proxy.Proxy) {
				defer func() {
					<-sem
					wg.Done()
				}()
				if _, _, err := a.checker.CheckConnectivityAndSpeed(pr); err == nil {
					// 测试成功，立即添加到有效列表并刷新UI
					if err := a.rotator.AddValidProxies([]*proxy.Proxy{pr}); err != nil {
						a.Log(fmt.Sprintf("添加有效代理失败: %v", err))
					}
					a.ApplyFiltersAndRefresh()
				}
				testedMutex.Lock()
				testedCount++
				a.progressText.Set(fmt.Sprintf("测试代理: %d/%d", testedCount, len(rawProxies)))
				testedMutex.Unlock()
			}(p)
		}
		wg.Wait()

		a.Log("基础测试完成。开始后台批量查询地理位置...")
		a.progressText.Set("测试代理: 查询地理位置...")
		// 后台批量查询地理位置，不阻塞主流程
		go func() {
			validProxies, err := a.rotator.GetValidProxies()
			if err != nil {
				a.Log(fmt.Sprintf("获取有效代理失败: %v", err))
				return
			}
			if len(validProxies) > 0 {
				if err := a.checker.BatchLookupLocations(validProxies); err != nil {
					a.Log(fmt.Sprintf("批量查询地理位置失败: %v", err))
				} else {
					a.Log("地理位置查询完成，列表已更新。")
					a.ApplyFiltersAndRefresh() // 再次刷新以显示地理位置
				}
			}
			a.progressText.Set("测试代理: 完成")
		}()

		a.Log("全部测试流程完成。")
	}()
}

// ApplyFilters 应用筛选条件并刷新UI
func (a *App) ApplyFilters(maxLatencyStr, minSpeedStr string) {
	var newMaxLatency float64
	if maxLatencyStr == "" {
		newMaxLatency = -1
	} else {
		maxLatency, err := strconv.ParseFloat(maxLatencyStr, 64)
		if err != nil || maxLatency <= 0 {
			newMaxLatency = -1
		} else {
			newMaxLatency = maxLatency / 1000 // ms转换为秒
		}
	}

	var newMinSpeed float64
	if minSpeedStr == "" {
		newMinSpeed = -1
	} else {
		minSpeed, err := strconv.ParseFloat(minSpeedStr, 64)
		if err != nil || minSpeed < 0 {
			newMinSpeed = -1
		} else {
			newMinSpeed = minSpeed
		}
	}

	a.mutex.Lock()
	a.maxLatency = newMaxLatency
	a.minSpeed = newMinSpeed
	a.config.MaxLatency = newMaxLatency
	a.config.MinSpeed = newMinSpeed
	a.mutex.Unlock()

	a.SaveConfig() // Save config after applying filters
	a.Log("应用筛选条件并刷新列表...")
	a.ApplyFiltersAndRefresh()
}

// ApplyFiltersAndRefresh 从rotator获取、筛选、排序并更新UI
func (a *App) ApplyFiltersAndRefresh() {
	proxies, err := a.rotator.GetFilteredAndSortedProxies(a.maxLatency, a.minSpeed)
	if err != nil {
		a.Log(fmt.Sprintf("获取筛选代理失败: %v", err))
		return
	}
	var proxyItems []interface{}
	for _, p := range proxies {
		proxyItems = append(proxyItems, p)
	}
	a.proxyList.Set(proxyItems)
}

// ImportProxies 从文件导入代理
func (a *App) ImportProxies() {
	fileDialog := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}
		defer reader.Close()

		var importedProxies []*proxy.Proxy
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				importedProxies = append(importedProxies, &proxy.Proxy{Address: line, Protocol: "http"})
			}
		}
		if len(importedProxies) > 0 {
			a.rotator.AddRawProxies(importedProxies)
			a.Log(fmt.Sprintf("成功导入 %d 个代理。现在开始自动测试...", len(importedProxies)))
			a.TestAllProxies()
		}
	}, a.win)
	fileDialog.SetFilter(storage.NewExtensionFileFilter([]string{".txt"}))
	fileDialog.Show()
}

// ExportProxies 导出当前显示的有效代理到文件
func (a *App) ExportProxies() {
	proxies, err := a.rotator.GetFilteredAndSortedProxies(a.maxLatency, a.minSpeed)
	if err != nil {
		a.Log(fmt.Sprintf("获取代理失败: %v", err))
		return
	}
	if len(proxies) == 0 {
		dialog.ShowInformation("无代理可导出", "当前列表没有可导出的有效代理。", a.win)
		return
	}

	fileDialog := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
		if err != nil || writer == nil {
			return
		}
		defer writer.Close()

		for _, p := range proxies {
			line := fmt.Sprintf("%s\n", p.Address)
			_, _ = writer.Write([]byte(line))
		}
		a.Log(fmt.Sprintf("成功导出 %d 个有效代理到 %s", len(proxies), writer.URI().Name()))
	}, a.win)
	fileDialog.SetFileName("valid_proxies.txt")
	fileDialog.Show()
}

// ClearProxies 清空所有代理
func (a *App) ClearProxies() {
	a.rotator.SetRawProxies([]*proxy.Proxy{})
	a.rotator.SetValidProxies([]*proxy.Proxy{})
	a.ApplyFiltersAndRefresh()
	a.Log("所有代理列表已清空。")
}

// ToggleServer 启动或停止本地代理服务
func (a *App) ToggleServer(portStr string) {
	running, _ := a.serverRunning.Get()
	if running {
		if a.server != nil {
			if err := a.server.Stop(); err != nil {
				a.Log(fmt.Sprintf("停止服务失败: %v", err))
				return
			}
			a.serverRunning.Set(false)
		}
		return
	}

	if a.rotator.GetValidProxyCount() == 0 {
		a.Log("错误：没有可用的有效代理来启动服务。")
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		a.Log(fmt.Sprintf("错误：端口 '%s' 无效。", portStr))
		return
	}

	a.server = server.NewServer("127.0.0.1", port, a.rotator)
	if err := a.server.Start(); err != nil {
		a.Log(fmt.Sprintf("启动服务失败: %v", err))
		return
	}
	a.serverRunning.Set(true)
}

func main() {
	myApp := NewApp()
	myApp.progressText.Set("就绪")

	// Set initial theme from config
	switch myApp.config.ThemeName {
	case "默认":
		myApp.fyneApp.Settings().SetTheme(fynetheme.LightTheme())
	case "深色":
		myApp.fyneApp.Settings().SetTheme(fynetheme.DarkTheme())
	case "自定义":
		myApp.fyneApp.Settings().SetTheme(&theme.MyTheme{})
	case "蓝色":
		myApp.fyneApp.Settings().SetTheme(&theme.BlueTheme{})
	case "绿色":
		myApp.fyneApp.Settings().SetTheme(&theme.GreenTheme{})
	default:
		myApp.fyneApp.Settings().SetTheme(&theme.MyTheme{})
	}

	go func() {
		myApp.Log("正在初始化，获取本机公网IP...")
		if err := myApp.checker.InitializePublicIP(); err != nil {
			myApp.Log(fmt.Sprintf("获取公网IP失败: %v", err))
		} else {
			myApp.Log("公网IP初始化成功。")
		}
	}()

	ui.SetupUI(myApp)
	myApp.win.ShowAndRun()

	// Save config on exit
	if err := myApp.SaveConfig(); err != nil {
		myApp.Log(fmt.Sprintf("保存配置失败: %v", err))
	}
	log.Println("应用已退出")
}

// --- 实现 ui.Apper 接口 ---
func (a *App) GetWindow() fyne.Window            { return a.win }
func (a *App) GetProxyList() binding.UntypedList { return a.proxyList }
func (a *App) GetProgressText() binding.String   { return a.progressText }
func (a *App) GetServerStatus() binding.Bool     { return a.serverRunning }
func (a *App) GetRotationStatus() binding.Bool   { return a.rotationStatus }
func (a *App) GetCurrentProxy() binding.String   { return a.currentProxy }
func (a *App) GetConfig() *config.AppConfig      { return a.config }

// --- SourceManager 接口实现 ---
func (a *App) GetProxySourcesData() []fetcher.ProxySource {
	a.mutex.RLock() // Assuming App has a mutex for thread safety
	defer a.mutex.RUnlock()
	sourcesCopy := make([]fetcher.ProxySource, len(a.proxySources))
	copy(sourcesCopy, a.proxySources)
	return sourcesCopy
}

func (a *App) AddProxySource(url, protocol string, isAPI bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	newSource := fetcher.ProxySource{
		URL:      url,
		Protocol: protocol,
		IsAPI:    isAPI,
	}
	a.proxySources = append(a.proxySources, newSource)
	a.config.ProxySources = a.proxySources // Update config
	a.SaveConfig()
	a.Log(fmt.Sprintf("已添加新的代理源: %s", url))
}

func (a *App) RemoveProxySource(index int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if index < 0 || index >= len(a.proxySources) {
		return
	}
	removed := a.proxySources[index]
	a.proxySources = append(a.proxySources[:index], a.proxySources[index+1:]...)
	a.config.ProxySources = a.proxySources // Update config
	a.SaveConfig()
	a.Log(fmt.Sprintf("已移除代理源: %s", removed.URL))
}

func (a *App) TestProxySource(source fetcher.ProxySource) (string, error) {
	a.Log(fmt.Sprintf("正在测试代理源: %s", source.URL))
	proxies, err := fetcher.FetchAllProxies([]fetcher.ProxySource{source})
	if err != nil {
		return fmt.Sprintf("测试失败: %v", err), err
	}
	if len(proxies) == 0 {
		return "测试完成: 未获取到任何代理。", nil
	}

	var validProxies []*proxy.Proxy
	var wg sync.WaitGroup
	sem := make(chan struct{}, 10) // Limit concurrency for testing individual source

	for _, p := range proxies {
		wg.Add(1)
		sem <- struct{}{}
		go func(pr *proxy.Proxy) {
			defer func() {
				<-sem
				wg.Done()
			}()
			if _, _, err := a.checker.CheckConnectivityAndSpeed(pr); err == nil {
				validProxies = append(validProxies, pr)
			}
		}(p)
	}
	wg.Wait()

	if len(validProxies) > 0 {
		return fmt.Sprintf("测试完成: 从该源获取到 %d 个代理，其中 %d 个有效。", len(proxies), len(validProxies)), nil
	}
	return fmt.Sprintf("测试完成: 从该源获取到 %d 个代理，但没有有效代理。", len(proxies)), nil
}

// ToggleRotation 切换代理轮换状态
func (a *App) ToggleRotation(enable bool) {
	if enable {
		a.startRotation()
	} else {
		a.stopRotation()
	}
}

// SetRotationInterval 设置轮换间隔时间(秒)
func (a *App) SetRotationInterval(seconds int) {
	if seconds <= 0 {
		return
	}
	a.mutex.Lock()
	a.rotationSeconds = seconds
	a.config.RotationInterval = seconds // Update config
	a.mutex.Unlock()

	a.SaveConfig()
	a.Log(fmt.Sprintf("轮换间隔已设置为 %d 秒", seconds))
	if running, _ := a.rotationStatus.Get(); running {
		a.stopRotation()
		a.startRotation()
	}
}

// SaveConfig 保存当前配置
func (a *App) SaveConfig() error {
	a.mutex.RLock()
	defer a.mutex.RUnlock()
	return config.SaveConfig(a.config)
}

// startRotation 开始代理轮换
func (a *App) startRotation() {
	a.rotationStatus.Set(true)
	a.rotationTicker = time.NewTicker(time.Duration(a.rotationSeconds) * time.Second)
	go func() {
		for {
			select {
			case <-a.rotationTicker.C:
				proxy := a.rotator.GetNextProxy("", false)
				if proxy != nil {
					a.currentProxy.Set(proxy.Address)
					a.Log(fmt.Sprintf("已轮换到新代理: %s", proxy.Address))
				}
			case <-a.rotationStop:
				return
			}
		}
	}()
	a.Log(fmt.Sprintf("代理轮换已启动，间隔 %d 秒", a.rotationSeconds))
}

// stopRotation 停止代理轮换
func (a *App) stopRotation() {
	if running, _ := a.rotationStatus.Get(); !running {
		return // 如果已经停止，则不执行任何操作
	}
	a.rotationStatus.Set(false)
	if a.rotationTicker != nil {
		a.rotationTicker.Stop()
	}
	// 检查通道是否已经关闭，避免重复关闭导致的panic
	select {
	case <-a.rotationStop:
		// 通道已关闭，无需操作
	default:
		close(a.rotationStop)
	}
	// 为下一次启动准备新的通道
	a.rotationStop = make(chan struct{})
	a.Log("代理轮换已停止")
}
