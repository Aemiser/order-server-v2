import { computed, reactive, ref } from 'vue';
import { GetAppVersion, GetChangelog } from '../../wailsjs/go/main/App';

// 发布记录（版本更新日志）：与后端 version.ReleaseInfo 结构一一对应（线格式为 published_at）。
export interface ReleaseInfo {
  tag: string;
  name: string;
  body: string;
  published_at: string;
}

// 更新状态机：idle（未检查）→ checking（检查中）→ downloading（下载中）→ ready（可重启）/ error（失败）
// 另外 uptodate 表示已是最新。所有状态只驱动顶栏显示，不弹强制窗口、不阻塞主界面。
export type UpdateStatus = 'idle' | 'checking' | 'downloading' | 'ready' | 'error' | 'uptodate';

export const updateState = reactive({
  status: 'idle' as UpdateStatus,
  version: '',          // 发现的新版本号（tag）
  name: '',             // 新版本展示名
  currentVersion: '',   // 当前已是最新时回填的版本号
  progress: 0,          // 下载进度 0~100
  speed: 0,             // 下载速度（MB/s）
  errorMsg: '',         // 失败原因
});

// ---------------- 当前程序版本（界面唯一数据源） ----------------
// 启动时从后端 global.Version 读一次并全局缓存：登录页角标、侧栏常驻标识都读这里。
// 不依赖更新检查结果，所以「检查中 / 下载中 / 更新失败」时版本号也始终可见。
export const appVersion = ref('');
export const versionLabel = computed(() => (appVersion.value ? `v${appVersion.value}` : '—'));

let versionLoad: Promise<string> | null = null;
export function loadAppVersion(): Promise<string> {
  if (versionLoad) return versionLoad;
  versionLoad = (async () => {
    try {
      const v = await GetAppVersion();
      appVersion.value = typeof v === 'string' ? v.trim() : '';
    } catch {
      // 纯浏览器 dev 模式没有 Wails 绑定，留空由界面显示占位符
      appVersion.value = '';
    }
    return appVersion.value;
  })();
  return versionLoad;
}

// ---------------- 版本更新日志（changelog） ----------------
// 数据源为编译进 exe 的 CHANGELOG.md（go:embed），离线可用、不请求网络；
// 发版脚本读取同一份文件写入 GitHub Release 说明，两处始终一致。
export const changelogList = ref<ReleaseInfo[]>([]);
export const changelogLoading = ref(false);
export const changelogError = ref('');
export const changelogModalOpen = ref(false);

export function openChangelogModal() {
  changelogModalOpen.value = true;
  // 若启动那次读取失败（或浏览器预览模式尚无绑定），打开弹窗时再试一次
  loadChangelog();
}
export function closeChangelogModal() {
  changelogModalOpen.value = false;
}

// 当前安装版本对应的更新日志（用于顶栏悬停预览）
export const currentChangelog = computed<ReleaseInfo | null>(() => {
  if (!appVersion.value) return null;
  return changelogList.value.find((r) => r.tag === appVersion.value) || null;
});

let changelogLoad: Promise<void> | null = null;
export function loadChangelog(): Promise<void> {
  if (changelogLoad) return changelogLoad;
  changelogLoading.value = true;
  changelogError.value = '';
  changelogLoad = (async () => {
    try {
      const raw = await GetChangelog();
      changelogList.value = Array.isArray(raw)
        ? (raw as unknown as ReleaseInfo[])
        : [];
    } catch {
      // 内嵌日志读取失败（纯浏览器 dev 模式无 Wails 绑定）：给出空态，
      // 并清空缓存，允许打开弹窗时重试；不阻塞主界面。
      changelogError.value = '更新日志读取失败，请在桌面客户端中查看';
      changelogList.value = [];
      changelogLoad = null;
    } finally {
      changelogLoading.value = false;
    }
  })();
  return changelogLoad;
}

// ---------------- 下载完成后的右下角轻量提醒 ----------------
// 只在 download_complete 弹一次，用户点「稍后」或关闭即消失；
// 顶栏的「已就绪 · 重启更新」仍常驻，随时可以从那里重启，不强制、不阻塞。
export const restartPrompt = reactive({
  visible: false,
  version: '',
});

export function showRestartPrompt(version: string) {
  restartPrompt.version = version || '';
  restartPrompt.visible = true;
}

export function dismissRestartPrompt() {
  restartPrompt.visible = false;
}
