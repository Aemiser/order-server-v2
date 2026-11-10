package windows

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/v4/process"

	"order_server/go/wechatlog/internal/wechat/model"
	"order_server/go/wechatlog/pkg/appver"
)

const (
	V3ProcessName = "WeChat"
	V4ProcessName = "Weixin"
	V3DBFile      = `Msg\Misc.db`
	V4DBFile      = `db_storage\session\session.db`
)

// Detector 实现 Windows 平台的进程检测器
type Detector struct{}

// NewDetector 创建一个新的 Windows 检测器
func NewDetector() *Detector {
	return &Detector{}
}

// FindProcesses 查找所有微信进程并返回它们的信息
func (d *Detector) FindProcesses() ([]*model.Process, error) {
	processes, err := process.Processes()
	if err != nil {
		log.Err(err).Msg("获取进程列表失败")
		return nil, err
	}

	var result []*model.Process
	for _, p := range processes {
		name, err := p.Name()
		name = strings.TrimSuffix(name, ".exe")
		if err != nil || (name != V3ProcessName && name != V4ProcessName) {
			continue
		}

		// v4 主进程与渲染/网络等辅助进程同名（均为 Weixin.exe），正常仅靠 cmdline
		// 是否含 "--" 来区分主进程与辅助进程。但读取 cmdline 需要打开目标进程，
		// 当本程序与微信的权限/完整性级别不一致时会失败——若此时直接丢弃，会误报
		// 「未检测到微信客户端」。因此：只有成功读到 cmdline 且确实含 "--" 才跳过；
		// 读不到时保守保留，交由内存扫描阶段自行判断（辅助进程里没有密钥，无害）。
		if name == V4ProcessName {
			cmdline, err := p.Cmdline()
			if err != nil {
				log.Err(err).Msg("获取进程命令行失败（可能权限/完整性级别不一致），保留该进程作为候选")
			} else if strings.Contains(cmdline, "--") {
				continue
			}
		}

		// 获取进程信息（进程名同时用于兜底判定微信大版本）
		procInfo, err := d.getProcessInfo(p, name)
		if err != nil {
			log.Err(err).Msgf("获取进程 %d 的信息失败", p.Pid)
			continue
		}

		result = append(result, procInfo)
	}

	return result, nil
}

// getProcessInfo 获取微信进程的详细信息。
// 大版本以进程名为准：WeChat.exe 是 3.9.x，Weixin.exe 是 4.x。
// 过去 exe 版本读不到时一律按 4 兜底，会让 3.9 在探测阶段就被误判成 4.x。
func (d *Detector) getProcessInfo(p *process.Process, processName string) (*model.Process, error) {
	expected := 3
	if processName == V4ProcessName {
		expected = 4
	}
	procInfo := &model.Process{
		PID:      uint32(p.Pid),
		Status:   model.StatusOffline,
		Platform: model.PlatformWindows,
		Version:  expected,
	}

	// 获取可执行文件路径。打开目标进程可能因权限/完整性级别不一致而失败，
	// 此时仍保留进程（PID 可用），交由内存扫描阶段探测，而不是误报「未检测到」。
	exePath, err := p.Exe()
	if err != nil {
		log.Err(err).Msg("获取可执行文件路径失败（本程序可能无法打开微信进程，常见于权限/完整性级别不一致）")
		return procInfo, nil
	}
	procInfo.ExePath = exePath

	// 获取版本信息（失败不致命，按进程名对应的大版本处理）
	versionInfo, err := appver.New(exePath)
	if err != nil {
		log.Err(err).Msgf("获取版本信息失败，按进程名判定为微信 %d.x", expected)
	} else {
		procInfo.FullVersion = versionInfo.FullVersion
		if versionInfo.Version == expected {
			procInfo.Version = versionInfo.Version
		} else {
			log.Warn().Msgf("exe 版本主版本 %d 与进程名 %s 不符，按进程名判定为微信 %d.x",
				versionInfo.Version, processName, expected)
		}
	}

	// 初始化附加信息（数据目录、账户名）；失败仅意味着拿不到数据目录，不影响进程存在性
	if err := initializeProcessInfo(p, procInfo); err != nil {
		log.Err(err).Msg("初始化进程信息失败")
	}

	return procInfo, nil
}
