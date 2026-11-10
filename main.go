package main

import (
	"context"
	"embed"
	"order_server/go/core"
	"order_server/go/flags"
	"order_server/go/global"
	"order_server/go/kernel"
	"order_server/go/schemaservice"
	"order_server/go/server/auth"
	dashboard "order_server/go/server/dashboard"
	"order_server/go/utils/httputils"

	"github.com/sirupsen/logrus"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// singleInstanceId 是单实例锁的全局唯一标识（用于命名 Windows mutex / IPC 窗口）。
// 必须固定，不能随版本号变化：否则新版本会被当成另一个程序，反而能多开。
const singleInstanceId = "com.aemiser.ordermaster.onepoint"

func main() {
	// Create an instance of the app structure
	app := NewApp()
	flags.Parse()
	// settings.yaml 只保留 log / db：日志和数据库连接是「连库之前」必须的引导配置。
	global.SetConfig(core.ReadConf())
	core.InitLogrus()
	global.Db = core.InitDB()

	// 进程级生命周期：所有长生命周期后台任务从这里领取可取消的 ctx。
	// 提前到各模块初始化之前，让模板工作台的在途识别任务与 AI 请求共享同一取消信号。
	lifecycle := kernel.NewLifecycle()
	httputils.SetBaseContext(lifecycle.Ctx())

	// 模板工作台（新架构纵切片）：orderschema 建表 + Store 初始化。
	// 失败不阻断启动：旧功能照常，模板工作台报「服务未初始化」。
	if err := schemaservice.InitWithContext(lifecycle.Ctx(), global.Db, kernel.NewLogrusLogger(logrus.StandardLogger())); err != nil {
		logrus.Warnf("模板工作台初始化失败: %v", err)
	}
	// 业务表与看板索引在正常启动也必须就绪（此前只在 -db 参数下建，导致看板增量优化失效）。
	flags.EnsureBusinessSchema()
	// 其余全部配置（settings.yaml 的非 log/db 段、aimap.json、productbelong.json、
	// systemPrompt.txt 以及代码里的所有 AI 提示词）统一从数据库 cfg_* 表装载。
	// 首次启动会自动建表并灌入内置种子数据。
	if err := core.LoadDBCfg(); err != nil {
		logrus.Fatalf("加载数据库配置失败: %v", err)
	}
	// 「启动需要登录」开关来自 cfg_settings：关闭时立刻建立内置访客会话，
	// 必须在界面加载前设置好，保证前端首帧 GetAuthState 就能直接进入主页。
	auth.SetGuestMode(!global.GetConfig().Auth.LoginRequired())
	flags.Run()

	// Create application with options
	err := wails.Run(&options.App{
		Title: "order_server",
		//Width:  1500,
		//Height: 1000,
		WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		// 重复双击 exe 时只允许一个窗口：第二个进程会把启动参数发给首个实例后自行退出。
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceId,
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				// 回调在第一个实例里执行，把已有窗口恢复到前台，避免"点了没反应"。
				app.bringToFront()
			},
		},
		// 关闭前优雅退出：取消生命周期 ctx（停掉在途 AI 请求）、通知看板 worker 退出并等待。
		OnBeforeClose: func(ctx context.Context) bool {
			lifecycle.Shutdown()
			dashboard.StopWorker()
			return true
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
