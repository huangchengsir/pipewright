import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import { i18n } from './i18n'
import { useProjectModeStore } from './stores/projectMode'
import './styles/global.css'

const app = createApp(App)

const pinia = createPinia()
app.use(pinia)
useProjectModeStore(pinia)
app.use(router)
app.use(i18n)

app.mount('#app')
