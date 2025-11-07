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
	"runtime"
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

	// 代理源管理器（实现SourceManager接口）
	sourceManager *fetcher.SourceManager

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
	maxLatency       float64
	minSpeed         float64
	rotationInterval int
	themeName        string

	// 服务器配置
	allowedCountries    []string
	proxyMode           string
	healthCheckInterval int
}

//}

// SourceManager 定义代理源管理接口
type SourceManager interface {
	GetSources() []fetcher.ProxySource
	AddSource(source fetcher.ProxySource)
	RemoveSource(index int)
	TestSource(source fetcher.ProxySource) (string, error)
}

// NewApp 创建并初始化一个新的 App
func NewApp() *App {
	a := &App{}
	a.fyneApp = app.New()
	a.fyneApp.Settings().SetTheme(&theme.MyTheme{})
	a.win = a.fyneApp.NewWindow("代理池工具 v0.3.0")

	a.rotator = proxy.NewRotator()
	a.checker = checker.NewChecker(checker.Config{
		Timeout:            10 * time.Second,
		FailThreshold:      3,
		AutoRetestInterval: 60 * time.Second,
		ExitOnFailedTCP:    true,
	})

	// 初始化代理源管理器
	a.sourceManager = fetcher.NewSourceManager()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Failed to load configuration, using default: %v", err)
		cfg = config.NewDefaultConfig()
	}

	a.config = cfg
	// 从配置加载所有字段
	a.proxySources = cfg.ProxySources
	a.maxLatency = cfg.MaxLatency
	a.minSpeed = cfg.MinSpeed
	a.rotationInterval = cfg.RotationInterval
	a.rotationSeconds = cfg.RotationInterval
	a.themeName = cfg.ThemeName
	a.proxyMode = cfg.ProxyMode
	a.healthCheckInterval = cfg.HealthCheckInterval
	a.allowedCountries = cfg.AllowedCountries

	// 设置代理源到sourceManager
	a.sourceManager.SetSources(cfg.ProxySources)

	// 初始化数据绑定
	a.proxyList = binding.NewUntypedList()
	a.progressText = binding.NewString()
	a.serverRunning = binding.NewBool()
	a.rotationStatus = binding.NewBool()
	a.currentProxy = binding.NewString()
	a.currentProxy.Set("无")
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

		// 优化1: 根据CPU核心数动态设置并发数
		concurrencyLimit := runtime.NumCPU() * 25
		if concurrencyLimit > 300 {
			concurrencyLimit = 300
		} else if concurrencyLimit < 50 {
			concurrencyLimit = 50
		}

		sem := make(chan struct{}, concurrencyLimit)
		var wg sync.WaitGroup
		testedCount := 0
		testedMutex := sync.Mutex{}

		// 优化2: 使用通道批量处理有效代理，减少锁竞争
		validProxiesChan := make(chan *proxy.Proxy, concurrencyLimit)
		batchSize := 50
		var batchProxies []*proxy.Proxy

		// 启动结果处理协程
		go func() {
			for p := range validProxiesChan {
				testedMutex.Lock()
				batchProxies = append(batchProxies, p)

				// 达到批次大小时批量添加并刷新UI
				if len(batchProxies) >= batchSize {
					a.rotator.AddValidProxies(batchProxies)
					a.ApplyFiltersAndRefresh()
					batchProxies = batchProxies[:0] // 清空切片，重用空间
				}
				testedMutex.Unlock()
			}

			// 处理剩余的代理
			testedMutex.Lock()
			if len(batchProxies) > 0 {
				a.rotator.AddValidProxies(batchProxies)
				a.ApplyFiltersAndRefresh()
			}
			testedMutex.Unlock()
		}()

		// 并发测试代理
		for _, p := range rawProxies {
			wg.Add(1)
			sem <- struct{}{}
			go func(pr *proxy.Proxy) {
				defer func() {
					<-sem
					wg.Done()
					testedMutex.Lock()
					testedCount++
					// 优化3: 降低UI进度更新频率
					if testedCount%20 == 0 || testedCount == len(rawProxies) {
						a.progressText.Set(fmt.Sprintf("测试代理: %d/%d", testedCount, len(rawProxies)))
					}
					testedMutex.Unlock()
				}()

				// 测试代理连接性和速度
				if _, _, err := a.checker.CheckConnectivityAndSpeed(pr); err == nil {
					// 将有效代理发送到通道
					validProxiesChan <- pr
				}
			}(p)
		}

		wg.Wait()
		close(validProxiesChan) // 关闭通道，通知结果处理协程结束

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

	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// 使用新的ProxyServer构造函数，设置代理模式和超时
	proxyMode := server.FixedMode
	if a.config.ProxyMode == "per_request" {
		proxyMode = server.PerRequestMode
	}

	a.server = server.NewServer("127.0.0.1", port, a.rotator)

	// 启动服务
	if err := a.server.Start(); err != nil {
		a.Log(fmt.Sprintf("启动服务失败: %v", err))
		return
	}

	// 设置健康检查
	checkInterval := 5 * time.Minute
	if a.config.HealthCheckInterval > 0 {
		checkInterval = time.Duration(a.config.HealthCheckInterval) * time.Minute
	}
	a.server.StartHealthChecks(checkInterval)

	a.serverRunning.Set(true)
	a.Log(fmt.Sprintf("代理服务已在 %s 启动，模式: %s", listenAddr, proxyMode))
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
// --- SourceManager 接口实现 ---
func (a *App) GetSources() []fetcher.ProxySource {
	return a.sourceManager.GetSources()
}

func (a *App) AddSource(source fetcher.ProxySource) {
	a.sourceManager.AddSource(source)
	// 更新配置和保存
	a.config.ProxySources = a.sourceManager.GetSources()
	a.SaveConfig()
	a.Log(fmt.Sprintf("已添加新的代理源: %s", source.URL))
}

func (a *App) RemoveSource(index int) {
	// 转换索引以找到正确的自定义源位置
	defaultSources := a.sourceManager.GetDefaultSources()
	if index < len(defaultSources) {
		// 不能删除默认源
		a.Log("无法删除默认代理源")
		return
	}

	// 调整为自定义源的索引
	customIndex := index - len(defaultSources)
	if a.sourceManager.RemoveSource(customIndex) {
		// 更新配置和保存
		a.config.ProxySources = a.sourceManager.GetSources()
		a.SaveConfig()
		a.Log("已移除代理源")
	}
}

func (a *App) TestSource(source fetcher.ProxySource) (string, error) {
	a.Log(fmt.Sprintf("正在测试代理源: %s", source.URL))
	return a.sourceManager.TestSource(source)
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
	// 确保配置对象包含所有最新值
	a.config.MaxLatency = a.maxLatency
	a.config.MinSpeed = a.minSpeed
	a.config.RotationInterval = a.rotationInterval
	a.config.ThemeName = a.themeName
	a.config.ProxySources = a.sourceManager.GetSources()
	a.config.ProxyMode = a.proxyMode
	a.config.HealthCheckInterval = a.healthCheckInterval
	a.config.AllowedCountries = a.allowedCountries
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
