import { createApp } from 'vue';
import App from './App.vue';
import 'element-plus/dist/index.css';
import ElementPlus from 'element-plus';
import './index.css';
import zhCn from 'element-plus/es/locale/lang/zh-cn';
import { loadAppVersion, loadChangelog } from './stores/update';

// 启动即读取一次版本号，登录页与主界面共用同一份数据
loadAppVersion();
// 同时读取内嵌的版本更新历史（离线数据，不依赖登录），供顶栏悬停预览与历史弹窗使用
loadChangelog();

const app = createApp(App);
app.use(ElementPlus, {
  locale: zhCn
});
app.mount('#root');
