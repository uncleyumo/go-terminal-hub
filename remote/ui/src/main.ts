import { createApp } from 'vue'
import App from './App.vue'
import { initCredentials } from './token'
import './style.css'

// 先定令牌、抹地址栏，再挂载。cookie 还有效时登录页根本不会挂载，那条路径也得
// 清干净，所以这一步不能放进登录页里。
initCredentials()

createApp(App).mount('#app')
