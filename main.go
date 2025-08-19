package main

import (
	"bufio"
	"fmt"
	"go_proxy/checker"
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
	"fyne.io/fyne/v2/widget"
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
	logBinding      binding.String
	progressBar     *widget.ProgressBar
	serverRunning   binding.Bool
	rotationStatus  binding.Bool
	currentProxy    binding.String
	rotationTicker  *time.Ticker
	rotationStop    chan struct{}
	rotationSeconds int

	// 筛选条件
	maxLatency float64
	minSpeed   float64
}

// NewApp 创建并初始化一个新的 App
func NewApp() *App {
	a := &App{}
	a.fyneApp = app.New()
	a.fyneApp.Settings().SetTheme(&theme.MyTheme{})
	a.win = a.fyneApp.NewWindow("代理池工具 v0.1")

	a.rotator = proxy.NewRotator()
	a.checker = checker.NewChecker()

	a.proxyList = binding.NewUntypedList()
	a.logBinding = binding.NewString()
	a.progressBar = widget.NewProgressBar()
	a.serverRunning = binding.NewBool()
	a.serverRunning.Set(false)
	a.rotationStatus = binding.NewBool()
	a.rotationStatus.Set(false)
	a.currentProxy = binding.NewString()
	a.currentProxy.Set("无")
	a.rotationSeconds = 60
	a.rotationStop = make(chan struct{})

	// 默认不筛选
	a.maxLatency = -1
	a.minSpeed = -1

	return a
}

// Log 向UI日志面板添加一条带时间戳的日志
func (a *App) Log(message string) {
	timestamp := time.Now().Format("15:04:05")
	logStr := fmt.Sprintf("[%s] %s\n", timestamp, message)
	currentLog, _ := a.logBinding.Get()
	lines := strings.Split(currentLog, "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	a.logBinding.Set(strings.Join(lines, "\n") + logStr)
	log.Println(message)
}

// FetchProxies 获取代理但不显示，仅存入原始列表
func (a *App) FetchProxies() {
	go func() {
		a.Log("开始从所有源获取在线代理...")
		a.progressBar.Show()
		a.progressBar.SetValue(0)

		proxies, err := fetcher.FetchAllProxies()
		if err != nil {
			a.Log(fmt.Sprintf("获取代理时发生错误: %v", err))
		}
		if len(proxies) == 0 {
			a.Log("未能获取到任何代理。")
			a.progressBar.Hide()
			return
		}

		a.rotator.SetRawProxies(proxies)
		a.progressBar.SetValue(1)
		time.Sleep(1 * time.Second)
		a.progressBar.Hide()
		a.Log(fmt.Sprintf("获取完成，发现 %d 个代理地址。请点击“全部测试”来验证它们。", len(proxies)))
	}()
}

// TestAllProxies 高并发测试所有原始代理，并将有效代理存入列表
func (a *App) TestAllProxies() {
	go func() {
		rawProxies, err := a.rotator.GetRawProxies()
		if err != nil {
			a.Log(fmt.Sprintf("获取原始代理失败: %v", err))
			return
		}
		if len(rawProxies) == 0 {
			a.Log("没有可测试的代理，请先获取代理。")
			return
		}
		a.Log(fmt.Sprintf("开始并发测试 %d 个代理...", len(rawProxies)))
		a.progressBar.Show()
		a.progressBar.SetValue(0)
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
				a.progressBar.SetValue(float64(testedCount) / float64(len(rawProxies)))
				testedMutex.Unlock()
			}(p)
		}
		wg.Wait()

		a.Log("基础测试完成。开始后台批量查询地理位置...")
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
		}()

		a.progressBar.SetValue(1)
		time.Sleep(1 * time.Second)
		a.progressBar.Hide()
		a.Log("全部测试流程完成。")
	}()
}

// ApplyFilters 应用筛选条件并刷新UI
func (a *App) ApplyFilters(maxLatencyStr, minSpeedStr string) {
	if maxLatencyStr == "" {
		a.maxLatency = -1
	} else {
		maxLatency, err := strconv.ParseFloat(maxLatencyStr, 64)
		if err != nil || maxLatency <= 0 {
			a.maxLatency = -1
		} else {
			a.maxLatency = maxLatency / 1000 // ms转换为秒
		}
	}

	if minSpeedStr == "" {
		a.minSpeed = -1
	} else {
		minSpeed, err := strconv.ParseFloat(minSpeedStr, 64)
		if err != nil || minSpeed < 0 {
			a.minSpeed = -1
		} else {
			a.minSpeed = minSpeed
		}
	}

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
			a.Log(fmt.Sprintf("成功导入 %d 个代理。请点击“全部测试”来验证它们。", len(importedProxies)))
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
	myApp.progressBar.Hide()

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
	log.Println("应用已退出")
}

// --- 实现 ui.Apper 接口 ---
func (a *App) GetWindow() fyne.Window              { return a.win }
func (a *App) GetProxyList() binding.UntypedList   { return a.proxyList }
func (a *App) GetLogBinding() binding.String       { return a.logBinding }
func (a *App) GetProgressBar() *widget.ProgressBar { return a.progressBar }
func (a *App) GetServerStatus() binding.Bool       { return a.serverRunning }
func (a *App) GetRotationStatus() binding.Bool     { return a.rotationStatus }
func (a *App) GetCurrentProxy() binding.String     { return a.currentProxy }

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
	a.rotationSeconds = seconds
	a.Log(fmt.Sprintf("轮换间隔已设置为 %d 秒", seconds))
	if running, _ := a.rotationStatus.Get(); running {
		a.stopRotation()
		a.startRotation()
	}
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
	a.rotationStatus.Set(false)
	if a.rotationTicker != nil {
		a.rotationTicker.Stop()
	}
	close(a.rotationStop)
	a.rotationStop = make(chan struct{})
	a.Log("代理轮换已停止")
}
